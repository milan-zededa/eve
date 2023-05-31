// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package conntester

import (
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lf-edge/eve/libs/nettrace"
	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/devicenetwork"
	"github.com/lf-edge/eve/pkg/pillar/hardware"
	"github.com/lf-edge/eve/pkg/pillar/netdump"
	"github.com/lf-edge/eve/pkg/pillar/types"
	"github.com/lf-edge/eve/pkg/pillar/zedcloud"
	uuid "github.com/satori/go.uuid"
)

// Hard-coded at 1 for now; at least one interface needs to work.
const requiredSuccessCount uint = 1

var nilUUID = uuid.UUID{} // used as a constant

// ZedcloudConnectivityTester implements external connectivity testing using
// the "/api/v2/edgeDevice/ping" endpoint provided by the zedcloud.
type ZedcloudConnectivityTester struct {
	// Exported attributes below should be injected.
	Log         *base.LogObject
	AgentName   string
	TestTimeout time.Duration // can be changed in run-time
	Metrics     *zedcloud.AgentMetrics

	iteration     int
	prevTLSConfig *tls.Config
}

// TestConnectivity uses VerifyAllIntf from the zedcloud package, which
// tries to call the "ping" API of the controller.
func (t *ZedcloudConnectivityTester) TestConnectivity(dns types.DeviceNetworkStatus,
	withNetTrace bool) (types.IntfStatusMap, []netdump.TracedNetRequest, error) {

	t.iteration++
	intfStatusMap := *types.NewIntfStatusMap()
	t.Log.Tracef("TestConnectivity() requiredSuccessCount %d, iteration %d",
		requiredSuccessCount, t.iteration)

	server, err := os.ReadFile(types.ServerFileName)
	if err != nil {
		t.Log.Fatal(err)
	}
	serverNameAndPort := strings.TrimSpace(string(server))

	zedcloudCtx := zedcloud.NewContext(t.Log, zedcloud.ContextOptions{
		DevNetworkStatus: &dns,
		SendTimeout:      uint32(t.TestTimeout.Seconds()),
		AgentMetrics:     t.Metrics,
		Serial:           hardware.GetProductSerial(t.Log),
		SoftSerial:       hardware.GetSoftSerial(t.Log),
		AgentName:        t.AgentName,
		NetTraceOpts:     t.netTraceOpts(dns),
	})
	t.Log.Functionf("TestConnectivity: Use V2 API %v\n", zedcloud.UseV2API())
	testURL := zedcloud.URLPathString(serverNameAndPort, zedcloudCtx.V2API, nilUUID, "ping")

	tlsConfig, err := zedcloud.GetTlsConfig(zedcloudCtx.DeviceNetworkStatus,
		nil, &zedcloudCtx)
	if err != nil {
		t.Log.Functionf("TestConnectivity: " +
			"Device certificate not found, looking for Onboarding certificate")
		onboardingCert, err := tls.LoadX509KeyPair(types.OnboardCertName,
			types.OnboardKeyName)
		if err != nil {
			err = fmt.Errorf("onboarding certificate cannot be loaded: %v", err)
			t.Log.Functionf("TestConnectivity: %v\n", err)
			return intfStatusMap, nil, err
		}
		clientCert := &onboardingCert
		tlsConfig, err = zedcloud.GetTlsConfig(zedcloudCtx.DeviceNetworkStatus,
			clientCert, &zedcloudCtx)
		if err != nil {
			err = fmt.Errorf("failed to load TLS config for talking to Zedcloud: %v", err)
			t.Log.Functionf("TestConnectivity: %v", err)
			return intfStatusMap, nil, err
		}
	}

	if t.prevTLSConfig != nil {
		tlsConfig.ClientSessionCache = t.prevTLSConfig.ClientSessionCache
	}
	zedcloudCtx.TlsConfig = tlsConfig
	for _, port := range dns.Ports {
		err = devicenetwork.CheckAndGetNetworkProxy(t.Log, &dns, port.Logicallabel, t.Metrics)
		if err != nil {
			err = fmt.Errorf("failed to get network proxy for port %s: %v",
				port.Logicallabel, err)
			t.Log.Errorf("TestConnectivity: %v", err)
			intfStatusMap.RecordFailure(port.Logicallabel, err.Error())
			return intfStatusMap, nil, err
		}
	}
	rv, err := zedcloud.VerifyAllIntf(&zedcloudCtx, testURL, requiredSuccessCount,
		t.iteration, withNetTrace)
	intfStatusMap.SetOrUpdateFromMap(rv.IntfStatusMap)
	t.Log.Tracef("TestConnectivity: intfStatusMap = %+v", intfStatusMap)
	for i := range rv.TracedReqs {
		// Differentiate ping tests from google tests.
		reqName := rv.TracedReqs[i].RequestName
		rv.TracedReqs[i].RequestName = "ping-" + reqName
	}
	if withNetTrace {
		if (!rv.CloudReachable || err != nil) && !rv.RemoteTempFailure {
			rv.TracedReqs = append(rv.TracedReqs, t.tryGoogleWithTracing(dns)...)
		}
	}
	if err != nil {
		if rv.RemoteTempFailure {
			err = &RemoteTemporaryFailure{
				Endpoint:   serverNameAndPort,
				WrappedErr: err,
			}
		} else if portsNotReady := t.getPortsNotReady(err); len(portsNotReady) > 0 {
			// At least one of the uplink ports is not ready in terms of L3 connectivity.
			// Signal to the caller that it might make sense to wait and repeat test later.
			err = &PortsNotReady{
				WrappedErr: err,
				Ports:      portsNotReady,
			}
		}
		t.Log.Errorf("TestConnectivity: %v", err)
		return intfStatusMap, rv.TracedReqs, err
	}

	t.prevTLSConfig = zedcloudCtx.TlsConfig
	if rv.CloudReachable {
		t.Log.Functionf("TestConnectivity: uplink test SUCCEEDED for URL: %s", testURL)
		return intfStatusMap, rv.TracedReqs, nil
	}
	err = fmt.Errorf("uplink test FAILED for URL: %s", testURL)
	t.Log.Errorf("TestConnectivity: %v, intfStatusMap: %+v", err, intfStatusMap)
	return intfStatusMap, rv.TracedReqs, err
}

