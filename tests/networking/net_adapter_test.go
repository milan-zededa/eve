package networking_test

import (
	"fmt"
	"net"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	eveconfig "github.com/lf-edge/eve-api/go/config"
	"github.com/lf-edge/eve-api/go/evecommon"
	eveinfo "github.com/lf-edge/eve-api/go/info"
	evemetrics "github.com/lf-edge/eve-api/go/metrics"
	"github.com/lf-edge/eve/evetest"
	"github.com/lf-edge/eve/tests/matchers"
	"github.com/lf-edge/eve/tests/netmodels"
)

func deviceRequirementsForNetAdapterTests(
	devName string, hypervisor evetest.Hypervisor) evetest.RequireEdgeDevice {
	return evetest.RequireEdgeDevice{
		Name:           devName,
		WithHypervisor: hypervisor,
		MinCPUs:        4,
		WithGrubOptions: []string{
			// No applications are deployed in these network adapter tests.
			// Prioritize maximizing EVE performance and reducing device onboarding time.
			"set_global hv_dom0_cpu_settings \"dom0_max_vcpus=4\"",
			"set_global hv_eve_cpu_settings \"eve_max_vcpus=3\"",
			"set_global hv_ctrd_cpu_settings \"ctrd_max_vcpus=3\""},
		DeviceReusePolicy: evetest.ResetDeviceConfig,
	}
}

func TestDHCPIPv4Only(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()

	// Define configurable parameters available for the test.
	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
	)

	// Get parameter values set for this test execution.
	hypervisor := evetest.GetHypervisorParameterValue()

	// Set up the test harness and specify the test prerequisites.
	devName := "edge-dev"
	requiredDevice := deviceRequirementsForNetAdapterTests(devName, hypervisor)
	requiredNetModel := evetest.RequireNetworkModel{
		NetworkModel: netmodels.SingleEthWithDHCPAndIPv6,
	}
	evetest.Setup(requiredDevice, requiredNetModel,
		evetest.RequireInternetConnectivity{RequireIPv6: true})
	device := evetest.GetEdgeDevice(devName)
	evetest.Checkpoint("setup-done")

	// Build and apply the initial device configuration.
	devConfig := evetest.NewEdgeDeviceConfig(devName)
	dhcpNet := devConfig.AddNetwork(
		evetest.DHCPNetworkConfig{
			NetworkType: evecommon.NetworkType_V4Only,
		})
	devConfig.AddNetworkAdapter(
		evetest.NetworkAdapterConfig{
			LogicalLabel:  "ethernet0",
			PhysicalLabel: "eth0",
			InterfaceName: "eth0",
			NetworkUUID:   dhcpNet,
			Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
		})
	device.ApplyConfig(devConfig, true)
	evetest.Checkpoint("config-applied")

	log := evetest.Logger()

	log.Infof("Waiting for device to report IPv4 (only) address...")
	t.Eventually(func() bool {
		ips := device.GetDeviceIPAddress("ethernet0")
		return containsIPv4(ips) && !containsIPv6(ips)
	}, 3*time.Minute, 10*time.Second).Should(BeTrue())

	t.Consistently(func() bool {
		log.Infof("Checking that eth0 remains without any IPv6 address assigned...")
		ips := device.GetDeviceIPAddress("ethernet0")
		return containsIPv6(ips)
	}, time.Minute, 10*time.Second).Should(BeFalse())
}

