// Test creating, changing, deleting NI. Try to run traffic etc.

package networking_test

import (
	"net"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/lf-edge/eve-api/go/evecommon"
	eveinfo "github.com/lf-edge/eve-api/go/info"
	evemetrics "github.com/lf-edge/eve-api/go/metrics"
	"github.com/lf-edge/eve/evetest"
	"github.com/lf-edge/eve/pkg/pillar/types"
	"github.com/lf-edge/eve/pkg/pillar/utils/generics"
	"github.com/lf-edge/eve/tests/matchers"
	"github.com/lf-edge/eve/tests/netmodels"
)

func TestLocalNI(test *testing.T) {
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
		DeviceReusePolicy: evetest.ResetDeviceConfig,
	}
	requiredNetModel := evetest.RequireNetworkModel{
		NetworkModel: netmodels.SingleEthWithDHCP,
	}
	evetest.Setup(requiredDevice, requiredNetModel)
	device := evetest.GetEdgeDevice(devName)
	evetest.Checkpoint("setup-done")

	// Build and apply the initial device configuration, without including any
	// network instances for now.
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

	// Try to create local network instance.
	niUUID := devConfig.AddNetworkInstance(evetest.LocalNetworkInstanceConfig{
		DisplayName: "local-ni",
		Port:        "ethernet0",
		Subnet:      evetest.IPSubnet("10.11.12.0/24"),
		DHCPRange: types.IPRange{
			Start: evetest.IPAddress("10.11.12.2"),
			End:   evetest.IPAddress("10.11.12.254"),
		},
		Gateway:       evetest.IPAddress("10.11.12.1"),
		EnableFlowlog: false,
		MTU:           1500,
		ForwardLLDP:   false,
	})
	niUpdates, stopNIWatch := device.WatchNetworkInstanceInfo(niUUID)
	device.ApplyConfig(devConfig, false)

	timeout := 3 * time.Minute
	var niInfo *eveinfo.ZInfoNetworkInstance
	// Do not stop monitoring the Network Instance state after an error
	// (StopIf(niHasError) is intentionally not used).
	// NI may enter a temporary error condition due to race conditions
	// between zedrouter and NIM, but this is expected to eventually resolve.
	t.Eventually(niUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"NI state is ONLINE",
		func(info *eveinfo.ZInfoNetworkInstance) bool {
			niInfo = info
			return info.State == eveinfo.ZNetworkInstanceState_ZNETINST_STATE_ONLINE
		})))

	evetest.Checkpoint("ni-created")

	t.Expect(niInfo.NetworkID).To(Equal(niUUID.String()))
	t.Expect(niInfo.Displayname).To(Equal("local-ni"))
	t.Expect(niInfo.Activated).To(BeTrue())
	t.Expect(niInfo.NetworkErr).To(BeEmpty())
	t.Expect(niInfo.Ports).To(HaveLen(1))
	t.Expect(niInfo.Ports[0]).To(Equal("ethernet0"))
	t.Expect(niInfo.BridgeIPAddr).To(Equal("10.11.12.1"))
	t.Expect(niInfo.IpAssignments).To(HaveLen(1))
	t.Expect(niInfo.IpAssignments[0].IpAddress).To(HaveLen(1))
	t.Expect(niInfo.IpAssignments[0].IpAddress[0]).To(Equal("10.11.12.1"))
	t.Expect(niInfo.AssignedAdapters).To(HaveLen(1))
	t.Expect(niInfo.AssignedAdapters[0].Name).To(Equal("ethernet0"))
	t.Expect(niInfo.AssignedAdapters[0].Type).To(Equal(evecommon.PhyIoType_PhyIoNetEth))
	t.Expect(niInfo.BridgeName).To(Equal("bn1"))
	t.Expect(niInfo.BridgeNum).To(BeEquivalentTo(1))
	t.Expect(niInfo.InstType).To(BeEquivalentTo(2))
	t.Expect(niInfo.Mtu).To(BeEquivalentTo(1500))
	t.Expect(niInfo.Vifs).To(BeEmpty())
	t.Expect(niInfo.IpRoutes).To(HaveLen(2))
	// Routes are returned by EVE in deterministic and therefore easy-to-test order.
	t.Expect(niInfo.IpRoutes[0].DestinationNetwork).To(Equal("0.0.0.0/0"))
	t.Expect(niInfo.IpRoutes[0].Gateway).To(Equal("172.20.20.1"))
	t.Expect(niInfo.IpRoutes[0].Port).To(Equal("ethernet0"))
	t.Expect(niInfo.IpRoutes[1].DestinationNetwork).To(Equal("172.20.20.0/24"))
	t.Expect(niInfo.IpRoutes[1].Gateway).To(Equal(""))
	t.Expect(niInfo.IpRoutes[1].Port).To(Equal("ethernet0"))

	// Try to update network instance - change IP subnet.
	devConfig.UpdateNetworkInstance(niUUID, evetest.LocalNetworkInstanceConfig{
		DisplayName: "local-ni",
		Port:        "ethernet0",
		Subnet:      evetest.IPSubnet("10.11.13.0/24"),
		DHCPRange: types.IPRange{
			Start: evetest.IPAddress("10.11.13.2"),
			End:   evetest.IPAddress("10.11.13.254"),
		},
		Gateway:       evetest.IPAddress("10.11.13.1"),
		EnableFlowlog: false,
		MTU:           1500,
		ForwardLLDP:   false,
	})
	device.ApplyConfig(devConfig, false)

	t.Eventually(niUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"NI bridgeIP is 10.11.13.1",
		func(info *eveinfo.ZInfoNetworkInstance) bool {
			niInfo = info
			return info.BridgeIPAddr == "10.11.13.1"
		}).StopIf(niHasError)))

	evetest.Checkpoint("ni-updated")

	t.Expect(niInfo.Activated).To(BeTrue())
	t.Expect(niInfo.NetworkErr).To(BeEmpty())
	t.Expect(niInfo.BridgeIPAddr).To(Equal("10.11.13.1"))
	t.Expect(niInfo.IpAssignments).To(HaveLen(1))
	t.Expect(niInfo.IpAssignments[0].IpAddress).To(HaveLen(1))
	t.Expect(niInfo.IpAssignments[0].IpAddress[0]).To(Equal("10.11.13.1"))

	// Try to delete the network instance.
	devConfig.DeleteNetworkInstance(niUUID)
	device.ApplyConfig(devConfig, false)

	t.Eventually(niUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"NI state is UNSPECIFIED",
		func(info *eveinfo.ZInfoNetworkInstance) bool {
			niInfo = info
			return info.State == eveinfo.ZNetworkInstanceState_ZNETINST_STATE_UNSPECIFIED
		}).StopIf(niHasError)))
	stopNIWatch()

	evetest.Checkpoint("ni-deleted")

	// Create NI again, this time with an app connected to it.
	subnet := evetest.IPSubnet("10.11.12.0/24")
	niUUID = devConfig.AddNetworkInstance(evetest.LocalNetworkInstanceConfig{
		DisplayName: "local-ni",
		Port:        "ethernet0",
		Subnet:      subnet,
		DHCPRange: types.IPRange{
			Start: evetest.IPAddress("10.11.12.2"),
			End:   evetest.IPAddress("10.11.12.254"),
		},
		Gateway:       evetest.IPAddress("10.11.12.1"),
		EnableFlowlog: true,
		MTU:           1500,
		ForwardLLDP:   false,
	})

	const appMACAddr = "02:16:3e:00:00:01"
	appUUID := devConfig.AddApplication(evetest.ApplicationInstanceConfig{
		DisplayName: "container-app",
		Activate:    true,
		Image: evetest.DockerContainer{
			ImageName: "milan4zededa/evetest-ubuntu-ctr",
			Tag:       "1.0",
		},
		CPUs:        1,
		MemoryBytes: 500 * evetest.MB,
		NetworkAdapters: []evetest.AppNetworkAdapter{
			evetest.VirtualNetworkAdapter{
				LogicalLabel:        "vif0",
				NetworkInstanceUUID: niUUID,
				MAC:                 evetest.MACAddress(appMACAddr),
				PortFwdRules: []evetest.PortFwdRule{
					{
						Protocol:     evetest.NetworkProtocolTCP,
						EdgeNodePort: 2222,
						AppPort:      22,
					},
				},
				ACLAllowRules: []evetest.ACLAllowRule{
					{
						Protocol:     evetest.NetworkProtocolAny,
						RemoteSubnet: evetest.IPSubnet("0.0.0.0/0"),
					},
				},
			},
		},
	})

	niUpdates, stopNIWatch = device.WatchNetworkInstanceInfo(niUUID)
	appUpdates, stopAppWatch := device.WatchAppInfo(appUUID)
	device.ApplyConfig(devConfig, false)

	timeoutExcludingDownload := 5 * time.Minute
	device.WaitUntilAppIsRunning(appUUID, timeoutExcludingDownload)

	evetest.Checkpoint("ni-with-app-created")

	// Wait until application receives IP address from the NI subnet.
	var appInfo *eveinfo.ZInfoApp
	t.Eventually(appUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"App receives IP address",
		func(info *eveinfo.ZInfoApp) bool {
			appInfo = info
			return len(appInfo.Network) == 1 && len(appInfo.Network[0].IPAddrs) == 1
		}).StopIf(appHasError)))
	t.Expect(appInfo.Network).To(HaveLen(1))
	t.Expect(appInfo.Network[0].DevName).To(Equal("vif0"))
	t.Expect(appInfo.Network[0].MacAddr).To(Equal(appMACAddr))
	t.Expect(appInfo.Network[0].IPAddrs).To(HaveLen(1))
	appIP := evetest.IPAddress(appInfo.Network[0].IPAddrs[0])
	t.Expect(subnet.Contains(appIP)).To(BeTrue())
	t.Expect(appInfo.Network[0].DefaultRouters).To(HaveLen(1))
	t.Expect(appInfo.Network[0].DefaultRouters[0]).To(Equal("10.11.12.1"))
	t.Expect(appInfo.Network[0].NtpServers).To(BeEmpty())
	t.Expect(appInfo.Network[0].NetworkErr).To(BeNil())
	t.Expect(appInfo.Network[0].Ipv4Up).To(BeTrue())
	t.Expect(appInfo.Network[0].IpAddrMisMatch).To(BeFalse())

	// Confirm that application IP address is (eventually) reported in the network
	// instance status.
	t.Eventually(niUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"App IP is reported inside the NI status",
		func(info *eveinfo.ZInfoNetworkInstance) bool {
			niInfo = info
			if len(niInfo.Vifs) == 0 || len(niInfo.IpAssignments) == 0 {
				return false
			}
			for _, ipAssignment := range niInfo.IpAssignments {
				if ipAssignment.MacAddress == appMACAddr {
					return generics.ContainsItem(ipAssignment.IpAddress, appIP.String())
				}
			}
			return false
		}).StopIf(niHasError)))
	t.Expect(niInfo.Vifs).To(HaveLen(1))
	t.Expect(niInfo.Vifs[0].VifName).To(Equal("nbu1x1"))
	t.Expect(niInfo.Vifs[0].MacAddress).To(Equal(appMACAddr))
	t.Expect(niInfo.Vifs[0].AppID).To(Equal(appUUID.String()))

	niMetricsUpdates, stopNIMetricsWatch := device.WatchNetworkInstanceMetrics(niUUID)

	// Test port forwarding.
	// RunShellScriptInsideApp will try to use the 2222->22 port forwarding rule.
	appAuth := evetest.UsernamePasswordAuth{
		Username: "root",
		Password: "testpassword",
	}
	sshTimeout := 20 * time.Second
	polling := 3 * time.Second
	log := evetest.Logger()
	log.Infof("Testing port forwarding")
	t.Eventually(func(t Gomega) {
		log.Infof("Waiting for app SSH daemon to start and become reachable...")
		output, _, err := device.RunShellScriptInsideApp(appUUID, appAuth,
			"hostname", sshTimeout, 0)
		t.Expect(err).ToNot(HaveOccurred())
		t.Expect(output).To(ContainSubstring(appUUID.String()))
	}, timeout, polling).Should(Succeed())

	// Test DNS provided by the Local NI.
	log.Infof("Testing DNS resolution from inside the application")
	output, _, err := device.RunShellScriptInsideApp(appUUID, appAuth,
		"nslookup "+evetest.GetControllerHostname(), sshTimeout, 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(output).To(ContainSubstring(evetest.GetControllerIPv4().String()))

	// Test application connectivity initiated from inside the application.
	log.Infof("Testing application connectivity")
	output, _, err = device.RunShellScriptInsideApp(appUUID, appAuth,
		"curl -sS http://http-server.test/helloworld", sshTimeout, 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(output).To(ContainSubstring("Hello world!"))

	// Check that NI metrics recorded the traffic that was created.
	t.Eventually(niMetricsUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"NI metrics have non-zero RX and TX packet counters",
		func(metrics *evemetrics.ZMetricNetworkInstance) bool {
			return metrics.GetNetworkStats().GetRx().GetTotalPackets() != 0 &&
				metrics.GetNetworkStats().GetTx().GetTotalPackets() != 0
		})))
	stopNIMetricsWatch()

	// Flowlog is disabled by default (it is enabled and tested in TestFlowLog).
	/* TODO: GetAppFlowLogs is not yet implemented
	t.Expect(device.GetAppFlowLogs(appUUID, evetest.FlowLogMatch{
		VirtualNetAdapter: "vif0",
		NetworkInstance:   niUUID,
	})).To(BeEmpty())
	t.Expect(device.GetAppDNSLogs(appUUID, evetest.DNSLogMatch{
		VirtualNetAdapter: "vif0",
		NetworkInstance:   niUUID,
	})).To(BeEmpty())
	*/

	// Undeploy app and check that VIF was disconnected from the network instance.
	devConfig.DeleteApplication(appUUID)
	device.ApplyConfig(devConfig, false)

	t.Eventually(appUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"App state is UNSPECIFIED",
		func(info *eveinfo.ZInfoApp) bool {
			return info.State == eveinfo.ZSwState_INVALID
		}).StopIf(appHasError)))
	stopAppWatch()

	evetest.Checkpoint("app-deleted")

	t.Eventually(niUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"NI has no VIFs attached",
		func(info *eveinfo.ZInfoNetworkInstance) bool {
			niInfo = info
			return len(niInfo.Vifs) == 0
		}).StopIf(niHasError)))

	t.Expect(niInfo.IpAssignments).To(HaveLen(1))
	t.Expect(niInfo.IpAssignments[0].IpAddress).To(HaveLen(1))
	t.Expect(niInfo.IpAssignments[0].IpAddress[0]).To(Equal("10.11.12.1"))

	// Delete the network instance in the end.
	devConfig.DeleteNetworkInstance(niUUID)
	device.ApplyConfig(devConfig, false)

	t.Eventually(niUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"NI state is UNSPECIFIED",
		func(info *eveinfo.ZInfoNetworkInstance) bool {
			niInfo = info
			return info.State == eveinfo.ZNetworkInstanceState_ZNETINST_STATE_UNSPECIFIED
		}).StopIf(niHasError)))
	stopNIWatch()
}

