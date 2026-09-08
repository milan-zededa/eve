# Evetest Nightly Ledger

One section per nightly run with at least one failing suite.

## [2026-09-08 -- 0.0.0-master-0408b69f (run #22)](https://github.com/milan-zededa/eve/actions/runs/34175160790)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/22/)

### TestApplicationConnectivitySuite: failure analysis

#### TestMoveAppBetweenNIs

##### Failure

``

##### Claude's conclusion

##### Root cause assessment

The failing assertion (`netinst_test.go:1268`) pings `pong`'s IP from `ping1` immediately after the `pong-moved-back-to-n1` checkpoint, which only confirms `pong` reported a VIF IP in the `10.11.13.0`→`10.11.12.0` subnet — it does not wait for `pong`'s guest network stack to actually come up after the reboot that the NI move triggers. Live device data confirms the reboot timing was tight: `pong`'s info stream shows `HALTED→BOOTING` at 02:06:55 and `RUNNING` (with its `10.11.12.2` IP reported) only ~45s before the checkpoint fired at 02:07:40, and the `pingFrom` call is a bare one-shot `ping -c 3 -W 1` with no `Eventually`/retry wrapper (unlike the NI/app-state waits elsewhere in this file), so a guest that hasn't finished bringing up ICMP yet fails the whole check. The live NI state (`916bf275-…`, bridge `bn2`) currently shows both `ping1` (10.11.12.3) and `pong` (10.11.12.2) correctly on the same bridge with successful pings from dom0 now that time has passed, and the stale-looking duplicate `n1` NI (`ef42d65f-…`, bridge `bn1`) seen in `ni-info` history is a leftover *info* record from an earlier test in the suite, not a currently active instance — so it's a red herring, not the cause. This looks like the same class of timing race fixed recently for other tests in this file (e.g. `e9154c5c2` for `TestDeviceIPv6Connectivity`'s default-router timing, `77e5eeb07` for SSH timeout), but there's no prior fix/ledger entry specific to `TestMoveAppBetweenNIs`'s post-reboot ping check, so this appears to be a newly-observed instance of that same pattern rather than a previously tracked recurring issue.

_Cost: $0.6683 | Duration: 163.0s_

### TestDeviceConnectivitySuite: failure analysis

#### TestMgmtTrafficRoutedViaApp

##### Failure

``

##### Claude's conclusion

##### Root cause

