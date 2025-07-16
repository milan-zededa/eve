// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package networking_test

import (
	"net"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	eveconfig "github.com/lf-edge/eve-api/go/config"
	"github.com/lf-edge/eve-api/go/evecommon"
	eveinfo "github.com/lf-edge/eve-api/go/info"
	"github.com/lf-edge/eve/evetest"
	api "github.com/lf-edge/eve/evetest/grpcapi/go"
	"github.com/lf-edge/eve/evetest/matchers"
	"github.com/lf-edge/eve/evetest/netmodels"
	pillartypes "github.com/lf-edge/eve/pkg/pillar/types"
)

// TestPropagatedRoutes verifies that EVE delivers connected port-subnet routes
// and statically-configured routes to applications via DHCP option 121, and
// that the resulting routing table steers application traffic through the
// correct NI and uplink port. It also covers the negative case: when
// PropagateConnectedRoutes is false the port subnet is withheld, while static
// routes on the same NI are still delivered.
//
// Topology
// --------
//
//	          +---------+    +-------------+     +--------------+
//	   ------>| ni-eth0 |----| eth0 (mgmt) | ... | http-server-0|
//	   |      +---------+    +-------------+     +--------------+
//	   |
//	+-----+   +---------+    +-------------------------+     +--------------+
//	| app |-->| ni-eth1 |----|    eth1 (app-shared)    | ... | http-server-1|
//	+-----+   +---------+    | (static, no def. route) |     +--------------+
//	   |                     +-------------------------+
//	   |
//	   |      +---------+    +-------------------------+     +--------------+
//	   ------>| ni-eth2 |----|    eth2 (app-shared)    | ... | http-server-2|
//	          +---------+    |  (DHCP, no def. route)  |     +--------------+
//	                         +-------------------------+
//
// Network model
// -------------
//   - netmodels.ThreeIsolatedPorts -- three ports each on its own bridge
//     and SDN network with strictly isolated routing: eth0 (DHCP,
//     controller-reachable, dns-server and http-server-0.test at 10.20.20.70
//     reachable), eth1 (no DHCP, http-server-1.test at 10.21.21.70 only),
//     eth2 (DHCP without router option, http-server-2.test at 10.22.22.70
//     only). A single DNS server (10.16.16.25, reachable via eth0) resolves
//     the controller hostname and all three HTTP server FQDNs.
//
// Phases
// ------
//  1. Device config: eth0 as management DHCP (PhyIoUsageMgmtAndApps),
//     eth1 as app-shared static 192.168.55.2/24 with gateway=0.0.0.0 (no
//     default route installed on EVE), eth2 as app-shared DHCP (the SDN
//     suppresses the router DHCP option so no default route is installed).
//     Three Local NIs are created -- one per port:
//     - ni-eth0 (10.50.0.0/24, bridge 10.50.0.1): PropagateConnectedRoutes=true,
//     static route 10.20.20.0/24 via 10.50.0.1.
//     - ni-eth1 (10.50.1.0/24, bridge 10.50.1.1): PropagateConnectedRoutes=true,
//     static route 10.21.21.0/24 via 192.168.55.1 (EVE normalises this to
//     the NI bridge IP 10.50.1.1 when advertising via DHCP option 121).
//     - ni-eth2 (10.50.2.0/24, bridge 10.50.2.1): PropagateConnectedRoutes=false,
//     static route 10.22.22.0/24 via 10.50.2.1.
//     One container app (milan4zededa/evetest-ubuntu-ctr:1.0) with three
//     VIFs (vif0..vif2), one per NI, EnforceNetIntfOrder=true for
//     deterministic vif-to-interface mapping inside the app, default-allow
//     ACL on each VIF, and a TCP 2222->22 port-forward on vif0.
//  2. VIF IPs: WatchAppInfo waits until the app reports all three VIFs, each
//     with exactly one IP from its respective NI subnet and the correct MAC.
//  3. Route table (RunShellScriptInsideApp via port-fwd 2222->22 on vif0):
//     `ip route` inside the app must contain:
//     - default via 10.50.0.1 -- the mgmt port (eth0) is the only uplink that
//     contributes a default route; eth1 and eth2 are app-shared.
//     - 10.20.20.0/24 via 10.50.0.1 -- static route on ni-eth0.
//     - 172.22.12.0/24 -- eth0 port subnet, propagated by ni-eth0.
//     - 10.21.21.0/24 via 10.50.1.1 -- static route on ni-eth1, gateway
//     normalised to the NI bridge IP.
//     - 192.168.55.0/24 -- eth1 port subnet, propagated by ni-eth1.
//     - 10.22.22.0/24 via 10.50.2.1 -- static route on ni-eth2.
//     - 10.140.2.0/24 must NOT appear -- eth2 port subnet is withheld
//     because PropagateConnectedRoutes=false on ni-eth2.
//  4. HTTP connectivity: `curl` to http-server-0.test, http-server-1.test,
//     and http-server-2.test from inside the app must each succeed. Because
//     each HTTP server is reachable only via its dedicated SDN port, a
//     misrouted packet is silently dropped by the SDN router, making these
//     curls the decisive check that the propagated routes are used correctly.
//
// Test params
// -----------
//   - HYPERVISOR. SkipIfHypervisorKubevirt() is called immediately after
//     reading the parameter -- Kubevirt is reserved for cluster tests.
func TestPropagatedRoutes(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()

	evetest.DefineTestParameters(evetest.HypervisorParameter())
	hypervisor := evetest.GetHypervisorParameterValue()
	evetest.SkipIfHypervisorKubevirt()

	devName := "edge-dev"
	evetest.Setup(
		evetest.RequireEdgeDevice{
			Name:              devName,
			WithHypervisor:    hypervisor,
			DeviceReusePolicy: evetest.ResetDeviceConfig,
		},
		evetest.RequireNetworkModel{
			NetworkModel: netmodels.ThreeIsolatedPorts,
		},
	)
	device := evetest.GetEdgeDevice(devName)
	evetest.Checkpoint("setup-done")

	devConfig := evetest.NewEdgeDeviceConfig(devName)

	// eth0: management DHCP.
	eth0Net := devConfig.AddNetwork(evetest.DHCPNetworkConfig{
		NetworkType: evecommon.NetworkType_V4Only,
	})
	devConfig.AddNetworkAdapter(evetest.NetworkAdapterConfig{
		LogicalLabel:  "ethernet0",
		PhysicalLabel: "eth0",
		InterfaceName: "eth0",
		NetworkUUID:   eth0Net,
		Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
	})

	// eth1: app-shared, static IP, gateway=0.0.0.0 so EVE installs no default route.
	eth1Net := devConfig.AddNetwork(evetest.StaticNetworkConfig{
		NetworkType: evecommon.NetworkType_V4Only,
		Subnet:      evetest.IPSubnet("192.168.55.0/24"),
		Gateway:     evetest.IPAddress("0.0.0.0"),
	})
	devConfig.AddNetworkAdapter(evetest.NetworkAdapterConfig{
		LogicalLabel:  "ethernet1",
		PhysicalLabel: "eth1",
		InterfaceName: "eth1",
		NetworkUUID:   eth1Net,
		Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageShared,
		StaticIP:      evetest.IPAddress("192.168.55.2"),
	})

	// eth2: app-shared, DHCP. The SDN suppresses the router option so EVE
	// does not install a default route via this port.
	eth2Net := devConfig.AddNetwork(evetest.DHCPNetworkConfig{
		NetworkType: evecommon.NetworkType_V4Only,
	})
	devConfig.AddNetworkAdapter(evetest.NetworkAdapterConfig{
		LogicalLabel:  "ethernet2",
		PhysicalLabel: "eth2",
		InterfaceName: "eth2",
		NetworkUUID:   eth2Net,
		Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageShared,
	})

	device.ApplyConfig(devConfig, true, true)

	// ni-eth0: PropagateConnectedRoutes=true so the eth0 port subnet (172.22.12.0/24)
	// is delivered to the app. Static route to http-server-0's subnet.
	ni0UUID := devConfig.AddNetworkInstance(evetest.LocalNetworkInstanceConfig{
		DisplayName: "ni-eth0",
		Port:        "ethernet0",
		Subnet:      evetest.IPSubnet("10.50.0.0/24"),
		DHCPRange: pillartypes.IPRange{
			Start: evetest.IPAddress("10.50.0.2"),
			End:   evetest.IPAddress("10.50.0.254"),
		},
		Gateway:                  evetest.IPAddress("10.50.0.1"),
		PropagateConnectedRoutes: true,
		StaticRoutes: []pillartypes.IPRouteConfig{
			{
				DstNetwork: evetest.IPSubnet("10.20.20.0/24"),
				Gateway:    evetest.IPAddress("10.50.0.1"),
			},
		},
		MTU: 1500,
	})

	// ni-eth1: PropagateConnectedRoutes=true so the eth1 port subnet (192.168.55.0/24)
	// is delivered to the app. Static route to http-server-1's subnet; EVE normalizes
	// the gateway (192.168.55.1) to the NI bridge IP (10.50.1.1) when advertising via
	// DHCP option 121.
	ni1UUID := devConfig.AddNetworkInstance(evetest.LocalNetworkInstanceConfig{
		DisplayName: "ni-eth1",
		Port:        "ethernet1",
		Subnet:      evetest.IPSubnet("10.50.1.0/24"),
		DHCPRange: pillartypes.IPRange{
			Start: evetest.IPAddress("10.50.1.2"),
			End:   evetest.IPAddress("10.50.1.254"),
		},
		Gateway:                  evetest.IPAddress("10.50.1.1"),
		PropagateConnectedRoutes: true,
		StaticRoutes: []pillartypes.IPRouteConfig{
			{
				DstNetwork: evetest.IPSubnet("10.21.21.0/24"),
				Gateway:    evetest.IPAddress("192.168.55.1"),
			},
		},
		MTU: 1500,
	})

	// ni-eth2: PropagateConnectedRoutes=false (negative case — the eth2 port subnet
	// 10.140.2.0/24 must NOT reach the app). Static routes are propagated regardless
	// of PropagateConnectedRoutes, so the app still receives the route to http-server-2.
	ni2UUID := devConfig.AddNetworkInstance(evetest.LocalNetworkInstanceConfig{
		DisplayName: "ni-eth2",
		Port:        "ethernet2",
		Subnet:      evetest.IPSubnet("10.50.2.0/24"),
		DHCPRange: pillartypes.IPRange{
			Start: evetest.IPAddress("10.50.2.2"),
			End:   evetest.IPAddress("10.50.2.254"),
		},
		Gateway:                  evetest.IPAddress("10.50.2.1"),
		PropagateConnectedRoutes: false,
		StaticRoutes: []pillartypes.IPRouteConfig{
			{
				DstNetwork: evetest.IPSubnet("10.22.22.0/24"),
				Gateway:    evetest.IPAddress("10.50.2.1"),
			},
		},
		MTU: 1500,
	})

	const (
		vif0MAC = "02:16:3e:00:01:00"
		vif1MAC = "02:16:3e:00:01:01"
		vif2MAC = "02:16:3e:00:01:02"
	)
	appUUID := devConfig.AddApplication(evetest.ApplicationInstanceConfig{
		DisplayName: "multi-ni-app",
		Activate:    true,
		Image: evetest.DockerContainer{
			ImageName: "milan4zededa/evetest-ubuntu-ctr",
			Tag:       "1.0",
		},
		VirtualizationMode:  eveconfig.VmMode_HVM,
		CPUs:                1,
		MemoryBytes:         500 * evetest.MB,
		EnforceNetIntfOrder: true,
		NetworkAdapters: []evetest.AppNetworkAdapter{
			evetest.VirtualNetworkAdapter{
				LogicalLabel:        "vif0",
				NetworkInstanceUUID: ni0UUID,
				MAC:                 evetest.MACAddress(vif0MAC),
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
			evetest.VirtualNetworkAdapter{
				LogicalLabel:        "vif1",
				NetworkInstanceUUID: ni1UUID,
				MAC:                 evetest.MACAddress(vif1MAC),
				ACLAllowRules: []evetest.ACLAllowRule{
					{
						Protocol:     evetest.NetworkProtocolAny,
						RemoteSubnet: evetest.IPSubnet("0.0.0.0/0"),
					},
				},
			},
			evetest.VirtualNetworkAdapter{
				LogicalLabel:        "vif2",
				NetworkInstanceUUID: ni2UUID,
				MAC:                 evetest.MACAddress(vif2MAC),
				ACLAllowRules: []evetest.ACLAllowRule{
					{
						Protocol:     evetest.NetworkProtocolAny,
						RemoteSubnet: evetest.IPSubnet("0.0.0.0/0"),
					},
				},
			},
		},
	})

	appUpdates, stopAppWatch := device.WatchAppInfo(appUUID)
	device.ApplyConfig(devConfig, false, false)

	device.WaitUntilAppIsRunning(appUUID, 5*time.Minute)
	evetest.Checkpoint("app-running")

	// Wait until app reports all 3 VIFs with IPs from their respective NI subnets.
	ni0Subnet := evetest.IPSubnet("10.50.0.0/24")
	ni1Subnet := evetest.IPSubnet("10.50.1.0/24")
	ni2Subnet := evetest.IPSubnet("10.50.2.0/24")
	timeout := 3 * time.Minute
	var appInfo *eveinfo.ZInfoApp
	t.Eventually(appUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"App reports 3 VIFs each with an IP",
		func(info *eveinfo.ZInfoApp) bool {
			appInfo = info
			if len(info.Network) != 3 {
				return false
			}
			for _, vif := range info.Network {
				if len(vif.IPAddrs) == 0 {
					return false
				}
			}
			return true
		}).StopIf(appHasError)))
	stopAppWatch()

	t.Expect(appInfo.Network[0].DevName).To(Equal("vif0"))
	t.Expect(appInfo.Network[0].MacAddr).To(Equal(vif0MAC))
	t.Expect(appInfo.Network[0].IPAddrs).To(HaveLen(1))
	t.Expect(ni0Subnet.Contains(evetest.IPAddress(appInfo.Network[0].IPAddrs[0]))).To(BeTrue())

	t.Expect(appInfo.Network[1].DevName).To(Equal("vif1"))
	t.Expect(appInfo.Network[1].MacAddr).To(Equal(vif1MAC))
	t.Expect(appInfo.Network[1].IPAddrs).To(HaveLen(1))
	t.Expect(ni1Subnet.Contains(evetest.IPAddress(appInfo.Network[1].IPAddrs[0]))).To(BeTrue())

	t.Expect(appInfo.Network[2].DevName).To(Equal("vif2"))
	t.Expect(appInfo.Network[2].MacAddr).To(Equal(vif2MAC))
	t.Expect(appInfo.Network[2].IPAddrs).To(HaveLen(1))
	t.Expect(ni2Subnet.Contains(evetest.IPAddress(appInfo.Network[2].IPAddrs[0]))).To(BeTrue())

	appAuth := evetest.UsernamePasswordAuth{
		Username: "root",
		Password: "testpassword",
	}
	sshTimeout := 20 * time.Second
	polling := 3 * time.Second
	log := evetest.Logger()

	// Wait for SSH to be ready; also serves as the first route check.
	log.Infof("Waiting for SSH and checking default route...")
	t.Eventually(func(t Gomega) {
		output, _, err := device.RunShellScriptInsideApp(appUUID, appAuth,
			"ip route", sshTimeout, 0)
		t.Expect(err).ToNot(HaveOccurred())
		t.Expect(output).To(ContainSubstring("default via 10.50.0.1"))
	}, timeout, polling).Should(Succeed())

	evetest.Checkpoint("ssh-ready")

	// Assert that the routing table inside the app reflects:
	//   - default route via ni-eth0 bridge IP (only the mgmt port contributes one),
	//   - static route to http-server-0 subnet via ni-eth0 bridge IP,
	//   - propagated connected route for eth0 port subnet,
	//   - static route to http-server-1 subnet (gateway normalised to ni-eth1 bridge IP),
	//   - propagated connected route for eth1 port subnet,
	//   - static route to http-server-2 subnet via ni-eth2 bridge IP,
	//   - NO connected route for eth2 port subnet (PropagateConnectedRoutes=false).
	output, _, err := device.RunShellScriptInsideApp(appUUID, appAuth,
		"ip route", sshTimeout, 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(output).To(ContainSubstring("default via 10.50.0.1"))
	t.Expect(output).To(ContainSubstring("10.20.20.0/24 via 10.50.0.1"))
	t.Expect(output).To(ContainSubstring("172.22.12.0/24"))
	// EVE normalises the static-route gateway (192.168.55.1) to the NI bridge IP.
	t.Expect(output).To(ContainSubstring("10.21.21.0/24 via 10.50.1.1"))
	t.Expect(output).To(ContainSubstring("192.168.55.0/24"))
	t.Expect(output).To(ContainSubstring("10.22.22.0/24 via 10.50.2.1"))
	t.Expect(output).NotTo(ContainSubstring("10.140.2.0/24"))

	// Verify that each HTTP server is reachable only via its dedicated port. If EVE
	// misroutes the traffic the SDN router drops it and curl fails, making this the
	// strongest check that propagated routes are both present and used.
	log.Infof("Testing HTTP connectivity via ni-eth0 -> http-server-0")
	output, _, err = device.RunShellScriptInsideApp(appUUID, appAuth,
		"curl -sS --max-time 10 http://http-server-0.test/helloworld", sshTimeout, 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(output).To(ContainSubstring("Hello from HTTP server 0!"))

	log.Infof("Testing HTTP connectivity via ni-eth1 -> http-server-1")
	output, _, err = device.RunShellScriptInsideApp(appUUID, appAuth,
		"curl -sS --max-time 10 http://http-server-1.test/helloworld", sshTimeout, 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(output).To(ContainSubstring("Hello from HTTP server 1!"))

	log.Infof("Testing HTTP connectivity via ni-eth2 -> http-server-2")
	output, _, err = device.RunShellScriptInsideApp(appUUID, appAuth,
		"curl -sS --max-time 10 http://http-server-2.test/helloworld", sshTimeout, 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(output).To(ContainSubstring("Hello from HTTP server 2!"))
}

// TestLocalNIWithMultiplePorts verifies a Local Network Instance configured
// with multiple uplink ports referenced via the predefined shared label "all".
// It checks that EVE selects the working, lowest-cost port for each multi-path
// static route, propagates connected routes for all port subnets, restricts
// port-forwarding to the ports carrying a specific shared label, and fails over
// both routes to the next eligible port when the current one loses connectivity.
//
// Topology
// --------
//
//	                         +-------------------+
//	              ---------->|    eth0 (mgmt)    |---------------
//	              |          |  (DHCP, portfwd)  |              |
//	              |          +-------------------+              |
//	              |                                             |
//	+-----+   +----------+   +-------------------+              |
//	| app |-->| Local NI |-->| eth1 (app-shared) |              |
//	+-----+   +----------+   | (static, portfwd) |              |
//	              |          +-------------------+              |
//	              |                                             |
//	              |          +-------------------+          +--------+  +-------------+
//	              ---------->| eth2 (app-shared) |----------| router |--| http-server |
//	              |          |  (DHCP, portfwd)  |          +--------+  +-------------+
//	              |          +-------------------+               |
//	              |                                              |
//	              |          +-------------------+               |
//	              ---------->|    eth3 (mgmt)    |----------------
//	                         |    (static IP)    |
//	                         +-------------------+
//
// Note: eth1 does NOT have a line to the router -- the SDN has no route to
// http-server.test from that port, so any traffic EVE sends via eth1 toward
// 10.88.88.0/24 is silently dropped.
//
// Network model
// -------------
//   - netmodels.FourPortsMixedAccess -- four ports each on its own bridge and
//     SDN network. eth0 (DHCP, controller-reachable, dns-server at 10.16.16.25
//     and http-server.test at 10.88.88.70 reachable), eth1 (no DHCP, no
//     controller path; only dns-server reachable, http-server NOT reachable
//     from this port), eth2 (DHCP, no controller path; dns-server and
//     http-server.test reachable), eth3 (no DHCP, controller-reachable;
//     dns-server and http-server.test reachable).
//
// Phases
// ------
//  1. Device config: eth0 as management DHCP with shared labels
//     ["internet","httpserver","portfwd"] (cost=0); eth1 as app-shared static
//     172.28.20.10/24 with DNS=10.16.16.25 and shared label ["portfwd"]
//     (cost=0); eth2 as app-shared DHCP with shared labels
//     ["httpserver","portfwd"] (cost=3); eth3 as management static
//     10.40.40.30/24 with DNS=10.16.16.25 and shared labels
//     ["internet","httpserver"] (cost=5, no "portfwd"). One Local NI
//     ("multi-port-ni", subnet 10.50.0.0/24, gateway 10.50.0.1) with
//     port="all" and PropagateConnectedRoutes=true. Two static routes on the
//     NI: 0.0.0.0/0 via label "internet" with gateway ping (GwPingMaxCost=5,
//     PreferLowerCost=true); 10.88.88.0/24 via label "httpserver" with TCP
//     probe to 10.88.88.70:80 (PreferLowerCost=true). One container app
//     (milan4zededa/evetest-ubuntu-ctr:1.0) with a VIF on the NI, a TCP
//     2222->22 port-forward scoped to shared label "portfwd" (eth3 lacks
//     "portfwd" and does not forward), and a default-allow ACL.
//  2. Initial routing: WatchNetworkInstanceInfo waits until the NI is ONLINE
//     with both the default route (0.0.0.0/0) and the http-server route
//     (10.88.88.0/24) resolved via ethernet0 (cost=0, lowest in each label
//     set). All four port subnets (172.22.10.0/24, 172.28.20.0/24,
//     192.168.30.0/24, 10.40.40.0/24) must appear as connected routes in
//     IpRoutes. App VIF receives an IP from 10.50.0.0/24. Both
//     `curl http://http-server.test/helloworld` and
//     `curl http://10.88.88.70/helloworld` from inside the app succeed.
//  3. Failover: UpdateNetworkModel sets eth0 AdminUp=false. EVE detects the
//     loss and reassigns: the default route moves to ethernet3 (next
//     "internet" port, cost=5); the http-server route moves to ethernet2
//     (cheapest remaining "httpserver" port, cost=3). HTTP server remains
//     reachable from inside the app via the new route.
//  4. Restoration: UpdateNetworkModel restores eth0 AdminUp=true. Both routes
//     converge back to ethernet0 (lowest cost). HTTP server remains reachable.
//
// Test params
// -----------
//   - HYPERVISOR. SkipIfHypervisorKubevirt() is called immediately after
//     reading the parameter -- Kubevirt is reserved for cluster tests.
func TestLocalNIWithMultiplePorts(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()

	evetest.DefineTestParameters(evetest.HypervisorParameter())
	hypervisor := evetest.GetHypervisorParameterValue()
	evetest.SkipIfHypervisorKubevirt()

	devName := "edge-dev"
	evetest.Setup(
		evetest.RequireEdgeDevice{
			Name:              devName,
			WithHypervisor:    hypervisor,
			DeviceReusePolicy: evetest.ResetDeviceConfig,
		},
		evetest.RequireNetworkModel{
			NetworkModel: netmodels.FourPortsMixedAccess,
		},
	)
	device := evetest.GetEdgeDevice(devName)
	evetest.Checkpoint("setup-done")

	devConfig := evetest.NewEdgeDeviceConfig(devName)

	// eth0: management DHCP, shared labels: internet + httpserver + portfwd, cost=0.
	eth0Net := devConfig.AddNetwork(evetest.DHCPNetworkConfig{
		NetworkType: evecommon.NetworkType_V4Only,
	})
	devConfig.AddNetworkAdapter(evetest.NetworkAdapterConfig{
		LogicalLabel:  "ethernet0",
		PhysicalLabel: "eth0",
		InterfaceName: "eth0",
		NetworkUUID:   eth0Net,
		Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
		SharedLabels:  []string{"internet", "httpserver", "portfwd"},
		Cost:          0,
	})

	// eth1: app-shared, static IP 172.28.20.10/24, shared labels: portfwd only,
	// cost=0. The SDN has no route to the HTTP server from this port.
	eth1Net := devConfig.AddNetwork(evetest.StaticNetworkConfig{
		NetworkType: evecommon.NetworkType_V4Only,
		Subnet:      evetest.IPSubnet("172.28.20.0/24"),
		Gateway:     evetest.IPAddress("172.28.20.1"),
		DNSServers:  []net.IP{evetest.IPAddress("10.16.16.25")},
	})
	devConfig.AddNetworkAdapter(evetest.NetworkAdapterConfig{
		LogicalLabel:  "ethernet1",
		PhysicalLabel: "eth1",
		InterfaceName: "eth1",
		NetworkUUID:   eth1Net,
		Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageShared,
		StaticIP:      evetest.IPAddress("172.28.20.10"),
		SharedLabels:  []string{"portfwd"},
		Cost:          0,
	})

	// eth2: app-shared, DHCP, shared labels: httpserver + portfwd, cost=3.
	eth2Net := devConfig.AddNetwork(evetest.DHCPNetworkConfig{
		NetworkType: evecommon.NetworkType_V4Only,
	})
	devConfig.AddNetworkAdapter(evetest.NetworkAdapterConfig{
		LogicalLabel:  "ethernet2",
		PhysicalLabel: "eth2",
		InterfaceName: "eth2",
		NetworkUUID:   eth2Net,
		Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageShared,
		SharedLabels:  []string{"httpserver", "portfwd"},
		Cost:          3,
	})

	// eth3: management, static IP 10.40.40.30/24, shared labels: internet + httpserver,
	// cost=5. No portfwd label, so port-forwarding rules do not apply here.
	eth3Net := devConfig.AddNetwork(evetest.StaticNetworkConfig{
		NetworkType: evecommon.NetworkType_V4Only,
		Subnet:      evetest.IPSubnet("10.40.40.0/24"),
		Gateway:     evetest.IPAddress("10.40.40.1"),
		DNSServers:  []net.IP{evetest.IPAddress("10.16.16.25")},
	})
	devConfig.AddNetworkAdapter(evetest.NetworkAdapterConfig{
		LogicalLabel:  "ethernet3",
		PhysicalLabel: "eth3",
		InterfaceName: "eth3",
		NetworkUUID:   eth3Net,
		Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
		StaticIP:      evetest.IPAddress("10.40.40.30"),
		SharedLabels:  []string{"internet", "httpserver"},
		Cost:          5,
	})

	device.ApplyConfig(devConfig, true, true)

	// Local NI spanning all 4 ports (port="all").
	// Static routes use shared labels with probing:
	//   - default: "internet" label (eth0+eth3), gw ping, prefer lower cost.
	//   - http-server subnet: "httpserver" label (eth0+eth2+eth3), TCP probe, prefer lower cost.
	niUUID := devConfig.AddNetworkInstance(evetest.LocalNetworkInstanceConfig{
		DisplayName: "multi-port-ni",
		Port:        "all",
		Subnet:      evetest.IPSubnet("10.50.0.0/24"),
		DHCPRange: pillartypes.IPRange{
			Start: evetest.IPAddress("10.50.0.2"),
			End:   evetest.IPAddress("10.50.0.254"),
		},
		Gateway:                  evetest.IPAddress("10.50.0.1"),
		PropagateConnectedRoutes: true,
		StaticRoutes: []pillartypes.IPRouteConfig{
			{
				DstNetwork:      evetest.IPSubnet("0.0.0.0/0"),
				OutputPortLabel: "internet",
				PortProbe: pillartypes.NIPortProbe{
					EnabledGwPing: true,
					GwPingMaxCost: 5,
				},
				PreferLowerCost: true,
			},
			{
				DstNetwork:      evetest.IPSubnet("10.88.88.0/24"),
				OutputPortLabel: "httpserver",
				PortProbe: pillartypes.NIPortProbe{
					UserDefinedProbe: pillartypes.ConnectivityProbe{
						Method:    pillartypes.ConnectivityProbeMethodTCP,
						ProbeHost: "10.88.88.70",
						ProbePort: 80,
					},
				},
				PreferLowerCost: true,
			},
		},
		MTU: 1500,
	})

	const vifMAC = "02:16:3e:00:02:00"
	appUUID := devConfig.AddApplication(evetest.ApplicationInstanceConfig{
		DisplayName: "multi-port-app",
		Activate:    true,
		Image: evetest.DockerContainer{
			ImageName: "milan4zededa/evetest-ubuntu-ctr",
			Tag:       "1.0",
		},
		VirtualizationMode:  eveconfig.VmMode_HVM,
		CPUs:                1,
		MemoryBytes:         500 * evetest.MB,
		EnforceNetIntfOrder: true,
		NetworkAdapters: []evetest.AppNetworkAdapter{
			evetest.VirtualNetworkAdapter{
				LogicalLabel:        "vif0",
				NetworkInstanceUUID: niUUID,
				MAC:                 evetest.MACAddress(vifMAC),
				// Port-forwarding is scoped to the "portfwd" label:
				// eth0, eth1, eth2 forward; eth3 (no "portfwd" label) does not.
				PortFwdRules: []evetest.PortFwdRule{
					{
						Protocol:     evetest.NetworkProtocolTCP,
						EdgeNodePort: 2222,
						AppPort:      22,
						AdapterLabel: "portfwd",
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

	niUpdates, stopNIWatch := device.WatchNetworkInstanceInfo(niUUID)
	appUpdates, stopAppWatch := device.WatchAppInfo(appUUID)
	device.ApplyConfig(devConfig, false, false)

	// Phase 1: verify initial routing state.
	// NI should be ONLINE with both static routes selecting ethernet0 (lowest cost).
	timeout := 3 * time.Minute
	var niInfo *eveinfo.ZInfoNetworkInstance
	t.Eventually(niUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"NI ONLINE with default route via ethernet0",
		func(info *eveinfo.ZInfoNetworkInstance) bool {
			niInfo = info
			if info.State != eveinfo.ZNetworkInstanceState_ZNETINST_STATE_ONLINE {
				return false
			}
			route := findRoute(info.IpRoutes, "0.0.0.0/0")
			return route != nil && route.Port == "ethernet0"
		}).StopIf(niHasError)))
	stopNIWatch()
	t.Expect(niInfo.NetworkErr).To(BeEmpty())

	evetest.Checkpoint("ni-online")

	device.WaitUntilAppIsRunning(appUUID, 5*time.Minute)

	evetest.Checkpoint("app-running")

	// Both static routes should be resolved via ethernet0 (cost=0, lowest).
	defaultRoute := findRoute(niInfo.IpRoutes, "0.0.0.0/0")
	t.Expect(defaultRoute).NotTo(BeNil())
	t.Expect(defaultRoute.Port).To(Equal("ethernet0"))

	httpRoute := findRoute(niInfo.IpRoutes, "10.88.88.0/24")
	t.Expect(httpRoute).NotTo(BeNil())
	t.Expect(httpRoute.Port).To(Equal("ethernet0"))

	// All four port subnets must appear as connected routes (PropagateConnectedRoutes=true).
	t.Expect(findRoute(niInfo.IpRoutes, "172.22.10.0/24")).NotTo(BeNil())
	t.Expect(findRoute(niInfo.IpRoutes, "172.28.20.0/24")).NotTo(BeNil())
	t.Expect(findRoute(niInfo.IpRoutes, "192.168.30.0/24")).NotTo(BeNil())
	t.Expect(findRoute(niInfo.IpRoutes, "10.40.40.0/24")).NotTo(BeNil())

	// Wait for the app VIF to receive an IP from the NI subnet.
	niSubnet := evetest.IPSubnet("10.50.0.0/24")
	var appInfo *eveinfo.ZInfoApp
	t.Eventually(appUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"App VIF has IP from NI subnet",
		func(info *eveinfo.ZInfoApp) bool {
			appInfo = info
			if len(info.Network) != 1 {
				return false
			}
			return len(info.Network[0].IPAddrs) > 0
		}).StopIf(appHasError)))
	stopAppWatch()

	t.Expect(appInfo.Network[0].MacAddr).To(Equal(vifMAC))
	t.Expect(niSubnet.Contains(evetest.IPAddress(appInfo.Network[0].IPAddrs[0]))).To(BeTrue())

	appAuth := evetest.UsernamePasswordAuth{
		Username: "root",
		Password: "testpassword",
	}
	sshTimeout := 20 * time.Second
	polling := 3 * time.Second
	log := evetest.Logger()

	// Verify HTTP server is reachable from inside the app (port-fwd through any
	// of eth0/eth1/eth2 -- all carry the "portfwd" label).
	log.Infof("Phase 1: waiting for SSH and testing HTTP connectivity...")
	t.Eventually(func(t Gomega) {
		output, _, err := device.RunShellScriptInsideApp(appUUID, appAuth,
			"curl -sS --max-time 10 http://http-server.test/helloworld", sshTimeout, 0)
		t.Expect(err).ToNot(HaveOccurred())
		t.Expect(output).To(ContainSubstring("Hello from HTTP server!"))
	}, 5*time.Minute, polling).Should(Succeed())

	evetest.Checkpoint("http-reachable")

	// Verify per-port port-forwarding: eth0, eth1, eth2 carry the "portfwd" shared
	// label and must forward port 2222; eth3 lacks "portfwd" and must not.
	log.Infof("Phase 1: verifying per-port port-forwarding (TCP dial to port 2222)...")
	portFwdDialTimeout := 3 * time.Second
	tryConnect := func(ipStr string) error {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(ipStr, "2222"), portFwdDialTimeout)
		if err == nil {
			conn.Close()
		}
		return err
	}
	eth0IPs := device.GetDeviceIPAddress("ethernet0")
	t.Expect(eth0IPs).NotTo(BeEmpty())
	t.Expect(tryConnect(eth0IPs[0].String())).To(Succeed()) // eth0: portfwd label
	t.Expect(tryConnect("172.28.20.10")).To(Succeed())      // eth1: portfwd label (static)
	eth2IPs := device.GetDeviceIPAddress("ethernet2")
	t.Expect(eth2IPs).NotTo(BeEmpty())
	t.Expect(tryConnect(eth2IPs[0].String())).To(Succeed()) // eth2: portfwd label
	t.Expect(tryConnect("10.40.40.30")).NotTo(Succeed())    // eth3: no portfwd label

	evetest.Checkpoint("portfwd-checked")

	// Also verify by raw IP (bypasses DNS, exercises the forwarding table directly).
	output, _, err := device.RunShellScriptInsideApp(appUUID, appAuth,
		"curl -sS --max-time 10 http://10.88.88.70/helloworld", sshTimeout, 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(output).To(ContainSubstring("Hello from HTTP server!"))

	// Phase 2: failover. Bring eth0 AdminUp=false.
	// Expected outcome:
	//   - default route (0.0.0.0/0): fails over to ethernet3 (next "internet" port, cost=5).
	//   - http-server route (10.88.88.0/24): fails over to ethernet2 (cheapest "httpserver"
	//     port other than the failing eth0, cost=3).
	log.Infof("Phase 2: bringing eth0 AdminUp=false to trigger failover...")
	updatedModel := proto.Clone(netmodels.FourPortsMixedAccess).(*api.NetworkModel)
	for _, p := range updatedModel.Ports {
		if p.LogicalLabel == "eth0" {
			p.AdminUp = false
		}
	}
	evetest.UpdateNetworkModel(updatedModel)
	// Ensure the model is restored on exit even if the test fails mid-phase.
	defer evetest.UpdateNetworkModel(netmodels.FourPortsMixedAccess)

	failoverTimeout := 10 * time.Minute

	niUpdates, stopNIWatch = device.WatchNetworkInstanceInfo(niUUID)
	t.Eventually(niUpdates, failoverTimeout).Should(Receive(matchers.SatisfyPredicate(
		"Default route fails over to ethernet3",
		func(info *eveinfo.ZInfoNetworkInstance) bool {
			route := findRoute(info.IpRoutes, "0.0.0.0/0")
			return route != nil && route.Port == "ethernet3"
		})))
	stopNIWatch()

	niUpdates, stopNIWatch = device.WatchNetworkInstanceInfo(niUUID)
	t.Eventually(niUpdates, failoverTimeout).Should(Receive(matchers.SatisfyPredicate(
		"HTTP server route fails over to ethernet2",
		func(info *eveinfo.ZInfoNetworkInstance) bool {
			route := findRoute(info.IpRoutes, "10.88.88.0/24")
			return route != nil && route.Port == "ethernet2"
		})))
	stopNIWatch()

	evetest.Checkpoint("failover-done")

	// HTTP server must still be reachable after failover (now via ethernet2).
	log.Infof("Phase 2: verifying HTTP connectivity after failover (via ethernet2)...")
	output, _, err = device.RunShellScriptInsideApp(appUUID, appAuth,
		"curl -sS --max-time 10 http://http-server.test/helloworld", sshTimeout, 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(output).To(ContainSubstring("Hello from HTTP server!"))

	// Phase 3: restore eth0. Both routes must converge back to ethernet0.
	log.Infof("Phase 3: restoring eth0, expecting routes to converge back to ethernet0...")
	evetest.UpdateNetworkModel(netmodels.FourPortsMixedAccess)

	niUpdates, stopNIWatch = device.WatchNetworkInstanceInfo(niUUID)
	t.Eventually(niUpdates, failoverTimeout).Should(Receive(matchers.SatisfyPredicate(
		"Both routes converge back to ethernet0",
		func(info *eveinfo.ZInfoNetworkInstance) bool {
			niInfo = info
			dr := findRoute(info.IpRoutes, "0.0.0.0/0")
			hr := findRoute(info.IpRoutes, "10.88.88.0/24")
			return dr != nil && dr.Port == "ethernet0" &&
				hr != nil && hr.Port == "ethernet0"
		})))
	stopNIWatch()

	evetest.Checkpoint("routes-restored")

	log.Infof("Phase 3: verifying HTTP connectivity after route restoration...")
	output, _, err = device.RunShellScriptInsideApp(appUUID, appAuth,
		"curl -sS --max-time 10 http://http-server.test/helloworld", sshTimeout, 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(output).To(ContainSubstring("Hello from HTTP server!"))
}

// findRoute returns the first IPRoute in routes whose DestinationNetwork matches dst,
// or nil if no such route exists.
func findRoute(routes []*eveinfo.IPRoute, dst string) *eveinfo.IPRoute {
	for _, r := range routes {
		if r.DestinationNetwork == dst {
			return r
		}
	}
	return nil
}

// TestApplicationGateway verifies the "gateway app" pattern: one application
// acts as an IP routing/NAT/firewall gateway for other applications running
// on the same edge node, using air-gap Local NIs to interconnect them and
// static routes propagated via DHCP option 121 to steer client traffic
// through the gateway.
//
// Replicates the eden SDN example:
//
//	github.com/lf-edge/eden/sdn/examples/app-routing/app-gateway
//
// Topology
// --------
//
//	                  +--------------+    +-------------+     +-------------------+
//	       ---------->| ni-eth0 (L3) |----| eth0 (mgmt) | ... | http-server.test  |
//	       |          +--------------+    +-------------+     +-------------------+
//	       |
//	+-------------+   +---------+
//	| app-client1 |-->| airgap1 |
//	+-------------+   +---------+
//	                       |
//	                       v
//	                  +--------+    +----------------+    +-------------------+    +-------------------+
//	                  | app-gw |--->| ni-eth1 (L2)   |----| eth1 (app-shared) |... | alt-server.test   |
//	                  +--------+    +----------------+    +-------------------+    +-------------------+
//	                       ^
//	                       |
//	+-------------+   +---------+
//	| app-client2 |-->| airgap2 |
//	+-------------+   +---------+
//
// Network model
// -------------
//   - Reuse netmodels.TwoMgmtPorts as the SDN-side base (eth0 for mgmt, eth1
//     for app-shared L2 access).
//   - Add a second SDN HTTP server "alt-server.test" reachable only on the
//     eth1 segment. http-server.test (from TwoMgmtPorts) remains the eth0
//     target. Keeping the two HTTP servers on disjoint segments makes it
//     unambiguous which interface a given request used.
//
// Device configuration
// --------------------
//   - SystemAdapter for eth0 (mgmt, DHCP) and eth1 (app-shared, DHCP).
//   - Local NI "ni-eth0" on eth0 -- used by app-client1 for direct mgmt
//     access (default route comes from this NI).
//   - Switch NI "ni-eth1" on eth1 -- used by app-gw for its egress
//     ("WAN" leg). app-gw is the only app on this NI.
//   - Air-gap Local NIs "airgap1" (172.28.1.0/24) and "airgap2"
//     (172.28.2.0/24), no port.
//   - airgap1 has StaticRoutes: 10.21.21.0/24 (alt-server's subnet) via
//     172.28.1.2 (app-gw's IP on airgap1). app-client1 receives this
//     static route via DHCP option 121.
//   - airgap2 has StaticRoutes: 0.0.0.0/0 via 172.28.2.2 (app-gw's IP
//     on airgap2). app-client2 receives this as its default route.
//   - app-gw container app (uses the existing milan4zededa/evetest-ubuntu-ctr
//     image; needs IP forwarding + MASQUERADE on its ni-eth1 egress vif,
//     configured via UserData: `sysctl -w net.ipv4.ip_forward=1` plus
//     `iptables -t nat -A POSTROUTING -o <ni-eth1-vif> -j MASQUERADE`).
//     Note that app-gw deliberately has NO vif on ni-eth0 -- it cannot
//     reach the eth0-side network. Its vifs are:
//     a) vif on ni-eth1 (default ACL allow) -- the "WAN" egress for
//     gatewayed traffic; reaches alt-server via eth1.
//     b) vif on airgap1 with StaticIP 172.28.1.2 (default ACL allow) --
//     the LAN side facing app-client1.
//     c) vif on airgap2 with StaticIP 172.28.2.2 (default ACL allow) --
//     the LAN side facing app-client2.
//   - app-client1 container app:
//     a) vif on ni-eth0 (default ACL allow) -- direct mgmt path; this is
//     where app-client1's default route comes from (ni-eth0 advertises
//     its bridge IP as the default gateway via DHCP). Lets the client
//     reach http-server.test directly, without going through app-gw.
//     b) vif on airgap1 with StaticIP 172.28.1.3 (default ACL allow) --
//     the LAN side facing app-gw; receives the propagated static route
//     for alt-server's subnet via app-gw.
//   - app-client2 container app:
//     a) one vif on airgap2 with StaticIP 172.28.2.3 (default ACL allow).
//     This is the only network it sees; everything (including the
//     default route) goes through app-gw.
//   - DNS for the air-gap NIs: use LocalNetworkInstanceConfig.DNSServers
//     on airgap1 / airgap2 to point at the SDN DNS server already
//     associated with eth1's network (its IP is part of the netmodel;
//     resolve it at test-setup time). The per-NI dnsmasq running on EVE
//     forwards queries to that SDN DNS server, which already knows both
//     "alt-server.test" and "http-server.test". EVE is the resolver
//     (not the client app), and EVE reaches the SDN DNS server via its
//     own management ports -- the air-gap nature of the NI affects only
//     the data plane for the connected app, not EVE's host networking.
//     This avoids duplicating static name mappings per NI.
//
// Assertions
// ----------
//   - All three apps reach RUNNING; all four NIs ONLINE.
//   - WatchAppInfo for each app reports the expected number of VIFs with
//     the expected (static-configured) IPs.
//   - Inside app-client1:
//     a) `ip route` shows: default via ni-eth0 bridge IP, connected
//     route for the ni-eth0 subnet, connected 172.28.1.0/24 (airgap1),
//     plus the propagated static route `10.21.21.0/24 via 172.28.1.2`.
//     b) `curl http://http-server.test/helloworld` succeeds. This goes
//     DIRECTLY via ni-eth0 -> eth0 -- NOT through app-gw -- because
//     the default route points at ni-eth0's bridge IP and matches
//     before the airgap1 static route.
//     c) `curl http://alt-server.test/helloworld` succeeds, routed via
//     the static route on airgap1: airgap1 -> app-gw -> ni-eth1 ->
//     eth1.
//   - Inside app-client2:
//     a) `ip route` shows: connected 172.28.2.0/24 (airgap2), plus a
//     default route via 172.28.2.2 (the propagated default,
//     pointing at app-gw on airgap2).
//     b) `curl http://alt-server.test/helloworld` succeeds: default
//     route -> app-gw -> ni-eth1 -> eth1.
//     c) `curl http://http-server.test/helloworld` FAILS. app-client2
//     sends the request via its default route (= app-gw on airgap2);
//     app-gw receives it and tries to forward, but app-gw has NO vif
//     on ni-eth0 and therefore no route to http-server's subnet, so
//     app-gw drops or rejects the packet. From inside app-client2 the
//     SYN reaches app-gw and there's nothing past it, so curl is
//     expected to TIME OUT (use --max-time 5 and assert exit code 28
//     plus elapsed ~ --max-time). This intentionally exercises the
//     limit of app-gw's reachability.
//   - Path confirmation on app-gw: inside app-gw, after a request from
//     app-client1 (alt-server) or app-client2 (alt-server),
//     `iptables -t nat -L POSTROUTING -nv` shows non-zero packet counts
//     on the MASQUERADE rule attached to the ni-eth1 vif (only one
//     egress leg in this topology). Once flow logs are implemented in
//     evetest, the same assertion can be derived from app-gw's flowlog
//     instead of SSH.
//
// Test params
// -----------
//   - HYPERVISOR. The test must call evetest.SkipIfHypervisorKubevirt()
//     after reading the parameter -- Kubevirt is reserved for cluster tests.
//
// Implementation notes
// --------------------
//   - app-gw's "gateway" behavior (IP forwarding + MASQUERADE) is configured
//     via UserData cloud-init. The existing ApplicationInstanceConfig.UserData
//     field already supports this.
func TestApplicationGateway(test *testing.T) {
	test.Skip("not yet implemented")
}
