// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package networking_test

import "testing"

// TestPortFailover verifies that EVE switches between management ports when
// the currently-used port loses connectivity, that it prefers the lowest-cost
// port and recovers back to it once it works again, and that an application
// connected to a Local NI through a multi-port label re-routes its default
// route accordingly.
//
// Network model
//   - netmodels.TwoMgmtPorts (eth0, eth1, two bridges, two networks). Both
//     ports have DHCP and reach the controller.
//   - SDN DNS servers for both networks must resolve the controller and the
//     "http-server.test" endpoint, so that fail-over is testable from both
//     the device side and the app side.
//
// Device configuration
//   - Two SystemAdapters (eth0, eth1) using a single DHCP NetworkConfig.
//   - Different costs: eth0 -> Cost=0 (preferred), eth1 -> Cost=10.
//   - One Local NI ("local-ni") with port="uplink" (this is EVE's predefined shared
//     label matching every mgmt port) so the NI can use either uplink (multi-path
//     via shared label, see APP-CONNECTIVITY.md  "Multi-Path IP Routing").
//     Default route uses next-hop probing for both ports plus controller reachability
//     check (automatic for uplink labels).
//   - One container app on the NI with a default-allow ACL plus a port-fwd
//     2222->22 ACE so the test can SSH into it from outside.
//
// Phase 1 — steady state on the cheapest port
//   - WatchDeviceInfo: SystemAdapterInfo.currentIndex must point at the DPC
//     that uses both ports without errors. Each port should report DPCState
//     consistent with success.
//   - WatchNetworkInstanceInfo: NI must be ONLINE. NI default route's port
//     must be eth0 (NetworkInstanceInfo.IpRoutes contains 0.0.0.0/0 with
//     Port="ethernet0" — the lower-cost port).
//   - From inside the app: `curl http://http-server.test/helloworld` returns
//     "Hello world!".
//
// Phase 2 — eth0 link-down (lower-cost port fails)
//   - UpdateNetworkModel: clone TwoMgmtPorts and set Ports[0].AdminUp=false.
//   - Eventually:
//     a) DevicePortStatus for eth0 reports an error (no IP / no gateway /
//     test failure). DevicePortStatus.lastError should remain empty for the
//     overall DPC because eth1 still works.
//     b) NetworkInstanceInfo.IpRoutes default route's Port flips to
//     "ethernet1".
//     c) The app `curl http-server.test` keeps working (verifies that the
//     NI re-converged and existing TCP flows are tolerated to break — for
//     stricter assertions we could use only ICMP/UDP probes which do not
//     suffer from NAT IP change disconnecting flows).
//
// Phase 3 — eth0 recovery
//   - UpdateNetworkModel back to TwoMgmtPorts (eth0 AdminUp=true).
//   - Eventually:
//     a) DevicePortStatus for eth0 returns to clean state (err empty,
//     IP assigned, lastSucceeded recent).
//     b) NetworkInstanceInfo default route flips back to eth0 (it is
//     cheaper). This may take 2-3 probe cycles because EVE requires
//     consecutive successful probes before re-selecting (see
//     APP-CONNECTIVITY.md "Multi-Path IP Routing").
//
// Test params
// -----------
//   - Hardcode WithHypervisor=HypervisorKVM in RequireEdgeDevice.
//     This test lives in TestDeviceConnectivitySuite and Device-suite tests
//     do not parameterize the hypervisor.
func TestPortFailover(test *testing.T) {
	test.Skip("not yet implemented")
}

