package evetest

import (
	"bytes"
	"context"
	"net"
	"regexp"
	"strconv"
	"time"

	eveconfig "github.com/lf-edge/eve-api/go/config"
	eveflowlog "github.com/lf-edge/eve-api/go/flowlog"
	eveinfo "github.com/lf-edge/eve-api/go/info"
	evemetrics "github.com/lf-edge/eve-api/go/metrics"
	uuid "github.com/satori/go.uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TODO: consider removing error value from the methods below, instead
//       just trigger d.th.t.Fatal
// TODO: also consider optional timeouts (+wait bool) arguments instead of contexts

// EdgeDevice represents a single onboarded EVE device and provides
// operations to manage its lifecycle, configuration, applications,
// and runtime state.
type EdgeDevice struct {
	th      *TestHarness
	devName string
}

// EdgeDeviceState represents the current lifecycle state of an EVE device.
type EdgeDeviceState int

const (
	// EdgeDeviceStateUndefined indicates an unknown or uninitialized device state.
	EdgeDeviceStateUndefined EdgeDeviceState = iota

	// EdgeDeviceStateOnline indicates that the device is online and operational.
	EdgeDeviceStateOnline

	// EdgeDeviceStateSuspect indicates that the device is onboarded but stopped
	// communicating with the controller.
	EdgeDeviceStateSuspect

	// EdgeDeviceStateBooting indicates that the device is currently booting.
	EdgeDeviceStateBooting

	// EdgeDeviceStateTesting indicates that the device was upgraded and still is
	// testing the new EVE version.
	EdgeDeviceStateTesting

	// EdgeDeviceStateUpgrading indicates that the device is upgrading its EVE OS.
	EdgeDeviceStateUpgrading
)

// LogMsg represents a single log message emitted by the device or an application.
type LogMsg struct {
	Severity  string
	Source    string
	Filename  string
	Message   string
	Timestamp time.Time
}

// LogMsgMatch defines filtering criteria for matching log messages.
type LogMsgMatch struct {
	Severity         string
	Source           string
	Filename         string
	MsgHasSubstring  string
	MsgMatchesRegexp regexp.Regexp
	NotBefore        time.Time
	NotAfter         time.Time
}

// FlowLogMatch defines filtering criteria for matching application flow logs.
type FlowLogMatch struct {
	Flow              eveflowlog.IpFlow // match every non-zero value from the 5-tuple
	Inbound           bool
	VirtualNetAdapter string // logical label
	NetworkInstance   uuid.UUID
	// NotBefore and NotAfter relates to FlowRecord.startTime
	NotBefore time.Time
	NotAfter  time.Time
}

// DNSLogMatch defines filtering criteria for matching application DNS logs.
type DNSLogMatch struct {
	VirtualNetAdapter string // logical label
	NetworkInstance   uuid.UUID
	// NotBefore and NotAfter relates to DnsRequest.requestTime
	NotBefore time.Time
	NotAfter  time.Time
}

// AuthMethod is a marker interface for application authentication methods.
type AuthMethod interface {
	isAuthMethod()
}

// UsernamePasswordAuth represents username/password authentication.
type UsernamePasswordAuth struct {
	Username string
	Password string
}

func (UsernamePasswordAuth) isAuthMethod() {}

// ClientCertAuth represents client certificate–based authentication.
type ClientCertAuth struct {
	KeyPEM string
}

func (ClientCertAuth) isAuthMethod() {}

// GetState returns the current lifecycle state of the device.
func (d *EdgeDevice) GetState() EdgeDeviceState {
	d.th.devicesM.Lock()
	defer d.th.devicesM.Unlock()
	devState, found := d.th.devices[d.devName]
	if !found {
		return EdgeDeviceStateUndefined
	}
	return devState.state
}

// ApplyConfig applies a device configuration and optionally waits until
// it is fully applied.
func (d *EdgeDevice) ApplyConfig(
	ctx context.Context, config *EdgeDeviceConfig, waitUntilApplied bool) {
	if d.devName != config.DeviceName {
		d.th.t.Fatalf("Device name mismatch: "+
			"EdgeDevice handle is for %q but config is for %q",
			d.devName, config.DeviceName)
	}

	// Get previous config.
	d.th.devicesM.Lock()
	devState, found := d.th.devices[d.devName]
	if !found {
		d.th.t.Fatalf("Unknown device %q", d.devName)
	}
	devUUID := devState.ID
	prevConfig := devState.config
	d.th.devicesM.Unlock()

	// Set config ID.
	var err error
	var configVer int
	if prevConfig != nil && prevConfig.GetId().GetVersion() != "" {
		configVer, err = strconv.Atoi(prevConfig.GetId().GetVersion())
		if err != nil {
			d.th.t.Fatalf("Failed to convert config version to integer: %w", err)
		}
	}
	configVer++
	newConfig := config.clone()
	newConfig.Id = &eveconfig.UUIDandVersion{
		Uuid:    devUUID.String(),
		Version: strconv.Itoa(configVer),
	}

	// Set timestamp.
	newConfig.ConfigTimestamp = timestamppb.New(time.Now())

	// Set default global configuration properties.
	newConfig.setDefaultConfigProperties()

	// TODO: preserve purge & reboot counters from the previous config

	// Preserve cipher contexts.
	if prevConfig != nil {
		newConfig.CipherContexts = prevConfig.GetCipherContexts()
	}

	ctx, cancel := context.WithTimeout(d.th.ctx, adamApplyConfigTimeout)
	err = d.th.adamClient.ApplyDeviceConfig(ctx, devUUID, newConfig.EdgeDevConfig)
	cancel()
	if err != nil {
		d.th.t.Fatalf("Failed to apply the new configuration "+
			"(version %d) for device %q: %v", configVer, d.devName, err)
	}

	// Save the applied config.
	d.th.devicesM.Lock()
	d.th.devices[d.devName].config = newConfig
	d.th.devicesM.Unlock()

	if waitUntilApplied {
		// TODO: Maybe instead of this, wait until info message is published
		//       with LastProcessedConfig equal or higher than newConfig.ConfigTimestamp
		//       But LastProcessedConfig only means that zedagent parsed the config and
		//       published pubsub messaged to other microservices, so it is not much
		//       better anyway.

		d.th.log.Infof(
			"Waiting for device %q to apply the latest config (version %d)...",
			d.devName, configVer)
		ctx, cancel = context.WithTimeout(d.th.ctx, deviceApplyConfigTimeout)
		err = d.th.adamClient.WaitUntilDevRequest(ctx, devUUID, "/config")
		cancel()
		if err != nil {
			d.th.t.Fatalf(
				"Device %q failed to fetch the latest config (version %d): %v",
				d.devName, configVer, err)
		}
		d.th.log.Infof("Device %q applied the latest config (version %d)",
			d.devName, configVer)
	}
}

// GetConfig returns the current device configuration.
func (d *EdgeDevice) GetConfig() *EdgeDeviceConfig {
	d.th.devicesM.Lock()
	defer d.th.devicesM.Unlock()
	devState, found := d.th.devices[d.devName]
	if !found {
		d.th.t.Fatalf("Unknown device %q", d.devName)
	}
	return devState.config.clone()
}

// GetDeviceIPAddress returns IP addresses assigned to the specified network adapter.
func (d *EdgeDevice) GetDeviceIPAddress(netAdapterLogicalLabel string) []net.IP {
	// TODO : read IP address from DevicePort info
	d.th.t.Fatalf("GetDeviceIPAddress is not implemented")
	return nil
}

// UpgradeEVE upgrades the EVE OS to the specified version and optionally
// waits until the upgrade completes.
func (d *EdgeDevice) UpgradeEVE(
	ctx context.Context, eveVersion string, waitUntilUpgraded bool) error {
	// TODO (do not forget to log progress - this is true for every function here with longer execution)
	d.th.t.Fatalf("UpgradeEVE is not implemented")
	return nil
}

// RequestReboot requests a device reboot via configuration and optionally
// waits until the reboot completes.
func (d *EdgeDevice) RequestReboot(ctx context.Context, waitUntilRebooted bool) error {
	// TODO - request reboot via the device config
	d.th.t.Fatalf("RequestReboot is not implemented")
	return nil
}

// SoftReboot reboots the device from the console/SSH.
func (d *EdgeDevice) SoftReboot(ctx context.Context, waitUntilRebooted bool) error {
	// TODO - run reboot over ssh
	d.th.t.Fatalf("SoftReboot is not implemented")
	return nil
}

// HardReboot triggers device reboot through the broker.
func (d *EdgeDevice) HardReboot(ctx context.Context, waitUntilRebooted bool) error {
	// TODO - reboot the VM
	d.th.t.Fatalf("HardReboot is not implemented")
	return nil
}

// GetLogs returns device log messages matching the provided criteria.
func (d *EdgeDevice) GetLogs(LogMsgMatch) []LogMsg {
	// TODO
	d.th.t.Fatalf("GetLogs is not implemented")
	return nil
}

// GetAppLogs returns application log messages matching the provided criteria.
func (d *EdgeDevice) GetAppLogs(appUUID uuid.UUID, match LogMsgMatch) []LogMsg {
	// TODO
	d.th.t.Fatalf("GetAppLogs is not implemented")
	return nil
}

// GetAppFlowLogs returns flow records for the specified application
// matching the provided criteria.
func (d *EdgeDevice) GetAppFlowLogs(
	appUUID uuid.UUID, match FlowLogMatch) []eveflowlog.FlowRecord {
	// TODO
	d.th.t.Fatalf("GetAppFlowLogs is not implemented")
	return nil
}

// GetAppDNSLogs returns DNS request logs for the specified application
// matching the provided criteria.
func (d *EdgeDevice) GetAppDNSLogs(
	appUUID uuid.UUID, match DNSLogMatch) []eveflowlog.DnsRequest {
	// TODO
	d.th.t.Fatalf("GetAppDNSLogs is not implemented")
	return nil
}

// WaitUntilAppIsRunning waits until the specified application reaches
// the running state or fails.
func (d *EdgeDevice) WaitUntilAppIsRunning(
	appUUID uuid.UUID, timeoutExcludingDownload time.Duration) error {
	// TODO - watch for app state changes
	//      - fail immediately if an unrecoverable error is reported (e.g. image download failed)
	//      - there should be no timeout for download as long as it is progressing
	//        (if download progress does not change for a minute, fail)
	//      - log state changes and Download progress
	// Example:
	//        time: 2025-07-22T13:53:42.702397711Z out: 	appName eclient state changed to UNKNOWN
	//        time: 2025-07-22T13:53:49.148303057Z out: 	appName eclient state changed to RESOLVING_TAG
	//        time: 2025-07-22T13:53:51.149552198Z out: 	appName eclient state changed to DOWNLOAD_STARTED
	//        time: 2025-07-22T13:53:54.152238557Z out: 	appName eclient state changed to DOWNLOAD_STARTED (0%)
	//        time: 2025-07-22T13:54:00.158332908Z out: 	appName eclient state changed to DOWNLOAD_STARTED (100%)
	//        time: 2025-07-22T13:54:07.164679058Z out: 	appName eclient state changed to DOWNLOAD_STARTED (0%)
	//        time: 2025-07-22T13:54:12.16916761Z out: 	appName eclient state changed to DOWNLOAD_STARTED (4%)
	//        time: 2025-07-22T13:54:15.171694386Z out: 	appName eclient state changed to DOWNLOAD_STARTED (10%)
	//        time: 2025-07-22T13:54:16.172834743Z out: 	appName eclient state changed to DOWNLOAD_STARTED (11%)
	//        time: 2025-07-22T13:54:17.17378552Z out: 	appName eclient state changed to DOWNLOAD_STARTED (17%)
	//        time: 2025-07-22T13:54:20.176271086Z out: 	appName eclient state changed to DOWNLOAD_STARTED (20%)
	//        time: 2025-07-22T13:54:21.177279535Z out: 	appName eclient state changed to DOWNLOAD_STARTED (23%)
	//        time: 2025-07-22T13:54:23.178792535Z out: 	appName eclient state changed to DOWNLOAD_STARTED (34%)
	//        time: 2025-07-22T13:54:23.178816225Z out: 	appName eclient state changed to DOWNLOAD_STARTED (39%)
	//        time: 2025-07-22T13:54:24.17942501Z out: 	appName eclient state changed to DOWNLOAD_STARTED (42%)
	//        time: 2025-07-22T13:54:25.17972504Z out: 	appName eclient state changed to DOWNLOAD_STARTED (46%)
	//        time: 2025-07-22T13:54:25.179750498Z out: 	appName eclient state changed to DOWNLOAD_STARTED (47%)
	//        time: 2025-07-22T13:54:27.180262439Z out: 	appName eclient state changed to DOWNLOAD_STARTED (51%)
	//        time: 2025-07-22T13:54:28.181304132Z out: 	appName eclient state changed to DOWNLOAD_STARTED (88%)
	//        time: 2025-07-22T13:54:29.182441214Z out: 	appName eclient state changed to DOWNLOAD_STARTED (92%)
	//        time: 2025-07-22T13:54:29.182466723Z out: 	appName eclient state changed to DOWNLOAD_STARTED (97%)
	//        time: 2025-07-22T13:54:32.184812327Z out: 	appName eclient state changed to DOWNLOAD_STARTED (100%)
	//        time: 2025-07-22T13:54:32.184828727Z out: 	appName eclient state changed to LOADING
	//        time: 2025-07-22T13:54:42.193246236Z out: 	appName eclient state changed to CREATING_VOLUME
	//        time: 2025-07-22T13:55:27.234218112Z out: 	appName eclient state changed to INSTALLED
	//        time: 2025-07-22T13:55:43.246112459Z out: 	appName eclient state changed to BOOTING
	//        time: 2025-07-22T13:55:44.246948475Z out: 	appName eclient state changed to RUNNING
	d.th.t.Fatalf("WaitUntilAppIsRunning is not implemented")
	return nil
}

// RebootApplication requests a reboot of the specified application instance.
func (d *EdgeDevice) RebootApplication(
	ctx context.Context, appUUID uuid.UUID, waitUntilRebooted bool) error {
	// TODO (increase reboot counter - this is not about running reboot over ssh)
	d.th.t.Fatalf("RebootApplication is not implemented")
	return nil
}

// PurgeApplication purges the specified application instance and its state.
func (d *EdgeDevice) PurgeApplication(
	ctx context.Context, appUUID uuid.UUID, waitUntilPurged bool) error {
	// TODO (increase reboot counter - this is not about running reboot over ssh)
	d.th.t.Fatalf("PurgeApplication is not implemented")
	return nil
}

// ActivateApplication activates the specified application instance.
func (d *EdgeDevice) ActivateApplication(
	ctx context.Context, appUUID uuid.UUID, waitUntilActivated bool) error {
	// TODO
	d.th.t.Fatalf("ActivateApplication is not implemented")
	return nil
}

// DeactivateApplication deactivates the specified application instance.
func (d *EdgeDevice) DeactivateApplication(
	ctx context.Context, appUUID uuid.UUID, waitUntilActivated bool) error {
	// TODO
	d.th.t.Fatalf("DeactivateApplication is not implemented")
	return nil
}

// RunShellScript executes the provided shell script on the device over SSH
// and returns its standard output and standard error as strings.
//
// If timeout is non-zero, execution is bounded by the given duration and
// will be canceled if the timeout expires. If timeout is zero, no explicit
// deadline is applied.
func (d *EdgeDevice) RunShellScript(
	script string, timeout time.Duration) (stdout, stderr string, err error) {
	ctx := context.Background()
	if timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var stdoutBuf, stderrBuf bytes.Buffer
	err = d.th.runScriptOnEVEOverSSH(ctx, d.devName, script, &stdoutBuf, &stderrBuf, 0)
	return stdoutBuf.String(), stderrBuf.String(), err
}

// RunShellScriptInsideApp executes a shell script inside the specified
// application context.
func (d *EdgeDevice) RunShellScriptInsideApp(appUUID uuid.UUID, auth AuthMethod,
	script string, timeout time.Duration) (stdout, stderr string, err error) {
	// TODO: look for port forwarding rule with app port 22, then try every EVE NIC
	//       used by the corresponding app VIF
	d.th.t.Fatalf("RunShellScriptInsideApp is not implemented")
	return "", "", nil
}

// FileExists checks whether a file exists on the device.
func (d *EdgeDevice) FileExists(fileName string) (bool, error) {
	// TODO
	d.th.t.Fatalf("FileExists is not implemented")
	return false, nil
}

// ReadFile reads the contents of a file from the device.
func (d *EdgeDevice) ReadFile(fileName string) ([]byte, error) {
	// TODO
	d.th.t.Fatalf("ReadFile is not implemented")
	return nil, nil
}

// DeleteFile removes a file from the device.
func (d *EdgeDevice) DeleteFile(fileName string) error {
	// TODO
	d.th.t.Fatalf("DeleteFile is not implemented")
	return nil
}

// GetDeviceInfo returns detailed device information.
func (d *EdgeDevice) GetDeviceInfo() *eveinfo.ZInfoDevice {
	// TODO
	d.th.t.Fatalf("GetDeviceInfo is not implemented")
	return nil
}

// GetAppInfo returns runtime information for the specified application.
func (d *EdgeDevice) GetAppInfo(appUUID uuid.UUID) *eveinfo.ZInfoApp {
	// TODO
	d.th.t.Fatalf("GetAppInfo is not implemented")
	return nil
}

// GetNetworkInstanceInfo returns information about a network instance.
func (d *EdgeDevice) GetNetworkInstanceInfo(niUUID uuid.UUID) *eveinfo.ZInfoNetworkInstance {
	// TODO
	d.th.t.Fatalf("GetNetworkInstanceInfo is not implemented")
	return nil
}

// GetVolumeInfo returns information about a storage volume.
func (d *EdgeDevice) GetVolumeInfo(volumeUUID uuid.UUID) *eveinfo.ZInfoVolume {
	// TODO
	d.th.t.Fatalf("GetVolumeInfo is not implemented")
	return nil
}

// GetContentTreeInfo returns information about a content tree.
func (d *EdgeDevice) GetContentTreeInfo(ctUUID uuid.UUID) *eveinfo.ZInfoContentTree {
	// TODO
	d.th.t.Fatalf("GetContentTreeInfo is not implemented")
	return nil
}

// GetBlobInfo returns information about stored blobs on the device.
func (d *EdgeDevice) GetBlobInfo() *eveinfo.ZInfoBlobList {
	// TODO
	d.th.t.Fatalf("GetBlobInfo is not implemented")
	return nil
}

// GetAppMetadata returns metadata associated with an application instance.
func (d *EdgeDevice) GetAppMetadata(appUUID uuid.UUID) *eveinfo.ZInfoAppInstMetaData {
	// TODO
	d.th.t.Fatalf("GetAppMetadata is not implemented")
	return nil
}

// GetHardwareInfo returns hardware inventory information.
func (d *EdgeDevice) GetHardwareInfo() *eveinfo.ZInfoHardware {
	// TODO
	d.th.t.Fatalf("GetHardwareInfo is not implemented")
	return nil
}

// GetLocationInfo returns device location information.
func (d *EdgeDevice) GetLocationInfo() *eveinfo.ZInfoHardware {
	// TODO
	d.th.t.Fatalf("GetLocationInfo is not implemented")
	return nil
}

// GetNTPSources returns configured NTP sources for the device.
func (d *EdgeDevice) GetNTPSources() *eveinfo.ZInfoNTPSources {
	// TODO
	d.th.t.Fatalf("GetNTPSources is not implemented")
	return nil
}

// GetClusterNodeInfo returns information about the device as a cluster node.
func (d *EdgeDevice) GetClusterNodeInfo() *eveinfo.ZInfoClusterNode {
	// TODO
	d.th.t.Fatalf("GetClusterNodeInfo is not implemented")
	return nil
}

// GetClusterInfo returns information about the Kubernetes cluster.
func (d *EdgeDevice) GetClusterInfo() *eveinfo.ZInfoKubeCluster {
	// TODO
	d.th.t.Fatalf("GetClusterInfo is not implemented")
	return nil
}

// GetDeviceMetrics returns current device-level metrics.
func (d *EdgeDevice) GetDeviceMetrics() *evemetrics.DeviceMetric {
	// TODO
	d.th.t.Fatalf("GetDeviceMetrics is not implemented")
	return nil
}

// GetAppMetrics returns metrics for the specified application.
func (d *EdgeDevice) GetAppMetrics(appUUID uuid.UUID) *evemetrics.AppMetric {
	// TODO
	d.th.t.Fatalf("GetAppMetrics is not implemented")
	return nil
}

// GetNetworkInstanceMetrics returns metrics for a network instance.
func (d *EdgeDevice) GetNetworkInstanceMetrics(
	niUUID uuid.UUID) *evemetrics.ZMetricNetworkInstance {
	// TODO
	d.th.t.Fatalf("GetNetworkInstanceMetrics is not implemented")
	return nil
}

// GetVolumeMetrics returns metrics for a storage volume.
func (d *EdgeDevice) GetVolumeMetrics(volumeUUID uuid.UUID) *evemetrics.ZMetricVolume {
	// TODO
	d.th.t.Fatalf("GetVolumeMetrics is not implemented")
	return nil
}

// GetClusterMetrics returns metrics for the Kubernetes cluster.
func (d *EdgeDevice) GetClusterMetrics() *evemetrics.KubeClusterMetrics {
	// TODO
	d.th.t.Fatalf("GetClusterMetrics is not implemented")
	return nil
}

// ReadPublication retrieves a single message from a pub-sub topic published by
// the specified device and agent (microservice).
//
// Parameters:
//   - d: the EdgeDevice handle to read from
//   - fromAgent: the name of the agent/microservice publishing the topic
//   - output: pointer to a value of type T to unmarshal the message into
//   - key: identifies the specific message within the topic to fetch
//
// Returns an error if the topic or message does not exist, cannot be read, or
// fails to unmarshal into the provided output type.
func ReadPublication[T any](d *EdgeDevice, fromAgent string, output *T, key string) error {
	// TODO
	d.th.t.Fatalf("ReadPublication is not implemented")
	return nil
}

// ReadAllPublications retrieves all messages from a pub-sub topic published by
// the specified device and agent (microservice).
//
// Parameters:
//   - d: the EdgeDevice handle to read from
//   - fromAgent: the name of the agent/microservice publishing the topic
//
// Returns a slice of values of type T representing all messages from the topic,
// or an error if reading or unmarshaling fails.
func ReadAllPublications[T any](d *EdgeDevice, fromAgent string) ([]T, error) {
	// TODO
	d.th.t.Fatalf("ReadAllPublications is not implemented")
	return nil, nil
}