func (t *ZedcloudConnectivityTester) getPortsNotReady(
	verifyErr error) (ports []string) {
	if sendErr, isSendErr := verifyErr.(*zedcloud.SendError); isSendErr {
		portMap := make(map[string]struct{}) // Avoid duplicate entries.
		for _, attempt := range sendErr.Attempts {
			var dnsErr *types.DNSNotAvail
			if errors.As(attempt.Err, &dnsErr) {
				portMap[dnsErr.PortLL] = struct{}{}
			}
			var ipErr *types.IPAddrNotAvail
			if errors.As(attempt.Err, &ipErr) {
				portMap[ipErr.PortLL] = struct{}{}
			}
		}
		for port := range portMap {
			ports = append(ports, port)
		}
	}
	return ports
}

// Enable all net traces, including packet capture - ping and google.com requests
// are quite small.
func (t *ZedcloudConnectivityTester) netTraceOpts(
	dns types.DeviceNetworkStatus) []nettrace.TraceOpt {
	var intfsForPcap []string
	for _, port := range dns.Ports {
		if port.IsMgmt && port.IfName != "" {
			intfsForPcap = append(intfsForPcap, port.IfName)
		}
	}
	return []nettrace.TraceOpt{
		&nettrace.WithLogging{
			CustomLogger: &base.LogrusWrapper{Log: t.Log},
		},
		&nettrace.WithConntrack{},
		&nettrace.WithSockTrace{},
		&nettrace.WithDNSQueryTrace{},
		&nettrace.WithHTTPReqTrace{
			// Hide secrets stored inside values of header fields.
			HeaderFields: nettrace.HdrFieldsOptValueLenOnly,
		},
		&nettrace.WithPacketCapture{
			Interfaces:  intfsForPcap,
			IncludeICMP: true,
			IncludeARP:  true,
		},
	}
}

// If net tracing is enabled and the controller connectivity test fails, we try to access
// google.com over HTTP and HTTPS and include collected traces in the output.
// This can help to determine if the issue is with the Internet access or with
// something specific to the controller.
func (t *ZedcloudConnectivityTester) tryGoogleWithTracing(
	dns types.DeviceNetworkStatus) (tracedReqs []netdump.TracedNetRequest) {
	const bailOnHTTPErr = true
	const withNetTracing = true
	zedcloudCtx := zedcloud.NewContext(t.Log, zedcloud.ContextOptions{
		DevNetworkStatus: &dns,
		SendTimeout:      uint32(t.TestTimeout.Seconds()),
		AgentName:        t.AgentName,
		NetTraceOpts:     t.netTraceOpts(dns),
	})
	ctxWork, cancel := zedcloud.GetContextForAllIntfFunctions(&zedcloudCtx)
	defer cancel()
	tests := []struct {
		url  string
		name string
	}{
		{url: "http://www.google.com", name: "google.com-over-http"},
		{url: "https://www.google.com", name: "google.com-over-https"},
	}
	for _, test := range tests {
		rv, _ := zedcloud.SendOnAllIntf(ctxWork, &zedcloudCtx, test.url, 0, nil,
			t.iteration, bailOnHTTPErr, withNetTracing)
		for i := range rv.TracedReqs {
			reqName := rv.TracedReqs[i].RequestName
			rv.TracedReqs[i].RequestName = test.name + "-" + reqName
		}
		tracedReqs = append(tracedReqs, rv.TracedReqs...)
	}
	return tracedReqs
}