// TestNetworkConfigFallback verifies that EVE rolls back to the previously
// working DevicePortConfig (DPC) when a newly applied configuration cannot
// reach the controller, and that it re-applies the new config once the
// network actually matches it.
//
// Network model
//   - Start with netmodels.TwoMgmtPorts. The "second" port (eth1) is initially
//     used as a backup with cost=10.
//
// Device configuration
//   - Initial config: SystemAdapter for eth0 (mgmt) DHCP, SystemAdapter for
//     eth1 (mgmt) DHCP. Apply, wait until SystemAdapterInfo (in published
//     device info) reports currentIndex=0 and exactly one DevicePortStatus
//     entry with key="zedagent" -- same pattern as bootstrap_test.go uses
//     via its matchSystemAdapterInfo helper. No raw pubsub readback is
//     needed; the SystemAdapterInfo embedded in ZInfoDevice carries the
//     full DPC list, the currentIndex pointer, and per-DPC lastError /
//     lastFailed / lastSucceeded timestamps.
//
// Phase 1 — induce a broken-config rollback
//   - Apply a NEW device config that intentionally does NOT match the SDN
//     network (so it cannot reach the controller):
//     -> Switch eth0 to StaticNetworkConfig with a wrong subnet/gateway
//     (e.g., 10.99.99.0/24 / 10.99.99.1).
//   - Wait for EVE to test the new config and fall back. All assertions
//     read SystemAdapterInfo from WatchDeviceInfo:
//   - SystemAdapterInfo.Status grows by one entry (the just-submitted DPC).
//     The new DPC -- the one at index 0 by priority -- must have
//     LastError set to a description mentioning the connectivity test
//     failure, and LastFailed timestamp populated.
//   - SystemAdapterInfo.CurrentIndex points at the OLDER (working) DPC,
//     not the one we just submitted (i.e. CurrentIndex > 0).
//   - The older DPC referenced by CurrentIndex must have LastSucceeded
//     advancing (it is still working).
//   - The device must REMAIN online — controller still receives info
//     messages, RunShellScript still works.
//
// Phase 2 — recovery
//   - UpdateNetworkModel (or update SDN router config) to make the network
//     actually match the broken config. For variant (a), change the SDN
//     network's subnet/gateway from 172.20.20.0/24 to 10.99.99.0/24
//     (clone netmodels.TwoMgmtPorts and rewrite Networks[0].Ipv4 — note
//     evetest.UpdateNetworkModel allows changing subnets but not the set
//     of ports).
//   - EVE periodically retests higher-priority DPCs (timer.port.testbetterinterval,
//     default 10 min — set it lower via SetConfigProperties for the test,
//     e.g. 60 s). Watching SystemAdapterInfo, eventually:
//   - CurrentIndex returns to 0 (the latest DPC works again).
//   - DevicePortStatus[0].LastSucceeded advances to a timestamp newer than
//     the recovery moment, and LastError is cleared.
//
// Future extension
// ----------------
//   - Variant where the new config IS valid (network model is updated to
//     match) but the controller temporarily blocks the device. Confirm
//     that a brief, "remote" failure (server cert expired) does NOT trigger
//     a fallback (per DEVICE-CONNECTIVITY.md "Handling remote (temporary)
//     failures").
func TestNetworkConfigFallback(test *testing.T) {
	test.Skip("not yet implemented")
}

// TestIntermittentConnectivity verifies that EVE remains (or eventually
// becomes) ONLINE when the network exhibits significant impairments such as
// high latency, packet loss, low bandwidth and intermittent outages.
//
// Network model
//   - Single management port (netmodels.SingleEthWithDHCP). The interesting
//     dimension is the per-port TrafficControl, not topology. We don't need
//     multi-port to exercise resilience to a flaky single uplink.
//
// Device configuration
//   - Plain DHCP-on-eth0 mgmt config.
//   - Add a Local NI + a small ICMP-only test app to also exercise app
//     connectivity under degraded network conditions.
//
// Phase 1 — baseline
//   - Apply config and confirm device is ONLINE, app is RUNNING, app can curl
//     http-server.test.
//
// Phase 2 — high-loss link
//   - UpdateNetworkModel: set TrafficControl on eth0 with loss_probability=20.
//   - Consistently for, say, 3 minutes (longer than EVE's default test
//     interval), poll device.GetState() / DeviceInfo: device must stay
//     ONLINE. SystemAdapterInfo.currentIndex must remain 0 (no fallback —
//     EVE retries succeed often enough).
//   - The app's `ping http-server.test` should mostly succeed (>50%
//     success rate) — assert >= 50% success over 100 pings.
//
// Phase 3 — high latency + jitter
//   - UpdateNetworkModel: TrafficControl{delay=500, delay_jitter=300, loss=0}.
//   - Device must stay ONLINE; HTTP request from app must still succeed
//     within a reasonable timeout (e.g. 30s).
//
// Phase 4 — narrow bandwidth
//   - UpdateNetworkModel: TrafficControl{rate_limit=64 KB/s, queue_limit=32 KB,
//     burst_limit=8 KB}.
//   - Device must stay ONLINE (controller traffic is small).
//   - The app's HTTP fetch of "/helloworld" must still succeed (it's a few
//     bytes of payload).
//
// Phase 5 — full outage windows
//   - For three iterations, alternate:
//     a) UpdateNetworkModel: AdminUp=false on eth0 -> hold for 90s.
//     b) UpdateNetworkModel: AdminUp=true -> hold for 90s.
//   - During AdminUp=false windows, device may transiently report an error
//     for the port; this is acceptable. The hard requirement is that the
//     device returns to ONLINE within X seconds (e.g. 60s) of every
//     AdminUp=true transition.
//   - lastSucceeded timestamp on the active DPC must keep advancing across
//     the test duration.
//
// Phase 6 — restore and verify steady state
//   - UpdateNetworkModel back to TrafficControl-less; verify ONLINE,
//     latency-free behavior.
//
// Notes
// -----
//   - This test is non-trivially time-sensitive. Generous timeouts are
//     necessary; the focus is on EVE's eventual recovery, not strict timing.
//   - If a CI run becomes too long, individual phases can be split into
//     separate test functions (each phase already maps cleanly to a sub-test).
func TestIntermittentConnectivity(test *testing.T) {
	test.Skip("not yet implemented")
}
