package networking_test

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	eveconfig "github.com/lf-edge/eve-api/go/config"
	"github.com/lf-edge/eve-api/go/evecommon"
	eveinfo "github.com/lf-edge/eve-api/go/info"
	"github.com/lf-edge/eve/evetest"
	pillartypes "github.com/lf-edge/eve/pkg/pillar/types"
	"github.com/lf-edge/eve/tests/matchers"
	"github.com/lf-edge/eve/tests/netmodels"
)

const (
	lastResortParamKey = "LAST_RESORT_ENABLED"
)

var (
	lastResortParam = evetest.TestParameterDefinition{
		Key:          lastResortParamKey,
		DefaultValue: false,
		Description:  "Specify if last-resort configuration should be explicitly enabled in the device config",
	}
)

func deviceRequirementsForBootstrap(
	devName string, hypervisor evetest.Hypervisor) evetest.RequireEdgeDevice {
	return evetest.RequireEdgeDevice{
		Name:           devName,
		WithHypervisor: hypervisor,
		MinCPUs:        4,
		WithGrubOptions: []string{
			// No applications are deployed in network bootstrapping tests.
			// Focus on maximizing EVE performance and reducing device onboarding time.
			"set_global hv_dom0_cpu_settings \"dom0_max_vcpus=4\"",
			"set_global hv_eve_cpu_settings \"eve_max_vcpus=3\"",
			"set_global hv_ctrd_cpu_settings \"ctrd_max_vcpus=3\""},
		// We start from scratch to test device connectivity bootstrapping.
		DeviceReusePolicy: evetest.CreateFromScratchWithLiveImage,
	}
}

func TestBootstrapWithLastResort(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()

	// Define configurable parameters available for the test.
	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
		lastResortParam,
	)

	// Get parameter values set for this test execution.
	hypervisor := evetest.GetHypervisorParameterValue()
	lastResortExplicitlyEnabled := evetest.GetTestParameter[bool](lastResortParamKey)

	// Set up the test harness and specify the test prerequisites.
	devName := "edge-dev"
	requiredDevice := deviceRequirementsForBootstrap(devName, hypervisor)
	requiredNetModel := evetest.RequireNetworkModel{
		NetworkModel: netmodels.SingleEthWithDHCP,
	}
	evetest.Setup(requiredDevice, requiredNetModel)

	// If we got here, device was able to bootstrap controller connectivity using
	// the bootstrap config or override.json.
	device := evetest.GetEdgeDevice(devName)
	devUpdates, stopDevWatch := device.WatchDeviceInfo()
	defer stopDevWatch()
	evetest.Checkpoint("setup-done")

	// Apply the initial device configuration.
	devConfig := evetest.NewEdgeDeviceConfig(devName)
	if lastResortExplicitlyEnabled {
		cfgProps := pillartypes.NewConfigItemValueMap()
		cfgProps.SetGlobalValueTriState(
			pillartypes.NetworkFallbackAnyEth, pillartypes.TS_ENABLED)
		devConfig.SetConfigProperties(cfgProps)
	}
	dhcpNet := devConfig.AddNetwork(
		evetest.DHCPNetworkConfig{
			NetworkType: evecommon.NetworkType_V4,
		})
	devConfig.AddNetworkAdapter(
		evetest.NetworkAdapterConfig{
			LogicalLabel:  "eth0",
			PhysicalLabel: "eth0",
			InterfaceName: "eth0",
			NetworkUUID:   dhcpNet,
			Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
		})
	device.ApplyConfig(devConfig, true)
	evetest.Checkpoint("config-applied")

	// Wait for device info to report the expected DevicePortStatus list.
	var expectedKeys []string
	if lastResortExplicitlyEnabled {
		expectedKeys = []string{"zedagent", "lastresort"}
	} else {
		// Last-resort was used only initially and once controller connectivity
		// was working it got removed from DPCL.
		expectedKeys = []string{"zedagent"}
	}
	timeout := 3 * time.Minute
	t.Eventually(devUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"Device has applied and reported expected network configuration",
		func(dinfo *eveinfo.ZInfoDevice) bool {
			return matchSystemAdapterInfo(dinfo.GetSystemAdapter(), 0, expectedKeys)
		})))
}

