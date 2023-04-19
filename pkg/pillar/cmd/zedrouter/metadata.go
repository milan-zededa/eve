// Copyright (c) 2020-2021 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

// A http server providing meta-data information to application instances
// at http://169.254.169.254. The source IP address is used to tell
// which app instance is sending the request

package zedrouter

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/lf-edge/eve/pkg/pillar/types"
	"github.com/lf-edge/eve/pkg/pillar/zedcloud"
)

// Provides a json file
type networkHandler struct {
	zedrouter *zedrouter
}

// Provides a LF-terminated text
type externalIPHandler struct {
	zedrouter *zedrouter
}

// Provides a LF-terminated text
type hostnameHandler struct {
	zedrouter *zedrouter
}

// Provides links for OpenStack metadata/userdata
type openstackHandler struct {
	zedrouter *zedrouter
}

// Let's application to report various metadata back to the cloud. At the
// moment used for:
//   - k3s cluster kubeconfig
//   - reporting custom application status
type appInstMetaHandler struct {
	zedrouter       *zedrouter
	maxResponseLen  int
	publishDataType types.AppInstMetaDataType
}

// Provides geographic location of the device.
type locationInfoHandler struct {
	zedrouter *zedrouter
}

// Provides information about cellular connectivity of the device
// (modems, SIM cards, network providers, etc.).
type wwanStatusHandler struct {
	zedrouter *zedrouter
}

// Provides cellular metrics (signal strength, packet counters).
type wwanMetricsHandler struct {
	zedrouter *zedrouter
}

// Provides a signing service
type signerHandler struct {
	zedrouter   *zedrouter
	zedcloudCtx *zedcloud.ZedCloudContext
}

// AppInfoHandler provides information about available patches for the application
type AppInfoHandler struct {
	zedrouter *zedrouter
}

// AppCustomBlobsHandler serves the AppCustom binary blobs
type AppCustomBlobsHandler struct {
	zedrouter *zedrouter
}

// KubeconfigFileSizeLimitInBytes holds the maximum expected size of Kubeconfig file
// received from k3s server appInst.
// Note: KubeconfigFileSizeLimitInBytes should always be < AppInstMetadataResponseSizeLimitInBytes.
const KubeconfigFileSizeLimitInBytes = 32768 // 32KB

// AppInstMetadataResponseSizeLimitInBytes holds the maximum expected size of appInst
// metadata received in the response.
// Note: KubeconfigFileSizeLimitInBytes should always be < AppInstMetadataResponseSizeLimitInBytes.
const AppInstMetadataResponseSizeLimitInBytes = 35840 // 35KB

// SignerMaxSize is how large objects we will sign
const SignerMaxSize = 65535

func (z *zedrouter) makeMetadataHandler() http.Handler {
	mux := http.NewServeMux()
	nh := &networkHandler{zedrouter: z}
	mux.Handle("/eve/v1/network.json", nh)
	ipHandler := &externalIPHandler{zedrouter: z}
	mux.Handle("/eve/v1/external_ipv4", ipHandler)
	hostnameHandler := &hostnameHandler{zedrouter: z}
	mux.Handle("/eve/v1/hostname", hostnameHandler)

	openstackHandler := &openstackHandler{zedrouter: z}
	mux.Handle("/openstack", openstackHandler)
	mux.Handle("/openstack/", openstackHandler)

	kubeConfigHandler := &appInstMetaHandler{
		zedrouter:       z,
		maxResponseLen:  KubeconfigFileSizeLimitInBytes,
		publishDataType: types.AppInstMetaDataTypeKubeConfig,
	}
	mux.Handle("/eve/v1/kubeconfig", kubeConfigHandler)

	AppCustomStatusHandler := &appInstMetaHandler{
		zedrouter: z,
		// For now use the same limit as Kubeconfig
		maxResponseLen:  KubeconfigFileSizeLimitInBytes,
		publishDataType: types.AppInstMetaDataCustomStatus,
	}
	mux.Handle("/eve/v1/app/appCustomStatus", AppCustomStatusHandler)

	locationInfoHandler := &locationInfoHandler{zedrouter: z}
	mux.Handle("/eve/v1/location.json", locationInfoHandler)

	wwanStatusHandler := &wwanStatusHandler{zedrouter: z}
	mux.Handle("/eve/v1/wwan/status.json", wwanStatusHandler)

	wwanMetricsHandler := &wwanMetricsHandler{zedrouter: z}
	mux.Handle("/eve/v1/wwan/metrics.json", wwanMetricsHandler)

	AppInfoHandler := &AppInfoHandler{zedrouter: z}
	mux.Handle("/eve/v1/app/info.json", AppInfoHandler)

	AppCustomBlobsHandler := &AppCustomBlobsHandler{zedrouter: z}
	mux.Handle("/eve/app-custom-blobs/", AppCustomBlobsHandler)

	zedcloudCtx := zedcloud.NewContext(z.log, zedcloud.ContextOptions{})
	signerHandler := &signerHandler{
		zedrouter:   z,
		zedcloudCtx: &zedcloudCtx,
	}
	mux.Handle("/eve/v1/tpm/signer", signerHandler)
	return mux
}