func TestStaticIPv4Only(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()

	// Define configurable parameters available for the test.
	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
	)

	// Get parameter values set for this test execution.
	hypervisor := evetest.GetHypervisorParameterValue()

	// Set up the test harness and specify the test prerequisites.
	devName := "edge-dev"
	requiredDevice := deviceRequirementsForNetAdapterTests(devName, hypervisor)
	requiredNetModel := evetest.RequireNetworkModel{
		NetworkModel: netmodels.SingleEthWithDHCPAndIPv6,
	}
	evetest.Setup(requiredDevice, requiredNetModel,
		evetest.RequireInternetConnectivity{RequireIPv6: true})
	device := evetest.GetEdgeDevice(devName)
	evetest.Checkpoint("setup-done")

	// Build and apply the initial device configuration.
	devConfig := evetest.NewEdgeDeviceConfig(devName)
	staticNet := devConfig.AddNetwork(
		evetest.StaticNetworkConfig{
			NetworkType: evecommon.NetworkType_V4Only,
			Subnet:      evetest.IPSubnet("172.22.12.0/24"),
			Gateway:     evetest.IPAddress("172.22.12.1"),
			DNSServers:  []net.IP{evetest.IPAddress("10.16.16.25")},
		})
	devConfig.AddNetworkAdapter(
		evetest.NetworkAdapterConfig{
			LogicalLabel:  "ethernet0",
			PhysicalLabel: "eth0",
			InterfaceName: "eth0",
			NetworkUUID:   staticNet,
			Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
			StaticIP:      evetest.IPAddress("172.22.12.100"),
		})
	device.ApplyConfig(devConfig, true)
	evetest.Checkpoint("config-applied")

	log := evetest.Logger()

	log.Infof("Waiting for device to report IPv4 (only) address...")
	t.Eventually(func() bool {
		ips := device.GetDeviceIPAddress("ethernet0")
		return containsIPv4(ips) && !containsIPv6(ips)
	}, 3*time.Minute, 10*time.Second).Should(BeTrue())

	t.Consistently(func() bool {
		log.Infof("Checking that eth0 remains without any IPv6 address assigned...")
		ips := device.GetDeviceIPAddress("ethernet0")
		return containsIPv6(ips)
	}, time.Minute, 10*time.Second).Should(BeFalse())
}

const requireSCEPProxyParamKey = "REQUIRE_SCEP_PROXY"

var (
	requireSCEPProxyParam = evetest.TestParameterDefinition{
		Key:          requireSCEPProxyParamKey,
		DefaultValue: false,
		Description: "Require use of the controller-provided SCEP proxy " +
			"(unauthenticated ports are not granted direct access to the SCEP server)",
	}
)