const useOverrideJSONParamKey = "USE_OVERRIDE_JSON"

var (
	useOverrideJSONParam = evetest.TestParameterDefinition{
		Key:          useOverrideJSONParamKey,
		DefaultValue: false,
		Description:  "Specify if static IP config should be injected using override.json",
	}
)

func TestBootstrapWithStaticIP(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()

	// Define configurable parameters available for the test.
	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
		useOverrideJSONParam,
	)

	// Get parameter values set for this test execution.
	hypervisor := evetest.GetHypervisorParameterValue()
	useOverrideJson := evetest.GetTestParameter[bool](useOverrideJSONParamKey)

	// Build bootstrap configuration.
	devName := "edge-dev"
	bootstrapConfig := evetest.NewEdgeDeviceConfig(devName)
	staticNet := bootstrapConfig.AddNetwork(
		// matches netmodels.SingleEthWithoutDHCP
		evetest.StaticNetworkConfig{
			NetworkType: evecommon.NetworkType_V4,
			Subnet:      evetest.IPSubnet("172.20.20.0/24"),
			Gateway:     evetest.IPAddress("172.20.20.1"),
			DNSServers:  []net.IP{evetest.IPAddress("10.16.16.25")},
		})
	bootstrapConfig.AddNetworkAdapter(
		evetest.NetworkAdapterConfig{
			LogicalLabel:  "eth0",
			PhysicalLabel: "eth0",
			InterfaceName: "eth0",
			NetworkUUID:   staticNet,
			// StaticIP matches netmodels.SingleEthWithoutDHCP
			StaticIP: evetest.IPAddress("172.20.20.10"),
			Usage:    evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
		})

	// Set up the test harness and specify test prerequisites.
	requiredDevice := deviceRequirementsForBootstrap(devName, hypervisor)
	if useOverrideJson {
		requiredDevice.WithInjectedNetworkOverride = &pillartypes.DevicePortConfig{
			Version:      1,
			Key:          "",
			TimePriority: time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC),
			Ports: []pillartypes.NetworkPortConfig{
				{
					IfName: "eth0",
					IsMgmt: true,
					DhcpConfig: pillartypes.DhcpConfig{
						Dhcp: pillartypes.DhcpTypeStatic,
						// IP config matches netmodels.SingleEthWithoutDHCP
						AddrSubnet: "172.20.20.10/24",
						Gateway:    evetest.IPAddress("172.20.20.1"),
						DNSServers: []net.IP{evetest.IPAddress("10.16.16.25")},
						Type:       pillartypes.NetworkTypeIPv4,
					},
				},
			},
		}
	} else {
		requiredDevice.WithInjectedBootstrapConfig = bootstrapConfig
	}
	requiredNetModel := evetest.RequireNetworkModel{
		NetworkModel: netmodels.SingleEthWithoutDHCP,
	}
	evetest.Setup(requiredDevice, requiredNetModel)

	// If we got here, device was able to bootstrap controller connectivity using
	// the bootstrap config or override.json.
	device := evetest.GetEdgeDevice(devName)
	devUpdates, stopDevWatch := device.WatchDeviceInfo()
	defer stopDevWatch()
	evetest.Checkpoint("setup-done")

	// Apply the same bootstrap configuration also through the controller.
	device.ApplyConfig(bootstrapConfig, true)
	evetest.Checkpoint("config-applied")

	// Neither bootstrap config nor override.json remain persisted after
	// the controller connectivity was established.
	timeout := 3 * time.Minute
	t.Eventually(devUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"Device has applied and reported expected network configuration",
		func(dinfo *eveinfo.ZInfoDevice) bool {
			return matchSystemAdapterInfo(dinfo.GetSystemAdapter(), 0, []string{"zedagent"})
		})))
}

// ProxyConfigType is used as a configurable parameter for TestBootstrapWithProxy.
type ProxyConfigType int

const (
	ProxyConfigUndefined ProxyConfigType = iota
	ProxyConfigManual
	ProxyConfigTransparent
	ProxyConfigAutoDiscovery
	ProxyConfigWPADURL
	ProxyConfigPACScript
)

