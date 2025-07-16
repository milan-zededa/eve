package networking_test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	eveconfig "github.com/lf-edge/eve-api/go/config"
	"github.com/lf-edge/eve-api/go/evecommon"
	"github.com/lf-edge/eve/evetest"
	"github.com/lf-edge/eve/tests/netmodels"
)

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
	requiredDevice := evetest.RequireEdgeDevice{
		Name:              devName,
		WithHypervisor:    hypervisor,
		DeviceReusePolicy: evetest.RebootEdgeDevice,
	}
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
	device.ApplyConfig(context.Background(), devConfig, true)
	evetest.Checkpoint("config-applied")

	t.Consistently(func() bool {
		ips := device.GetDeviceIPAddress("ethernet0")
		return containsIPv6(ips)
	}).To(BeFalse(), 25*time.Second, 5*time.Second)
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
	requiredDevice := evetest.RequireEdgeDevice{
		Name:              devName,
		WithHypervisor:    hypervisor,
		DeviceReusePolicy: evetest.RebootEdgeDevice,
	}
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
	device.ApplyConfig(context.Background(), devConfig, true)
	evetest.Checkpoint("config-applied")

	t.Consistently(func() bool {
		ips := device.GetDeviceIPAddress("ethernet0")
		return containsIPv6(ips)
	}).To(BeFalse(), 25*time.Second, 5*time.Second)
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
	requiredDevice := evetest.RequireEdgeDevice{
		Name:              devName,
		WithHypervisor:    hypervisor,
		DeviceReusePolicy: evetest.CreateFromScratchWithLiveImage,
	}
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
	devConfig.AddSCEPProfile(
		evetest.SCEPProfile{
			Name:               "scep-test",
			SCEPServerURL:      "http://scep-server.test:8080/scep",
			UseControllerProxy: requireSCEPProxy,
			ChallengePassword:  "123456789",
			CACertsPEM:         []string{netmodels.PnacCACertPEM},
			CSR: evetest.CSRProfile{
				CommonName:         devName,
				Organization:       "lf-edge",
				Country:            "US",
				SanURIs:            []string{fmt.Sprintf("URN:Name:%s", devName)},
				RenewPeriodPercent: 50,
				KeyType:            eveconfig.KeyType_KEY_TYPE_ECDSA_P521,
				HashAlgorithm:      eveconfig.HashAlgorithm_HASH_ALGORITHM_SHA512,
			},
		})
	device.ApplyConfig(context.Background(), devConfig, true)
	evetest.Checkpoint("config-applied")

	// TODO: This must be run on the EVE side:
	// wpa_supplicant -i eth0 -Dwired -c /persist/wpa_supplicant.conf -d

	// TODO: more checks (cert status, pnac status, etc.)
	log := evetest.Logger()
	log.Infof("Waiting for EVE to authenticate eth0 and enable Internet connectivity")
	t.Eventually(func() error {
		stdout, _, err := device.RunShellScript("ping -c 3 8.8.8.8", 0)
		if err != nil {
			err = fmt.Errorf("ping 8.8.8.8 failed: %s", stdout)
			log.Error(err)
		} else {
			log.Infof("ping 8.8.8.8 succeeded")
		}
		return err
	}).WithTimeout(25 * time.Second).WithPolling(5 * time.Second).Should(Succeed())
}

func containsIPv6(ips []net.IP) bool {
	for _, ip := range ips {
		if ip.To16() != nil {
			return true
		}
	}
	return false
}