// ServeHTTP for networkHandler provides a json return
func (hdl networkHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hdl.zedrouter.log.Tracef("networkHandler.ServeHTTP")
	remoteIP := net.ParseIP(strings.Split(r.RemoteAddr, ":")[0])
	externalIP, code := hdl.zedrouter.getExternalIPForApp(remoteIP)
	var ipStr string
	var hostname string
	// Avoid returning the string <nil>
	if !isEmptyIP(externalIP) {
		ipStr = externalIP.String()
	}
	anStatus := hdl.zedrouter.lookupAppNetworkStatusByAppIP(remoteIP)
	if anStatus != nil {
		hostname = anStatus.UUIDandVersion.UUID.String()
	}

	enInfoObj, err := hdl.zedrouter.subEdgeNodeInfo.Get("global")
	if err != nil {
		errorLine := fmt.Sprintf("cannot fetch edge node information: %s", err)
		hdl.zedrouter.log.Error(errorLine)
		http.Error(w, errorLine, http.StatusInternalServerError)
		return
	}
	enInfo := enInfoObj.(types.EdgeNodeInfo)
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(code)
	resp, _ := json.Marshal(map[string]interface{}{
		"caller-ip":         r.RemoteAddr,
		"external-ipv4":     ipStr,
		"hostname":          hostname, // Do not delete this line for backward compatibility
		"app-instance-uuid": hostname,
		"device-uuid":       enInfo.DeviceID,
		"device-name":       enInfo.DeviceName,
		"project-name":      enInfo.ProjectName,
		"project-uuid":      enInfo.ProjectID,
		"enterprise-name":   enInfo.EnterpriseName,
		"enterprise-id":     enInfo.EnterpriseID,
		// TBD: add public-ipv4 when controller tells us
	})
	w.Write(resp)
}

// ServeHTTP for externalIPHandler provides a text IP address
func (hdl externalIPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hdl.zedrouter.log.Tracef("externalIPHandler.ServeHTTP")
	remoteIP := net.ParseIP(strings.Split(r.RemoteAddr, ":")[0])
	externalIP, code := hdl.zedrouter.getExternalIPForApp(remoteIP)
	w.WriteHeader(code)
	w.Header().Add("Content-Type", "text/plain")
	// Avoid returning the string <nil>
	if !isEmptyIP(externalIP) {
		resp := []byte(externalIP.String() + "\n")
		w.Write(resp)
	}
}

// ServeHTTP for hostnameHandler returns text
func (hdl hostnameHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hdl.zedrouter.log.Tracef("hostnameHandler.ServeHTTP")
	remoteIP := net.ParseIP(strings.Split(r.RemoteAddr, ":")[0])
	anStatus := hdl.zedrouter.lookupAppNetworkStatusByAppIP(remoteIP)
	w.Header().Add("Content-Type", "text/plain")
	if anStatus == nil {
		w.WriteHeader(http.StatusNoContent)
		hdl.zedrouter.log.Errorf("No AppNetworkStatus for %s",
			remoteIP.String())
	} else {
		w.WriteHeader(http.StatusOK)
		resp := []byte(anStatus.UUIDandVersion.UUID.String() + "\n")
		w.Write(resp)
	}
}

