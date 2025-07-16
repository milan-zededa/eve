// Test creating, changing, deleting NI. Try to run traffic etc.

package networking_test

import (
	"context"
	"net"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/lf-edge/eve-api/go/evecommon"
	eveinfo "github.com/lf-edge/eve-api/go/info"
	evemetrics "github.com/lf-edge/eve-api/go/metrics"
	"github.com/lf-edge/eve/evetest"
	"github.com/lf-edge/eve/pkg/pillar/types"
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
	device.ApplyConfig(context.Background(), devConfig, true)

	// Try to create local network instance.
	niUUID := devConfig.AddNetworkInstance(evetest.LocalNetworkInstanceConfig{
		DisplayName: "local-ni",
		Port:        "eth0",
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
	niConfigAppliedAt := time.Now()
	device.ApplyConfig(context.Background(), devConfig, false)

	timeout := 3 * time.Minute
	var niInfo *eveinfo.ZInfoNetworkInstance
	t.Eventually(func() eveinfo.ZNetworkInstanceState {
		niInfo = device.GetNetworkInstanceInfo(niUUID)
		return niInfo.State
	}, timeout).To(Equal(eveinfo.ZNetworkInstanceState_ZNETINST_STATE_ONLINE))
	niOnlineAt := time.Now()

	evetest.Checkpoint("ni-created")

	t.Expect(niInfo.NetworkID).To(Equal(niUUID))
	t.Expect(niInfo.Displayname).To(Equal("local-ni"))
	t.Expect(niInfo.Activated).To(BeTrue())
	t.Expect(niInfo.NetworkErr).To(BeEmpty())
	t.Expect(niInfo.Ports).To(HaveLen(1))
	t.Expect(niInfo.Ports[0]).To(Equal("ethernet0"))
	t.Expect(niInfo.BridgeIPAddr).To(Equal("10.11.12.1"))
	t.Expect(niInfo.IpAssignments).To(HaveLen(1))
	t.Expect(niInfo.IpAssignments[0].IpAddress).To(HaveLen(1))
	t.Expect(niInfo.IpAssignments[0].IpAddress[0]).To(Equal("10.11.12.1"))
	t.Expect(niInfo.BridgeName).To(Equal("bn1"))
	t.Expect(niInfo.BridgeNum).To(BeEquivalentTo(1))
	t.Expect(niInfo.InstType).To(BeEquivalentTo(2))
	t.Expect(niInfo.Mtu).To(BeEquivalentTo(1500))
	t.Expect(niInfo.Vifs).To(BeEmpty())
	t.Expect(niInfo.UpTimeStamp.AsTime().After(niConfigAppliedAt)).To(BeTrue())
	t.Expect(niInfo.UpTimeStamp.AsTime().Before(niOnlineAt)).To(BeTrue())
	t.Expect(niInfo.IpRoutes).To(HaveLen(2))
	// Routes are returned by EVE in deterministic and therefore easy-to-test order.
	t.Expect(niInfo.IpRoutes[0].DestinationNetwork).To(Equal("0.0.0.0/0"))
	t.Expect(niInfo.IpRoutes[0].Gateway).To(Equal("172.22.12.1"))
	t.Expect(niInfo.IpRoutes[0].Port).To(Equal("ethernet0"))
	t.Expect(niInfo.IpRoutes[1].DestinationNetwork).To(Equal("172.22.12.0/24"))
	t.Expect(niInfo.IpRoutes[1].Gateway).To(Equal(""))
	t.Expect(niInfo.IpRoutes[1].Port).To(Equal("ethernet0"))

	// Try to update network instance - change IP subnet.
	devConfig.UpdateNetworkInstance(niUUID, evetest.LocalNetworkInstanceConfig{
		DisplayName: "local-ni",
		Port:        "eth0",
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
	device.ApplyConfig(context.Background(), devConfig, false)

	t.Eventually(func() string {
		niInfo = device.GetNetworkInstanceInfo(niUUID)
		return niInfo.BridgeIPAddr
	}, timeout).To(Equal("10.11.13.1"))

	evetest.Checkpoint("ni-updated")

	t.Expect(niInfo.Activated).To(BeTrue())
	t.Expect(niInfo.NetworkErr).To(BeEmpty())
	t.Expect(niInfo.BridgeIPAddr).To(Equal("10.11.13.1"))
	t.Expect(niInfo.IpAssignments).To(HaveLen(1))
	t.Expect(niInfo.IpAssignments[0].IpAddress).To(HaveLen(1))
	t.Expect(niInfo.IpAssignments[0].IpAddress[0]).To(Equal("10.11.13.1"))

	// Try to delete the network instance.
	devConfig.DeleteNetworkInstance(niUUID)
	device.ApplyConfig(context.Background(), devConfig, false)

	t.Eventually(func() eveinfo.ZNetworkInstanceState {
		niInfo = device.GetNetworkInstanceInfo(niUUID)
		return niInfo.State
	}, timeout).To(Equal(eveinfo.ZNetworkInstanceState_ZNETINST_STATE_UNSPECIFIED))

	evetest.Checkpoint("ni-deleted")

	// Create NI again, this time with an app connected to it.
	subnet := evetest.IPSubnet("10.11.12.0/24")
	niUUID = devConfig.AddNetworkInstance(evetest.LocalNetworkInstanceConfig{
		DisplayName: "local-ni",
		Port:        "eth0",
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
	const macAddr = "02:16:3e:00:00:01"
	appUUID := devConfig.AddApplication(evetest.ApplicationInstanceConfig{
		DisplayName: "container-app",
		Activate:    true,
		// TODO: we will create example app(s) here in the eve repo, under evetest/ or /tests dir
		//       lfedge/evetest-container will run sshd, and allow login evetest:evetest
		Image: evetest.DockerContainer{
			ImageName: "lfedge/evetest-container",
			Tag:       "v1.0.0",
		},
		CPUs:        1,
		MemoryBytes: 500 * evetest.MB,
		NetworkAdapters: []evetest.AppNetworkAdapter{
			evetest.VirtualNetworkAdapter{
				LogicalLabel:        "vif0",
				NetworkInstanceUUID: niUUID,
				MAC:                 evetest.MACAddress(macAddr),
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
	device.ApplyConfig(context.Background(), devConfig, false)

	timeoutExcludingDownload := 5 * time.Minute
	err := device.WaitUntilAppIsRunning(appUUID, timeoutExcludingDownload)
	t.Expect(err).ToNot(HaveOccurred())

	evetest.Checkpoint("ni-with-app-created")

	// Check that application received IP address from the NI subnet.
	niInfo = device.GetNetworkInstanceInfo(niUUID)
	t.Expect(niInfo.Vifs).To(HaveLen(1))
	t.Expect(niInfo.Vifs[0].VifName).To(Equal("vif0"))
	t.Expect(niInfo.Vifs[0].MacAddress).To(Equal(macAddr))
	t.Expect(niInfo.Vifs[0].AppID).To(Equal(appUUID.String()))
	var vifIP net.IP
	for _, ipAssignment := range niInfo.IpAssignments {
		if ipAssignment.MacAddress == macAddr {
			t.Expect(ipAssignment.IpAddress).To(HaveLen(1))
			vifIP = evetest.IPAddress(ipAssignment.IpAddress[0])
			break
		}
	}
	t.Expect(vifIP).ToNot(BeNil())
	t.Expect(subnet.Contains(vifIP)).To(BeTrue())

	var appInfo *eveinfo.ZInfoApp
	appInfo = device.GetAppInfo(appUUID)
	t.Expect(appInfo.Network).To(HaveLen(1))
	t.Expect(appInfo.Network[0].DevName).To(Equal("vif0"))
	t.Expect(appInfo.Network[0].MacAddr).To(Equal(macAddr))
	t.Expect(appInfo.Network[0].IPAddrs).To(HaveLen(1))
	t.Expect(appInfo.Network[0].IPAddrs[0]).To(Equal(vifIP.String()))
	t.Expect(appInfo.Network[0].DefaultRouters).To(HaveLen(1))
	t.Expect(appInfo.Network[0].DefaultRouters[0]).To(Equal("10.11.12.1"))
	t.Expect(appInfo.Network[0].GetDns().GetDNSservers()).To(HaveLen(1))
	t.Expect(appInfo.Network[0].GetDns().GetDNSservers()[0]).To(Equal("10.11.12.1"))
	t.Expect(appInfo.Network[0].NtpServers).To(BeEmpty())
	t.Expect(appInfo.Network[0].NetworkErr).To(BeNil())
	t.Expect(appInfo.Network[0].Ipv4Up).To(BeTrue())
	t.Expect(appInfo.Network[0].IpAddrMisMatch).To(BeFalse())

	// Test port forwarding.
	// RunShellCommandFromApp will try to use the 2222->22 port forwarding rule.
	output, _, err := device.RunShellScriptInsideApp(appUUID, evetest.UsernamePasswordAuth{
		Username: "evetest",
		Password: "evetest",
	}, "hostname", 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(string(output)).To(ContainSubstring(appUUID.String()))

	// Test application connectivity initiated from inside the application.
	output, _, err = device.RunShellScriptInsideApp(appUUID, evetest.UsernamePasswordAuth{
		Username: "evetest",
		Password: "evetest",
	}, "curl "+evetest.GetHTTPDatastoreIPv4().String(), 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(string(output)).To(ContainSubstring(evetest.GetHTTPDatastoreWelcomeMsg()))

	// Test DNS provided by the Local NI.
	output, _, err = device.RunShellScriptInsideApp(appUUID, evetest.UsernamePasswordAuth{
		Username: "evetest",
		Password: "evetest",
	}, "nslookup "+evetest.GetControllerHostname(), 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(string(output)).To(ContainSubstring(evetest.GetControllerIPv4().String()))

	// Check that NI metrics recorded the traffic that was created.
	var niMetrics *evemetrics.ZMetricNetworkInstance
	t.Eventually(func() (nonZeroCounters bool) {
		niMetrics = device.GetNetworkInstanceMetrics(niUUID)
		nonZeroCounters = niMetrics.GetNetworkStats().GetRx().GetTotalPackets() != 0 &&
			niMetrics.GetNetworkStats().GetTx().GetTotalPackets() != 0
		return nonZeroCounters
	}, timeout).To(BeTrue())

	// Flowlog is disabled by default (it is enabled and tested in TestFlowLog).
	t.Expect(device.GetAppFlowLogs(appUUID, evetest.FlowLogMatch{
		VirtualNetAdapter: "vif0",
		NetworkInstance:   niUUID,
	})).To(BeEmpty())
	t.Expect(device.GetAppDNSLogs(appUUID, evetest.DNSLogMatch{
		VirtualNetAdapter: "vif0",
		NetworkInstance:   niUUID,
	})).To(BeEmpty())

	// Undeploy app and check that VIF was disconnected from the network instance.
	devConfig.DeleteApplication(appUUID)
	device.ApplyConfig(context.Background(), devConfig, false)

	t.Eventually(func() eveinfo.ZSwState {
		appInfo = device.GetAppInfo(appUUID)
		return appInfo.State
	}, timeout).To(Equal(eveinfo.ZSwState_INVALID))

	evetest.Checkpoint("app-deleted")

	niInfo = device.GetNetworkInstanceInfo(niUUID)
	t.Expect(niInfo.Vifs).To(BeEmpty())
	t.Expect(niInfo.IpAssignments).To(HaveLen(1))
	t.Expect(niInfo.IpAssignments[0].IpAddress).To(HaveLen(1))
	t.Expect(niInfo.IpAssignments[0].IpAddress[0]).To(Equal("10.11.12.1"))

	// Delete the network instance in the end.
	devConfig.DeleteNetworkInstance(niUUID)
	device.ApplyConfig(context.Background(), devConfig, false)

	t.Eventually(func() eveinfo.ZNetworkInstanceState {
		niInfo = device.GetNetworkInstanceInfo(niUUID)
		return niInfo.State
	}, timeout).To(Equal(eveinfo.ZNetworkInstanceState_ZNETINST_STATE_UNSPECIFIED))
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
	device.ApplyConfig(context.Background(), devConfig, true)

	// Try to create switch network instance.
	niUUID := devConfig.AddNetworkInstance(evetest.SwitchNetworkInstanceConfig{
		DisplayName:   "switch-ni",
		Port:          "eth0",
		EnableFlowlog: false,
		MTU:           1500,
		ForwardLLDP:   false,
	})
	niConfigAppliedAt := time.Now()
	device.ApplyConfig(context.Background(), devConfig, false)

	timeout := 3 * time.Minute
	var niInfo *eveinfo.ZInfoNetworkInstance
	t.Eventually(func() eveinfo.ZNetworkInstanceState {
		niInfo = device.GetNetworkInstanceInfo(niUUID)
		return niInfo.State
	}, timeout).To(Equal(eveinfo.ZNetworkInstanceState_ZNETINST_STATE_ONLINE))
	niOnlineAt := time.Now()

	evetest.Checkpoint("ni-created")

	t.Expect(niInfo.NetworkID).To(Equal(niUUID))
	t.Expect(niInfo.Displayname).To(Equal("switch-ni"))
	t.Expect(niInfo.Activated).To(BeTrue())
	t.Expect(niInfo.NetworkErr).To(BeEmpty())
	t.Expect(niInfo.Ports).To(HaveLen(1))
	t.Expect(niInfo.Ports[0]).To(Equal("eth0"))
	t.Expect(niInfo.BridgeIPAddr).To(BeEmpty())
	t.Expect(niInfo.IpAssignments).To(BeEmpty())
	t.Expect(niInfo.AssignedAdapters).To(HaveLen(1))
	t.Expect(niInfo.AssignedAdapters[0].Name).To(Equal("eth0"))
	t.Expect(niInfo.AssignedAdapters[0].Type).To(Equal(evecommon.PhyIoType_PhyIoNetEth))
	t.Expect(niInfo.BridgeName).To(Equal("eth0"))
	t.Expect(niInfo.BridgeNum).To(BeEquivalentTo(1))
	t.Expect(niInfo.InstType).To(BeEquivalentTo(1))
	t.Expect(niInfo.Mtu).To(BeEquivalentTo(1500))
	t.Expect(niInfo.Vifs).To(BeEmpty())
	t.Expect(niInfo.UpTimeStamp.AsTime().After(niConfigAppliedAt)).To(BeTrue())
	t.Expect(niInfo.UpTimeStamp.AsTime().Before(niOnlineAt)).To(BeTrue())
	t.Expect(niInfo.IpRoutes).To(BeEmpty())

	// Try to update network instance - make it air-gaped and increase MTU.
	devConfig.UpdateNetworkInstance(niUUID, evetest.SwitchNetworkInstanceConfig{
		DisplayName:   "switch-ni",
		Port:          "",
		EnableFlowlog: false,
		MTU:           2000,
		ForwardLLDP:   false,
	})

	device.ApplyConfig(context.Background(), devConfig, false)

	t.Eventually(func() int {
		niInfo = device.GetNetworkInstanceInfo(niUUID)
		return len(niInfo.Ports)
	}, timeout).To(BeZero())

	evetest.Checkpoint("ni-updated")

	t.Expect(niInfo.Activated).To(BeTrue())
	t.Expect(niInfo.NetworkErr).To(BeEmpty())
	t.Expect(niInfo.AssignedAdapters).To(BeEmpty())
	t.Expect(niInfo.BridgeName).To(Equal("bn1"))
	t.Expect(niInfo.BridgeNum).To(BeEquivalentTo(1))
	t.Expect(niInfo.Mtu).To(BeEquivalentTo(2000))

	// Try to delete the network instance.
	devConfig.DeleteNetworkInstance(niUUID)
	device.ApplyConfig(context.Background(), devConfig, false)

	t.Eventually(func() eveinfo.ZNetworkInstanceState {
		niInfo = device.GetNetworkInstanceInfo(niUUID)
		return niInfo.State
	}, timeout).To(Equal(eveinfo.ZNetworkInstanceState_ZNETINST_STATE_UNSPECIFIED))

	evetest.Checkpoint("ni-deleted")

	// Create NI again, this time with an app connected to it.
	niUUID = devConfig.AddNetworkInstance(evetest.SwitchNetworkInstanceConfig{
		DisplayName:   "switch-ni",
		Port:          "eth0",
		EnableFlowlog: false,
		MTU:           1500,
		ForwardLLDP:   false,
	})

	const macAddr = "02:16:3e:00:00:01"
	appUUID := devConfig.AddApplication(evetest.ApplicationInstanceConfig{
		DisplayName: "container-app",
		Activate:    true,
		Image: evetest.DockerContainer{
			ImageName: "lfedge/evetest-container",
			Tag:       "v1.0.0",
		},
		CPUs:        1,
		MemoryBytes: 500 * evetest.MB,
		NetworkAdapters: []evetest.AppNetworkAdapter{
			evetest.VirtualNetworkAdapter{
				LogicalLabel:        "vif0",
				NetworkInstanceUUID: niUUID,
				MAC:                 evetest.MACAddress(macAddr),
				ACLAllowRules: []evetest.ACLAllowRule{
					{
						Protocol:     evetest.NetworkProtocolAny,
						RemoteSubnet: evetest.IPSubnet("0.0.0.0/0"),
					},
				},
			},
		},
	})
	device.ApplyConfig(context.Background(), devConfig, false)

	timeoutExcludingDownload := 5 * time.Minute
	err := device.WaitUntilAppIsRunning(appUUID, timeoutExcludingDownload)
	t.Expect(err).ToNot(HaveOccurred())

	evetest.Checkpoint("ni-with-app-created")

	// Check that application received IP address from the eth0 subnet
	// (see netmodels.SingleEthWithDHCP).
	subnet := evetest.IPSubnet("172.22.12.0/24")
	niInfo = device.GetNetworkInstanceInfo(niUUID)
	t.Expect(niInfo.Vifs).To(HaveLen(1))
	t.Expect(niInfo.Vifs[0].VifName).To(Equal("vif0"))
	t.Expect(niInfo.Vifs[0].MacAddress).To(Equal(macAddr))
	t.Expect(niInfo.Vifs[0].AppID).To(Equal(appUUID.String()))
	t.Expect(niInfo.IpAssignments).To(HaveLen(1))
	t.Expect(niInfo.IpAssignments[0].MacAddress).To(Equal(macAddr))
	t.Expect(niInfo.IpAssignments[0].IpAddress).To(HaveLen(1))
	vifIP := evetest.IPAddress(niInfo.IpAssignments[0].IpAddress[0])
	t.Expect(subnet.Contains(vifIP)).To(BeTrue())

	var appInfo *eveinfo.ZInfoApp
	appInfo = device.GetAppInfo(appUUID)
	t.Expect(appInfo.Network).To(HaveLen(1))
	t.Expect(appInfo.Network[0].DevName).To(Equal("vif0"))
	t.Expect(appInfo.Network[0].MacAddr).To(Equal(macAddr))
	t.Expect(appInfo.Network[0].IPAddrs).To(HaveLen(1))
	t.Expect(appInfo.Network[0].IPAddrs[0]).To(Equal(vifIP.String()))
	t.Expect(appInfo.Network[0].DefaultRouters).To(HaveLen(1))
	t.Expect(appInfo.Network[0].DefaultRouters[0]).To(Equal("172.22.12.1"))
	t.Expect(appInfo.Network[0].GetDns().GetDNSservers()).To(HaveLen(1))
	t.Expect(appInfo.Network[0].GetDns().GetDNSservers()[0]).To(Equal("10.16.16.25"))
	t.Expect(appInfo.Network[0].NtpServers).To(BeEmpty())
	t.Expect(appInfo.Network[0].NetworkErr).To(BeNil())
	t.Expect(appInfo.Network[0].Ipv4Up).To(BeTrue())
	t.Expect(appInfo.Network[0].IpAddrMisMatch).To(BeFalse())

	// Test that application is accessible from outside.
	// RunShellCommandFromApp will try to access <vifIP>:22
	output, _, err := device.RunShellScriptInsideApp(appUUID, evetest.UsernamePasswordAuth{
		Username: "evetest",
		Password: "evetest",
	}, "hostname", 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(string(output)).To(ContainSubstring(appUUID.String()))

	// Test application connectivity initiated from inside the application.
	output, _, err = device.RunShellScriptInsideApp(appUUID, evetest.UsernamePasswordAuth{
		Username: "evetest",
		Password: "evetest",
	}, "curl "+evetest.GetHTTPDatastoreIPv4().String(), 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(string(output)).To(ContainSubstring(evetest.GetHTTPDatastoreWelcomeMsg()))

	// Test DNS provided by the external network (running inside SDN).
	output, _, err = device.RunShellScriptInsideApp(appUUID, evetest.UsernamePasswordAuth{
		Username: "evetest",
		Password: "evetest",
	}, "nslookup "+evetest.GetControllerHostname(), 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(string(output)).To(ContainSubstring(evetest.GetControllerIPv4().String()))

	// Check that NI metrics recorded the traffic that was created.
	var niMetrics *evemetrics.ZMetricNetworkInstance
	t.Eventually(func() (nonZeroCounters bool) {
		niMetrics = device.GetNetworkInstanceMetrics(niUUID)
		nonZeroCounters = niMetrics.GetNetworkStats().GetRx().GetTotalPackets() != 0 &&
			niMetrics.GetNetworkStats().GetTx().GetTotalPackets() != 0
		return nonZeroCounters
	}, timeout).To(BeTrue())

	// Flowlog is disabled by default (it is enabled and tested in TestFlowLog).
	t.Expect(device.GetAppFlowLogs(appUUID, evetest.FlowLogMatch{
		VirtualNetAdapter: "vif0",
		NetworkInstance:   niUUID,
	})).To(BeEmpty())
	t.Expect(device.GetAppDNSLogs(appUUID, evetest.DNSLogMatch{
		VirtualNetAdapter: "vif0",
		NetworkInstance:   niUUID,
	})).To(BeEmpty())

	// Undeploy app and check that VIF was disconnected from the network instance.
	devConfig.DeleteApplication(appUUID)
	device.ApplyConfig(context.Background(), devConfig, false)

	t.Eventually(func() eveinfo.ZSwState {
		appInfo = device.GetAppInfo(appUUID)
		return appInfo.State
	}, timeout).To(Equal(eveinfo.ZSwState_INVALID))

	evetest.Checkpoint("app-deleted")

	niInfo = device.GetNetworkInstanceInfo(niUUID)
	t.Expect(niInfo.Vifs).To(BeEmpty())
	t.Expect(niInfo.IpAssignments).To(BeEmpty())

	// Delete the network instance in the end.
	devConfig.DeleteNetworkInstance(niUUID)
	device.ApplyConfig(context.Background(), devConfig, false)

	t.Eventually(func() eveinfo.ZNetworkInstanceState {
		niInfo = device.GetNetworkInstanceInfo(niUUID)
		return niInfo.State
	}, timeout).To(Equal(eveinfo.ZNetworkInstanceState_ZNETINST_STATE_UNSPECIFIED))
}

func TestFlowLog(test *testing.T) {
	// TODO
}