func (pc ProxyConfigType) String() string {
	switch pc {
	case ProxyConfigManual:
		return "manual"
	case ProxyConfigTransparent:
		return "transparent"
	case ProxyConfigAutoDiscovery:
		return "autodiscovery"
	case ProxyConfigWPADURL:
		return "wpad-url"
	case ProxyConfigPACScript:
		return "pac-script"
	case ProxyConfigUndefined:
		fallthrough
	default:
		return "undefined"
	}
}

func (pc *ProxyConfigType) FromString(s string) error {
	switch strings.ToLower(s) {
	case "manual":
		*pc = ProxyConfigManual
	case "transparent":
		*pc = ProxyConfigTransparent
	case "autodiscovery":
		*pc = ProxyConfigAutoDiscovery
	case "wpad-url":
		*pc = ProxyConfigWPADURL
	case "pac-script":
		*pc = ProxyConfigPACScript
	case "", "undefined":
		*pc = ProxyConfigUndefined
	default:
		return fmt.Errorf("invalid PROXY_CONFIG_TYPE: %q", s)
	}
	return nil
}

const proxyConfigTypeParamKey = "PROXY_CONFIG_TYPE"

var (
	proxyConfigTypeParam = evetest.TestParameterDefinition{
		Key:          proxyConfigTypeParamKey,
		DefaultValue: ProxyConfigManual,
		Description:  "Specify the type of the HTTP proxy to deploy for testing",
	}
)