func TestPNAC(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()

	// Define configurable parameters available for the test.
	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
		requireSCEPProxyParam,
	)

	// Get parameter values set for this test execution.
	hypervisor := evetest.GetHypervisorParameterValue()
	requireSCEPProxy := evetest.GetTestParameter[bool](requireSCEPProxyParamKey)

	// Set up the test harness and specify the test prerequisites.
	devName := "edge-dev"
	requiredDevice := deviceRequirementsForNetAdapterTests(devName, hypervisor)
	requiredNetModel := evetest.RequireNetworkModel{
		NetworkModel: netmodels.SingleEthWithPNAC(requireSCEPProxy),
	}
	evetest.Setup(requiredDevice, requiredNetModel,
		evetest.RequireInternetConnectivity{RequireIPv6: false})
	device := evetest.GetEdgeDevice(devName)
	evetest.Checkpoint("setup-done")

	// Build and apply the initial device configuration.
	devConfig := evetest.NewEdgeDeviceConfig(devName)
	dhcpNet := devConfig.AddNetwork(
		evetest.DHCPNetworkConfig{
			NetworkType: evecommon.NetworkType_V4Only,
		})
	devConfig.AddNetworkAdapter(
		evetest.NetworkAdapterConfig{
			LogicalLabel:  "ethernet0",
			PhysicalLabel: "eth0",
			InterfaceName: "eth0",
			NetworkUUID:   dhcpNet,
			Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
			PNAC: evetest.PNAC{
				Enable:                    true,
				EAPIdentity:               "evetest",
				EAPMethod:                 eveconfig.EAPMethod_EAP_METHOD_TLS,
				CertEnrollmentProfileName: "scep-test",
			},
		})
	scepServerHostname := "scep-server.test"
	if requireSCEPProxy {
		// Adam does not use DNS servers running inside SDN and therefore
		// cannot resolve hostnames defined only within the network model.
		// When the controller proxy is required, reference the SCEP server
		// directly by its IP address.
		scepServerHostname = "10.17.17.25"
	}
	devConfig.AddSCEPProfile(
		evetest.SCEPProfile{
			Name:               "scep-test",
			SCEPServerURL:      fmt.Sprintf("http://%s:8080/scep", scepServerHostname),
			UseControllerProxy: requireSCEPProxy,
			ChallengePassword:  "123456789",
			CACertsPEM:         []string{netmodels.PnacRootCACertPEM},
			CSR: evetest.CSRProfile{
				CommonName:         devName,
				Organization:       "lf-edge",
				Country:            "US",
				SanURIs:            []string{fmt.Sprintf("URN:Name:%s", devName)},
				RenewPeriodPercent: 50,
				KeyType:            eveconfig.KeyType_KEY_TYPE_RSA_2048,
				HashAlgorithm:      eveconfig.HashAlgorithm_HASH_ALGORITHM_SHA256,
			},
		})

	devUpdates, stopDevWatch := device.WatchDeviceInfo()
	defer stopDevWatch()
	devMetrics, stopDevMetricsWatch := device.WatchDeviceMetrics()
	defer stopDevMetricsWatch()
	configAppliedAt := time.Now()
	device.ApplyConfig(devConfig, true)
	evetest.Checkpoint("config-applied")

	timeout := 3 * time.Minute
	var cert *eveinfo.CertInfo
	t.Eventually(devUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"Device has enrolled certificate for 802.1x",
		func(info *eveinfo.ZInfoDevice) bool {
			if len(info.GetEnrolledCerts()) != 1 {
				return false
			}
			cert = info.GetEnrolledCerts()[0]
			return cert.GetStatus() == eveinfo.CertStatus_CERT_STATUS_AVAILABLE
		})))

	evetest.Checkpoint("cert-enrolled")

	// Verify certificate content.
	t.Expect(cert.GetCertEnrollmentProfileName()).To(Equal("scep-test"))
	t.Expect(cert.GetStatus()).To(Equal(eveinfo.CertStatus_CERT_STATUS_AVAILABLE))
	t.Expect(cert.GetErr()).To(BeNil())
	t.Expect(cert.GetRenewPeriodPercent()).To(Equal(uint32(50)))
	t.Expect(cert.GetSha256Fingerprint()).ToNot(BeEmpty())
	// Subject: CN=edge-dev, O=lf-edge, C=US
	subject := cert.GetSubject()
	t.Expect(subject.GetCommonName()).To(Equal(devName))
	t.Expect(subject.GetOrganization()).To(Equal([]string{"lf-edge"}))
	t.Expect(subject.GetCountry()).To(Equal([]string{"US"}))
	// Issuer: CN=SCEP CA, O=Example, OU=Lab, C=US
	issuer := cert.GetIssuer()
	t.Expect(issuer.GetCommonName()).To(Equal("SCEP CA"))
	t.Expect(issuer.GetOrganization()).To(Equal([]string{"Example"}))
	t.Expect(issuer.GetOrganizationalUnit()).To(Equal([]string{"Lab"}))
	t.Expect(issuer.GetCountry()).To(Equal([]string{"US"}))
	// SAN URI
	t.Expect(cert.GetSanUri()).To(Equal([]string{fmt.Sprintf("urn:Name:%s", devName)}))
	// Note: SCEP server issues certificate valid from 10 minutes ago.
	issueTime := cert.GetIssueTimestamp().AsTime()
	expirationTime := cert.GetExpirationTimestamp().AsTime()
	t.Expect(issueTime.After(configAppliedAt.Add(-11 * time.Minute))).To(BeTrue())
	t.Expect(issueTime.Before(time.Now())).To(BeTrue())
	t.Expect(expirationTime.After(time.Now())).To(BeTrue())

	dinfo := device.GetDeviceInfo()
	pnacStatus := getPNACStatus("ethernet0", dinfo)
	if pnacStatus.GetState() != eveinfo.SupplicantState_SUPPLICANT_STATE_AUTHENTICATED {
		t.Eventually(devUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
			"Device has authenticated port ethernet0",
			func(info *eveinfo.ZInfoDevice) bool {
				dinfo = info
				pnacStatus = getPNACStatus("ethernet0", dinfo)
				pnacState := pnacStatus.GetState()
				return pnacState == eveinfo.SupplicantState_SUPPLICANT_STATE_AUTHENTICATED
			})))
	}

	evetest.Checkpoint("port-authenticated")

	t.Expect(pnacStatus.Enabled).To(BeTrue())
	t.Expect(pnacStatus.Err).To(BeNil())
	lastAuthAt := pnacStatus.LastAuthTimestamp.AsTime()
	t.Expect(lastAuthAt.After(configAppliedAt)).To(BeTrue())
	t.Expect(lastAuthAt.Before(time.Now())).To(BeTrue())

	authVLANSubnet := evetest.IPSubnet("172.20.20.0/24")
	portIP := getPortIPv4Addr("ethernet0", dinfo)
	if portIP == nil || !authVLANSubnet.Contains(portIP) {
		t.Eventually(devUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
			"Device has acquired IP for ethernet0 from the authenticated VLAN",
			func(info *eveinfo.ZInfoDevice) bool {
				dinfo = info
				portIP := getPortIPv4Addr("ethernet0", dinfo)
				return portIP != nil && authVLANSubnet.Contains(portIP)
			})))
	}

	evetest.Checkpoint("ip-updated")

	var eth0PNACMetrics *evemetrics.PNACMetrics
	t.Eventually(devMetrics, timeout).Should(Receive(matchers.SatisfyPredicate(
		"Device has reported non-zero PNAC metrics",
		func(metrics *evemetrics.DeviceMetric) bool {
			pnacMetrics := metrics.GetPnacMetrics()
			if len(pnacMetrics) != 1 || pnacMetrics[0].Logicallabel != "ethernet0" {
				return false
			}
			eth0PNACMetrics = pnacMetrics[0]
			return eth0PNACMetrics.EapolFramesRx > 0 &&
				eth0PNACMetrics.EapolFramesTx > 0 &&
				eth0PNACMetrics.EapolReqFramesRx > 0 &&
				eth0PNACMetrics.EapolRespFramesTx > 0
		})))
	t.Expect(eth0PNACMetrics.EapLengthErrorFramesRx).To(BeZero())
	t.Expect(eth0PNACMetrics.InvalidEapolFramesRx).To(BeZero())

	httpServerURL := "http://http-server.test/helloworld"
	command := fmt.Sprintf("curl -sS %s", httpServerURL)
	_, stderr, err := device.RunShellScript(command, 0, 5*time.Second)
	if err != nil {
		err = fmt.Errorf("curl %s failed: %s", httpServerURL, stderr)
	}
	t.Expect(err).ToNot(HaveOccurred())
}