func TestSwitchNI(test *testing.T) {
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
		DeviceReusePolicy: evetest.ResetDeviceConfig,
	}
	requiredNetModel := evetest.RequireNetworkModel{
		NetworkModel: netmodels.SingleEthWithDHCP,
	}
	evetest.Setup(requiredDevice, requiredNetModel)
	device := evetest.GetEdgeDevice(devName)
	evetest.Checkpoint("setup-done")

	// Build and apply the initial device configuration, without including any
	// network instances for now.
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

	// Try to create switch network instance.
	niUUID := devConfig.AddNetworkInstance(evetest.SwitchNetworkInstanceConfig{
		DisplayName:   "switch-ni",
		Port:          "ethernet0",
		EnableFlowlog: false,
		MTU:           1500,
		ForwardLLDP:   false,
	})
	niUpdates, stopNIWatch := device.WatchNetworkInstanceInfo(niUUID)
	device.ApplyConfig(devConfig, false)

	timeout := 3 * time.Minute
	var niInfo *eveinfo.ZInfoNetworkInstance
	// Do not stop monitoring the Network Instance state after an error
	// (StopIf(niHasError) is intentionally not used).
	// NI may enter a temporary error condition due to race conditions
	// between zedrouter and NIM, but this is expected to eventually resolve.
	t.Eventually(niUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"NI state is ONLINE",
		func(info *eveinfo.ZInfoNetworkInstance) bool {
			niInfo = info
			return info.State == eveinfo.ZNetworkInstanceState_ZNETINST_STATE_ONLINE
		})))

	evetest.Checkpoint("ni-created")

	t.Expect(niInfo.NetworkID).To(Equal(niUUID.String()))
	t.Expect(niInfo.Displayname).To(Equal("switch-ni"))
	t.Expect(niInfo.Activated).To(BeTrue())
	t.Expect(niInfo.NetworkErr).To(BeEmpty())
	t.Expect(niInfo.Ports).To(HaveLen(1))
	t.Expect(niInfo.Ports[0]).To(Equal("ethernet0"))
	t.Expect(niInfo.BridgeIPAddr).To(BeEmpty())
	t.Expect(niInfo.IpAssignments).To(BeEmpty())
	t.Expect(niInfo.AssignedAdapters).To(HaveLen(1))
	t.Expect(niInfo.AssignedAdapters[0].Name).To(Equal("ethernet0"))
	t.Expect(niInfo.AssignedAdapters[0].Type).To(Equal(evecommon.PhyIoType_PhyIoNetEth))
	t.Expect(niInfo.BridgeName).To(Equal("eth0"))
	t.Expect(niInfo.BridgeNum).To(BeEquivalentTo(1))
	t.Expect(niInfo.InstType).To(BeEquivalentTo(1))
	t.Expect(niInfo.Mtu).To(BeEquivalentTo(1500))
	t.Expect(niInfo.Vifs).To(BeEmpty())
	t.Expect(niInfo.IpRoutes).To(BeEmpty())

	// Try to update network instance - make it air-gaped and increase MTU.
	devConfig.UpdateNetworkInstance(niUUID, evetest.SwitchNetworkInstanceConfig{
		DisplayName:   "switch-ni",
		Port:          "",
		EnableFlowlog: false,
		MTU:           2000,
		ForwardLLDP:   false,
	})

	device.ApplyConfig(devConfig, false)

	t.Eventually(niUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"NI has no ports assigned",
		func(info *eveinfo.ZInfoNetworkInstance) bool {
			niInfo = info
			return len(info.Ports) == 0 && info.BridgeName != "eth0"
		}).StopIf(niHasError)))

	evetest.Checkpoint("ni-updated")

	t.Expect(niInfo.Activated).To(BeTrue())
	t.Expect(niInfo.NetworkErr).To(BeEmpty())
	t.Expect(niInfo.AssignedAdapters).To(BeEmpty())
	t.Expect(niInfo.BridgeName).To(Equal("bn1"))
	t.Expect(niInfo.BridgeNum).To(BeEquivalentTo(1))
	t.Expect(niInfo.Mtu).To(BeEquivalentTo(2000))

	// Try to delete the network instance.
	devConfig.DeleteNetworkInstance(niUUID)
	device.ApplyConfig(devConfig, false)

	t.Eventually(niUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"NI state is UNSPECIFIED",
		func(info *eveinfo.ZInfoNetworkInstance) bool {
			niInfo = info
			return info.State == eveinfo.ZNetworkInstanceState_ZNETINST_STATE_UNSPECIFIED
		}).StopIf(niHasError)))
	stopNIWatch()

	evetest.Checkpoint("ni-deleted")

	// Create NI again, this time with an app connected to it.
	niUUID = devConfig.AddNetworkInstance(evetest.SwitchNetworkInstanceConfig{
		DisplayName:   "switch-ni",
		Port:          "ethernet0",
		EnableFlowlog: false,
		MTU:           1500,
		ForwardLLDP:   false,
	})

	const appMACAddr = "02:16:3e:00:00:01"
	appUUID := devConfig.AddApplication(evetest.ApplicationInstanceConfig{
		DisplayName: "container-app",
		Activate:    true,
		Image: evetest.DockerContainer{
			ImageName: "milan4zededa/evetest-ubuntu-ctr",
			Tag:       "1.0",
		},
		CPUs:        1,
		MemoryBytes: 500 * evetest.MB,
		NetworkAdapters: []evetest.AppNetworkAdapter{
			evetest.VirtualNetworkAdapter{
				LogicalLabel:        "vif0",
				NetworkInstanceUUID: niUUID,
				MAC:                 evetest.MACAddress(appMACAddr),
				ACLAllowRules: []evetest.ACLAllowRule{
					{
						Protocol:     evetest.NetworkProtocolAny,
						RemoteSubnet: evetest.IPSubnet("0.0.0.0/0"),
					},
				},
			},
		},
	})

	niUpdates, stopNIWatch = device.WatchNetworkInstanceInfo(niUUID)
	appUpdates, stopAppWatch := device.WatchAppInfo(appUUID)
	device.ApplyConfig(devConfig, false)

	timeoutExcludingDownload := 5 * time.Minute
	device.WaitUntilAppIsRunning(appUUID, timeoutExcludingDownload)

	evetest.Checkpoint("ni-with-app-created")

	// Wait until application receives IP address from the eth0 subnet
	// (see netmodels.SingleEthWithDHCP).
	var appIPs []net.IP
	var appInfo *eveinfo.ZInfoApp
	t.Eventually(appUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"App receives IP address",
		func(info *eveinfo.ZInfoApp) bool {
			appInfo = info
			if len(appInfo.Network) == 0 {
				return false
			}
			for _, ipAddr := range appInfo.Network[0].IPAddrs {
				// Ignore link-local (IPv6) addresses.
				appIP := evetest.IPAddress(ipAddr)
				if appIP.IsGlobalUnicast() {
					appIPs = append(appIPs, appIP)
				}
			}
			return len(appIPs) > 0
		}).StopIf(appHasError)))
	t.Expect(appInfo.Network).To(HaveLen(1))
	t.Expect(appInfo.Network[0].DevName).To(Equal("vif0"))
	t.Expect(appInfo.Network[0].MacAddr).To(Equal(appMACAddr))
	t.Expect(appIPs).To(HaveLen(1))
	appIP := appIPs[0]
	subnet := evetest.IPSubnet("172.20.20.0/24")
	t.Expect(subnet.Contains(appIP)).To(BeTrue())
	t.Expect(appInfo.Network[0].DefaultRouters).To(HaveLen(1))
	// TODO: we need to fix this in EVE ("nil" is returned instead)
	// t.Expect(appInfo.Network[0].DefaultRouters[0]).To(Equal("172.20.20.1"))
	t.Expect(appInfo.Network[0].NtpServers).To(BeEmpty())
	t.Expect(appInfo.Network[0].NetworkErr).To(BeNil())
	t.Expect(appInfo.Network[0].Ipv4Up).To(BeTrue())
	t.Expect(appInfo.Network[0].IpAddrMisMatch).To(BeFalse())

	// Confirm that application IP address is (eventually) reported in the network
	// instance status.
	t.Eventually(niUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"App IP is reported inside the NI status",
		func(info *eveinfo.ZInfoNetworkInstance) bool {
			niInfo = info
			if len(niInfo.Vifs) == 0 || len(niInfo.IpAssignments) == 0 {
				return false
			}
			for _, ipAssignment := range niInfo.IpAssignments {
				if ipAssignment.MacAddress == appMACAddr {
					return generics.ContainsItem(ipAssignment.IpAddress, appIP.String())
				}
			}
			return false
		}).StopIf(niHasError)))
	t.Expect(niInfo.Vifs).To(HaveLen(1))
	t.Expect(niInfo.Vifs[0].VifName).To(Equal("nbu1x1"))
	t.Expect(niInfo.Vifs[0].MacAddress).To(Equal(appMACAddr))
	t.Expect(niInfo.Vifs[0].AppID).To(Equal(appUUID.String()))

	niMetrics, stopNIMetricsWatch := device.WatchNetworkInstanceMetrics(niUUID)

	// Test that application is accessible from outside.
	// RunShellCommandFromApp will try to access <vifIP>:22
	appAuth := evetest.UsernamePasswordAuth{
		Username: "root",
		Password: "testpassword",
	}
	sshTimeout := 20 * time.Second
	polling := 3 * time.Second
	log := evetest.Logger()
	log.Infof("Testing application accessibility from outside.")
	t.Eventually(func(t Gomega) {
		log.Infof("Waiting for app SSH daemon to start and become reachable...")
		output, _, err := device.RunShellScriptInsideApp(appUUID, appAuth,
			"ip addr", sshTimeout, 0)
		t.Expect(err).ToNot(HaveOccurred())
		t.Expect(output).To(ContainSubstring(appIPs[0].String() + "/24"))
	}, timeout, polling).Should(Succeed())

	// Test DNS provided by the external network (running inside SDN).
	log.Infof("Testing DNS resolution from inside the application")
	output, _, err := device.RunShellScriptInsideApp(appUUID, appAuth,
		"nslookup "+evetest.GetControllerHostname(), sshTimeout, 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(output).To(ContainSubstring(evetest.GetControllerIPv4().String()))

	// Test application connectivity initiated from inside the application.
	log.Infof("Testing application connectivity")
	output, _, err = device.RunShellScriptInsideApp(appUUID, appAuth,
		"curl -sS http://http-server.test/helloworld", sshTimeout, 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(output).To(ContainSubstring("Hello world!"))

	// Check that NI metrics recorded the traffic that was created.
	t.Eventually(niMetrics, timeout).Should(Receive(matchers.SatisfyPredicate(
		"NI metrics have non-zero RX and TX packet counters",
		func(metrics *evemetrics.ZMetricNetworkInstance) bool {
			return metrics.GetNetworkStats().GetRx().GetTotalPackets() != 0 &&
				metrics.GetNetworkStats().GetTx().GetTotalPackets() != 0
		})))
	stopNIMetricsWatch()

	// Flowlog is disabled by default (it is enabled and tested in TestFlowLog).
	/* TODO: GetAppFlowLogs is not yet implemented
	t.Expect(device.GetAppFlowLogs(appUUID, evetest.FlowLogMatch{
		VirtualNetAdapter: "vif0",
		NetworkInstance:   niUUID,
	})).To(BeEmpty())
	t.Expect(device.GetAppDNSLogs(appUUID, evetest.DNSLogMatch{
		VirtualNetAdapter: "vif0",
		NetworkInstance:   niUUID,
	})).To(BeEmpty())
	*/

	// Undeploy app and check that VIF was disconnected from the network instance.
	devConfig.DeleteApplication(appUUID)
	device.ApplyConfig(devConfig, false)

	t.Eventually(appUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"App state is UNSPECIFIED",
		func(info *eveinfo.ZInfoApp) bool {
			return info.State == eveinfo.ZSwState_INVALID
		}).StopIf(appHasError)))
	stopAppWatch()

	evetest.Checkpoint("app-deleted")

	t.Eventually(niUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"NI has no VIFs attached",
		func(info *eveinfo.ZInfoNetworkInstance) bool {
			niInfo = info
			return len(niInfo.Vifs) == 0 && len(niInfo.IpAssignments) == 0
		}).StopIf(niHasError)))

	// Delete the network instance in the end.
	devConfig.DeleteNetworkInstance(niUUID)
	device.ApplyConfig(devConfig, false)

	t.Eventually(niUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"NI state is UNSPECIFIED",
		func(info *eveinfo.ZInfoNetworkInstance) bool {
			niInfo = info
			return info.State == eveinfo.ZNetworkInstanceState_ZNETINST_STATE_UNSPECIFIED
		}).StopIf(niHasError)))
	stopNIWatch()
}

func TestFlowLog(test *testing.T) {
	// TODO
}

func niHasError(info *eveinfo.ZInfoNetworkInstance) (string, bool) {
	stop := info.State == eveinfo.ZNetworkInstanceState_ZNETINST_STATE_ERROR
	if stop {
		return "Network instance is in error state", true
	}
	return "", false
}

func appHasError(info *eveinfo.ZInfoApp) (string, bool) {
	stop := info.State == eveinfo.ZSwState_ERROR
	if stop {
		return "Application instance is in error state", true
	}
	return "", false
}