func TestBootstrapWithProxy(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()

	// Define configurable parameters available for the test.
	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
		useOverrideJSONParam,
		proxyConfigTypeParam,
	)

	// Get parameter values set for this test execution.
	hypervisor := evetest.GetHypervisorParameterValue()
	useOverrideJson := evetest.GetTestParameter[bool](useOverrideJSONParamKey)
	proxyConfigType := evetest.GetTestParameter[ProxyConfigType](proxyConfigTypeParamKey)

	// Build bootstrap configuration.
	devName := "edge-dev"
	bootstrapConfig := evetest.NewEdgeDeviceConfig(devName)
	var proxyConfig evetest.ProxyConfig
	switch proxyConfigType {
	case ProxyConfigManual:
		proxyConfig = evetest.ManualProxyConfig{
			Proxies: []evetest.ProxyServer{
				{
					Proto:   evecommon.ProxyProto_PROXY_HTTP,
					Address: "http://http-proxy.test",
					Port:    9090,
				},
				{
					Proto:   evecommon.ProxyProto_PROXY_HTTPS,
					Address: "http://http-proxy.test",
					Port:    9091,
				},
			},
			ProxyCertsPEM: []string{netmodels.ProxyCACertPEM},
		}
	case ProxyConfigTransparent:
		proxyConfig = evetest.TransparentProxyConfig{
			ProxyCertsPEM: []string{netmodels.ProxyCACertPEM},
		}
	case ProxyConfigAutoDiscovery:
		proxyConfig = evetest.ProxyAutoDiscoveryConfig{
			ProxyCertsPEM: []string{netmodels.ProxyCACertPEM},
		}
	default:
		evetestT.Skipf("PROXY_CONFIG_TYPE %s is not (yet) covered by the test",
			proxyConfigType)
	}
	dhcpNetWithProxy := bootstrapConfig.AddNetwork(
		evetest.DHCPNetworkConfig{
			NetworkType: evecommon.NetworkType_V4,
			ProxyConfig: proxyConfig,
		})
	bootstrapConfig.AddNetworkAdapter(
		evetest.NetworkAdapterConfig{
			LogicalLabel:  "eth0",
			PhysicalLabel: "eth0",
			InterfaceName: "eth0",
			NetworkUUID:   dhcpNetWithProxy,
			Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
		})

	// Set up the test harness and specify test prerequisites.
	requiredDevice := deviceRequirementsForBootstrap(devName, hypervisor)
	if useOverrideJson {
		var proxyConfig pillartypes.ProxyConfig
		switch proxyConfigType {
		case ProxyConfigManual:
			proxyConfig = pillartypes.ProxyConfig{
				Proxies: []pillartypes.ProxyEntry{
					{
						Type:   pillartypes.NetworkProxyTypeHTTP,
						Server: "http://http-proxy.test",
						Port:   9090,
					},
					{
						Type:   pillartypes.NetworkProxyTypeHTTPS,
						Server: "http://http-proxy.test",
						Port:   9091,
					},
				},
				Exceptions: "github.com", // this is blocked by the proxy
				ProxyCertPEM: [][]byte{
					[]byte(netmodels.ProxyCACertPEM),
				},
			}
		case ProxyConfigTransparent:
			proxyConfig = pillartypes.ProxyConfig{
				ProxyCertPEM: [][]byte{
					[]byte(netmodels.ProxyCACertPEM),
				},
			}
		case ProxyConfigAutoDiscovery:
			proxyConfig = pillartypes.ProxyConfig{
				NetworkProxyEnable: true,
				ProxyCertPEM: [][]byte{
					[]byte(netmodels.ProxyCACertPEM),
				},
			}
		default:
			evetestT.Skipf("PROXY_CONFIG_TYPE %s is not (yet) covered by the test",
				proxyConfigType)
		}
		requiredDevice.WithInjectedNetworkOverride = &pillartypes.DevicePortConfig{
			Version:      1,
			Key:          "",
			TimePriority: time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC),
			Ports: []pillartypes.NetworkPortConfig{
				{
					IfName: "eth0",
					IsMgmt: true,
					DhcpConfig: pillartypes.DhcpConfig{
						Dhcp: pillartypes.DhcpTypeClient,
						Type: pillartypes.NetworkTypeIPv4,
					},
					ProxyConfig: proxyConfig,
				},
			},
		}
	} else {
		requiredDevice.WithInjectedBootstrapConfig = bootstrapConfig
	}
	var requiredNetModel evetest.RequireNetworkModel
	switch proxyConfigType {
	case ProxyConfigManual:
		requiredNetModel = evetest.RequireNetworkModel{
			NetworkModel: netmodels.SingleEthWithDHCPAndExplicitProxy,
		}
	case ProxyConfigTransparent:
		requiredNetModel = evetest.RequireNetworkModel{
			NetworkModel: netmodels.SingleEthWithDHCPAndTransparentProxy,
		}
	case ProxyConfigAutoDiscovery:
		requiredNetModel = evetest.RequireNetworkModel{
			NetworkModel: netmodels.SingleEthWithDHCPAndAutoDiscoveredProxy,
		}
	default:
		evetestT.Skipf("PROXY_CONFIG_TYPE %s is not (yet) covered by the test",
			proxyConfigType)
	}
	evetest.Setup(requiredDevice, requiredNetModel)

	// If we got here, device was able to bootstrap controller connectivity using
	// the bootstrap config or override.json.
	device := evetest.GetEdgeDevice(devName)
	devUpdates, stopDevWatch := device.WatchDeviceInfo()
	defer stopDevWatch()
	evetest.Checkpoint("setup-done")

	// Apply the same bootstrap configuration also through the controller.
	device.ApplyConfig(bootstrapConfig, true)
	evetest.Checkpoint("config-applied")

	// Neither bootstrap config nor override.json remain persisted after
	// the controller connectivity was established.
	timeout := 3 * time.Minute
	t.Eventually(devUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"Device has applied and reported expected network configuration",
		func(dinfo *eveinfo.ZInfoDevice) bool {
			return matchSystemAdapterInfo(dinfo.GetSystemAdapter(), 0, []string{"zedagent"})
		})))
}