// getPNACStatus returns the PNAC (802.1X) status for the port with the given
// logical label from the currently active DevicePortStatus entry, or nil if
// the port is not found.
func getPNACStatus(portLL string, dinfo *eveinfo.ZInfoDevice) *eveinfo.PNACStatus {
	port := getDevicePort(portLL, dinfo)
	if port == nil {
		return nil
	}
	return port.GetPnacStatus()
}

// getPortIPv4Addr returns the first IPv4 address assigned to the port with
// the given logical label from the currently active DevicePortStatus entry,
// or nil if the port is not found or has no IPv4 address.
func getPortIPv4Addr(portLL string, dinfo *eveinfo.ZInfoDevice) net.IP {
	port := getDevicePort(portLL, dinfo)
	if port == nil {
		return nil
	}
	for _, ipStr := range port.GetIPAddrs() {
		ip := net.ParseIP(ipStr)
		if ip != nil && ip.To4() != nil {
			return ip
		}
	}
	return nil
}

// getDevicePort finds and returns the DevicePort with the given logical label
// from the currently active DevicePortStatus entry in the device info.
func getDevicePort(portLL string, dinfo *eveinfo.ZInfoDevice) *eveinfo.DevicePort {
	sa := dinfo.GetSystemAdapter()
	if sa == nil {
		return nil
	}
	statusList := sa.GetStatus()
	idx := int(sa.GetCurrentIndex())
	if idx >= len(statusList) {
		return nil
	}
	for _, port := range statusList[idx].GetPorts() {
		if port.GetName() == portLL {
			return port
		}
	}
	return nil
}

func containsIPv4(ips []net.IP) bool {
	for _, ip := range ips {
		if ip.To4() != nil {
			return true
		}
	}
	return false
}

func containsIPv6(ips []net.IP) bool {
	for _, ip := range ips {
		if ip.To4() == nil {
			return true
		}
	}
	return false
}