// ServeHTTP for openstackHandler metadata service
func (hdl openstackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hdl.zedrouter.log.Tracef("openstackHandler ServeHTTP request: %s", r.URL.String())
	dirname, filename := path.Split(strings.TrimSuffix(r.URL.Path, "/"))
	dirname = strings.TrimSuffix(dirname, "/")
	remoteIP := net.ParseIP(strings.Split(r.RemoteAddr, ":")[0])
	anStatus := hdl.zedrouter.lookupAppNetworkStatusByAppIP(remoteIP)
	var hostname string
	var id string
	if anStatus != nil {
		hostname = anStatus.DisplayName
		id = anStatus.UUIDandVersion.UUID.String()
	} else {
		errorLine := fmt.Sprintf("no AppNetworkStatus for %s",
			remoteIP.String())
		hdl.zedrouter.log.Error(errorLine)
		http.Error(w, errorLine, http.StatusNotImplemented)
		return
	}
	anConfig := hdl.zedrouter.lookupAppNetworkConfig(anStatus.Key())
	if anConfig == nil {
		errorLine := fmt.Sprintf("no AppNetworkConfig for %s",
			anStatus.Key())
		hdl.zedrouter.log.Error(errorLine)
		http.Error(w, errorLine, http.StatusNotImplemented)
		return
	}
	if anConfig.MetaDataType != types.MetaDataOpenStack {
		errorLine := fmt.Sprintf("no MetaDataOpenStack for %s",
			anStatus.Key())
		hdl.zedrouter.log.Tracef(errorLine)
		http.Error(w, errorLine, http.StatusNotFound)
		return
	}
	switch filename {
	case "openstack":
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "latest")
	case "meta_data.json":
		keys := hdl.zedrouter.getSSHPublicKeys(anConfig)
		var keysMap []map[string]string
		publicKeys := make(map[string]string)
		for ind, key := range keys {
			keysMap = append(keysMap, map[string]string{
				"data": fmt.Sprintf("%s\n", key),
				"type": "ssh",
				"name": fmt.Sprintf("key-%d", ind),
			})
			publicKeys[fmt.Sprintf("key-%d", ind)] = fmt.Sprintf("%s\n", key)
		}
		resp, _ := json.Marshal(map[string]interface{}{
			"uuid":         id,
			"hostname":     hostname,
			"name":         hostname,
			"launch_index": 0,
			"keys":         keysMap,
			"public_keys":  publicKeys,
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(resp)
	case "network_data.json":
		resp, _ := json.Marshal(map[string]interface{}{
			"services": []string{},
			"networks": []string{},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(resp)
	case "user_data":
		userData, err := hdl.zedrouter.getCloudInitUserData(anConfig)
		if err != nil {
			errorLine := fmt.Sprintf("cannot get userData for %s: %v",
				anStatus.Key(), err)
			hdl.zedrouter.log.Error(errorLine)
			http.Error(w, errorLine, http.StatusInternalServerError)
			return
		}
		ud, err := base64.StdEncoding.DecodeString(userData)
		if err != nil {
			errorLine := fmt.Sprintf("cannot decode userData for %s: %v",
				anStatus.Key(), err)
			hdl.zedrouter.log.Error(errorLine)
			http.Error(w, errorLine, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/yaml")
		w.WriteHeader(http.StatusOK)
		w.Write(ud)
	case "vendor_data.json":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}
	w.WriteHeader(http.StatusNotFound)
}

// ServeHTTP for kubeConfigHandler provides cluster kube config
func (hdl appInstMetaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		msg := "appInstMetaHandler: request method is not Post"
		hdl.zedrouter.log.Error(msg)
		http.Error(w, msg, http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		msg := "appInstMetaHandler: Content-Type header is not application/json"
		hdl.zedrouter.log.Error(msg)
		http.Error(w, msg, http.StatusUnsupportedMediaType)
		return
	}

	kubeConfig, err := io.ReadAll(io.LimitReader(r.Body, AppInstMetadataResponseSizeLimitInBytes))
	if err != nil {
		msg := fmt.Sprintf("appInstMetaHandler: ReadAll failed: %v", err)
		hdl.zedrouter.log.Errorf(msg)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if binary.Size(kubeConfig) > hdl.maxResponseLen {
		msg := fmt.Sprintf("appInstMetaHandler: kubeconfig size exceeds limit. Expected <= %v, actual size: %v",
			hdl.maxResponseLen, binary.Size(kubeConfig))
		hdl.zedrouter.log.Errorf(msg)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	remoteIP := net.ParseIP(strings.Split(r.RemoteAddr, ":")[0])
	anStatus := hdl.zedrouter.lookupAppNetworkStatusByAppIP(remoteIP)
	if anStatus == nil {
		msg := fmt.Sprintf("appInstMetaHandler: no AppNetworkStatus for %s", remoteIP.String())
		hdl.zedrouter.log.Errorf(msg)
		http.Error(w, http.StatusText(http.StatusNoContent), http.StatusNoContent)
		return
	}

	var appInstMetaData = &types.AppInstMetaData{
		AppInstUUID: anStatus.UUIDandVersion.UUID,
		Data:        kubeConfig,
		Type:        hdl.publishDataType,
	}
	hdl.zedrouter.publishAppInstMetadata(appInstMetaData)
	return
}

// ServeHTTP for locationInfoHandler provides a json return
func (hdl locationInfoHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hdl.zedrouter.log.Tracef("locationInfoHandler.ServeHTTP")
	locInfoObj, err := hdl.zedrouter.subLocationInfo.Get("global")
	if err != nil {
		http.Error(w, http.StatusText(http.StatusNoContent), http.StatusNoContent)
		return
	}
	locInfo := locInfoObj.(types.WwanLocationInfo)
	resp, err := json.Marshal(locInfo)
	if err != nil {
		msg := fmt.Sprintf("Failed to marshal location info: %v", err)
		hdl.zedrouter.log.Errorf(msg)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(resp)
}

// ServeHTTP for wwanStatusHandler returns json output.
func (hdl wwanStatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hdl.zedrouter.log.Tracef("wwanStatusHandler.ServeHTTP")
	statusObj, err := hdl.zedrouter.subWwanStatus.Get("global")
	if err != nil {
		http.Error(w, http.StatusText(http.StatusNoContent), http.StatusNoContent)
		return
	}
	status := statusObj.(types.WwanStatus)
	// SIM card and modem (logical) names are not relevant to applications.
	// They are generated by EVE and used in the EVE<->Controller API (ZInfoDevice)
	// for reference purposes. ConfigChecksum is also cleared because it is used
	// only internally by EVE microservices.
	// All these fields will be completely omitted from the json output
	// (all have omitempty json tag).
	for i := range status.Networks {
		status.Networks[i].Module.Name = ""
		for j := range status.Networks[i].SimCards {
			status.Networks[i].SimCards[j].Name = ""
		}
	}
	status.ConfigChecksum = ""
	resp, err := json.Marshal(status)
	if err != nil {
		msg := fmt.Sprintf("Failed to marshal WWAN status: %v", err)
		hdl.zedrouter.log.Errorf(msg)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(resp)
}

// ServeHTTP for wwanMetricsHandler returns json output.
func (hdl wwanMetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hdl.zedrouter.log.Tracef("wwanMetricsHandler.ServeHTTP")
	metricsObj, err := hdl.zedrouter.subWwanMetrics.Get("global")
	if err != nil {
		http.Error(w, http.StatusText(http.StatusNoContent), http.StatusNoContent)
		return
	}
	metrics := metricsObj.(types.WwanMetrics)
	resp, err := json.Marshal(metrics)
	if err != nil {
		msg := fmt.Sprintf("Failed to marshal WWAN metrics: %v", err)
		hdl.zedrouter.log.Errorf(msg)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(resp)
}

// ServeHTTP for signerHandler returns protobuf output
func (hdl signerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hdl.zedrouter.log.Tracef("signerHandler.ServeHTTP")

	if r.Method != http.MethodPost {
		msg := "signerHandler: request method is not POST"
		hdl.zedrouter.log.Error(msg)
		http.Error(w, msg, http.StatusMethodNotAllowed)
		return
	}
	// One larger to make sure we detect too large below.
	payload, err := io.ReadAll(io.LimitReader(r.Body, SignerMaxSize+1))
	if err != nil {
		msg := fmt.Sprintf("signerHandler: ReadAll failed: %v", err)
		hdl.zedrouter.log.Errorf(msg)
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}

	if binary.Size(payload) > SignerMaxSize {
		msg := fmt.Sprintf("signerHandler: size exceeds limit. Expected <= %v",
			SignerMaxSize)
		hdl.zedrouter.log.Errorf(msg)
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	remoteIP := net.ParseIP(strings.Split(r.RemoteAddr, ":")[0])
	anStatus := hdl.zedrouter.lookupAppNetworkStatusByAppIP(remoteIP)
	if anStatus == nil {
		msg := fmt.Sprintf("signerHandler: no AppNetworkStatus for %s",
			remoteIP.String())
		hdl.zedrouter.log.Errorf(msg)
		http.Error(w, msg, http.StatusForbidden)
		return
	}

	resp, err := zedcloud.AddAuthentication(hdl.zedcloudCtx,
		bytes.NewBuffer(payload), false)
	if err != nil {
		msg := fmt.Sprintf("Failed to AddAuthentication: %v", err)
		hdl.zedrouter.log.Errorf(msg)
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}
	w.Header().Add("Content-Type", "application/x-proto-binary")
	w.WriteHeader(http.StatusOK)
	w.Write(resp.Bytes())
}

func (hdl AppInfoHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hdl.zedrouter.log.Tracef("wwanAppInfoHandler.ServeHTTP")
	w.Header().Add("Content-Type", "application/json")

	remoteIP := net.ParseIP(strings.Split(r.RemoteAddr, ":")[0])
	anStatus := hdl.zedrouter.lookupAppNetworkStatusByAppIP(remoteIP)
	if anStatus == nil {
		hdl.zedrouter.log.Errorf("Could not find network instance by ip %v", remoteIP)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not found"))
		return
	}

	diskStatusList := hdl.zedrouter.lookupDiskStatusList(
		anStatus.UUIDandVersion.UUID.String())

	var appInfo types.AppInfo
	for _, st := range diskStatusList {
		if st.Devtype != "AppCustom" {
			continue
		}

		blob := types.AppBlobsAvailable{
			CustomMeta: st.CustomMeta,
			DownloadURL: fmt.Sprintf("http://169.254.169.254/eve/app-custom-blobs/%s",
				st.DisplayName),
		}

		appInfo.AppBlobs = append(appInfo.AppBlobs, blob)
	}

	resp, _ := json.Marshal(appInfo)
	w.WriteHeader(http.StatusOK)
	w.Write(resp)
}

func (hdl AppCustomBlobsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	blobName := path.Base(r.URL.Path)

	remoteIP := net.ParseIP(strings.Split(r.RemoteAddr, ":")[0])
	anStatus := hdl.zedrouter.lookupAppNetworkStatusByAppIP(remoteIP)
	if anStatus == nil {
		hdl.zedrouter.log.Errorf("Could not find network instance by ip %v", remoteIP)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not found"))
		return
	}

	diskStatusList := hdl.zedrouter.lookupDiskStatusList(
		anStatus.UUIDandVersion.UUID.String())

	var blobFileLocation string
	for _, st := range diskStatusList {
		if st.Devtype != "AppCustom" {
			continue
		}

		if st.DisplayName == blobName {
			blobFileLocation = st.FileLocation
			break
		}
	}

	if blobFileLocation == "" {
		http.Error(w, r.RequestURI, http.StatusNotFound)
		return
	}

	f, err := os.Open(blobFileLocation)
	if err != nil {
		http.Error(w, r.RequestURI, http.StatusNotFound)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, r.RequestURI, http.StatusNotFound)
		return
	}
	modTime := fi.ModTime()

	http.ServeContent(w, r, blobFileLocation, modTime, f)
}