func TestBootstrapWithMgmtVLAN(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()

	// Define configurable parameters available for the test.
	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
		useOverrideJSONParam,
	)

	// Get parameter values set for this test execution.
	hypervisor := evetest.GetHypervisorParameterValue()
	useOverrideJson := evetest.GetTestParameter[bool](useOverrideJSONParamKey)

	// Build bootstrap configuration.
	devName := "edge-dev"
	bootstrapConfig := evetest.NewEdgeDeviceConfig(devName)
	dhcpNet := bootstrapConfig.AddNetwork(
		evetest.DHCPNetworkConfig{
			NetworkType: evecommon.NetworkType_V4,
		})
	bootstrapConfig.AddNetworkAdapter(
		evetest.NetworkAdapterConfig{
			LogicalLabel:  "ethernet0",
			PhysicalLabel: "eth0",
			InterfaceName: "eth0",
		})
	bootstrapConfig.AddVLANSubinterface(
		evetest.VLANSubinterfaceConfig{
			LogicalLabel:       "mgmt-vlan",
			InterfaceName:      "vlan100",
			ParentLogicalLabel: "ethernet0",
			VlanID:             100,
			NetworkUUID:        dhcpNet,
			Usage:              evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
		})

	// Set up the test harness and specify test prerequisites.
	requiredDevice := deviceRequirementsForBootstrap(devName, hypervisor)
	if useOverrideJson {
		requiredDevice.WithInjectedNetworkOverride = &pillartypes.DevicePortConfig{
			Version:      1,
			Key:          "",
			TimePriority: time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC),
			Ports: []pillartypes.NetworkPortConfig{
				{
					IfName:       "eth0",
					Logicallabel: "ethernet0",
					IsMgmt:       false,
					IsL3Port:     false,
				},
				{
					IfName:       "vlan100",
					Logicallabel: "mgmt-vlan",
					IsMgmt:       true,
					IsL3Port:     true,
					DhcpConfig: pillartypes.DhcpConfig{
						Dhcp: pillartypes.DhcpTypeClient,
						Type: pillartypes.NetworkTypeIPv4,
					},
					L2LinkConfig: pillartypes.L2LinkConfig{
						L2Type: pillartypes.L2LinkTypeVLAN,
						VLAN: pillartypes.VLANConfig{
							ParentPort: "ethernet0",
							ID:         100,
						},
					},
				},
			},
		}
	} else {
		requiredDevice.WithInjectedBootstrapConfig = bootstrapConfig
	}
	requiredNetModel := evetest.RequireNetworkModel{
		NetworkModel: netmodels.SingleEthWithMgmtAndAppVLANs,
	}
	evetest.Setup(requiredDevice, requiredNetModel)

	// If we got here, device was able to bootstrap controller connectivity using
	// the bootstrap config or override.json.
	device := evetest.GetEdgeDevice(devName)
	devUpdates, stopDevWatch := device.WatchDeviceInfo()
	defer stopDevWatch()
	evetest.Checkpoint("setup-done")

	// Apply the same bootstrap configuration also through the controller.
	device.ApplyConfig(bootstrapConfig, true)
	evetest.Checkpoint("config-applied")

	// Neither bootstrap config nor override.json remain persisted after
	// the controller connectivity was established.
	timeout := 3 * time.Minute
	t.Eventually(devUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"Device has applied and reported expected network configuration",
		func(dinfo *eveinfo.ZInfoDevice) bool {
			return matchSystemAdapterInfo(dinfo.GetSystemAdapter(), 0, []string{"zedagent"})
		})))
}