After the test migrated EVE's own management port (`eth1`) to a static IP with the gateway app (`10.60.20.150`) as its default router (config version 3, phase 4), NIM had to reconfigure the port (DHCP client → static), which recreated the `eth1` netdevice (its ifindex changed 17→19 in the logs). That churn repeatedly triggered `nireconciler`'s "bridge IP change"/"bridge state change" rebuild path for the `ni-lan` switch NI, which tore down and recreated the `BridgePort/eth1/nbu2x1` entry (the gateway app's LAN vif) several times in a row (`05:21:56`→`05:23:16`). On the last cycle the port was deleted but never recreated — live inspection confirms `nbu2x1` still exists and is up, but is **not** a member of the `eth1` bridge (`/sys/class/net/eth1/brif/` only lists `keth1`), so the device's own `eth1` (10.60.20.5) can no longer reach the gateway app's LAN IP (10.60.20.150): pings/curl to it fail with "Host is unreachable". Concurrently the device logged 70+ `"no suitable IP address available"` / `"no DNS server available"` errors from `zedagent`/`controllerconn` while trying to reach `adam.evetest`, so no fresh `ZInfoDevice` ever got through within the 180s budget, causing the `Eventually(devUpdates,...)` timeout at `routing_test.go:1750`.

This looks like a real interaction bug between the DPC reconciler's port recreation and the NI reconciler's switch-NI bridge-port lifecycle (the app vif attach/detach getting permanently desynced from the intended state), not a flaky timing issue — the test's own comment already budgets for "one hiccup on the first attempt," but here the hiccup never resolved. I don't have access to a known-issues ledger in this environment, so I can't confirm whether this is a recurring/tracked problem or how long it's been present.

_Cost: $1.7523 | Duration: 489.3s_
#### TestVLANSubinterfacesOnTopOfLAGs

##### Failure

``

##### Claude's conclusion

I found a confirmed root cause via live evidence, cross-checked against the pillar source.

**Root cause:** The SDN's dnsmasq for `network-30` shows `vlan30-bond-app`'s vif (MAC `02:16:3e:00:00:02`) completed a normal DHCP handshake and got `DHCPACK 172.22.30.19` at 05:58:48 — so the LAG/VLAN30 data path works fine (the device's own `vlan30-on-bond` port and the sibling `vlan30-eth2-app` both got IPs the same way). But EVE's `zedrouter` never learned this: at boot, `nistate`'s flow/DHCP-snooper tried to open a pcap on the switch-NI's traffic-mirror interface *before* the reconciler had actually created it — `NI State (FlowStats): Installing pcap on vlan30bond-m ... error: unknown interface vlan30bond-m: route ip+net: no such network interface` (05:58:06.222). Per `pkg/pillar/nistate/linux.go` `StartCollectingForNI`/`sniffDNSandDHCP` (`linux_flow.go:446`), once `pcap.OpenLive` fails the goroutine just logs and returns, but `ni.cancelPCAP` was already set non-nil — so `UpdateCollectingForNI`'s retry guard (`ni.cancelPCAP == nil`) never fires again, permanently disabling DHCP/ARP snooping for that NI. That's why `vlan30-bond-app` (whose bridge/mirror pair happens to come up slightly later, being layered on a LACP bond + VLAN) never got a reported IP and `AppInfo` stayed empty, while `vlan30-eth2-app` (plain `eth2`, no bond/VLAN setup delay) had its pcap install succeed and its IP observed via ARP within seconds.

This is a genuine race + missing-retry bug in `nistate`'s pcap installation for switch NIs, not a network/DHCP problem — I don't have access to a known-issues ledger in this environment, so I can't say whether/since-when it's tracked as recurring, but the mechanism (mirror-interface-not-yet-created race with no retry) would plausibly hit any switch NI on a bond/VLAN whose bridge setup is a bit slower than a plain physical port.

_Cost: $1.8892 | Duration: 492.8s_

### TestNodeClusterSuite: failure analysis

#### TestThreeNodesCluster

##### Failure

`Timed out waiting for app "6138d62b-7c4e-449d-8666-6fcabde1e650" to be scheduled in cluster "test-cluster"`

##### Claude's conclusion

##### Root cause

Live evidence shows the 3-node k3s/Longhorn/KubeVirt cluster took far longer than the test's timeout to stabilize. `edge-dev2`/`edge-dev3` confirmed their config by ~01:40, but the harness had to poll "Waiting for cluster nodes to become ready" every minute from 01:41:36 until 02:02:05 (~20 min) before all three reported ready. Even after that, cluster events on edge-dev1 show ongoing instability well past that point — `cdi-operator`/`cdi-deployment` crash-looping (7 restarts each), `virt-operator`/`virt-controller` readiness/liveness probes failing with connection-refused, and repeated `"Node edge-dev3 is down: manager pod longhorn-manager-d8f8t is not running"` events, with the volume's Longhorn replicas still reported `OFFLINE`. The target app's pod (`virt-launcher-container-app-6138d-...`) only got its volume attached and actually started `BOOTING` at ~02:13:03, about 53s *after* the 10-minute scheduling timeout fired at 02:12:20 — i.e. the app was on track to schedule, just too slowly for the current wait budget, not blocked by a hard failure.

I don't have any prior memory/ledger entries for this suite to say whether this specific slow-cluster-formation pattern is recurring, and no PR diff was supplied to judge relatedness — so I can't confirm if this is a known/regressing issue versus incidental environment slowness this run.

_Cost: $0.6158 | Duration: 166.5s_
#### TestClusterToSingleConversion

##### Failure

`Device "edge-dev3" failed to confirm the latest config (version 5): timed out waiting for LastProcessedConfig >= 2026-09-08 03:04:00.203967967 +0000 UTC`

##### Claude's conclusion

##### Root cause

Live evidence on `edge-dev3` shows `k3s.log` recording `"Stopping etcd server..."` / `"etcd server stopped"` at **03:04:02** — the exact moment config version 5 (the cluster→single-node conversion) was fetched — and the k3s server did not come back up (`"Starting k3s v1.34.2+k3s1"`) until **03:07:05**, with all system pods not Ready until **03:08:35** (per `kube-init` log: `WAIT_K3S_READY took 1m26s`, `FIRST BOOT COMPLETE in 2m29s`). So the whole k3s/etcd/kubelet stack was torn down and fully reinitialized as part of applying the config, taking roughly 4.5 minutes end-to-end — well past the harness's ~2-minute confirm-timeout window (03:04:02 → pause logged at 03:06:02). The device wasn't hung or erroring, it was just still mid-reinitialization when the test gave up.

This looks like the same class of issue already visible in this suite run's stale `live-investigation.md` for `TestThreeNodesCluster` (02:xx), where cluster formation similarly outran the test's wait budget (cdi/virt-operator crash-looping, Longhorn manager flapping) — i.e. a recurring pattern this run of k3s/cluster-membership transitions being significantly slower than the test timeouts assume, rather than a hard failure. I don't have access to a persistent known-issues ledger to say since when this has been recurring across runs, and no PR diff was supplied to judge relatedness.

_Cost: $0.4793 | Duration: 237.0s_

### TestUpgradeSuite: failure analysis

#### TestEVEUpgradeKVMtoKVMWithOCIDatastore

##### Failure

`Failed to receive SDN tunnel properties: rpc error: code = Unavailable desc = unable to connect to SDN gRPC service on any of the uplink IPs ([192.168.170.4]): failed to establish tunnel to SDN: rpc error: code = Unavailable desc = connection error: desc = "transport: Error while dialing: dial tcp 192.168.170.4:50121: connect: no route to host"`

##### Claude's conclusion

**Root cause:** The SDN emulator VM for this test never became network-reachable. The broker log shows Proxmox VM 106 (`sdn-5601a7ae`) was powered on and immediately reported uplink IP `192.168.170.4` (within 9ms of power-on — clearly a cached/config-derived address, not a verified-live one), but every subsequent gRPC dial to `192.168.170.4:50121` over the next ~5 minutes failed with `no route to host` until the test gave up and failed. `no route to host` (vs. connection-refused/timeout) indicates the guest's network interface/bridge port was never actually brought up on the Proxmox side, not a slow-boot or firewall issue. Live confirmation: `evetest sdn status/logs/net-model` all currently return `SDN client is not initialized`, and `evetest eve collect-info` fails with "network model not applied", consistent with the tunnel handshake having never completed — the device itself shows `State: UNDEFINED` with no interfaces since it depends on that same SDN network model. Notably, the prior VM for this same SDN device (Proxmox VM 111) was torn down only ~6 minutes earlier before VM 106 was created — plausibly a Proxmox-side bridge/tap cleanup race, though I can't confirm this without Proxmox host access (docker exec/network introspection was blocked by the sandbox in this session). I found no ledger/memory entry for this specific pattern, so I can't say whether it's recurring — this looks like the first time it's been investigated.

_Cost: $0.4366 | Duration: 86.8s_
#### TestEVEUpgradeKubevirtToKubevirt

##### Failure

`EVE upgrade to 0.0.0-master-0408b69f-kvm-amd64 failed: `

##### Claude's conclusion

This confirms the device is still running the original `-k-amd64` (kubevirt) image, exactly as reported.

**Root cause:** baseosmgr on the device rejected the upgrade outright — the live `ZInfoDevice.swList` entry for the target shows `swErr: "Upgrade to non EVE-k (0.0.0-master-0408b69f-kvm-amd64) from EVE-k (0.0.0-master-66475696-k-amd64) is not supported"` (confirmed via `evetest eve info -t`; device is still on `0.0.0-master-66475696-k-amd64` per `eve version` over SSH). The test log shows evetest pulled and inspected `milan4zededa/eve:0.0.0-master-0408b69f-k-amd64` (a `-k` kubevirt-tagged image), but the image's own `version` command reported itself as `...-kvm-amd64` (plain KVM), so evetest built/served a KVM rootfs while the device is running EVE-k — a self-inconsistent Docker tag vs. baked-in version string on that `milan4zededa/eve` image, not an evetest or pillar bug. This is the first time I've seen this pattern (no ledger/memory entry for it), and since `milan4zededa/eve` is a personal dev registry rather than the official `lfedge/eve` build, it's most likely a mistagged/mislabeled custom image push rather than a regression in the EVE upgrade logic itself.

_Cost: $0.4539 | Duration: 79.4s_