func TestBootstrapWithLACPBond(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()

	// Define configurable parameters available for the test.
	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
		useOverrideJSONParam,
	)

	// Get parameter values set for this test execution.
	hypervisor := evetest.GetHypervisorParameterValue()
	useOverrideJson := evetest.GetTestParameter[bool](useOverrideJSONParamKey)

	// Build bootstrap configuration with an LACP bond.
	// With bootstrap config, EVE creates the bond at boot time, before
	// attempting to reach the controller. This avoids the chicken-and-egg
	// problem where individual ports cannot pass traffic through an SDN-side
	// LACP bond without LACP negotiation.
	devName := "edge-dev"
	bootstrapConfig := evetest.NewEdgeDeviceConfig(devName)
	dhcpNet := bootstrapConfig.AddNetwork(
		evetest.DHCPNetworkConfig{
			NetworkType: evecommon.NetworkType_V4,
		})
	bootstrapConfig.AddNetworkAdapter(
		evetest.NetworkAdapterConfig{
			LogicalLabel:  "ethernet0",
			PhysicalLabel: "eth0",
			InterfaceName: "eth0",
			Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
		})
	bootstrapConfig.AddNetworkAdapter(
		evetest.NetworkAdapterConfig{
			LogicalLabel:  "ethernet1",
			PhysicalLabel: "eth1",
			InterfaceName: "eth1",
			Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
		})
	bootstrapConfig.AddBond(
		evetest.BondConfig{
			LogicalLabel:  "lacp-bond",
			InterfaceName: "bond1",
			MemberLabels:  []string{"ethernet0", "ethernet1"},
			BondMode:      evecommon.BondMode_BOND_MODE_802_3AD,
			LACPRate:      evecommon.LacpRate_LACP_RATE_FAST,
			MIIMonitor: &eveconfig.MIIMonitor{
				Interval: 100,
			},
			NetworkUUID: dhcpNet,
			Usage:       evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
		})

	// Set up the test harness and specify test prerequisites.
	requiredDevice := deviceRequirementsForBootstrap(devName, hypervisor)
	if useOverrideJson {
		requiredDevice.WithInjectedNetworkOverride = &pillartypes.DevicePortConfig{
			Version:      1,
			Key:          "",
			TimePriority: time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC),
			Ports: []pillartypes.NetworkPortConfig{
				{
					IfName:       "eth0",
					Logicallabel: "ethernet0",
					IsMgmt:       false,
					IsL3Port:     false,
				},
				{
					IfName:       "eth1",
					Logicallabel: "ethernet1",
					IsMgmt:       false,
					IsL3Port:     false,
				},
				{
					IfName:       "bond1",
					Logicallabel: "lacp-bond",
					IsMgmt:       true,
					IsL3Port:     true,
					DhcpConfig: pillartypes.DhcpConfig{
						Dhcp: pillartypes.DhcpTypeClient,
						Type: pillartypes.NetworkTypeIPv4,
					},
					L2LinkConfig: pillartypes.L2LinkConfig{
						L2Type: pillartypes.L2LinkTypeBond,
						Bond: pillartypes.BondConfig{
							AggregatedPorts: []string{"ethernet0", "ethernet1"},
							Mode:            pillartypes.BondMode802Dot3AD,
							LacpRate:        pillartypes.LacpRateFast,
							MIIMonitor: pillartypes.BondMIIMonitor{
								Enabled:  true,
								Interval: 100,
							},
						},
					},
				},
			},
		}
	} else {
		requiredDevice.WithInjectedBootstrapConfig = bootstrapConfig
	}

	requiredNetModel := evetest.RequireNetworkModel{
		NetworkModel: netmodels.TwoMgmtPortsWithLACPBond,
	}
	evetest.Setup(requiredDevice, requiredNetModel)

	// If we got here, device was able to bootstrap controller connectivity using
	// the bootstrap config or override.json.
	device := evetest.GetEdgeDevice(devName)
	devUpdates, stopDevWatch := device.WatchDeviceInfo()
	defer stopDevWatch()
	evetest.Checkpoint("setup-done")

	// Apply the same bootstrap configuration also through the controller.
	device.ApplyConfig(bootstrapConfig, true)
	evetest.Checkpoint("config-applied")

	// Neither bootstrap config nor override.json remain persisted after
	// the controller connectivity was established.
	timeout := 3 * time.Minute
	t.Eventually(devUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"Device has applied and reported expected network configuration",
		func(dinfo *eveinfo.ZInfoDevice) bool {
			return matchSystemAdapterInfo(dinfo.GetSystemAdapter(), 0, []string{"zedagent"})
		})))
}

// matchSystemAdapterInfo checks that the SystemAdapterInfo has the expected
// currentIndex and that the DevicePortStatus entries match the expected keys
// (in order).
func matchSystemAdapterInfo(sa *eveinfo.SystemAdapterInfo,
	expectedIndex uint32, expectedKeys []string) bool {
	if sa == nil {
		return false
	}
	if sa.GetCurrentIndex() != expectedIndex {
		return false
	}
	status := sa.GetStatus()
	if len(status) != len(expectedKeys) {
		return false
	}
	for i, key := range expectedKeys {
		if status[i].GetKey() != key {
			return false
		}
	}
	return true
}
