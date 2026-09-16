# Evetest Nightly Ledger

One section per nightly run with at least one failing suite.

## [2026-09-16 -- 0.0.0-master-604fd6a5 (run #53)](https://github.com/milan-zededa/eve/actions/runs/35042303584)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/53/)

### TestApplicationConnectivitySuite: failure analysis

#### TestLocalNetInstanceACLsWithFlowLog

##### Failure

```
timed out after 5m0.001890056s (excluding download) waiting for app "5d5e3281-aaf8-4537-b46e-2312ff1fe70e" (acl-app-1) on device "edge-dev" to reach RUNNING state (last state: RESOLVING_TAG)
```

##### Claude's conclusion

This confirms the earliest occurrence is run #31 (2026-09-09), matching the ledger's own analysis text I already saw.

###### Root cause

`acl-app-1` is stuck in `RESOLVING_TAG` while pulling `lfedge/evetest-ubuntu-ctr:1.0` from datastore `docker://192.168.170.2:5000` (confirmed via `evetest eve config`/`app-info`). Live evidence confirms the exact known mechanism: `/proc/net/tcp` on the device shows two `ESTABLISHED` connections to `192.168.170.2:5000` (hex `02AAA8C0:1388`) with a stalled, non-zero `tx_queue` (`0000010F`), and a live check shows the registry is HTTP-only — `curl http://192.168.170.2:5000/v2/` returns a clean `200`, while `curl https://…` fails outright (handshake error, exit 35). Since the datastore isn't marked insecure/HTTP-only, the OCI tag-resolution path only attempts HTTPS with no request deadline, so the connection hangs indefinitely instead of falling back to HTTP, blocking the app from ever leaving `RESOLVING_TAG`.

This is not a new bug — it's a known, recurring issue against this exact registry and even this exact test name (`TestLocalNetInstanceACLsWithFlowLog`), first recorded in `live-ledger.md` at run #31 (2026-09-09) and recurring repeatedly since (most recently again in run #51, 2026-09-15, under `TestFlowLog`/`TestApplicationGateway`/etc., all sharing the identical HTTPS-probe-hang-against-HTTP-only-registry mechanism). No other suite failures are noted in this run to correlate against, and no PR diff was supplied for this investigation.

### TestNodeClusterSuite: failure analysis

#### TestThreeNodesCluster

##### Failure

```
Timed out waiting for app "aabe2daf-d729-44a3-a504-c2936df5e6fc" to be scheduled in cluster "test-cluster"
```

##### Claude's conclusion

###### Root cause

Live evidence on `edge-dev1` (`volumemgr` log, confirmed via SSH) shows app `aabe2daf...` (`container-app`)'s volume upload (`RolloutDiskToPVC`) hit a string of transient cluster-storage errors from 02:33:31–02:39:28 — `longhorn-admission-webhook: network is unreachable`, then repeated `PVC not found`/`Using existing PVC` — consistent with the same chronic etcd/Longhorn instability during 3-node cluster formation that the ledger has documented as a recurring root cause since at least 2026-09-09 (`kubectl`/`evetest cluster info` here also show storage health flapping HEALTHY↔FAILED↔DEGRADED during the ~16-minute node-join window). The app then parked in a terminal `CREATING_VOLUME` error at 02:39:28. Crucially, `volumemgr` does have a bounded retry path for this (`retryFailedClusterVolumeCreate`, added in commit `3d35218e9`), but it's only driven off the `gc` ticker, which fires every `vdiskGCTime/10` (≈6 minutes) — the retry (`attempt 1/12`, logged at 02:42:56) didn't fire until 42 seconds *after* the test's fixed 10-minute "app scheduled" timeout (02:32:14→02:42:14) had already expired.

This is the same recurring failure signature/message ("Timed out waiting for app ... to be scheduled in cluster") the ledger records for `TestThreeNodesCluster` in run #43 (2026-09-12) and again in a more recent run, both tracing to the same underlying etcd/Longhorn-instability-during-join issue class (known since 2026-09-09). It's a distinct variant, though: those earlier entries found `volumemgr` never retrying at all, whereas here the newer retry logic did fire — just one gc-tick cycle too late relative to the harness's fixed wait window, so the coarse ~6-minute retry cadence is itself a contributing gap worth tightening (e.g. event-driven re-drive on cluster-storage-readiness transition rather than waiting for the next gc tick).

## [2026-09-15 -- 0.0.0-master-604fd6a5 (run #52)](https://github.com/milan-zededa/eve/actions/runs/35010946707)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/52/)

### TestDeviceConnectivitySuite: failure analysis

#### TestMgmtTrafficRoutedViaApp

##### Failure

```

Timed out after 180.001s.
Expected to satisfy: app reports 2 VIFs with the reserved IPs
AppID:"d27389cb-c3be-43ce-87d5-3502e534fa75"  appVersion:"1"  AppName:"mgmt-gw-app"  bootTime:{seconds:1789511824  nanos:941921379}  state:RUNNING  network:{macAddr:"02:16:3e:02:00:00"  devName:"vif0"  defaultRouters:"<nil>"  dns:{}  localName:"nbu1x1"}  network:{macAddr:"02:16:3e:02:00:01"  devName:"vif1"  IPAddrs:"10.60.20.150"  defaultRouters:"<nil>"  dns:{}  ipv4_up:true  localName:"nbu2x1"}  volumeRefs:"8e7ba88f-3130-4c59-839c-9930728ae395"  cluster_app_running:true
```

##### Claude's conclusion

###### Root cause

Live evidence confirms this is the same pcap/mirror-interface race documented repeatedly in the ledger. At `22:36:52.323` pillar logs show `NI State (FlowStats): Installing pcap on eth0-m (mirror from bridge eth0, bridge-num 2)...` immediately followed by `unknown interface eth0-m: route ip+net: no such network interface` — the pcap capture open for `ni-wan`'s mirror (`eth0-m`) raced ahead of the `DummyInterface/eth0-m` creation, which only completes ~0.1s later at `22:36:52.430`. Because pcap capture is opened once at `StartCollectingForNI` time and never retried/refreshed after a failed open, `ni-wan`'s ARP/DHCP snooping never ran, so `vif0` (`nbu1x1`, MAC `02:16:3e:02:00:00`) never got its IP recorded — confirmed live via `evetest eve ni-info ni-wan`, which shows `ipAssignments:{macAddress:"02:16:3e:02:00:00"}` with no `ipAddress` ever populated, while the sibling `ni-lan`/`vif1` (whose `eth1-m` pcap opened successfully) correctly reports `10.60.20.150`.

This is a known, recurring issue: the exact same defect class (pcap permanently failing after a raced/failed open against a not-yet-ready mirror interface, silently starving one switch-NI VIF of its IP) is recorded in `live-ledger.md` under this identical test (`TestMgmtTrafficRoutedViaApp`, run #50, earlier today 2026-09-15) and traced back to first appearing as `TestAirGapSwitchNI` in run #31 (2026-09-09). Today's manifestation is a mirror-image variant: previously `vif1`/`eth1` was the one starved while `vif0`/`eth0` succeeded; here it's the reverse (`vif0`/`eth0` starved, `vif1`/`eth1` succeeded) — consistent with a race whose loser varies run-to-run rather than a fixed side. No other suite failures are present in this run's `gotest.json` to correlate against, and no PR diff was supplied for this investigation.

### TestNodeClusterSuite: failure analysis

#### TestTieBreakerCluster

##### Failure

```
timed out waiting for apps/NIs to be removed from device "edge-dev1" (apps: 1, NIs: 1)
```

##### Claude's conclusion

###### Root cause

`TestTieBreakerCluster` failed on a **near-miss race**, not a permanent hang: the harness's app/NI-removal wait for `edge-dev1` timed out at 19:58:52, but live logs show cleanup was already in flight and completed only seconds later — `domainmgr` logged `doCleanup: failed to cleanup domain ... (waitforvmi failed ... VMI still available)` at 19:58:59, the domain finally reached `HALTED` at 19:58:59.21, and `zedrouter`'s NI reconciler executed the actual iptables/NI teardown at 19:59:03 — 7–11 seconds after the harness gave up. `kubectl get pods -A` and `AppInstanceStatus`/`NetworkInstanceStatus` on the device are now empty, confirming it converged fine, just too slowly.

The delay traces back to kubevirt/control-plane churn on this freshly-formed 3-node cluster: `virt-controller`, `cdi-*`, and `longhorn-manager` pods all show 2–6 restarts within the ~20–25 minute cluster lifetime, and `evetest cluster info` shows storage health flapping `HEALTHY`↔`FAILED`. This is the same **etcd-apply-slowness / Longhorn-instability during cluster formation** issue class documented repeatedly in `live-ledger.md` since at least 2026-09-09 (previously manifesting as node-join-readiness timeouts in `TestThreeNodesCluster`/`TestTieBreakerCluster`). Here it instead delayed the kubevirt VMI/domain teardown between subtests. This run's earlier `TestThreeNodesCluster` actually passed (albeit slowly, 1620.88s), so it isn't the direct trigger, but shares the same underlying control-plane instability. The specific manifestation — an app/NI-removal timeout caused by `"VMI still available"` on domain cleanup — does not appear anywhere in the ledger, so this is a new symptom of the known recurring root cause rather than a previously-recorded exact repeat.

### TestSecuritySuite: failure analysis

#### TestVCom

##### Failure

```

vComLink check script failed: 
Unexpected error:
    <*fmt.wrapError | 0xc000c464e0>: 
    unable to reach app "bc3d686b-f619-4caa-b552-8744f731a56a" SSH on device "edge-dev": no reachable endpoint among [172.20.20.123:2222]
    {
        msg: "unable to reach app \"bc3d686b-f619-4caa-b552-8744f731a56a\" SSH on device \"edge-dev\": no reachable endpoint among [172.20.20.123:2222]",
        err: <*errors.errorString | 0xc000c68c30>{
            s: "no reachable endpoint among [172.20.20.123:2222]",
        },
    }
occurred
```

##### Claude's conclusion

**Root cause:** The port-forward path itself is sound — `zedrouter` installed the `172.20.20.123:2222 → 10.50.0.2:22` DNAT/SNAT/FORWARD iptables rules at 20:13:52, a full 11s before the VM even reached `RUNNING` (20:14:03), and live re-checks now (`evetest sdn ssh -- nc -zv 172.20.20.123 2222`, repeated) succeed 100% of the time, with the guest's port 22 also directly reachable. The failure was a narrow timing race at the exact moment the VM's SSH became reachable: `gotest.json` shows the harness's own "reachability" probe succeeded once at 20:14:52.648 (checkpoint `vm-ssh-ready`), but the very next connection attempts — made within ~100ms, to actually run the check script — got three rapid `connection refused` in under 5ms total, exhausting the retry budget before the harness could fall back and retry later. That signature (briefly reachable, then instantly refused, then solid forever after) is consistent with the guest's sshd bouncing once right after first-boot startup (e.g. host-key generation) rather than any EVE/pillar networking defect — the NAT rules, bridge forwarding, and app state transitions all look clean and timely in the logs.

This is a brand-new failure/symptom: it doesn't appear anywhere in `live-ledger.md` (the only superficially similar `"no reachable endpoint"` entry there is from a completely different suite/root cause — an EVE-K/Longhorn/k3s crash-restart loop tearing down a KubeVirt VMI's network, which doesn't apply here since this is a plain KVM VM with no cluster involved). No other suite/test failed earlier in this run to correlate with, and no PR diff was provided for this investigation.

## [2026-09-15 -- 0.0.0-master-604fd6a5 (run #51)](https://github.com/milan-zededa/eve/actions/runs/34915466635)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/51/)

### TestApplicationConnectivitySuite: failure analysis

#### TestFlowLog

##### Failure

```
timed out after 5m0.000908175s (excluding download) waiting for app "f9597448-001b-4171-91ec-23e4a529d367" (flowlog-test-app) on device "edge-dev" to reach RUNNING state (last state: RESOLVING_TAG)
```

##### Claude's conclusion

###### Root cause

`flowlog-test-app` is stuck in `RESOLVING_TAG` while pulling its image from datastore `docker://192.168.170.2:5000` (confirmed via `evetest eve app-info`/`evetest eve config`). Live evidence on the device confirms the exact known failure mode: `/proc/net/tcp` shows a connection to `192.168.170.2:5000` in `ESTABLISHED` state with a stalled, non-zero `tx_queue` (`0000010F`), and a live `curl` from the device shows the registry is HTTP-only — `http://192.168.170.2:5000/v2/` returns a clean `200`, while `https://192.168.170.2:5000/v2/` fails immediately with `wrong version number`. Since the datastore config doesn't mark the registry as insecure/HTTP, the OCI tag-resolution path only attempts HTTPS with no request deadline, so the connection hangs indefinitely instead of falling back to HTTP, blocking the app from ever reaching `RUNNING`.

This is not a new issue — it's the same HTTPS-probe-hang-against-an-HTTP-only-registry defect documented extensively in `live-ledger.md` for `TestFlowLog`/this same `192.168.170.2:5000` registry, recurring repeatedly since at least **2026-09-09** (first as `TestLocalNetInstanceACLsWithFlowLog` in run #31, and recurring again multiple times through today's earlier runs). No other suite failure has been reported in this run to compare for a shared root cause, and no PR diff was supplied for correlation.


#### TestApplicationGateway

##### Failure

```

Timed out after 180.001s.
Expected to satisfy: app-gw reports 3 VIFs with IPs
AppID:"776d1c92-fc2e-40c8-ab9e-b26a4b14059b"  appVersion:"1"  AppName:"app-gw"  bootTime:{seconds:1789440060  nanos:5703868}  state:RUNNING  network:{macAddr:"02:16:3e:01:00:00"  devName:"vif0"  defaultRouters:"<nil>"  dns:{}  localName:"nbu1x2"}  network:{macAddr:"02:16:3e:01:00:01"  devName:"vif1"  IPAddrs:"172.28.1.2"  defaultRouters:"172.28.1.1"  dns:{}  ipv4_up:true  localName:"nbu2x2"}  network:{macAddr:"02:16:3e:01:00:02"  devName:"vif2"  IPAddrs:"172.28.2.2"  defaultRouters:"172.28.2.1"  dns:{DNSservers:"172.28.2.1"}  ipv4_up:true  localName:"nbu3x2"}  volumeRefs:"e1f771d7-cc9a-484d-8023-6b197cdc60aa"  cluster_app_running:true
```

##### Claude's conclusion

###### Root cause

The guest actually completed DHCP successfully — the SDN's dnsmasq log for the `app-network` segment (bridged to EVE's `ethernet1`/switch NI `ni-eth1`) shows a full DORA exchange for `vif0`'s MAC `02:16:3e:01:00:00` completing with `DHCPACK ... 10.203.10.150` at `02:41:11`, just 11s after boot — well inside the 180s test window. However, EVE's own reporting of that VIF never reflects it: every `PublishAppInfoToZedCloud` for `app-gw` from boot through the timeout shows `vif0` with `defaultRouters:"<nil>"` and no `IPAddrs`, and live `evetest eve ni-info ni-eth1` right now still shows `ipAssignments:{macAddress:"02:16:3e:01:00:00"}` with no address populated — while the sibling local-NI vifs (`vif1`/`vif2`) correctly report their static IPs. Since `ni-eth1` is a switch NI, EVE relies on DHCP/ARP pcap-snooping (`nistate/linux_flow.go`) via the `bn2-m` mirror to learn the guest's DHCP-assigned IP, and the mirror/TC rules for `nbu1x2` were confirmed in place (`02:41:03`) before the DHCP handshake (`02:41:10-11`), yet the assignment was still never recorded — pointing to a bug in pillar's switch-NI DHCP-snoop → VIF-IP-assignment path rather than anything wrong with the app, network config, or DHCP server. This does not share the earlier `TestFlowLog` failure's root cause (an unrelated HTTPS-probe-hang against an HTTP-only registry), and this specific test/failure (`TestApplicationGateway`, "3 VIFs with IPs") does not appear anywhere in `live-ledger.md`, so it looks like a new, not-previously-recorded occurrence.


#### TestNICCountChangeOrderedInterface

##### Failure

```
timed out after 3m0.000688621s (excluding download) waiting for app "3ac6e657-52fe-453f-b76e-16af755672a2" (container-app) on device "edge-dev" to reach RUNNING state (last state: RESOLVING_TAG)
```

##### Claude's conclusion

No prior ledger entries mention this specific `IngestBlob`/CAS-load-hang mechanism, so this appears to be a new failure mode not previously recorded.

###### Root cause

`container-app` (uuid `3ac6e657...`) never hangs in `RESOLVING_TAG` as the failure summary's "last state" suggests — live device logs show it actually cleared tag-resolution and download in under a second and progressed all the way to `LOADING` at 100% by 03:41:15 (confirmed via `evetest eve logs`/`app-info`). The real blocker is downstream: `zedbox`'s `IngestBlob` for one specific 26.9MB image layer (`sha256:1dde92b8...`) logged "Attempting to load blob" into containerd/CAS at 03:41:42 and never logged "Loaded the blob successfully" — it's still stuck there 6+ minutes later at investigation time, whereas the identical blob loaded in well under a second (0.7s) the first time this same image was ingested earlier in the run (03:31:49–03:31:50). Since `doUpdateContentTree` requires all component blobs loaded before the content tree can reach `LOADED`, this single stuck CAS ingest permanently blocks the volume, so `domainmgr` never gets a chance to schedule the VM and the app can never reach `RUNNING` — consistent with the reported 3-minute timeout. There's also a lone kernel `clocksource: Long readout interval` warning at 03:41:27 (a ~1.3s host scheduling stall) right around when the hang began, suggesting host/VM contention as a possible trigger, though a single blip wouldn't normally explain a hang lasting many minutes. This does not match the root cause of the other failures already investigated in this run (`TestFlowLog`'s HTTPS-probe-hang-against-HTTP-only-registry, and `TestApplicationGateway`'s switch-NI DHCP-snoop bug) — those never got past `RESOLVING_TAG`/DHCP-snooping, whereas here that stage completed cleanly. It also doesn't match anything in `live-ledger.md` (no prior entries mention `IngestBlob`/CAS blob-load hangs), so this looks like a new, previously-unrecorded failure mode rather than a known recurring issue. I was unable to get an interactive SSH session on the device (evetest's SSH tunnel reported "failed to detect any IP address," despite the device's own reported info showing a valid, up management IP) to directly inspect the live containerd/CAS state, so I can't confirm the exact reason the ingest call itself is blocked (e.g. containerd daemon contention, disk I/O stall, or a lock/deadlock in the CAS ingest path).

### TestNodeClusterSuite: failure analysis

#### TestThreeNodesCluster

##### Failure

```
Timed out waiting for cluster nodes to become ready in cluster "test-cluster" (device "edge-dev3")
```

##### Claude's conclusion

###### Root cause

Live evidence shows all three devices (`edge-dev1/2/3`) rebooted together at 03:54:42, then never re-formed the 3-node etcd cluster: `edge-dev1`'s `/persist/kubelog/k3s-install.log` shows kube-init logging `"single-node config for edge-dev1: node-name only"` at 03:55:54 (identical to its initial single-node startup at 03:35:58) and starting `k3s server` standalone again, and its `zedkube` log confirms this with a steady stream of `clusterStatusHTTPHandler: not control-plane and etcd` from 04:00 onward. `kubectl get nodes` on `edge-dev1` and `edge-dev3` each show only themselves (not a 3-node cluster), and `edge-dev2` has no running `k3s server` process at all (kubectl fails with `connection refused` to `localhost:8080`) — so instead of resuming the joined cluster after the reboot, each node fell back to (or lost) its single-node bootstrap path, which is exactly what the harness's 30-minute "waiting for cluster nodes to become ready" timeout caught. Separately, `edge-dev1`'s kubevirt `virt-api` pods are stuck `CrashLoopBackOff` with `exec /usr/bin/virt-api: exec format error`, blocking kube-init's own readiness wait — the same "exec format error" symptom class the ledger recorded for `longhorn-manager` in run #48 (2026-09-14), though on a different binary this time. No other suite failure occurred earlier in this same run to correlate with. This is a recurring, known issue class: the ledger documents an essentially identical kube-init "single-node config" race (falling back to standalone instead of joining) for `edge-dev2` in run #42 (2026-09-12), and separately documents etcd/Longhorn join-instability and "exec format error" crash-looping (run #48, 2026-09-14) as long-standing recurring themes for this suite dating back to at least 2026-09-09 — today's failure looks like the same "post-reboot kube-init falls back to single-node" bug from 2026-09-12, now triggered by a full 3-node simultaneous reboot rather than a cert-fetch race.

### TestStorageSuite: failure analysis

#### TestVaultZvolTrimReclaimsBlocks

##### Failure

```

Timed out after 120.001s.
/persist/vault ext4-on-zvol must be mounted before writing test data
Expected success, but got an error:
    <*fmt.wrapError | 0xc001647dc0>: 
    failed to execute script over SSH for EVE device edge-dev: Process exited with status 32 (stderr: time="2026-09-15T02:35:55Z" level=warning msg="DEPRECATION: The support for cgroup v1 is deprecated since containerd v2.2 and will be removed by no later than May 2029. Upgrade the host to use cgroup v2."
    )
    {
        msg: "failed to execute script over SSH for EVE device edge-dev: Process exited with status 32 (stderr: time=\"2026-09-15T02:35:55Z\" level=warning msg=\"DEPRECATION: The support for cgroup v1 is deprecated since containerd v2.2 and will be removed by no later than May 2029. Upgrade the host to use cgroup v2.\"\n)",
        err: <*ssh.ExitError | 0xc0011c5f00>{
            Waitmsg: {status: 32, signal: "", msg: "", lang: ""},
        },
    }
```

##### Claude's conclusion

No prior occurrences of this exact SSH/exec exit-32 symptom found in the ledger. I have enough evidence now to conclude.

**Root cause:** The `/persist/vault` ext4-on-zvol *was* actually mounted very early — kernel log shows `EXT4-fs (zd0): mounted filesystem ... r/w` at 02:29:07.946, only ~56s after this freshly-provisioned device's first boot (02:28:11.808) — so the test's mount-readiness gate wasn't waiting on a real unmet condition. The failure is instead in the SSH transport used to check it: `eve exec pillar mountpoint -q /persist/vault` runs via `ctr t exec` into the pillar container (per `pkg/dom0-ztools/rootfs/bin/eve`), and I confirmed live that `mountpoint`'s own exit code for a non-mounted/nonexistent path is `1`, not `32` — so the reported "status 32" isn't the command's logical answer but a wrapper/exec-layer failure. Live evidence shows this device is severely CPU-starved during that window: load average is currently 9.12 on only 4 vCPUs, `volumemgr`/`vaultmgr` watchdog log lines show a ~4.5-minute gap with no activity (02:30:12 → 02:34:55/02:35:17) right as this single-node EVE-K cluster was standing up k3s/kubevirt/CDI/Longhorn control-plane components (`virt-operator`, `cdi-operator`, `virt-api`, `longhorn-manager`, `containerd` all near the top of current CPU usage), and `containerd` itself is at 15% CPU with an `exe` process pegged at 133%/18%. This matches the same broader "chronic CPU/etcd/control-plane overload during single/multi-node cluster formation on this EVE-K device profile" issue class documented repeatedly in the ledger since at least 2026-09-09 (e.g., `TestThreeNodesCluster`/`TestTieBreakerCluster` timeouts) — but this is the first ledger occurrence of that overload manifesting as an SSH/`ctr exec` transport failure inside `TestStorageSuite` rather than a k3s scheduling/readiness timeout. It's unrelated to the only prior `TestVaultZvolTrimReclaimsBlocks` ledger entry (run #45, 2026-09-13), which was a different symptom entirely (fstrim reclaiming less than expected due to background write contention, not an SSH exec failure), so this is effectively a new manifestation rather than a repeat of that specific bug.

### TestUpgradeSuite: failure analysis

#### TestEVEUpgradeKubevirtToKubevirt

##### Failure

```
Timed out waiting for device "edge-dev" to upgrade to 0.0.0-master-604fd6a5-k-amd64
```

##### Claude's conclusion

###### Root cause

Live evidence points to a mid-run network/infra outage on the SDN emulator's path, not an EVE/pillar defect. `gotest.json` shows the harness's SDN tunnel proxy started failing with `connection reset by peer` and `Failed to send keepalive ping: EOF` between 03:42:30–03:42:37, and the Adam controller's `adam-stdout` log corroborates this independently: the device's own last request to Adam is at 03:41:51, after which it goes completely silent — confirming the device itself lost its network path (not just the harness's control tunnel), which is consistent with an SDN VM/network-stack outage rather than a device-side hang. The device flipped `ONLINE → SUSPECT` at 03:47:07 (roughly one missed heartbeat cycle later) and never recovered through the 04:01:45 timeout; live checks right now (`evetest sdn status`, `evetest eve ssh`) still fail with `i/o timeout` dialing the SDN gRPC endpoint `192.168.170.6:50121`, showing the outage is ongoing, not transient.

This doesn't share a root cause with anything else in this run — the two prior upgrade tests (`TestEVEUpgradeKVMtoKVM`, `TestEVEUpgradeKVMtoKVMWithOCIDatastore`) both passed cleanly using the same SDN/broker infra for ~28 minutes beforehand, ruling out an upgrade-code regression. Per the ledger, this exact test previously failed on 2026-09-08 for an unrelated reason (a mistagged kvm-vs-kubevirt test image causing an explicit, safe upgrade rejection) — that is not what happened here. The ledger does record a recurring *class* of SDN/broker network flakiness (VM provisioned but gRPC path never comes up, "no route to host"), but every prior instance of that occurred at initial SDN-VM boot/provisioning; this run's signature — a stable connection that dies mid-test with `connection reset by peer` and drives the device to `SUSPECT` — doesn't match those and doesn't appear anywhere in the ledger, so it looks like a new manifestation of that broader infra-flakiness class rather than a previously-recorded exact repeat.

## [2026-09-15 -- 0.0.0-master-604fd6a5 (run #50)](https://github.com/milan-zededa/eve/actions/runs/34884207026)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/50/)

### TestApplicationConnectivitySuite: failure analysis

#### TestSwitchNIWithMultiplePorts

##### Failure

```

Timed out after 180.001s.
When passed a matcher, ReceiveMatcher's channel *must* receive something.
```

##### Claude's conclusion

I have enough evidence to conclude. Here's the root cause analysis:

**Root cause:** This is a test-harness timing/race issue, not a pillar defect. Live evidence from the Adam controller's `info` archive shows zedagent published a `ZiNetworkInstance` info object for `switch-ni` — already containing `vifs:[{vifName:"nbu1x1", appID:"2844bd9c-..."}]` — in the very same info-POST batch (23:22:27) as the `RUNNING`/app-network-IP transition that made the test print "Reached checkpoint 'app-running'" and immediately start "Waiting for: NI reports the app VIF...". Because EVE's `ZiNetworkInstance` info is diff-published (only sent when it changes), that was the *only* `ZiNetworkInstance` message ever sent for this NI in the whole test run — none of the subsequent full-device-info batches (23:23:41, 23:24:27, 23:25:34, 23:26:40) contain another one. The test's `Eventually(channel).Should(Receive(...))` at `stp_test.go:293` appears to subscribe for *future* NI-info updates only after reaching `app-running`, so it raced against (and lost) that single, already-correct update, then had nothing further to receive for the full 180s — matching Gomega's generic "ReceiveMatcher's channel must receive something" timeout text. Confirming this, live `evetest eve ni-info switch-ni` right now shows the exact same, already-correct `vifs` entry the device reported at 23:22:27 — the data was never actually missing or wrong on the device side. No other suite failure occurred in this run to compare against, and this exact test/failure does not appear anywhere in `live-ledger.md`, so this looks like a new (not previously recorded) occurrence of a checkpoint/info-subscription race in the test harness rather than a recurring known issue.

### TestDeviceConnectivitySuite: failure analysis

#### TestMgmtTrafficRoutedViaApp

##### Failure

```

Timed out after 180.000s.
Expected to satisfy: app reports 2 VIFs with the reserved IPs
AppID:"aa37c9c0-d427-472f-a35a-b0e93a82a008"  appVersion:"1"  AppName:"mgmt-gw-app"  bootTime:{seconds:1789427254  nanos:761462180}  state:RUNNING  network:{macAddr:"02:16:3e:02:00:00"  devName:"vif0"  IPAddrs:"10.60.10.150"  IPAddrs:"fe80::16:3eff:fe02:0"  defaultRouters:"<nil>"  dns:{}  ipv4_up:true  localName:"nbu1x1"}  network:{macAddr:"02:16:3e:02:00:01"  devName:"vif1"  defaultRouters:"<nil>"  dns:{}  localName:"nbu2x1"}  volumeRefs:"c7692507-8cf9-4fda-be1e-90df56029f17"  cluster_app_running:true
```

##### Claude's conclusion

###### Root cause

Live evidence shows `vif1` (`nbu2x1`, on switch NI `ni-lan`/`eth1`) never got **any** IP — not even the IPv6 link-local address that normally appears via ND snooping — while `vif0` on `ni-wan`/`eth0` got both IPv4 (DHCP) and IPv6 fine. Pillar's `pcap` traces confirm why: capture only ever ran successfully on `eth0-m` (269 debug entries, the correct mirror for `ni-wan`), and the only capture attempt tied to `eth1` was on the **bare bridge `eth1`** itself (not the mirror `eth1-m`), and it was canceled at `23:07:29.864` — before the NI reconciler even installed the TC-mirror redirect rules for `nbu2x1→eth1-m` (`23:07:36.804`, ~7s later). This lines up with a code-level gap in `pkg/pillar/nistate/linux.go`: `zedrouter.doActivateNetworkInstance` starts `StartCollectingForNI` (which captures `ni.bridge`, including `MirrorIfName`, once) essentially in parallel with the asynchronous NI reconciler that actually creates the mirror device, while `UpdateCollectingForNI` (called on every later config/VIF change) takes no bridge argument and never refreshes `ni.bridge` — so once the pcap goroutine has locked onto a bridge/mirror snapshot, no interface-mirror correction is ever applied, permanently starving that NI's ARP/DHCP/ND snooping.

This is the same defect class recorded repeatedly in `live-ledger.md` since 2026-09-09 (first via `TestAirGapSwitchNI` in run #31, recurring under `TestMgmtTrafficRoutedViaApp` and several VLAN/switch-NI tests through run #49), described there as pcap giving up permanently after a failed/raced open on the mirror interface — this run's manifestation (zero IPs at all, rather than IPv6-link-local-only) fits as a variant where the stale bridge snapshot pointed at the bare interface instead of a not-yet-created mirror. No other suite in this run failed (gotest.json shows no other fail actions), so there's no shared root cause to reconcile with.

### TestNodeClusterSuite: failure analysis

#### TestThreeNodesCluster

##### Failure

```
Timed out waiting for app "58f94a20-bf2d-4142-b40a-e3b411f48f4d" to be scheduled in cluster "test-cluster"
```

##### Claude's conclusion

I have sufficient evidence to write the root cause now.

###### Root cause

The 3-node k3s/kubevirt cluster on `edge-dev1` (the app's assigned host) is chronically resource-starved: `free -h` shows only 345Mi free out of 7.6Gi and `uptime` shows load average 3.86–6.62 on a 4-vCPU node, and `kubectl describe node edge-dev1` shows the kubelet restarting five separate times over the last 25 minutes ("Starting kubelet" events at 25m/14m/7m19s/4m44s/109s ago), with dozens of system pods (cdi, kubevirt, longhorn-system) showing 2-4 restarts each in that same window. This churn is what stalled the app: the volume's Longhorn replicas kept flapping OFFLINE/REBUILDING/ONLINE and `cluster info` recorded the volume erroring with "attempts to upload image failed" during `CREATING_VOLUME`, so the CDI upload/volume-attach cycle never finished inside the harness's scheduling-wait window — by the time I checked live, it had *just* completed (virt-launcher pod created ~21s before, volume attach succeeded ~10s before), i.e. another slow-convergence near-miss rather than a hard break.

This is the same root-cause class already investigated earlier in `live-ledger.md`: the identical message "Timed out waiting for app ... to be scheduled in cluster" occurred in run #43 (2026-09-12), also on `edge-dev1`, also due to repeated k3s/kubelet crash-restarts (etcd apply-slowness → k3s restarts → CDI upload window too short). More broadly, this belongs to the ledger's recurring "etcd apply-slowness / Longhorn-instability during cluster join/formation" issue class documented since at least **2026-09-09**, which has produced several distinct symptom variants (node-readiness timeouts, SSH-unreachable-after-VMI-churn, and now-twice this exact app-scheduling timeout). No PR diff was provided for this investigation, so no code-change correlation to assess.


#### TestTieBreakerCluster

##### Failure

```
Timed out waiting for cluster nodes to become ready in cluster "test-cluster" (device "edge-dev1")
```

##### Claude's conclusion

I have enough evidence to write the root cause now.

**Root cause:** `edge-dev3` (the last node to join `test-cluster`) shows 1653 etcd `"apply request took too long"` warnings and repeated `watch chan error: etcdserver: mvcc: required revision has been compacted` in its `k3s.log`, causing widespread control-plane instability during the join window. This left Longhorn unhealthy on that node — `longhorn-manager-64782` is stuck `1/2 Ready` with its own local `9501/healthz` readiness probe timing out, and the `longhorn-csi-plugin-ts8kb` pod's `node-driver-registrar` container is in `CrashLoopBackOff` (14 restarts) because `longhorn-csi-plugin`'s own container can't even resolve/reach `longhorn-backend` (DNS/connect timeouts), so `/csi/csi.sock` never appears. `evetest cluster info` timestamps confirm `edge-dev1` joined first at 20:26:27 UTC while `edge-dev2`/`edge-dev3` didn't even start joining until ~20:39, with cluster `storage.health` flapping HEALTHY/FAILED/DEGRADED throughout — the CSI/storage stack never stabilized inside the harness's readiness-wait window, producing the reported timeout (nodes are now all `Ready` in `kubectl get nodes`, so this resolved shortly after).

This shares the same root cause already identified for this run's earlier `TestThreeNodesCluster` failure on `edge-dev1` (etcd/control-plane churn during join stalling Longhorn/CDI convergence), and it's the **same known, recurring issue class** documented in `live-ledger.md` as "etcd apply-slowness / Longhorn-instability during cluster join/formation," tracked since at least **2026-09-09** — including two near-identical `TestTieBreakerCluster` "Timed out waiting for cluster nodes to become ready" occurrences in run #47 (2026-09-13) citing the exact same etcd-apply-slowness/Longhorn-flapping signature.


#### TestClusterToSingleConversion

##### Failure

```
Device "edge-dev3" failed to confirm the latest config (version 2): timed out waiting for LastProcessedConfig >= 2026-09-14 21:29:09.753308962 +0000 UTC
```

##### Claude's conclusion

Confirms live: zedbox restarted at 21:31:42, still in the middle of pubsub bootstrap (subscribing/initializing all agents) well after the test's 21:31:10 deadline. This matches exactly the known reboot-bound bootstrap race.

I now have sufficient evidence.

###### Root cause

`edge-dev3` was withdrawn from the cluster at 21:29:09, fetched config v2 at 21:29:10, and rebooted (boot time 21:29:49 per harness). The `ApplyConfig` confirm step uses a fixed 2-minute `deviceApplyConfigTimeout` (`conversion_test.go:212` / `edgedevice.go:280-327`), giving up at 21:31:10 — but live SSH into `edge-dev3` right now shows `uptime` of only 1 minute and `zedbox`/pillar logs still mid-bootstrap (agents like `volumemgr`, `zedmanager`, `zfsmanager` still creating pubsub sockets and subscribing at 21:31:42), i.e. it hadn't even reached the point of publishing an updated `LastProcessedConfig` when the harness's deadline hit. This is the identical mechanism as the earlier `TestClusterToSingleConversion` failure already analyzed in `live-investigation.md` for this run's ledger history, and it's a **known, recurring issue since at least 2026-09-10** per `live-ledger.md` (repeated on 2026-09-10 v8, 2026-09-11 v8, 2026-09-12 v5, and now 2026-09-14 v2): a test-timeout-margin bug where the withdrawal-triggered reboot + full pillar re-bootstrap routinely exceeds the fixed 2-minute confirm budget, not a new EVE/pillar regression.

## [2026-09-14 -- 0.0.0-master-03335b43 (run #49)](https://github.com/milan-zededa/eve/actions/runs/34797892351)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/49/)

### TestApplicationConnectivitySuite: failure analysis

#### TestSwitchNIPortConfigRace

##### Failure

```

Told to stop trying after 0.031s.
vlan-switch-ni: Network instance is in error state
networkID:"546078ac-6cd2-4cc6-9bdb-6aabe642e6cc"  networkVersion:"1"  instType:1  displayname:"vlan-switch-ni"  activated:true  CurrentUplinkIntf:"ethernet3"  ports:"ethernet3"  bridgeNum:2  bridgeName:"eth3"  ipAssignments:{macAddress:"02:16:3e:00:00:02"  ipAddress:"10.53.100.181"}  vifs:{vifName:"nbu2x1"  macAddress:"02:16:3e:00:00:02"  appID:"b42c9b66-ddeb-4e29-850c-e1a0f19606a7"}  assignedAdapters:{type:PhyIoNetEth  name:"ethernet3"  members:"ethernet3"  usedByAppUUID:"35b47967-76a8-4e6a-b56f-0b16caaf0956"  ioAddressList:{macAddress:"d6:42:ef:26:d8:c8"}}  networkErr:{description:"failed items: BridgeFwdMask/eth3 (failed to zero-out forwarding mask for bridge eth3: open /sys/class/net/eth3/bridge/group_fwd_mask: no such file or directory)"  timestamp:{seconds:1789368338  nanos:559214967}  severity:SEVERITY_ERROR}  state:ZNETINST_STATE_ERROR  mtu:1500
```

##### Claude's conclusion

I have enough evidence for the current run (#48). Two earlier failures this run (`TestIntermittentConnectivity`, `TestMgmtTrafficRoutedViaApp`) are unrelated (DNS/NIM-retest cadence and a test-predicate race, respectively) — no shared root cause with this one.

###### Root cause

Live device logs show `zedrouter`'s NI reconciler repeatedly deleting/recreating the `eth3` bridge (0.0.0-master-03335b43, `TestSwitchNIPortConfigRace`'s deliberate port-config churn moving `ethernet3`/`keth3` and the app VIF `nbu2x1` between NI configurations). At 06:45:38.549–.551 the reconciler tore down `bn2`'s `TCMirror`/`BridgePort`/`VLANPort`/`TCIngress`/`BridgeFwdMask` items for `eth3`, and the `BridgeFwdMask/eth3` delete at 06:45:38.598 (the retry logged just before) failed with `open /sys/class/net/eth3/bridge/group_fwd_mask: no such file or directory` — surfaced by zedagent at 06:45:38.662, matching the reported `networkErr` timestamp — because the bridge was transiently absent from sysfs mid-recreation (the new `VLANBridge/eth3` create didn't finish until 06:45:39.105–40.065, `expectedBridgeID` moving 0→16). Live inspection right now confirms `eth3` is a healthy bridge with `keth3`/`nbu2x1` attached and `group_fwd_mask 0`, i.e. it self-healed and this was transient, not a lasting fault.

This is the same **known, recurring** `BridgeFwdMask`/bridge-recreate race documented extensively in `live-ledger.md` — a lack of race-tolerance in the NI reconciler's bridge-dependent `Create`/`Delete` paths when the underlying bridge is torn down/recreated during `TestSwitchNIPortConfigRace`'s port churn (commit `8f277c37b` hardened some `Delete` paths but not all `Create` paths). It's been recurring since it was first exposed as a regression by this test on 2026-09-03/09-08 (run #26) and has reappeared in runs #31, #32, #34–#36, #42, #43, always against the synthetic `vlan100` bridge — today's occurrence is the same defect family but the first time it's hit the *physical-port* bridge `eth3` itself (and on a delete-path retry rather than a create), so it's a new manifestation of an old, still-unfixed bug rather than a new one.

### TestControllerFaultsSuite: failure analysis

#### TestDeferredQueueDropsReported

##### Failure

```

Timed out after 480.000s.
When passed a matcher, ReceiveMatcher's channel *must* receive something.
```

##### Claude's conclusion

**Root cause:** `reported-app` (`4927c1c3-…`) transitioned to `HALTED` (state 119) at 03:34:27.147, and zedagent published/sent its terminal `ZiApp` info at 03:34:27.168–.174 — but the test's armed `/info` 404 fault (active 03:33:55.716 → 03:34:28.773) was still in effect, so it was dropped as a permanent rejection at 03:34:27.183, ~1.6s before `ClearControllerFaults()` ran. Live evidence confirms this stands: `AppInstanceStatus` on the device still shows `State:119`/`Activated:false` (truly HALTED locally), but that was the last `PublishAppInfoToZedCloud` call ever logged for this app UUID — since its state never changes again, nothing re-triggers a fresh publish, so Adam/the harness's `app-info` view is stuck at `RUNNING` and `settleApp()` blocked for the full 8-minute timeout.

This does not share a root cause with today's earlier suite failures — `TestDeferredQueueBacklogReported` (the test immediately prior in the same suite) passed cleanly (193.8s). It **is** a known, recurring issue: this exact "terminal HALTED report raced/lost against fault-clear" mechanism was first accurately identified in run #46 (2026-09-13) — explicitly called first-seen there — recurred again in run #47 later that same day, and this is now its third occurrence (run #48, today). Note: the ledger's run #47 entry loosely attributes this to "since 2026-09-08" (run #26), but run #46's own analysis shows the 2026-09-08 occurrence was actually a different, unrelated bug (missing `DeferredQueueMetric`/image staleness) — so 2026-09-13 is the accurate start date for this specific race. No PR diff was supplied for this investigation, so no code-change correlation applies; this looks like a genuine gap (either in the test's fault-window sizing, or in zedagent lacking a resend path for a dropped info object once its source status stops changing) rather than a fresh regression.

### TestNodeClusterSuite: failure analysis

#### TestThreeNodesCluster

##### Failure

```
Timed out waiting for cluster nodes to become ready in cluster "test-cluster" (device "edge-dev3")
```

##### Claude's conclusion

**Root cause:** `edge-dev3`'s Longhorn/CDI pods are stuck crash-looping with `exec /usr/local/sbin/longhorn-manager: exec format error` (confirmed live via `kubectl logs`, reproducible on repeat calls, not a one-off) — and the identical failure occurs on `edge-dev2`, while `edge-dev1` (the first node to join) runs the exact same `longhornio/longhorn-manager:v1.9.1` image successfully. Node arch is confirmed `x86_64` on `edge-dev3`, ruling out a genuine cross-arch mismatch, so this looks like a corrupted/truncated image layer or overlay unpack specific to these two nodes. Pod events on `edge-dev3` show every Longhorn pod (`longhorn-manager`, `longhorn-ui` x2, `longhorn-driver-deployer`) hit `SandboxChanged: Pod sandbox changed, it will be killed and re-created` simultaneously ~15 minutes into the run — a mass sandbox invalidation consistent with the node's containerd/CRI runtime being disrupted mid-join, after which the re-created containers came up unable to exec their binaries. Because Longhorn never became healthy on `edge-dev3` (`cluster info` shows its `storage.health` stuck `FAILED` throughout), the node never satisfied cluster readiness, producing the reported timeout. This shares the same broader "joining-node instability during 3-node etcd-backed cluster formation" issue class documented in `live-ledger.md` as recurring since at least 2026-09-09, but every prior `TestThreeNodesCluster`/related ledger entry describes a different concrete mechanism (etcd apply-slowness/raft flapping, CSI `node-driver-registrar` no-route-to-host, or a stalled volume upload) — none mention an `exec format error`/corrupted-binary symptom, so this specific manifestation appears to be new/previously unrecorded, not a confirmed repeat of an exact known bug.

## [2026-09-14 -- 0.0.0-master-03335b43 (run #48)](https://github.com/milan-zededa/eve/actions/runs/34779372694)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/48/)

### TestDeviceConnectivitySuite: failure analysis

#### TestIntermittentConnectivity

##### Failure

```

Timed out after 300.000s.
Expected to satisfy: DPC LastSucceeded advances past the AdminUp=true transition
machineArch:"x86_64"  cpuArch:"x86_64"  platform:"x86_64"  ncpu:4  memory:7797  storage:40006  powerCycleCounter:-1  minfo:{manufacturer:"QEMU"  productName:"Standard PC (Q35 + ICH9, 2009)"  version:"pc-q35-10.1"  serialNumber:"NTXLTD4I"  UUID:"Not Settable"  biosVendor:"EDK II"  biosVersion:"unknown"  biosReleaseDate:"02/02/2022"}  assignableAdapters:{type:PhyIoNetEth  name:"ethernet0"  members:"ethernet0"  usedByBaseOS:true  ioAddressList:{macAddress:"da:39:78:d0:19:f2"}  usage:PhyIoUsageMgmtAndApps}  dns:{DNSservers:"127.0.0.1:53"  DNSsearch:"test."}  storageList:{device:"nbd3"}  storageList:{device:"nbd9"}  storageList:{device:"vda7"  total:2048  partitionLabel:"EFI System"  partitionTypeGuid:"c12a7328-f81f-11d2-ba4b-00a0c93ec93b"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30056"}  storageList:{mountPath:"/config"  total:5}  storageList:{mountPath:"/persist/clear/volumes"}  storageList:{mountPath:"/persist/ingested"}  storageList:{device:"nbd13"}  storageList:{device:"vda2"  total:10240  partitionLabel:"IMGA"  partitionTypeGuid:"5dfbf5f4-2848-4bac-aa5e-0d9a20b745a6"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30052"}  storageList:{mountPath:"/persist/clear"}  storageList:{mountPath:"/persist/agentdebug"}  storageList:{mountPath:"/persist/netdump"}  storageList:{mountPath:"/persist/tmp"}  storageList:{device:"vda9"  total:40953  partitionLabel:"P3"  partitionTypeGuid:"5f24425a-2dfa-11e8-a270-7b663faccc2c"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30059"}  storageList:{device:"vda4"  total:5  partitionLabel:"CONFIG"  partitionTypeGuid:"13307e62-cd9c-4920-8f9b-91b45828b798"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30054"}  storageList:{mountPath:"/persist/vault/containerd"}  storageList:{device:"nbd12"}  storageList:{device:"nbd14"}  storageList:{device:"nbd6"}  storageList:{mountPath:"/persist"  total:40006  storageLocation:true}  storageList:{mountPath:"/persist/nettrace"}  storageList:{mountPath:"/persist/vault/volumes"}  storageList:{mountPath:"/persist/vault"}  storageList:{device:"vda3"  total:10240  partitionLabel:"IMGB"  partitionTypeGuid:"5dfbf5f4-2848-4bac-aa5e-0d9a20b745a6"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30053"}  storageList:{device:"nbd0"}  storageList:{device:"nbd10"}  storageList:{mountPath:"/persist/certs"}  storageList:{device:"/persist/vector/data/buffer/v2/keep_sent_queue_socket/buffer-data-0.dat"}  storageList:{device:"nbd4"}  storageList:{mountPath:"/persist/checkpoint"}  storageList:{device:"vda1"  total:2048  partitionLabel:"EFI System"  partitionTypeGuid:"c12a7328-f81f-11d2-ba4b-00a0c93ec93b"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30051"}  storageList:{device:"nbd11"}  storageList:{mountPath:"/persist/containerd-system-root"}  storageList:{device:"nbd1"}  storageList:{mountPath:"/hostfs"  total:272}  storageList:{mountPath:"/persist/status"}  storageList:{mountPath:"/persist/newlog"}  storageList:{device:"nbd8"}  storageList:{mountPath:"/persist/vault/verifier"}  storageList:{device:"nbd2"}  storageList:{device:"/persist/vector/data/buffer/v2/dev_upload_socket/buffer-data-0.dat"}  storageList:{mountPath:"/"  total:3898}  storageList:{mountPath:"/persist/vault/downloader"}  storageList:{device:"nbd15"}  storageList:{mountPath:"/persist/patchEnvelopesUsageCache"}  storageList:{mountPath:"/persist/patchEnvelopesCache"}  storageList:{device:"vda"  total:65536}  storageList:{mountPath:"/persist/pubsub-large"}  storageList:{device:"nbd5"}  storageList:{mountPath:"/persist/memory-monitor/output"}  storageList:{mountPath:"/persist/log"}  storageList:{device:"nbd7"}  bootTime:{seconds:1789336625}  swList:{activated:true  partitionLabel:"IMGA"  partitionDevice:"/dev/vda2"  partitionState:"active"  status:INSTALLED  shortVersion:"0.0.0-master-03335b43-kvm-amd64"  downloadProgress:100  userStatus:UPDATED}  swList:{partitionLabel:"IMGB"  partitionDevice:"/dev/vda3"  partitionState:"unused"  status:INITIAL}  HostName:"d8a40db6-63c4-45d6-8f18-28972252cf31"  lastRebootReason:"NORMAL: First boot of device - at 2026-09-13T21:57:55.453064319Z"  lastRebootTime:{seconds:1789336675  nanos:453064319}  systemAdapter:{status:{version:1  key:"zedagent"  timePriority:{seconds:1789336707  nanos:971971147}  lastSucceeded:{seconds:1789336716  nanos:646776169}  ports:{ifname:"eth0"  name:"ethernet0"  isMgmt:true  free:true  dhcpType:4  subnet:"172.20.20.0/24"  domainname:"test."  proxy:{}  macAddr:"da:39:78:d0:19:f2"  IPAddrs:"172.20.20.123"  defaultRouters:"172.20.20.1"  dns:{DNSservers:"10.16.16.25"  DNSdomain:"test."}  up:true  err:{description:"All attempts to connect to https://adam.evetest:443/api/v2/edgedevice/ping failed: send via eth0 with src IP 172.20.20.123: Get \"https://adam.evetest:443/api/v2/edgedevice/ping\": dial tcp: lookup adam.evetest on 127.0.0.1:53: read udp 127.0.0.1:54349->127.0.0.1:53: i/o timeout"  timestamp:{seconds:1789337028  nanos:89247942}  severity:SEVERITY_ERROR}  usage:PhyIoUsageMgmtAndApps  networkUUID:"f64bdb05-b7a3-44fd-a334-63827ca5a01c"  mtu:1500  config_source:{origin:NETWORK_CONFIG_ORIGIN_CONTROLLER  submitted_at:{seconds:1789336707  nanos:971971147}}  pnac_status:{}}}}  HSMStatus:NOTFOUND  dataSecAtRestInfo:{status:DATASEC_AT_REST_DISABLED  info:"TPM is either absent or not in use"  vaultList:{name:"Application Data Store"  status:DATASEC_AT_REST_DISABLED  vaultErr:{description:"TPM is either absent or not in use"  timestamp:{seconds:1789336698  nanos:399548886}  severity:SEVERITY_ERROR}  pcrStatus:PCR_DISABLED}}  sec_info:{sha_root_ca:"\x8b<\x91\xcc\xc7y\x9f\xbe;w\xbf\x0f\x83d\xf0\xa5]\x01F\xcc\x19Y=\xf0\\,\xa5\x9e\x80\xb6\x9d@"}  configItemStatus:{configItems:{key:"app.allow.vnc"  value:{value:"true"}}  configItems:{key:"debug.default.loglevel"  value:{value:"debug"}}  configItems:{key:"debug.default.remote.loglevel"  value:{value:"debug"}}  configItems:{key:"debug.enable.console"  value:{value:"true"}}  configItems:{key:"debug.enable.ssh"  value:{value:"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAQC+H1RQUqHjFBJgGpslC73XsLz8Fg5WpNPble9naKyWz1Um8D2bOtQK/yguCImPeBYcH7/73z8dtC6d+dT0UF26+o7Vh6RN/U2X/5nkaZr7oM5QwwZTsD7Nd2Szww9wrRhXvpV0aFgUBDM9BIF1qBQxLNd+Jp8uttrgF3zj/cm7+SXllG54sv8WFBMfTX7J8cQ1jxLyp/Sc6PXK0zBaVzZwhmCCmI6CIzJK6ahMRgXm2vSP6doYibkB3ETSskaCXSHxDiZoaQK2ZY+GqNZkUusbau43MXVPTiJknXqUcmXhQmwyMSltQ3G54jcgn4TDObSQnW7vGdLI7zIEAHnk5D1BmKzQUBh5aRLdhBtb6T3uvVtAqGgnXWKD+d2GjMiy4G31zfIlArvC3G8LxwsDoxQL0XKaFjnEmXIptVXC68zq+laIM8YGDDOCEc6RfczP7lA4p6rv0gQUfTNqy0P3a4ulvIDb4hET1Gkh+Azkuw1do9NIhXPxDBmPdkTnKwJv6XelpdCPCw1QT5o7WdOkUgorf/e03jGDnnn1QBWpSPB9gLB/oDmT/Gzm8tCYaq7ggwYUq1fSMBvnbFaclH1KVcC2Gwn8UPLt9HHa/mGywuISZYl3gy7ztlKyAjHEZH053U7I8OaMvv/CFo9aR4Teeb5848REZYAes+yJIz3lJV1K5Q=="}}  configItems:{key:"newlog.allow.fastupload"  value:{value:"true"}}  configItems:{key:"timer.config.interval"  value:{value:"5"}}  configItems:{key:"timer.deviceinfo.interval"  value:{value:"30"}}  configItems:{key:"timer.download.retry"  value:{value:"60"}}  configItems:{key:"timer.location.app.interval"  value:{value:"20"}}  configItems:{key:"timer.location.cloud.interval"  value:{value:"300"}}  configItems:{key:"timer.metric.interval"  value:{value:"20"}}  configItems:{key:"timer.test.baseimage.update"  value:{value:"60"}}}  appInstances:{uuid:"71ca8bf0-a4da-4d8b-857d-4560c0f712fc"  name:"intermittent-test-app"  domainName:"71ca8bf0-a4da-4d8b-857d-4560c0f712fc.1.1"}  last_boot_reason:BOOT_REASON_FIRST  hardware_watchdog_present:true  capabilities:{HWAssistedVirtualization:true  IOVirtualization:true}  state:ZDEVICE_STATE_ONLINE  storage_info:{pool_name:"/persist"  storage_type:STORAGE_TYPE_INFO_EXT4  storage_state:STORAGE_STATUS_ONLINE  disks:{disk_name:{name:"/dev/vda9"  serial:"unknown"}  status:STORAGE_STATUS_ONLINE  smart_status:"failed"}}  attestation_info:{state:ATTESTATION_STATE_COMPLETE}  api_capability:API_CAPABILITY_DEFERRED_QUEUE_METRICS  optional_capabilities:{hw_inventory_support:true}  device_name:"edge-dev"
```

##### Claude's conclusion

**Root cause:** Live evidence from `/run/nim/DeviceNetworkStatus/global.json` and the `nim` agent log confirms the failure precisely: after the SDN restored `eth0` (`admin_up:true`) at `22:03:45`, the very next connectivity probe at `22:03:48.089` failed with `dial tcp: lookup adam.evetest on 127.0.0.1:53: read udp 127.0.0.1:54349->127.0.0.1:53: i/o timeout` (a transient local-resolver hiccup right as the link came back), setting `LastFailed`. NIM then did not retest the port again until `22:08:48.217` — exactly one `NetworkTestInterval` (300s) later — so `LastSucceeded` only advanced to `22:08:48.216`, ~5 minutes after the test's own 300s timeout had already fired at `22:08:45`. This is not related to the earlier `TestIntermittentConnectivity` failure logged for this same run #47 (that one failed during setup on an unrelated SDN gRPC "no route to host" infra issue before onboarding even completed) — this is a later retry of the same test that got much further. It does share the same underlying class of defect documented repeatedly in `live-ledger.md` since 2026-09-08 (run #26) for `TestLACPBond`/`TestActiveBackupBond` — NIM's periodic DPC re-test cadence being gated to the full 300s `NetworkTestInterval` with no event-driven fast retest — but those prior entries were specifically about a two-port same-cost race via `controllerconn.VerifyAllIntf`'s dry-run skip logic, whereas here there's only a single mgmt port and a genuinely transient DNS failure, so this exact single-port manifestation doesn't appear previously recorded in the ledger. Net: known/recurring defect class (NIM's 300s test-interval cadence colliding with 300s-timeout tests), but this specific single-port trigger scenario for `TestIntermittentConnectivity` is new.


#### TestMgmtTrafficRoutedViaApp

##### Failure

```

Expected
    <[]string | len:1, cap:1>: [
        "fe80::16:3eff:fe02:1",
    ]
to contain element matching
    <string>: 10.60.20.150
```

##### Claude's conclusion

###### Root cause

Live `app-info` history for `mgmt-gw-app` (`c45f1889-…`) shows the VIF-1/`nbu2x1` (ni-lan) update stream go: `IPAddrs:["fe80::16:3eff:fe02:1"]` in one `ZInfoApp` snapshot, then `IPAddrs:["10.60.20.150","fe80::16:3eff:fe02:1"]` in the very next one — the DHCPv4 ACK (`YourClientIP=10.60.20.150`) was captured by zedrouter/nistate's pcap sniffer on `eth1-m` only fractions of a second after the link-local IPv6 address appeared. `routing_test.go`'s `Eventually` predicate (line ~1651) only checks `len(vif.IPAddrs) != 0` for both VIFs, so it latched onto the first (IPv6-link-local-only) snapshot and froze `appInfo` there; the subsequent one-shot `Expect(...ContainElement(lanAppIP))` then failed even though the real DHCP-assigned IP arrived correctly moments later. This is a test-side race (the predicate should wait for a real IPv4, not just "any non-empty IPAddrs"), not the previously-known "pcap-open race" defect in `nistate/linux.go` — here pcap opened successfully on both mirrors and DHCP snooping worked correctly, unlike the permanent-capture-loss bug behind the many `TestMgmtTrafficRoutedViaApp`/`TestAirGapSwitchNI` ledger entries since 2026-09-09. This is unrelated to the earlier `TestIntermittentConnectivity` failure in this run (a DNS-resolution/NIM-retest-cadence issue). Net: same *symptom* (only IPv6 link-local reported) as the long-standing ledger entries, but a distinct, previously unrecorded root cause — a benign timing race in the test's own "any IP present" predicate rather than the known pillar pcap defect.

### TestNodeClusterSuite: failure analysis

#### TestThreeNodesCluster

##### Failure

```
Timed out waiting for cluster nodes to become ready in cluster "test-cluster" (device "edge-dev3")
```

##### Claude's conclusion

###### Root cause

Live evidence points to the same chronic etcd/control-plane + Longhorn instability during 3-node cluster join that's recurred in this suite repeatedly. `evetest cluster info` shows Longhorn `storage.health` for all three nodes flapping `HEALTHY`↔`FAILED`↔`DEGRADED` throughout the join window, and `kubectl get pods -A` shows `longhorn-csi-plugin-gw7l7` on `edge-dev2` in `CrashLoopBackOff` (10 restarts/14m), with events showing the CSI plugin's `node-driver-registrar` unable to reach `/csi/csi.sock` and the pod's own IP becoming briefly unroutable ("no route to host"), triggering repeated startup-probe kills. Both `edge-dev2` and `edge-dev3` show hundreds of etcd `"apply request took too long"` warnings in `k3s.log` (487 and 616 occurrences respectively), and `edge-dev2`'s `k3s init` process is only ~16 minutes old against a 34-minute device uptime — i.e. it crash-restarted mid-join, delaying that node's readiness past the harness's wait window even though `kubectl get nodes` now shows all three `Ready`.

This is a known, recurring issue class per `live-ledger.md`: the identical "Timed out waiting for cluster nodes to become ready" message and etcd-apply-slowness/Longhorn-flapping signature appear multiple times (e.g. a near-identical `edge-dev2` timeout entry, and a `TestTieBreakerCluster` entry citing "same failure mechanism as TestThreeNodesCluster"), all tracing back to an etcd apply-slowness/Longhorn-instability theme documented since at least **2026-09-09**. It's a slow-convergence near-miss rather than a permanent break — the cluster does eventually reach Ready shortly after the timeout, consistent with past occurrences. No other test failed earlier in this run to correlate with, and no PR diff was provided for this investigation.


#### TestTieBreakerCluster

##### Failure

```
Timed out waiting for cluster nodes to become ready in cluster "test-cluster" (device "edge-dev2")
```

##### Claude's conclusion

###### Root cause

Live evidence on `edge-dev2` mirrors the exact signature already root-caused for `TestThreeNodesCluster` earlier in this run (see `live-investigation.md`): `k3s.log` on `edge-dev2` shows 763 occurrences of etcd `"apply request took too long"` spanning the entire 00:14:32–00:40:57 window, with rafthttp repeatedly losing and re-establishing its TCP streaming connection to peer `9c1cec3e6968c9f5` (`lost TCP streaming connection... error: EOF` at 00:21:17); `edge-dev3` shows 835 of the same warnings. `evetest cluster info` shows Longhorn `storage.health` flapping `HEALTHY`↔`FAILED`↔`DEGRADED` across all three nodes throughout the join, and `kubectl get nodes` confirms `edge-dev2`/`edge-dev3` (age 19m) joined ~11 minutes after `edge-dev1` (age 30m) — i.e. the non-first nodes were delayed past the harness's readiness-wait window by etcd apply-slowness, not a permanent failure (by now all three nodes are `Ready` and `kubectl get pods -A` shows nothing outside `Running`/`Completed`).

This is the same root-cause mechanism as `TestThreeNodesCluster` (already investigated in this run) and, per `live-ledger.md`, this exact suite run (`run #47`, build `03335b43`) already recorded an earlier `TestTieBreakerCluster` timeout with identical symptoms (that one on `edge-dev1`, at ~14:xx) — so this test has now failed twice tonight with the same signature, on different nodes each time. The broader etcd-apply-slowness/Longhorn-instability issue class is a known, recurring problem documented in the ledger since at least **2026-09-09**, and it's a slow-convergence near-miss (the cluster does reach Ready, just after the timeout) rather than a functional break.

### TestStorageSuite: failure analysis

#### TestVolumes

##### Failure

```

Told to stop trying after 0.010s.
Stop waiting for: volume v-vhdx is created
Volume reports an error: Found error in content tree v-vhdx-image attached to volume v-vhdx: 
Size '0' provided in image config of 'v-vhdx.vhdx' is incorrect.
Download status (2162688 / 0). Aborting the download

uuid:"7b2a3327-d363-42f4-8572-1540098f5cc5" displayName:"v-vhdx" usage:{createTime:{seconds:-62135596800} refCount:1 lastRefcountChangeTime:{seconds:1789338545 nanos:907201081}} state:DOWNLOAD_STARTED volumeErr:{description:"Found error in content tree v-vhdx-image attached to volume v-vhdx: \nSize '0' provided in image config of 'v-vhdx.vhdx' is incorrect.\nDownload status (2162688 / 0). Aborting the download\n" timestamp:{seconds:1789338548 nanos:155437226} severity:SEVERITY_NOTICE entities:{entity:ENTITY_CONTENT_TREE entity_id:"c35a7fef-3f26-49fc-8fcc-7222f4aa66a2"} retry_condition:"Will retry in 1m0s; have retried 0 times"}
```

##### Claude's conclusion

Root cause confirmed live: `downloader` logs show `ExecCmd get http://244.244.244.244:80/v-vhdx.vhdx failed (attempt 0/7): error from CopyN after 7394912 out of 0 bytes: context canceled`, immediately followed by `Size '0' provided in image config of 'v-vhdx.vhdx' is incorrect` — the datastore reported `totalSize:0` while `currentSize` was already nonzero, which trips the unconditional `currentSize > totalSize` abort check. Current master (`pkg/pillar/cmd/downloader/download.go:327`) already has the fix guarding this with `totalSize > 0 &&`, but the device under test is running image `0.0.0-master-03335b43`, confirmed via `git merge-base --is-ancestor ed743e546 03335b43c` to predate that fix commit.

This is the same `TestVolumes`/`v-vhdx` failure documented extensively in `live-ledger.md` — a known, recurring issue tracked since at least 2026-09-09 (run #31), recurring through runs #32/#35/#36/#37/#41+ and again in yesterday's run #47 (which ran the identical stale build `03335b43`). It doesn't share a root cause with any other failure in this run (no other suite has failed yet), and it's not a new regression — it's simply that the nightly device build still hasn't picked up the already-merged fix.


#### TestZFSDiskLayout

##### Failure

```

Timed out after 600.001s.
Expected to satisfy: no disk is degraded or offline
machineArch:"x86_64" cpuArch:"x86_64" platform:"x86_64" ncpu:4 memory:7797 storage:35080 powerCycleCounter:-1 minfo:{manufacturer:"QEMU" productName:"Standard PC (Q35 + ICH9, 2009)" version:"pc-q35-10.1" serialNumber:"SRHG60XC" UUID:"Not Settable" biosVendor:"EDK II" biosVersion:"unknown" biosReleaseDate:"02/02/2022"} assignableAdapters:{type:PhyIoNetEth name:"ethernet0" members:"ethernet0" usedByBaseOS:true ioAddressList:{macAddress:"da:39:78:d0:19:f2"} usage:PhyIoUsageMgmtAndApps} dns:{DNSservers:"127.0.0.1:53" DNSsearch:"test."} storageList:{mountPath:"/persist/memory-monitor/output"} storageList:{device:"nbd2"} storageList:{device:"vdc9" total:8 partitionTypeGuid:"6a945a3b-1dd2-11b2-99a6-080020736631" partitionUuid:"3b042239-fa8a-fa4d-ae52-68a3364590f0"} storageList:{mountPath:"/persist/nettrace"} storageList:{mountPath:"/persist/clear/volumes"} storageList:{mountPath:"/persist/checkpoint"} storageList:{mountPath:"/" total:3898} storageList:{device:"nbd11"} storageList:{mountPath:"/persist/patchEnvelopesUsageCache"} storageList:{mountPath:"dataset persist/vault/volumes"} storageList:{device:"nbd13"} storageList:{device:"/persist/vector/data/buffer/v2/keep_sent_queue_socket/buffer-data-0.dat"} storageList:{mountPath:"dataset persist/reserved"} storageList:{mountPath:"/persist" total:35080 storageLocation:true} storageList:{device:"nbd0"} storageList:{mountPath:"/persist/tmp"} storageList:{device:"vdb9" total:8 partitionTypeGuid:"6a945a3b-1dd2-11b2-99a6-080020736631" partitionUuid:"5fa07498-b800-e04c-a6e7-290a200aa9a5"} storageList:{mountPath:"/hostfs" total:272} storageList:{device:"/persist/vector/data/buffer/v2/dev_upload_socket/buffer-data-0.dat"} storageList:{device:"vde9" total:8 partitionTypeGuid:"6a945a3b-1dd2-11b2-99a6-080020736631" partitionUuid:"1dfe42e0-66ef-7543-a5d1-c297c318bfe4"} storageList:{device:"nbd10"} storageList:{device:"nbd1"} storageList:{mountPath:"/persist/patchEnvelopesCache"} storageList:{device:"vda4" total:5 partitionLabel:"CONFIG" partitionTypeGuid:"13307e62-cd9c-4920-8f9b-91b45828b798" partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30054"} storageList:{device:"vdb" total:2048} storageList:{device:"vde" total:2048} storageList:{mountPath:"/persist/vault/containerd"} storageList:{mountPath:"/persist/vault/volumes"} storageList:{device:"nbd9"} storageList:{device:"nbd4"} storageList:{device:"vda" total:65536} storageList:{mountPath:"/persist/containerd"} storageList:{device:"nbd8"} storageList:{device:"vdd1" total:2038 partitionLabel:"zfs-f12312ddfa9bbe09" partitionTypeGuid:"6a898cc3-1dd2-11b2-99a6-080020736631" partitionUuid:"e254c577-500a-724f-bd77-ffab424d86b6"} storageList:{device:"vdd" total:2048} storageList:{mountPath:"dataset persist/snapshots"} storageList:{device:"vda3" total:10240 partitionLabel:"IMGB" partitionTypeGuid:"5dfbf5f4-2848-4bac-aa5e-0d9a20b745a6" partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30053"} storageList:{device:"nbd14"} storageList:{mountPath:"/persist/vault"} storageList:{mountPath:"/persist/clear"} storageList:{mountPath:"/persist/reserved"} storageList:{device:"nbd15"} storageList:{device:"vdc1" total:2038 partitionLabel:"zfs-bdb677b9c278abee" partitionTypeGuid:"6a898cc3-1dd2-11b2-99a6-080020736631" partitionUuid:"f2236ecb-4816-f540-b089-9f31ce1bd704"} storageList:{mountPath:"dataset persist/clear/volumes"} storageList:{mountPath:"dataset persist/clear"} storageList:{device:"nbd5"} storageList:{device:"vda7" total:2048 partitionLabel:"EFI System" partitionTypeGuid:"c12a7328-f81f-11d2-ba4b-00a0c93ec93b" partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30056"} storageList:{mountPath:"/persist/agentdebug"} storageList:{mountPath:"/persist/pubsub-large"} storageList:{device:"vde1" total:2038 partitionLabel:"zfs-2ee8f31f11d5600d" partitionTypeGuid:"6a898cc3-1dd2-11b2-99a6-080020736631" partitionUuid:"ef19be2f-a2d3-ba44-bb3d-f20cb991eea6"} storageList:{device:"vdc" total:2048} storageList:{mountPath:"dataset persist/vault"} storageList:{device:"vda9" total:40953 partitionLabel:"P3" partitionTypeGuid:"5f24425a-2dfa-11e8-a270-7b663faccc2c" partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30059"} storageList:{mountPath:"/persist/ingested"} storageList:{device:"nbd12"} storageList:{device:"nbd7"} storageList:{device:"nbd6"} storageList:{mountPath:"/persist/newlog"} storageList:{device:"vdb1" total:2038 partitionLabel:"zfs-1ef1bfceb8a518f3" partitionTypeGuid:"6a898cc3-1dd2-11b2-99a6-080020736631" partitionUuid:"1a169ffc-97f7-4a46-b6bb-1721d758d708"} storageList:{device:"vda1" total:2048 partitionLabel:"EFI System" partitionTypeGuid:"c12a7328-f81f-11d2-ba4b-00a0c93ec93b" partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30051"} storageList:{mountPath:"/persist/vault/downloader"} storageList:{device:"vdf" total:2048} storageList:{mountPath:"/persist/log"} storageList:{mountPath:"/persist/containerd-system-root"} storageList:{mountPath:"/config" total:5} storageList:{mountPath:"dataset persist"} storageList:{device:"vdd9" total:8 partitionTypeGuid:"6a945a3b-1dd2-11b2-99a6-080020736631" partitionUuid:"deb34bba-0a9d-6544-b083-9dba0a4f8d68"} storageList:{mountPath:"/persist/vault/verifier"} storageList:{mountPath:"/persist/status"} storageList:{device:"nbd3"} storageList:{mountPath:"/persist/certs"} storageList:{device:"vda2" total:10240 partitionLabel:"IMGA" partitionTypeGuid:"5dfbf5f4-2848-4bac-aa5e-0d9a20b745a6" partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30052"} storageList:{mountPath:"/persist/netdump"} bootTime:{seconds:1789340706} swList:{activated:true partitionLabel:"IMGA" partitionDevice:"/dev/vda2" partitionState:"active" status:INSTALLED shortVersion:"0.0.0-master-03335b43-kvm-amd64" downloadProgress:100 userStatus:UPDATED} swList:{partitionLabel:"IMGB" partitionDevice:"/dev/vda3" partitionState:"unused" status:INITIAL} HostName:"09e89c80-2c0e-4d84-a036-06718f4e9250" lastRebootReason:"NORMAL: First boot of device - at 2026-09-13T23:05:44.423643761Z" lastRebootTime:{seconds:1789340744 nanos:423643761} systemAdapter:{status:{version:1 key:"zedagent" timePriority:{seconds:1789340803 nanos:26531587} lastSucceeded:{seconds:1789341455 nanos:121110212} ports:{ifname:"eth0" name:"ethernet0" isMgmt:true free:true dhcpType:4 subnet:"172.20.20.0/24" domainname:"test." proxy:{} macAddr:"da:39:78:d0:19:f2" IPAddrs:"172.20.20.123" defaultRouters:"172.20.20.1" dns:{DNSservers:"10.16.16.25" DNSdomain:"test."} up:true err:{timestamp:{seconds:1789341455 nanos:121007866}} usage:PhyIoUsageMgmtAndApps networkUUID:"1976a765-7d38-47e0-9b7d-225fca54b040" mtu:1500 config_source:{origin:NETWORK_CONFIG_ORIGIN_CONTROLLER submitted_at:{seconds:1789340803 nanos:26531587}} pnac_status:{}}}} HSMStatus:NOTFOUND dataSecAtRestInfo:{status:DATASEC_AT_REST_DISABLED info:"TPM is either absent or not in use" vaultList:{name:"Application Data Store" status:DATASEC_AT_REST_DISABLED vaultErr:{description:"TPM is either absent or not in use" timestamp:{seconds:1789340791 nanos:775946433} severity:SEVERITY_ERROR} pcrStatus:PCR_DISABLED}} sec_info:{sha_root_ca:"\xc9Q\xaa\\\xa2\x96D\xe5\xce\n\xec\xf3\x101\x93*AK\x87\xb1.Ea\x89\xd4xx)|Y\x1f\x8a"} configItemStatus:{configItems:{key:"app.allow.vnc" value:{value:"true"}} configItems:{key:"debug.default.loglevel" value:{value:"debug"}} configItems:{key:"debug.default.remote.loglevel" value:{value:"debug"}} configItems:{key:"debug.enable.console" value:{value:"true"}} configItems:{key:"debug.enable.ssh" value:{value:"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAQC+H1RQUqHjFBJgGpslC73XsLz8Fg5WpNPble9naKyWz1Um8D2bOtQK/yguCImPeBYcH7/73z8dtC6d+dT0UF26+o7Vh6RN/U2X/5nkaZr7oM5QwwZTsD7Nd2Szww9wrRhXvpV0aFgUBDM9BIF1qBQxLNd+Jp8uttrgF3zj/cm7+SXllG54sv8WFBMfTX7J8cQ1jxLyp/Sc6PXK0zBaVzZwhmCCmI6CIzJK6ahMRgXm2vSP6doYibkB3ETSskaCXSHxDiZoaQK2ZY+GqNZkUusbau43MXVPTiJknXqUcmXhQmwyMSltQ3G54jcgn4TDObSQnW7vGdLI7zIEAHnk5D1BmKzQUBh5aRLdhBtb6T3uvVtAqGgnXWKD+d2GjMiy4G31zfIlArvC3G8LxwsDoxQL0XKaFjnEmXIptVXC68zq+laIM8YGDDOCEc6RfczP7lA4p6rv0gQUfTNqy0P3a4ulvIDb4hET1Gkh+Azkuw1do9NIhXPxDBmPdkTnKwJv6XelpdCPCw1QT5o7WdOkUgorf/e03jGDnnn1QBWpSPB9gLB/oDmT/Gzm8tCYaq7ggwYUq1fSMBvnbFaclH1KVcC2Gwn8UPLt9HHa/mGywuISZYl3gy7ztlKyAjHEZH053U7I8OaMvv/CFo9aR4Teeb5848REZYAes+yJIz3lJV1K5Q=="}} configItems:{key:"newlog.allow.fastupload" value:{value:"true"}} configItems:{key:"timer.config.interval" value:{value:"5"}} configItems:{key:"timer.deviceinfo.interval" value:{value:"30"}} configItems:{key:"timer.download.retry" value:{value:"60"}} configItems:{key:"timer.location.app.interval" value:{value:"20"}} configItems:{key:"timer.location.cloud.interval" value:{value:"300"}} configItems:{key:"timer.metric.diskscan.interval" value:{value:"15"}} configItems:{key:"timer.metric.interval" value:{value:"20"}} configItems:{key:"timer.test.baseimage.update" value:{value:"60"}}} last_boot_reason:BOOT_REASON_FIRST hardware_watchdog_present:true capabilities:{HWAssistedVirtualization:true IOVirtualization:true} state:ZDEVICE_STATE_ONLINE storage_info:{pool_name:"persist" storage_type:STORAGE_TYPE_INFO_ZFS zfs_version:"zfs-kmod-2.3.6-1" current_raid:STORAGE_RAID_TYPE_NORAID compression_ratio:5.39 zpool_size:46439333888 storage_state:STORAGE_STATUS_DEGRADED children:{current_raid:STORAGE_RAID_TYPE_NORAID disks:{disk_name:{name:"/dev/vda9" serial:"unknown"} status:STORAGE_STATUS_ONLINE} display_name:"/dev/vda9"} children:{current_raid:STORAGE_RAID_TYPE_RAID1 disks:{disk_name:{name:"/dev/vdb" serial:"unknown"}} disks:{disk_name:{name:"/dev/vdc" serial:"unknown"} status:STORAGE_STATUS_ONLINE} display_name:"mirror" g_u_i_d:16126133111966984000} children:{current_raid:STORAGE_RAID_TYPE_RAID1 disks:{disk_name:{name:"/dev/vdd" serial:"unknown"} status:STORAGE_STATUS_ONLINE} disks:{disk_name:{name:"/dev/vde" serial:"unknown"} status:STORAGE_STATUS_ONLINE} display_name:"mirror" g_u_i_d:8723469386646624000} pool_status_msg:"One or more devices could not be used because the label is missing or invalid. Sufficient replicas exist for the pool to continue functioning in a degraded state."} attestation_info:{state:ATTESTATION_STATE_COMPLETE} api_capability:API_CAPABILITY_DEFERRED_QUEUE_METRICS optional_capabilities:{hw_inventory_support:true} device_name:"edge-dev"
```

##### Claude's conclusion

No prior occurrences in the ledger. I have enough evidence now to write the root-cause summary.

###### Root cause

Live `zfsmanager` pillar logs pinpoint the failure precisely: the offline→online sequence raced against the RAID10 mirror attach ZFS had just performed. The first attempt to offline `/dev/vdb` (at 23:08:16, ~4s after the `raid10-applied` checkpoint) errored with `cannot bring /dev/vdb offline: dataset does not exist`, and although the disk did subsequently go OFFLINE, when the test's config change asked ZFS to bring it back online at 23:09:00, ZFS itself reported `Device '/dev/vdb' onlined, but remains in faulted state` — a real libzfs-level fault, not a pillar bug in issuing the call. From that point the vdev sat in `CANT_OPEN`, and `handlediskconfig.go`'s `disksStateProcessing` switch only knows how to drive `Offline→Online` and `Online→Offline` transitions; `CANT_OPEN`/faulted falls into its `default:` branch, which just logs `unexpected state of disk /dev/vdb (CANT_OPEN) to make online` every ~60s forever with no recovery action — exactly matching the live `zpool status`/`zpool list -v` output showing `vdb UNAVAIL` / "label is missing or invalid" 10+ minutes later when the test gave up. This is a distinct subsystem/mechanism from the `TestVolumes`/downloader `Size '0'` bug recorded in this run's `live-investigation.md` (that's a stale-build downloader issue, unrelated to ZFS disk-layout handling), and I found no prior occurrence of this `TestZFSDiskLayout`/CANT_OPEN signature in `live-ledger.md`, so this looks like a newly-observed issue in this run rather than a known recurring one.

## [2026-09-13 -- 0.0.0-master-03335b43 (run #47)](https://github.com/milan-zededa/eve/actions/runs/34761384855)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/47/)

### TestApplicationConnectivitySuite: failure analysis

#### TestFlowLog

##### Failure

```
timed out after 5m0.00098094s (excluding download) waiting for app "56a7900d-aa7d-4cf5-ada0-88fdc87252e5" (flowlog-test-app) on device "edge-dev" to reach RUNNING state (last state: RESOLVING_TAG)
```

##### Claude's conclusion

That confirms it: local socket at `7B1414AC:C5B3 → 02AAA8C0:1388` (192.168.170.2:5000) is state `01` (ESTABLISHED) with a non-zero `tx_queue` (0xD7), i.e. a stalled connection that never completes/errors — exactly the same stuck-HTTPS-probe pattern from before.

###### Root cause

`flowlog-test-app` is stuck in `RESOLVING_TAG` while pulling `lfedge/evetest-ubuntu-ctr:1.0` from datastore `docker://192.168.170.2:5000` (confirmed via `evetest eve app-info` and `evetest eve config`). Live `ss`/`/proc/net/tcp` on the device shows the tag-resolve connection to `192.168.170.2:5000` sitting `ESTABLISHED` with a non-zero, stalled `tx_queue`, and a live `curl` confirms the registry is HTTP-only (`http://…/v2/` → clean `200`) while HTTPS fails immediately with `wrong version number`. Since the datastore isn't marked insecure/localhost, `go-containerregistry`'s resolve path only tries HTTPS with no request deadline, so the connection hangs rather than falling back to HTTP — this is the identical mechanism already root-caused in this same run's ledger entry for `TestFlowLog` (timestamped earlier today), and per `live-ledger.md` it's a known, recurring issue against this same registry going back to at least 2026-09-09 (`TestLocalNetInstanceACLsWithFlowLog` run #31, and a `multi-port-app` occurrence). This is not a new bug — it's a re-occurrence of the long-standing HTTPS-probe-hang-against-an-HTTP-only-registry defect in the OCI tag-resolution path, not a pillar/EVE regression from recent commits.


#### TestPortForwarding

##### Failure

```
Failed to receive SDN tunnel properties: rpc error: code = Unavailable desc = unable to connect to SDN gRPC service on any of the uplink IPs ([192.168.170.4]): failed to establish tunnel to SDN: rpc error: code = Unavailable desc = connection error: desc = "transport: Error while dialing: dial tcp 192.168.170.4:50121: connect: no route to host"
```

##### Claude's conclusion

This confirms the identical signature seen repeatedly in the ledger: SDN client uninitialized, SSH resetting during key exchange, device not onboarded. This is exactly the same recurring infra pattern, not a new bug.

###### Root cause

Test-infrastructure failure, not an EVE/pillar code defect. The broker provisioned SDN VM `sdn-114d8a89`, which obtained uplink IP `192.168.170.4` at 18:03:22, but every gRPC tunnel-connect retry from 18:03:37 through at least 18:05:31 failed with `no route to host` (per `gotest.json`), matching the reported error exactly. Live checks right now reproduce the identical signature: `evetest sdn status`/`logs` → "SDN client is not initialized", `evetest sdn ssh` → TCP connects then resets during SSH key exchange, and `evetest eve info` → device "not onboarded" — the SDN VM's network path never came up at all.

This does not share a root cause with the earlier `TestFlowLog` failure in this same run (that was an unrelated HTTPS-probe-hang against the HTTP-only registry). It is, however, a well-established, recurring infra issue per `live-ledger.md`: the identical "uplink IP obtained, then every gRPC/SSH connect attempt fails with `no route to host` for the entire retry budget" pattern — including this exact same test, `TestPortForwarding` — has been recorded repeatedly since at least 2026-09-10 (runs #34, #36), again on 2026-09-11 (run #37), and twice earlier in today's runs #41 and #42 (once against `TestPortForwarding` itself, at uplink IP `192.168.170.5`). It's a Proxmox/SDN-VM provisioning hiccup on the test infrastructure side, unrelated to EVE/pillar code.

### TestControllerFaultsSuite: failure analysis

#### TestDeferredQueueDropsReported

##### Failure

```

Timed out after 480.001s.
When passed a matcher, ReceiveMatcher's channel *must* receive something.
```

##### Claude's conclusion

###### Root cause

Live `zedagent` logs confirm the exact drop: at 17:46:37.827 the app (`adcd97a9-…`, reported-app) transitioned to `HALTING`, and at 17:47:09.282 zedagent published its terminal `HALTED` info message and immediately tried to send it (17:47:09.289) — but the test's armed `/info` 404 fault was still active, so it was dropped as a permanent rejection (`dropping message, controller responded 404 Not Found`) at 17:47:09.298, three seconds before the test's `ClearControllerFaults()` ran at 17:47:12. Since the app's state never changes again after HALTED, no further `PublishAppInfoToZedCloud` call for this app UUID ever fires (confirmed — only the unrelated device-level 495a7ee2 object keeps retrying every ~30s afterward), so `evetest eve app-info` still shows the stale `RUNNING` state and the test's `settleApp()` watch blocks for the full 8-minute timeout.

This is a known, recurring issue: it matches, evidence-for-evidence, the top entry already in `live-ledger.md` for this same test/build/run, and per that entry's own analysis this exact "final HALTED report racing/lost against fault-clear" failure mode has been recurring since it was first attributed to this test on 2026-09-08. It does not share a root cause with the other suite failures earlier in this run — `TestDeferredQueueBacklogReported` (the previous test in the same suite) passed cleanly beforehand, so this is isolated to the race between the app's terminal-state report and the fault-clear timing in `TestDeferredQueueDropsReported` specifically.

### TestDeviceConnectivitySuite: failure analysis

#### TestPNACWithoutProxy

##### Failure

```
failed to get device "edge-dev" ECDH certificate: context deadline exceeded
```

##### Claude's conclusion

No other failures in this run and no prior occurrence of this test/signature in the ledger — this is a new failure, not a recurrence.

###### Root cause

The device onboarded successfully into Adam at 16:08:34, and tpmmgr locally generated both EdgeNodeCert entries (restricted-signing at 16:08:24, ECDH-exchange by 16:08:26 — `zedrouter: msrv: EdgeNodeCert, len 2`). zedagent then sent 4 concurrent `POST .../attest` requests at 16:08:55–56 (its normal multi-interface retry behavior) to publish those certs. Adam's log shows one of those concurrent requests hit: `Failed to attestProcess: failed to get device options: getDeviceOptions failed to unmarshal: unexpected end of JSON input` — Adam read its own per-device `options.json` while another goroutine was mid-write (truncated/empty JSON), a file-write race in the Adam test-controller stub, not in EVE. That's the last `/attest` request ever seen — no retries followed for the remaining ~90s before the test's fixed deadline — so the device's ECDH cert (type `CERT_TYPE_DEVICE_ECDH_EXCHANGE`) never landed in Adam's `certs.json` (which still only has the type-11 restricted-signing cert), and the harness's polling GET on `/admin/device/.../certs` eventually hit `context deadline exceeded`.

This is a distinct failure signature from anything in `live-ledger.md` (no `attestProcess`/`getDeviceOptions`/`TestPNAC` hits there) and no other test failed in this same run, so it doesn't share a root cause with an earlier failure — it looks like a new, first-seen issue, most likely a race condition in the Adam mock controller's file-based device-options storage rather than an EVE regression.


#### TestVLANSubinterfacesOnTopOfLAGs

##### Failure

```

Timed out after 300.001s.
Expected to satisfy: App2 receives IP from VLAN 30 subnet
AppID:"e9a68b2a-446f-4250-a0bc-551471e51689"  appVersion:"1"  AppName:"vlan30-bond-app"  bootTime:{seconds:1789322432  nanos:703249299}  state:RUNNING  network:{macAddr:"02:16:3e:00:00:02"  devName:"vif0"  defaultRouters:"<nil>"  dns:{}  localName:"nbu1x2"}  volumeRefs:"ed3597a4-de04-4142-928b-db04b6390bb0"  cluster_app_running:true
```

##### Claude's conclusion

###### Root cause

Live evidence confirms the same known pcap-race bug already logged multiple times in the ledger for this exact test. Zedrouter's log shows it attempted `Installing pcap on vlan30bond-m` for NI `fcf1daec-...` (`ni2-vlan30-bond`) at `17:59:53.747`, but it failed 3ms later with `unknown interface vlan30bond-m: route ip+net: no such network interface` — the reconciler didn't actually create `DummyInterface/vlan30bond-m` until `17:59:54.519`, ~770ms later. No further "Installing pcap" attempt is ever logged afterward (confirmed via full device log search), matching the known `cancelPCAP`-guard bug in `pkg/pillar/nistate/linux.go` where a failed `pcap.OpenLive` never clears the retry guard. I confirmed the DHCP exchange itself succeeded on the wire: the SDN's `dnsmasq` lease file for VLAN 30 shows App2's MAC (`02:16:3e:00:00:02`) got `172.22.30.19` — but zedrouter's `AppNetworkStatus`/`IPAssignments` for that MAC is still `null`, since it never captured the packet to learn the IP.

This is the same recurring issue already recorded in `live-ledger.md` for this exact test (`TestVLANSubinterfacesOnTopOfLAGs`, App3 in run #40, App2 again in run #43) and for the sibling `TestAirGapSwitchNI`/`TestMgmtTrafficRoutedViaApp` failures — known/recurring since at least **2026-09-09**, unfixed on current master. It does **not** share a root cause with the other failure already investigated in this run (`TestPNACWithoutProxy`, an unrelated Adam-controller `attestProcess`/`getDeviceOptions` file-write race) — these are two independent, coincidentally co-occurring bugs in the same run.

### TestNodeClusterSuite: failure analysis

#### TestThreeNodesCluster

##### Failure

```
Timed out waiting for app "ee993922-972e-4b63-ae91-b085305bc32b" to be scheduled in cluster "test-cluster"
```

##### Claude's conclusion

###### Root cause

Live evidence confirms this is the same chronic etcd/control-plane overload during cluster formation that's been recurring in this suite since at least 2026‑09‑09 — specifically the same failure signature ("Timed out waiting for app ... to be scheduled in cluster") already seen once before, in run #43 on 2026‑09‑12.

On `edge-dev1` (host of app `container-app`, still `CREATING_VOLUME`), `k3s.log` shows 959 "apply request took too long" etcd warnings and 4 crash/restarts within the device's 33‑minute uptime (`k3s.log.restart.*` at 15:38:14, 15:41:19, 15:44:09, plus the current `k3s server`/`k3s init` processes only 67s old at investigation time), with load average 8.24 on a 4‑vCPU node and repeated `connection refused`/proxy-dialer errors to peer 10.244.244.4:6443. This churn delayed the app's `cdi-upload-*` pod all the way through scheduling (PVC-scratch-not-found retries), image pull, and container start — it only actually started running its upload server at ~15:43:50, seconds before the test's 10‑minute wait (15:34:17–15:44:17) expired, so the volume never finished uploading and `domainmgr` never got to scheduling the VMI/pod (confirmed: PVCs Bound, `cdi-upload` pod Running but no VMI/virt-launcher pod exists anywhere in the cluster).

This is the same root-cause class as run #43 (2026‑09‑12, ledger line ~414–420), which hit the identical "app never scheduled" message via a related mechanism (a stalled/non-retried `volumemgr` upload after a k3s restart); today's manifestation is the upload simply never getting a large enough uninterrupted window due to repeated k3s crash-restarts. It also shares the same underlying etcd/Longhorn-instability-during-join issue documented in the ledger going back to at least 2026‑09‑09, though the exact "waiting for app to be scheduled" symptom is a variant first seen only in run #43, so it isn't yet a long-standing named entry beyond these last two occurrences.

### TestStorageSuite: failure analysis

#### TestVaultZvolTrimReclaimsBlocks

##### Failure

```

fstrim should reclaim at least ~256 MiB of ghost blocks; trimmed 190656512 bytes (output: "/persist/vault: 181.8 MiB (190656512 bytes) trimmed\n")
Expected
    <int64>: 190656512
to be >=
    <int>: 209715200
```

##### Claude's conclusion

Confirmed: no prior ledger entries for `TestVaultZvolTrimReclaimsBlocks` or this fstrim failure signature — this is a first-time (new) failure, not a recurring one.

###### Root cause assessment

`fstrim -v /persist/vault` reclaimed only 181.8 MiB (190,656,512 bytes) against a required ≥200 MiB (256 MiB written), a ~74 MiB shortfall. Live evidence points to concurrent I/O contention on the shared `persist` ZFS pool during the test's write→delete→trim window:

- `zpool iostat -v persist 1 3`, run right now while the run is paused, still shows active background writes to the pool (up to 177 write ops / 11.1 MB/s in the first sample) even with no test actively driving I/O — confirming this eve-k device has continuous background write churn on the same pool that backs `/persist/vault`.
- `/persist/vault/containerd` alone holds 6.2 GiB, and `/persist/vault/volumes/replicas` (Longhorn) exists on this node — exactly the two concurrent writers the test's own doc comment calls out as sources of unrelated churn on this dataset ("image pulls, replica I/O, k3s state churn").
- The test's tight timeline (all of vault-readiness wait, baseline trim, 256 MiB write, and delete+trim happened in ~107s between 15:43:33–15:45:20) leaves a narrow but real window where a concurrent writer on the ext4-on-zvol filesystem could reuse a portion of the just-freed blocks from `rm trim_test` before `fstrim` ran over them, so those blocks were no longer eligible for DISCARD — accounting for the shortfall.

This does not appear related to the earlier `TestControllerFaultsSuite`/`TestDeferredQueueDropsReported` entry in the ledger (different suite, different subsystem — controller-queue timeout vs. storage/ZFS trim), and no PR diff was supplied for this run, so no code-change correlation can be assessed. Given the test explicitly designed its assertion around fstrim's own reported count *because* concurrent cluster I/O on this dataset is expected and unavoidable, this looks like a flaky-by-design race between the 256 MiB test write/delete and background Longhorn/containerd/k3s writes to the same zvol, rather than a regression in trim/DISCARD or vault logic itself.

## [2026-09-13 -- 0.0.0-master-ebd50eb8 (run #46)](https://github.com/milan-zededa/eve/actions/runs/34744410714)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/46/)

### TestControllerFaultsSuite: failure analysis

#### TestDeferredQueueDropsReported

##### Failure

```

Timed out after 480.001s.
When passed a matcher, ReceiveMatcher's channel *must* receive something.
```

##### Claude's conclusion

This is a known, recurring issue — confirmed by an earlier ledger entry for this exact test/failure, and today's evidence matches it precisely.

**Root cause:** The test itself passed its main assertions (rejections-reported checkpoint hit at 08:13:54), but hangs in cleanup's `settleApp()` waiting for the app to be reported HALTED. Live evidence: `AppInstanceStatus`/`DomainStatus` on the device both show `State:119` (HALTED) since ~08:14:08, and zedagent's logs show it published a `ZiApp` info message with `state:HALTED` at 08:14:07.823 — but this happened while the test's 404-on-`/info` fault was still armed (`ClearControllerFaults()` didn't run until 08:14:09.124). That HALTED report was dropped by the deferred queue as a permanent rejection (404 = "controller doesn't know this object", not retried), and since the app's state never changes again after HALTED, nothing re-triggers a fresh per-app info publish — confirmed by Adam's `info` DB and `evetest eve app-info` still showing the stale `RUNNING` state minutes later, and zedagent's logs showing no further `PublishAppInfoToZedCloud` for this app UUID after 08:14:07 (only periodic device-level info, which doesn't carry per-app state). The controller therefore never learns the app reached HALTED, so the test's watch blocks for the full 8-minute `convergeTimeout`.

This is the identical failure mode recorded in the ledger for this same test, first seen 2026-09-08 (there attributed to an unrelated missing-feature/image-staleness issue) and already analyzed in detail as this exact "final HALTED report racing/lost against fault-clear" scenario in a prior run of this same suite (`live-ledger.md` line 271-287, same root cause description verbatim). It's a race: the app halts fast enough that its terminal-state report gets caught by the same fault the test just armed, exposing either a test gap (not accounting for the terminal report itself being droppable) or a genuine gap in zedagent's info-retry logic for objects that stop changing after a dropped report. It does not share a root cause with the earlier suite runs in today's session (`TestDeferredQueueBacklogReported` ran and completed fine beforehand).

### TestLPSSuite: failure analysis

#### TestNetworkLocalChanges

##### Failure

```

Timed out after 153.580s.
The function passed to Eventually failed at /evetest/tests/lps/helpers_test.go:134 with:
Unexpected error:
    <*fmt.wrapError | 0xc0015b6d00>: 
    failed to execute script over SSH for app cfce1cd2-43db-4d11-8ffc-2babba67a959: SSH session creation failed: ssh: unexpected packet in response to channel open: <nil> (stderr: )
    {
        msg: "failed to execute script over SSH for app cfce1cd2-43db-4d11-8ffc-2babba67a959: SSH session creation failed: ssh: unexpected packet in response to channel open: <nil> (stderr: )",
        err: <*fmt.wrapError | 0xc0015b6c80>{
            msg: "SSH session creation failed: ssh: unexpected packet in response to channel open: <nil>",
            err: <*errors.errorString | 0xc003f71420>{
                s: "ssh: unexpected packet in response to channel open: <nil>",
            },
        },
    }
occurred
```

##### Claude's conclusion

###### Root cause

Live device logs show the SSH failure lines up exactly with `TestNetworkLocalChanges`'s own network-revert step: at 08:36:37 the test submitted the empty LPS config, and at 08:36:38.475 `nim` began a DPC re-verify that (a) released/re-leased DHCP on eth0, briefly removing its `172.20.20.123` address from 08:36:38.514 to 08:36:39.909, and (b) had `zedrouter` delete the local-NI's DNAT port-forward rules (`2222→10.11.12.128:22`) at 08:36:38.844 and only recreate them at 08:36:40.602 — with the whole DPC cycle not reaching `DPC_SUCCESS` until 08:36:43–44. The harness's TCP connect to `172.20.20.123:2222` (logged "Connected... on attempt 1/3") landed at 08:36:38.126, just before this ~2s NAT/address flap, so this time the raw TCP session was already established when the DNAT rule and eth0 IP were pulled out from under it — producing the SSH-layer symptom "unexpected packet in response to channel open" (a corrupted/redirected mid-handshake session) rather than the TCP-level dial/handshake timeout seen previously. The app (`lps-app`, `cfce1cd2-...`) never rebooted (single boot at 08:34:47, still `RUNNING`), and the device fully recovered (DPC_SUCCESS, DNAT rules restored) well before the test's 153s budget expired, confirming this is a transient harness/device race, not a lasting defect. This is the same root-cause family already recorded in `live-ledger.md` for `TestLPSSuite`/`TestNetworkLocalChanges` — first seen 2026-09-09 (run #31), recurring again 2026-09-10 (run #36) — i.e. a known, recurring issue since 2026-09-09: the LPS config-revert step's brief NAT/DHCP teardown racing a single un-timeout-bounded SSH session, just surfacing today with a slightly different low-level SSH error signature than the prior two occurrences.

### TestNodeClusterSuite: failure analysis

#### TestClusterToSingleConversion

##### Failure

```
Device "edge-dev3" failed to confirm the latest config (version 8): timed out waiting for LastProcessedConfig >= 2026-09-13 09:57:05.290902696 +0000 UTC
```

##### Claude's conclusion

###### Root cause

This is the same known, recurring issue documented in `live-ledger.md`, reproducing for at least the fourth time (2026‑09‑10 config v8, 2026‑09‑11 config v8, 2026‑09‑12 config v5, and now 2026‑09‑13 config v8 again): `TestClusterToSingleConversion`'s `ApplyConfig` confirm step uses a fixed 2‑minute `deviceApplyConfigTimeout`, but withdrawing `edge-dev3` from the cluster forces a full reboot + pillar/zedbox re-bootstrap before it can publish an updated `LastProcessedConfig`, which routinely blows through that budget.

Live evidence on `edge-dev3` confirms the identical mechanism: config version 8 (timestamp `09:57:05.290902696Z`) was fetched, and `/proc/uptime` shows the device has only been up **44.75s** as of now (`09:59:28`), i.e. it rebooted around `09:58:43` — well past the harness's ~`09:59:05` deadline. `ps -ef` on the device currently shows only kernel threads and `/sbin/init` — `zedbox`/`zedagent`/`zedkube`/`domainmgr` haven't even started yet — so it's still mid-boot, nowhere near able to publish a fresh `last_processed_config` (the metrics endpoint still reports the stale pre-reboot value from `09:18:40`).

This is unrelated to any other issue in this run and matches exactly the mechanism (and even the same config version, 8) recorded for the 2026‑09‑10 and 2026‑09‑11 occurrences: a test-timeout-margin bug in the harness racing a fixed 2‑minute confirm window against an inherently reboot-bound per-device cluster-withdrawal path — not a new EVE/pillar regression.

## [2026-09-13 -- 0.0.0-master-03335b43 (run #45)](https://github.com/milan-zededa/eve/actions/runs/34729399208)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/45/)

### TestApplicationConnectivitySuite: failure analysis

#### TestSwitchNIPortConfigRace

##### Failure

```

Told to stop trying after 0.024s.
vlan-switch-ni: Network instance is in error state
networkID:"d79b9372-0898-4b23-909e-755c8c85e92f"  networkVersion:"1"  instType:1  displayname:"vlan-switch-ni"  activated:true  CurrentUplinkIntf:"vlan100"  ports:"vlan100"  bridgeNum:2  bridgeName:"vlan100"  ipAssignments:{macAddress:"02:16:3e:00:00:02"  ipAddress:"10.53.100.181"}  vifs:{vifName:"nbu2x1"  macAddress:"02:16:3e:00:00:02"  appID:"d21791f9-1b86-4830-ba46-8cd88858aae9"}  networkErr:{description:"failed items: BridgeFwdMask/vlan100 (failed to zero-out forwarding mask for bridge vlan100: open /sys/class/net/vlan100/bridge/group_fwd_mask: no such file or directory)"  timestamp:{seconds:1789272780  nanos:816384969}  severity:SEVERITY_ERROR}  state:ZNETINST_STATE_ERROR  mtu:1500
```

##### Claude's conclusion

This confirms the exact same bridge-recreate race documented in the ledger. I have enough evidence now.

###### Root cause

This is the same known, recurring `BridgeFwdMask`/`VLANBridge` create-path race already logged in `live-ledger.md` for `TestSwitchNIPortConfigRace` (runs #26, #31, #32, #34–#36, #42, #43) and for the sibling `vlan100` port-config-race scenario. Live `zedrouter` logs show the reconciler tearing down and rebuilding `vlan100` repeatedly during this test's deliberate port churn: at `04:12:43.423` it created `VLANBridge/bn2` after moving the port away, then at `04:13:00.143` recreated `VLANBridge/vlan100` (`expectedBridgeID: 0`) in the same pass that ran `BridgeFwdMask/vlan100`, which failed with `open /sys/class/net/vlan100/bridge/group_fwd_mask: no such file or directory` — the bridge sysfs directory transiently didn't exist because `vlan100` wasn't a real bridge yet at that instant (`VLANBridge/vlan100 with error: link vlan100 is not a bridge`, same timestamp). The reconciler self-healed 250ms later (`04:13:00.762`, "Fixed config items: BridgePort/vlan100/nbu2x1, VLANBridge/vlan100"), then rebuilt the bridge once more with `expectedBridgeID: 14` at `04:13:01.096`, and live inspection right now confirms `group_fwd_mask` is healthy (`0x0`) and `vlan100` is a proper bridge — so this was transient, not a lasting fault.

This failure has no relation to the earlier `TestSwitchNIPortConfigRace` failure already recorded for this same run/build in the ledger's top entry (that one, at `23:16–23:17` on 2026-09-12, was the unrelated pcap-retry/IP-race defect in `nistate/linux.go`) — this is a second, distinct rerun of the test roughly 5 hours later hitting a different bug. This `BridgeFwdMask`/bridge-recreate race is a known, recurring issue per `live-ledger.md`, first attributed to this test in run #26/#31/#32 (~2026-09-08/09), recurring in runs #34–#36, #42 (twice), and most recently #43 — so it has been an open, unfixed defect since at least 2026-09-08, still present on today's build (`0.0.0-master-b5242393`).

### TestBootstrapSuite: failure analysis

#### TestBootstrapWithMgmtVLANUsingOverrideJSON

##### Failure

```
Failed to setup devices [edge-dev]: rpc error: code = Unknown desc = failed to setup EVE device "edge-dev": failed to upload disk image "/root/.evetest/images/eve-f11f6bf8-edge-dev/disk.qcow2" to storage "local": Post "https://10.208.13.184:8006/api2/json/nodes/farfalle/storage/local/upload": write tcp 10.208.13.67:39858->10.208.13.184:8006: write: broken pipe
```

##### Claude's conclusion

**Root cause: recurring Proxmox API upload flakiness against the storage backend, not an EVE/pillar defect.** Live `gotest.json` for this test shows 3 upload attempts of the EVE device's `disk.qcow2` to Proxmox node `farfalle` storage `local`: attempt 1 failed with `unexpected end of JSON input` (02:10:11), attempts 2 and 3 both failed identically with `write tcp 10.208.13.67:*->10.208.13.184:8006: write: broken pipe` (02:10:16, 02:10:21) — the connection to the Proxmox API host was accepted but died mid-write of the image body each time. The harness then tore down before any device/SDN was provisioned, which live checks confirm: `evetest sdn status` → "SDN client is not initialized", `evetest eve info` → "no EVE devices are currently onboarded" — there's nothing further live to inspect.

This shares the same root cause as the earlier `TestBootstrapWithTransparentProxy` failure in this very run (00:09:52–00:10:21, logged in `live-ledger.md`), which hit the identical `broken pipe` writing to the same `farfalle:8006`/storage `local` endpoint — that time uploading the SDN image, this time the EVE device image. Per the ledger, that first occurrence was itself noted as a new, not-previously-recorded signature (no prior "broken pipe"/farfalle/8006 matches before this run), so this is not a long-standing known issue — it's the same infra-side Proxmox connectivity problem recurring for the second time within tonight's run (#44), pointing at instability on the Proxmox host/network path rather than in EVE or pillar code.

### TestStorageSuite: failure analysis

#### TestVolumes

##### Failure

```

Told to stop trying after 0.011s.
Stop waiting for: volume v-vhdx is created
Volume reports an error: Found error in content tree v-vhdx-image attached to volume v-vhdx: 
Size '0' provided in image config of 'v-vhdx.vhdx' is incorrect.
Download status (5570560 / 0). Aborting the download

uuid:"34905987-5904-4b56-8e10-5635d8c01ca5" displayName:"v-vhdx" usage:{createTime:{seconds:-62135596800} refCount:1 lastRefcountChangeTime:{seconds:1789262075 nanos:304371794}} state:DOWNLOAD_STARTED volumeErr:{description:"Found error in content tree v-vhdx-image attached to volume v-vhdx: \nSize '0' provided in image config of 'v-vhdx.vhdx' is incorrect.\nDownload status (5570560 / 0). Aborting the download\n" timestamp:{seconds:1789262076 nanos:619495217} severity:SEVERITY_NOTICE entities:{entity:ENTITY_CONTENT_TREE entity_id:"3e42b373-6132-43ce-afc0-4eca9f9047a8"} retry_condition:"Will retry in 1m0s; have retried 0 times"}
```

##### Claude's conclusion

Confirmed live evidence matches exactly. This is a known, long-recurring bug with a stale device build.

###### Root cause

Live logs on the paused device confirm the same mechanism seen throughout `live-ledger.md`: the datastore server for `v-vhdx.vhdx` "ignored Range; skipping copiedBytes manually" (01:14:35.704), then `downloader` logged `Update progress for v-vhdx.vhdx: 5570560/0` (nonzero `currentSize`, zero `totalSize`) at 01:14:36.398, immediately tripping the unconditional `currentSize > totalSize` check and aborting with `Size '0' provided in image config ... incorrect` — this propagated through `doUpdateContentTree`/`doUpdateVol` into the reported `volumeErr`, matching the failure verbatim.

This is a **known, recurring issue**, documented in the ledger for `TestStorageSuite/TestVolumes` going back to at least **2026-09-09 (run #31)**, and recurring repeatedly through runs #32/#35/#36/#37/#41+ up to today. The fix (guarding the check with `totalSize > 0`, landed as `2e809b3ee`/`ed743e546` et al.) **is present on current master** (`git merge-base --is-ancestor 2e809b3ee HEAD` succeeds), but the device under test here is running image `0.0.0-master-03335b43`, and `git merge-base --is-ancestor 2e809b3ee 03335b43c` fails — confirming the deployed build predates the fix. So, as with the identical entry from this same run at ledger line 470, this is a **stale test image**, not a regression: master already has the guard, the nightly device build just hasn't picked it up yet. No other suite has failed in this run to compare for a shared root cause.

## [2026-09-13 -- 0.0.0-master-b5242393 (run #44)](https://github.com/milan-zededa/eve/actions/runs/34713594419)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/44/)

### TestApplicationConnectivitySuite: failure analysis

#### TestSwitchNetInstanceACLsWithFlowLog

##### Failure

```
timed out after 5m0.000217071s (excluding download) waiting for app "142cb6cb-dec5-4d4c-898d-b1b013b180d3" (acl-app-2) on device "edge-dev" to reach RUNNING state (last state: RESOLVING_TAG)
```

##### Claude's conclusion

App entered `RESOLVING_TAG` at 21:29:43 and never left it through the full 5-minute timeout at 21:37:45.

Root cause: `acl-app-2` (uuid `142cb6cb...`) is hung in `RESOLVING_TAG` resolving its image tag against the registry at `192.168.170.2:5000`. Live evidence: `/proc/net/tcp` shows a connection from the device to `192.168.170.2:5000` sitting `ESTABLISHED` (state `01`) with a non-zero, unflushed `tx_queue` (`0x10F`) — i.e. the resolve request is stuck mid-flight rather than erroring out. A live `curl` from the device confirms why: plain HTTP to that registry returns a clean `200`, but HTTPS fails immediately with `wrong version number` (it's an HTTP-only registry), so `go-containerregistry`'s HTTPS-first ping/resolve — issued with no request deadline (`context.Background()`) — can hang indefinitely instead of falling back to HTTP once it doesn't get a fast rejection.

This is the exact same signature already hit twice earlier in this same run (`acl-app-1` in `TestSwitchNetInstanceACLs`/multi-port-app, `flowlog-test-app` in `TestFlowLog`) against the same registry, and per `live-ledger.md` it's a known/recurring issue going back to at least run #31 (2026-09-09, `TestLocalNetInstanceACLsWithFlowLog`). This is not a new bug — it's the same HTTPS-probe-hang-against-an-HTTP-only-registry defect in the OCI tag-resolution path.


#### TestSwitchNIPortConfigRace

##### Failure

```

Timed out after 180.000s.
Expected to satisfy: app has IPs from both application subnets
AppID:"9662b4ce-10cd-48ee-b50a-6deeb7922eed"  appVersion:"1"  AppName:"container-app"  bootTime:{seconds:1789255023  nanos:587726566}  state:RUNNING  network:{macAddr:"02:16:3e:00:00:01"  devName:"vif0"  defaultRouters:"<nil>"  dns:{}  localName:"nbu1x1"}  network:{macAddr:"02:16:3e:00:00:02"  devName:"vif1"  IPAddrs:"10.53.100.181"  defaultRouters:"<nil>"  dns:{}  ipv4_up:true  localName:"nbu2x1"}  volumeRefs:"17522573-22c0-4bf4-b1d5-736ff2644e2e"  cluster_app_running:true
```

##### Claude's conclusion

This is a well-documented, known bug: a permanent "give up after first failed pcap open" defect in `pkg/pillar/nistate/linux.go`'s ARP/DHCP snooping for switch network instances.

**Root cause:** Live `zedrouter` logs show that at 23:16:49.250, zedrouter tried to install a pcap capture on the mirror interface `bn2-m` (for `multiswitch-ni`, which carries `vif0`/`nbu1x1` of `container-app`), and it failed immediately with `unknown interface bn2-m: route ip+net: no such network interface` — because the NI reconciler didn't actually create `DummyInterface/bn2-m` until 233ms later, at 23:16:49.483. `StartCollectingForNI` sets `ni.cancelPCAP` before the `sniffDNSandDHCP` goroutine runs, and when `pcap.OpenLive` fails inside that goroutine it just logs and returns without clearing `ni.cancelPCAP`, so the retry guard never re-fires — even after `nbu1x1` attached later at 23:17:05, no further "Installing pcap on bn2-m" attempt appears anywhere in the logs (unlike the sibling `vlan100-m` mirror for `vlan-switch-ni`, which succeeded on its one attempt). I confirmed the underlying connectivity was actually fine: the SDN's own `dnsmasq` log for `multiport-network` shows a complete DHCP exchange for the app's MAC (`02:16:3e:00:00:01`), granting it `10.53.20.180` at 23:17:53 — but since zedrouter's snooper never got a working pcap handle, it never observed that lease, so `container-app`'s vif0 never got an `IPAddrs` in `AppNetworkStatus`, causing the test's `Eventually` to time out waiting for IPs on both application subnets.

This does **not** share a root cause with the earlier-investigated `TestSwitchNetInstanceACLsWithFlowLog` failure in this run (an unrelated OCI-registry HTTPS-probe-hang). It **is however the same known, recurring `nistate/linux.go` pcap-retry defect** documented extensively in `live-ledger.md`, first seen via `TestAirGapSwitchNI` on 2026-09-09 (run #31) and recurring across many different tests and runs since (`TestMgmtTrafficRoutedViaApp`, `ni3-eth2`/VLAN switch-NI tests, `ni-lan`, etc.) — a long-standing, still-unfixed issue dating back to at least 2026-09-09, now manifesting for the first time under `TestSwitchNIPortConfigRace` specifically (this test's ledger entries previously only showed the unrelated `BridgeFwdMask`/`group_fwd_mask` bug).

### TestBootstrapSuite: failure analysis

#### TestBootstrapWithTransparentProxy

##### Failure

```
Failed to setup devices [edge-dev]: rpc error: code = Unknown desc = failed to setup SDN device: failed to upload disk image "/root/.evetest/images/sdn-abbb2b4d/evetest-sdn.img.qcow2" to storage "local": Post "https://10.208.13.184:8006/api2/json/nodes/farfalle/storage/local/upload": write tcp 10.208.13.67:60284->10.208.13.184:8006: write: broken pipe
```

##### Claude's conclusion

No prior "broken pipe"/upload/8006/farfalle matches in the ledger — this is a new failure signature, not previously recorded. I have enough evidence now.

###### Root cause

**Root cause: transient network/TLS failure between the evetest broker and the Proxmox API host (`10.208.13.184:8006`, node `farfalle`) during the SDN disk-image upload, not an EVE/pillar defect.** Live `gotest.json` shows the EVE device VM (107) was successfully prepared, but all 3 upload attempts of the SDN qcow2 image (`POST .../nodes/farfalle/storage/local/upload`) failed identically with `write tcp 10.208.13.67:*->10.208.13.184:8006: write: broken pipe` at 00:09:52, 00:09:57, and 00:10:02 — the connection was accepted but died mid-write of the (multi-hundred-MB) upload body each time, pointing to the Proxmox API server or an intermediate path dropping/resetting the connection rather than a client-side bug. This occurred before any SDN VM or EVE device onboarding happened, so `evetest sdn status`/`evetest eve info` now correctly report no client/device initialized — the harness had already torn down the partially-provisioned EVE VM (107) by 00:10:16 per its own logs, and there's nothing further live to inspect for this failure. It doesn't share a root cause with any other failure in this run (no other suite in this run has been investigated yet), and this exact "broken pipe" / upload-to-`farfalle`/8006 signature does not appear anywhere in `live-ledger.md`, so this looks like a new, not-previously-recorded infra hiccup rather than a known recurring issue.

### TestDeviceConnectivitySuite: failure analysis

#### TestIntermittentConnectivity

##### Failure

```
Failed to receive SDN tunnel properties: rpc error: code = Unavailable desc = unable to connect to SDN gRPC service on any of the uplink IPs ([192.168.170.4]): failed to establish tunnel to SDN: rpc error: code = Unavailable desc = connection error: desc = "transport: Error while dialing: dial tcp 192.168.170.4:50121: connect: no route to host"
```

##### Claude's conclusion

This is exactly the same signature confirmed live: `evetest sdn status`/`logs` return "SDN client is not initialized", `evetest sdn ssh` gets a TCP connection that resets during SSH key exchange, and the EVE device is "not onboarded" — the SDN VM's network path never came up.

**Root cause:** The SDN VM (`sdn-8aecfaba`, Proxmox VM 106) was powered on and reported uplink IP `192.168.170.4` at 21:40:37, but every gRPC tunnel-connect retry from then through 21:45:39 (the full 5-minute budget) failed with `no route to host` before `openTunnelToSDN` gave up (`setup.go:823` per the stack trace). Live checks right now reproduce the identical state: `evetest sdn status`/`logs` → "SDN client is not initialized," `evetest sdn ssh` → TCP connects but resets during SSH key exchange, and `evetest eve info` → device "not onboarded" — i.e. the SDN VM never actually became reachable on its network path, matching the earlier-run signature verbatim.

This does not share a root cause with any other failure in this run (no other suite failures have been logged for this run yet). Per `live-ledger.md`, this is a well-established, recurring infra issue — the identical "uplink IP obtained, then every gRPC/SSH connect attempt fails with `no route to host` for the entire retry budget" pattern has been recorded repeatedly since at least 2026-09-10 (runs #34, #36), again on 2026-09-11 (run #37), and twice more in today's earlier runs #41 and #42 (`TestActiveBackupBond`, `TestPortForwarding`, `TestBootstrapWithLastResortDisabled`). It's a Proxmox/SDN-VM provisioning hiccup on the test infrastructure side, unrelated to EVE/pillar code.


#### TestMgmtTrafficRoutedViaApp

##### Failure

```

Timed out after 180.000s.
Expected to satisfy: app reports 2 VIFs with the reserved IPs
AppID:"ad48b802-5525-4708-90f9-d3f5e5e13a71"  appVersion:"1"  AppName:"mgmt-gw-app"  bootTime:{seconds:1789249870  nanos:720399418}  state:RUNNING  network:{macAddr:"02:16:3e:02:00:00"  devName:"vif0"  IPAddrs:"10.60.10.150"  IPAddrs:"fe80::16:3eff:fe02:0"  defaultRouters:"<nil>"  dns:{}  ipv4_up:true  localName:"nbu1x1"}  network:{macAddr:"02:16:3e:02:00:01"  devName:"vif1"  defaultRouters:"<nil>"  dns:{}  localName:"nbu2x1"}  volumeRefs:"ff916b8c-5215-441e-aa83-8105bc4a5e2c"  cluster_app_running:true
```

##### Claude's conclusion

Confirmed — only one attempt, no retry, matching the known bug pattern exactly.

###### Root cause

Same defect as the ledger's recurring `nistate/linux.go` pcap-open race, confirmed live in this device's `zedrouter` log: at `21:50:39.285` it tried `Installing pcap on eth1-m` for NI `5ebe05aa-4b77-4d3a-bca3-428c1c7225d6` (bridge `eth1`, carrying vif1/`nbu2x1` of `mgmt-gw-app`) and failed with `unknown interface eth1-m: route ip+net: no such network interface`, but the NI reconciler didn't actually create `DummyInterface/eth1-m` until `21:50:39.377` — 92ms later. `AddVif(eth1, nbu2x1, ...)` didn't happen until `21:50:59.142`, and "Installing pcap on eth1-m" appears exactly once in the whole log with no retry, so the ARP/DHCP snooper never got a working handle on that mirror and `zedrouter` never reports an IP for vif1 — exactly matching the app-info snapshot (vif0 has `10.60.10.150`, vif1 has none) and the test's 180s timeout.

This is the same root cause as the earlier `TestIntermittentConnectivity` failure's underlying class of issue is different (that one was an SDN VM reachability problem, unrelated), but this failure itself is identical to the multiple `TestMgmtTrafficRoutedViaApp` entries already logged in `live-ledger.md` for this same run (#43) and prior runs, and is the same defect first identified via `TestAirGapSwitchNI` on 2026-09-09 — a known, recurring, unfixed issue (pcap-open-before-dummy-interface-exists race in `pkg/pillar/nistate/linux.go`, with the failed-open guard never clearing to allow retry) that has now surfaced repeatedly across many different switch-NI tests through at least today's build.


#### TestLACPBond

##### Failure

```

Timed out after 300.001s.
Expected to satisfy: LACP bond has IP, no errors and reports LACP status
machineArch:"x86_64"  cpuArch:"x86_64"  platform:"x86_64"  ncpu:4  memory:7796  storage:40006  powerCycleCounter:-1  minfo:{manufacturer:"QEMU"  productName:"Standard PC (Q35 + ICH9, 2009)"  version:"pc-q35-10.1"  serialNumber:"7FVBLC16"  UUID:"Not Settable"  biosVendor:"EDK II"  biosVersion:"unknown"  biosReleaseDate:"02/02/2022"}  assignableAdapters:{type:PhyIoNetEth  name:"ethernet0"  members:"ethernet0"  usedByBaseOS:true  ioAddressList:{macAddress:"da:39:78:d0:19:f2"}  usage:PhyIoUsageMgmtAndApps}  assignableAdapters:{type:PhyIoNetEth  name:"ethernet1"  members:"ethernet1"  usedByBaseOS:true  ioAddressList:{macAddress:"1e:68:56:62:e1:30"}  usage:PhyIoUsageMgmtAndApps}  assignableAdapters:{type:PhyIoNetEth  name:"ethernet2"  members:"ethernet2"  usedByBaseOS:true  ioAddressList:{macAddress:"ce:30:0f:9c:43:d9"}  usage:PhyIoUsageMgmtAndApps}  dns:{DNSservers:"127.0.0.1:53"  DNSsearch:"test."}  storageList:{device:"nbd12"}  storageList:{device:"vda"  total:65536}  storageList:{device:"nbd0"}  storageList:{mountPath:"/persist/checkpoint"}  storageList:{mountPath:"/persist/clear/volumes"}  storageList:{mountPath:"/hostfs"  total:273}  storageList:{device:"vda4"  total:5  partitionLabel:"CONFIG"  partitionTypeGuid:"13307e62-cd9c-4920-8f9b-91b45828b798"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30054"}  storageList:{device:"nbd10"}  storageList:{device:"nbd11"}  storageList:{device:"nbd5"}  storageList:{mountPath:"/persist/vault/verifier"}  storageList:{mountPath:"/persist/status"}  storageList:{mountPath:"/persist/agentdebug"}  storageList:{mountPath:"/persist/vault/containerd"}  storageList:{mountPath:"/persist/ingested"}  storageList:{device:"/persist/vector/data/buffer/v2/dev_upload_socket/buffer-data-0.dat"}  storageList:{mountPath:"/persist/certs"}  storageList:{device:"vda3"  total:10240  partitionLabel:"IMGB"  partitionTypeGuid:"5dfbf5f4-2848-4bac-aa5e-0d9a20b745a6"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30053"}  storageList:{mountPath:"/persist/log"}  storageList:{mountPath:"/persist/pubsub-large"}  storageList:{device:"vda2"  total:10240  partitionLabel:"IMGA"  partitionTypeGuid:"5dfbf5f4-2848-4bac-aa5e-0d9a20b745a6"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30052"}  storageList:{device:"nbd1"}  storageList:{device:"nbd8"}  storageList:{mountPath:"/persist/containerd-system-root"}  storageList:{mountPath:"/config"  total:5}  storageList:{mountPath:"/persist/newlog"}  storageList:{mountPath:"/"  total:3898}  storageList:{mountPath:"/persist/clear"}  storageList:{mountPath:"/persist/vault/volumes"}  storageList:{mountPath:"/persist/patchEnvelopesCache"}  storageList:{mountPath:"/persist/vault/downloader"}  storageList:{device:"nbd9"}  storageList:{device:"vda1"  total:2048  partitionLabel:"EFI System"  partitionTypeGuid:"c12a7328-f81f-11d2-ba4b-00a0c93ec93b"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30051"}  storageList:{device:"nbd6"}  storageList:{mountPath:"/persist/vault"}  storageList:{mountPath:"/persist/tmp"}  storageList:{mountPath:"/persist"  total:40006  storageLocation:true}  storageList:{device:"nbd4"}  storageList:{device:"/persist/vector/data/buffer/v2/keep_sent_queue_socket/buffer-data-0.dat"}  storageList:{mountPath:"/persist/netdump"}  storageList:{device:"vda7"  total:2048  partitionLabel:"EFI System"  partitionTypeGuid:"c12a7328-f81f-11d2-ba4b-00a0c93ec93b"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30056"}  storageList:{device:"vda9"  total:40953  partitionLabel:"P3"  partitionTypeGuid:"5f24425a-2dfa-11e8-a270-7b663faccc2c"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30059"}  storageList:{device:"nbd15"}  storageList:{device:"nbd14"}  storageList:{mountPath:"/persist/patchEnvelopesUsageCache"}  storageList:{device:"nbd13"}  storageList:{device:"nbd7"}  storageList:{mountPath:"/persist/memory-monitor/output"}  storageList:{device:"nbd3"}  storageList:{device:"nbd2"}  storageList:{mountPath:"/persist/nettrace"}  bootTime:{seconds:1789251402}  swList:{activated:true  partitionLabel:"IMGA"  partitionDevice:"/dev/vda2"  partitionState:"active"  status:INSTALLED  shortVersion:"0.0.0-pr34713594419-b5242393-kvm-amd64"  downloadProgress:100  userStatus:UPDATED}  swList:{partitionLabel:"IMGB"  partitionDevice:"/dev/vda3"  partitionState:"unused"  status:INITIAL}  HostName:"f30734de-2529-46b0-9847-cf5ac44939f5"  lastRebootReason:"NORMAL: First boot of device - at 2026-09-12T22:17:29.849557002Z"  lastRebootTime:{seconds:1789251449  nanos:849557002}  systemAdapter:{status:{version:1  key:"zedagent"  timePriority:{seconds:1789251504  nanos:769828168}  lastSucceeded:{seconds:1789251512  nanos:253204304}  ports:{ifname:"eth0"  name:"ethernet0"  free:true  proxy:{}  macAddr:"da:39:78:d0:19:f2"  dns:{}  up:true  err:{timestamp:{seconds:1789251512  nanos:253063832}}  usage:PhyIoUsageMgmtAndApps  networkUUID:"00000000-0000-0000-0000-000000000000"  mtu:1500  config_source:{origin:NETWORK_CONFIG_ORIGIN_CONTROLLER  submitted_at:{seconds:1789251504  nanos:769828168}}  pnac_status:{}}  ports:{ifname:"eth1"  name:"ethernet1"  free:true  proxy:{}  macAddr:"da:39:78:d0:19:f2"  dns:{}  up:true  err:{timestamp:{seconds:1789251512  nanos:253067723}}  usage:PhyIoUsageMgmtAndApps  networkUUID:"00000000-0000-0000-0000-000000000000"  mtu:1500  config_source:{origin:NETWORK_CONFIG_ORIGIN_CONTROLLER  submitted_at:{seconds:1789251504  nanos:769828168}}  pnac_status:{}}  ports:{ifname:"bond1"  name:"lacp-bond"  isMgmt:true  free:true  dhcpType:4  subnet:"172.20.20.0/24"  domainname:"test."  proxy:{}  macAddr:"da:39:78:d0:19:f2"  IPAddrs:"172.20.20.123"  IPAddrs:"fe80::851d:da7a:126a:663e"  defaultRouters:"172.20.20.1"  dns:{DNSservers:"10.16.16.25"  DNSdomain:"test."}  up:true  err:{description:"interface bond1: no suitable IP address available"  timestamp:{seconds:1789251512  nanos:253128905}  severity:SEVERITY_ERROR}  networkUUID:"b319bc45-2c79-4464-b519-e0f45e1d9fe1"  mtu:1500  config_source:{origin:NETWORK_CONFIG_ORIGIN_CONTROLLER  submitted_at:{seconds:1789251504  nanos:769828168}}  pnac_status:{}  bond_status:{mode:BOND_MODE_802_3AD  mii_monitor:{enabled:true  polling_interval:100}  arp_monitor:{}  lacp:{lacp_rate:LACP_RATE_FAST  active_aggregator_id:1  partner_mac:"ca:fe:fe:80:68:22"  actor_key:9  partner_key:9}  members:{logicallabel:"ethernet0"  mii_up:true  lacp:{aggregator_id:1  actor_churn_state:BOND_LACP_CHURN_STATE_MONITORING  partner_churn_state:BOND_LACP_CHURN_STATE_MONITORING}}  members:{logicallabel:"ethernet1"  mii_up:true  lacp:{aggregator_id:1  actor_churn_state:BOND_LACP_CHURN_STATE_MONITORING  partner_churn_state:BOND_LACP_CHURN_STATE_MONITORING}}}}  ports:{ifname:"eth2"  name:"ethernet2"  isMgmt:true  free:true  dhcpType:4  subnet:"172.20.21.0/24"  domainname:"test."  proxy:{}  macAddr:"ce:30:0f:9c:43:d9"  IPAddrs:"172.20.21.146"  IPAddrs:"fe80::6d3c:ab03:4057:9b83"  defaultRouters:"172.20.21.1"  dns:{DNSservers:"10.16.17.25"  DNSdomain:"test."}  up:true  err:{timestamp:{seconds:1789251512  nanos:253049657}}  usage:PhyIoUsageMgmtAndApps  networkUUID:"b2e3cd7c-7b94-421a-8ad5-b7da9a1a5e67"  mtu:1500  config_source:{origin:NETWORK_CONFIG_ORIGIN_CONTROLLER  submitted_at:{seconds:1789251504  nanos:769828168}}  pnac_status:{}}}}  HSMStatus:NOTFOUND  dataSecAtRestInfo:{status:DATASEC_AT_REST_DISABLED  info:"TPM is either absent or not in use"  vaultList:{name:"Application Data Store"  status:DATASEC_AT_REST_DISABLED  vaultErr:{description:"TPM is either absent or not in use"  timestamp:{seconds:1789251493  nanos:497527698}  severity:SEVERITY_ERROR}  pcrStatus:PCR_DISABLED}}  sec_info:{sha_root_ca:"\xdf4j\x1c\xac(m\xff\x0e@c\xfd\xce\\'\x87\x96ζ\x8f\xbf&\xb9\x88\xaf\xe1Vkm%\xb16"}  configItemStatus:{configItems:{key:"app.allow.vnc"  value:{value:"true"}}  configItems:{key:"debug.default.loglevel"  value:{value:"debug"}}  configItems:{key:"debug.default.remote.loglevel"  value:{value:"debug"}}  configItems:{key:"debug.enable.console"  value:{value:"true"}}  configItems:{key:"debug.enable.ssh"  value:{value:"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAQC+H1RQUqHjFBJgGpslC73XsLz8Fg5WpNPble9naKyWz1Um8D2bOtQK/yguCImPeBYcH7/73z8dtC6d+dT0UF26+o7Vh6RN/U2X/5nkaZr7oM5QwwZTsD7Nd2Szww9wrRhXvpV0aFgUBDM9BIF1qBQxLNd+Jp8uttrgF3zj/cm7+SXllG54sv8WFBMfTX7J8cQ1jxLyp/Sc6PXK0zBaVzZwhmCCmI6CIzJK6ahMRgXm2vSP6doYibkB3ETSskaCXSHxDiZoaQK2ZY+GqNZkUusbau43MXVPTiJknXqUcmXhQmwyMSltQ3G54jcgn4TDObSQnW7vGdLI7zIEAHnk5D1BmKzQUBh5aRLdhBtb6T3uvVtAqGgnXWKD+d2GjMiy4G31zfIlArvC3G8LxwsDoxQL0XKaFjnEmXIptVXC68zq+laIM8YGDDOCEc6RfczP7lA4p6rv0gQUfTNqy0P3a4ulvIDb4hET1Gkh+Azkuw1do9NIhXPxDBmPdkTnKwJv6XelpdCPCw1QT5o7WdOkUgorf/e03jGDnnn1QBWpSPB9gLB/oDmT/Gzm8tCYaq7ggwYUq1fSMBvnbFaclH1KVcC2Gwn8UPLt9HHa/mGywuISZYl3gy7ztlKyAjHEZH053U7I8OaMvv/CFo9aR4Teeb5848REZYAes+yJIz3lJV1K5Q=="}}  configItems:{key:"newlog.allow.fastupload"  value:{value:"true"}}  configItems:{key:"timer.config.interval"  value:{value:"5"}}  configItems:{key:"timer.deviceinfo.interval"  value:{value:"30"}}  configItems:{key:"timer.download.retry"  value:{value:"60"}}  configItems:{key:"timer.location.app.interval"  value:{value:"20"}}  configItems:{key:"timer.location.cloud.interval"  value:{value:"300"}}  configItems:{key:"timer.metric.interval"  value:{value:"20"}}  configItems:{key:"timer.test.baseimage.update"  value:{value:"60"}}}  last_boot_reason:BOOT_REASON_FIRST  hardware_watchdog_present:true  capabilities:{HWAssistedVirtualization:true  IOVirtualization:true}  state:ZDEVICE_STATE_ONLINE  storage_info:{pool_name:"/persist"  storage_type:STORAGE_TYPE_INFO_EXT4  storage_state:STORAGE_STATUS_ONLINE  disks:{disk_name:{name:"/dev/vda9"  serial:"unknown"}  status:STORAGE_STATUS_ONLINE  smart_status:"failed"}}  attestation_info:{state:ATTESTATION_STATE_COMPLETE}  api_capability:API_CAPABILITY_DEFERRED_QUEUE_METRICS  optional_capabilities:{hw_inventory_support:true}  device_name:"edge-dev"
```

##### Claude's conclusion

**Root cause:** This is the same known NIM port-retest race documented for `TestLACPBond` in run #40 (earlier today) and originally in run #26 (2026-09-08). Live evidence from this device's `/run/nim/DeviceNetworkStatus/global.json` matches that pattern exactly: `bond1` (lacp-bond) and `ethernet2` are both same-cost mgmt ports; `eth2` failed at `22:18:31.463Z` but was immediately retested and succeeded at `22:18:32.253Z`, while `bond1`'s failure recorded at `22:18:32.253Z` (matching the "no suitable IP address available" error in the test's failure snapshot) wasn't cleared until `22:23:32.504Z` — a full `NetworkTestInterval` (300s) later, arriving just after the test's own 300.001s timeout. Per `controllerconn.VerifyAllIntf`, once `eth2` satisfies `requiredSuccessCount`, `bond1`'s stale failed `TestResults` aren't refreshed until the next full DPC test rotation, so with both intervals set to 300s it's a coin-flip whether the bond's error clears in time. This is not related to the earlier `TestIntermittentConnectivity`/SDN-VM-reachability failure in this run — different mechanism entirely. Per `live-ledger.md`, this exact race has recurred intermittently for `TestLACPBond` since at least 2026-09-08 (run #26), most recently again earlier today (run #40) — it's a known, unfixed timing issue, not a new regression.

### TestUpgradeSuite: failure analysis

#### TestEVEUpgradeKVMtoKVM

##### Failure

```
EVE upgrade to 0.0.0-pr34713594419-b5242393-kvm-amd64 failed: Download 0% done
```

##### Claude's conclusion

###### Root cause

The device (info file `01789248539882603640` in the Adam DB, timestamped 21:29:11) recorded a `swErr` on the IMGB base-os entry: `"Retry download in 1m0s: rootfs-...img failed: lookupDatastoreConfig(c47ba22f-5938-41ec-8a8e-67d16d23d248) error: Get(zedagent/DatastoreConfig) unknown key c47ba22f-5938-41ec-8a8e-67d16d23d248"` — volumemgr/downloader tried to resolve the BaseOsConfig's datastore UUID before zedagent had actually published that `DatastoreConfig` object over pubsub, a transient startup-ordering race (only ~3 min after first boot). Because `addUserSwInfo` in `pkg/pillar/cmd/zedagent/reportinfo.go` unconditionally flips `UserStatus` to `FAILED` whenever `SwErr` is non-empty but doesn't refresh `SubStatusStr` to match, the controller reported the stale `"Download 0% done"` string (left over from the `DOWNLOAD_STARTED`/0%-progress case) instead of the real error — and evetest's `waitForUpgrade` prefers `SubStatusStr` over `SwErr.Description`, so it fatal'd on the misleading text rather than the actual (retryable) cause.

Live evidence confirms this was purely transient: the device's own downloader retried and succeeded fully by 21:29:42 (100% progress, `DOWNLOADED`), the image was installed to IMGB, and by 21:29:46 baseosmgr set IMGB to `"updating"` — the device is now mid-reboot into the new image (unreachable via SSH) with the upgrade otherwise proceeding normally, entirely after evetest had already failed the test. The identical `lookupDatastoreConfig(...) unknown key` race recurred harmlessly again at 21:32:08 right after the device's reboot (self-corrected in ~7s), showing it's a generic pubsub-ordering race between volumemgr and zedagent, not specific to this run.

I found no prior `TestEVEUpgradeKVMtoKVM` entry and no other ledger entry mentioning `lookupDatastoreConfig`/"unknown key"/DatastoreConfig races, so this does not appear to be a known/recurring issue in the ledger — it looks like a first-time-observed occurrence. No PR diff was provided in this task, so I can't comment on PR relatedness.

## [2026-09-12 -- 0.0.0-master-03335b43 (run #43)](https://github.com/milan-zededa/eve/actions/runs/34695189622)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/43/)

### TestApplicationConnectivitySuite: failure analysis

#### TestPortForwarding

##### Failure

```
Failed to receive SDN tunnel properties: rpc error: code = Unavailable desc = unable to connect to SDN gRPC service on any of the uplink IPs ([192.168.170.5]): failed to establish tunnel to SDN: rpc error: code = Unavailable desc = connection error: desc = "transport: Error while dialing: dial tcp 192.168.170.5:50121: connect: no route to host"
```

##### Claude's conclusion

Root cause: this is the SDN VM's network path failing to come up, not an EVE/pillar bug. Live `gotest.json` shows a fresh SDN Proxmox VM (`sdn-29886669`, VM 110) was provisioned and powered on for `TestPortForwarding`, obtained uplink IP `192.168.170.5` at 14:30:43, but every gRPC connect retry from 14:30:57 through 14:35:43 (~50 retries, the full 5-minute budget) failed with `no route to host` before `openTunnelToSDN` gave up. Live checks right now reproduce the identical signature: `evetest sdn status/logs` → "SDN client is not initialized", `evetest sdn ssh` → TCP connects but resets during SSH key exchange, and `evetest eve info` → device "not onboarded" — i.e. the SDN VM never actually became reachable on its network path.

This is a known, recurring infra issue: the ledger records the identical "uplink IP obtained, gRPC retries fail with `no route to host` for the entire budget" pattern at least four times before — 2026-09-10 (run #34, `TestVolumeSizeAlignment`/similar), 2026-09-10 (run #36, `TestNetworkAdapterPassthrough`), 2026-09-11 (run #37, one-off SDN test), and most recently run #41 (2026-09-12, `TestActiveBackupBond`) — so it's been recurring since at least 2026-09-10. It shares no root cause with the other failure already analyzed in this same run (`TestFlowLog`, which was a container-registry HTTPS-probe hang) — this is purely an SDN/Proxmox VM provisioning hiccup, unrelated to any EVE/pillar code path.


#### TestSwitchNIPortConfigRace

##### Failure

```

Told to stop trying after 0.037s.
vlan-switch-ni: Network instance is in error state
networkID:"55cfaaf1-fbe7-42b0-895c-87a6f84912e5"  networkVersion:"1"  instType:1  displayname:"vlan-switch-ni"  activated:true  CurrentUplinkIntf:"vlan100"  ports:"vlan100"  bridgeNum:2  bridgeName:"vlan100"  ipAssignments:{macAddress:"02:16:3e:00:00:02"  ipAddress:"10.53.100.181"}  vifs:{vifName:"nbu2x1"  macAddress:"02:16:3e:00:00:02"  appID:"5b5e0b12-3d51-4949-b06d-83946a897ec9"}  networkErr:{description:"failed items: BridgeFwdMask/vlan100 (failed to zero-out forwarding mask for bridge vlan100: open /sys/class/net/vlan100/bridge/group_fwd_mask: no such file or directory)"  timestamp:{seconds:1789227450  nanos:853129753}  severity:SEVERITY_ERROR}  state:ZNETINST_STATE_ERROR  mtu:1500
```

##### Claude's conclusion

This matches the identical, well-documented `TestSwitchNIPortConfigRace` defect family exactly.

Root cause: live logs at 15:37:30.838–31.288 UTC show the NI reconciler creating `VLANBridge/vlan100` with `expectedBridgeID: 0` while `BridgeFwdMask/vlan100` runs in the same pass and fails with `open /sys/class/net/vlan100/bridge/group_fwd_mask: no such file or directory` (and even `VLANBridge/vlan100 "link vlan100 is not a bridge"`), immediately followed by the reconciler detecting a bridge-identity mismatch and redoing the bridge with `expectedBridgeID: 14` — i.e. the bridge was transiently torn down/recreated mid-reconcile during this test's deliberate port-config churn. Live inspection confirms `vlan100`'s `group_fwd_mask` is now present and healthy (`0x0`), so the absence was transient, not a lasting fault — consistent with the reconciler's known gap of race-tolerance in bridge-dependent `Create` paths (`BridgeFwdMask`, `TCMirror`) versus already-hardened `Delete` paths (commit `8f277c37b`).

This does not share a root cause with the earlier-investigated `TestPortForwarding` failure in this run (an unrelated SDN VM `no route to host` provisioning issue). It is, however, the same test/error signature already recorded once earlier in this very run (`live-ledger.md` run #42, networkID `6fbf579b...`, timestamp ~15:16:15 UTC) — same build `03335b43` — so this is a second reproduction within the same run, and per the ledger it's been a known, recurring, still-unfixed issue since at least 2026-09-08 (first exposed by this test as a regression, runs #26, #31, #32, #34–#36, and now #42 twice).

### TestBootstrapSuite: failure analysis

#### TestBootstrapWithLastResortDisabled

##### Failure

```
Failed to receive SDN tunnel properties: rpc error: code = Unavailable desc = unable to connect to SDN gRPC service on any of the uplink IPs ([192.168.170.6]): failed to establish tunnel to SDN: rpc error: code = Unavailable desc = connection error: desc = "transport: Error while dialing: dial tcp 192.168.170.6:50121: connect: no route to host"
```

##### Claude's conclusion

###### Root cause

This is the SDN Proxmox VM never coming up network-wise, not an EVE/pillar defect. Live evidence: `evetest sdn status`/`net-model` return "SDN client is not initialized," and `evetest sdn ssh` gets a TCP connection that resets during SSH key exchange — the SDN VM's network path is dead, not just slow. `gotest.json` confirms VM 106 (`sdn-240723a0`) was prepared at 16:28:24, got uplink IP `192.168.170.6`, but every gRPC connect retry from 16:28:40 through 16:33:26 (the full ~4m46s budget, 50 consecutive attempts) failed with `no route to host` before the test gave up — it never once succeeded.

This is a known, recurring infra issue per `live-ledger.md`: first seen 2026-09-10 (run #34, `TestZVolProvisionedSizeReported`, then judged a one-off), recurred the same day (run #36, `TestNetworkAdapterPassthrough`), and again earlier today in run #41 (`TestActiveBackupBond`, same build `03335b43`) — now a 4th occurrence in run #42 with a different suite (`TestBootstrapSuite`), same exact "gets uplink IP but gRPC/SSH never becomes reachable" signature each time. It doesn't share a root cause with any other failure investigated in this run and is unrelated to pillar/EVE code — it's a Proxmox/SDN-VM provisioning hiccup on the test infrastructure side.

### TestControllerFaultsSuite: failure analysis

#### TestDeferredQueueDropsReported

##### Failure

```

Timed out after 480.000s.
When passed a matcher, ReceiveMatcher's channel *must* receive something.
```

##### Claude's conclusion

That earlier test passed, so it's unrelated. I now have enough to write the root-cause summary.

###### Root cause

The test itself passed — the deferred-queue drop/rejection assertions succeeded (`rejections-reported` checkpoint hit at 16:59:35) — but it then hung in cleanup's `settleApp()`, waiting up to 480s for the app to be reported `HALTED`. Live evidence shows the app actually *did* halt correctly on-device: `AppInstanceStatus`/`DomainStatus` on the EVE device both show `State:119` (HALTED) since 16:59:48, and zedagent's logs show it built and queued exactly two `ZiApp` info messages carrying `state:HALTED` at 16:59:48.273 and 16:59:48.323. Both were immediately dropped by `handleDeferred` with `controller responded 404 Not Found` — because the test's fault (armed to reject `/info` with 404) was still in effect at that exact moment, just before `ClearControllerFaults()` ran at 16:59:49. After that, no further `handleAppInstanceStatusModify`/`PublishAppInfoToZedCloud` log line for this app UUID ever appears, and the Adam DB's `info` archive confirms no `ZiApp` record for this app was received after 16:58:30 (RUNNING) — the two HALTED reports were the last attempts and both were discarded. Since a 404 is treated as a permanent rejection (not retried via the deferred queue) and nothing else re-triggers a fresh `AppInstanceStatus`-driven info publish once the object stops changing, the controller never learned the app reached HALTED, so the test's watch on `ZInfoApp` blocked for the full 8-minute timeout. This does not share a root cause with the sibling `TestDeferredQueueBacklogReported` in this run (which passed) or with this same test's failure in the ledger's run #26 (2026-09-08), which was an unrelated image-staleness issue (missing `DeferredQueueMetric` feature entirely). This specific "final HALTED report racing/lost against fault-clear" failure mode does not appear elsewhere in the ledger, so it looks like a new/first-seen issue rather than a recurring one — likely a genuine gap in the test (not accounting for the possibility that the app's terminal-state report itself can be dropped by the very fault the test just armed) or, less likely, in zedagent's lack of a resend path for a dropped info object once its source status stops changing.

### TestDeviceConnectivitySuite: failure analysis

#### TestMgmtTrafficRoutedViaApp

##### Failure

```

Timed out after 180.000s.
Expected to satisfy: app reports 2 VIFs with the reserved IPs
AppID:"4776864c-6310-436d-8245-482c84bfb62c"  appVersion:"1"  AppName:"mgmt-gw-app"  bootTime:{seconds:1789223118  nanos:149209691}  state:RUNNING  network:{macAddr:"02:16:3e:02:00:00"  devName:"vif0"  IPAddrs:"10.60.10.150"  defaultRouters:"<nil>"  dns:{}  ipv4_up:true  localName:"nbu1x1"}  network:{macAddr:"02:16:3e:02:00:01"  devName:"vif1"  defaultRouters:"<nil>"  dns:{}  localName:"nbu2x1"}  volumeRefs:"6680f9e8-58de-4124-bb88-76191288a125"  cluster_app_running:true
```

##### Claude's conclusion

I already have the git context from the system reminder (HEAD `08a14977c`, recent commits are all evetest/coverage tooling changes unrelated to `zedrouter`/`nistate`), so no need to re-fetch it.

###### Root cause

**Same pcap-open race in `pkg/pillar/nistate/linux.go` documented in the ledger.** Live `zedrouter` logs on the paused device show, for NI `bc288a6d-4a7a-4961-b8ba-df0abad8b140` (bridge `eth1`, mirror `eth1-m`, carrying vif1/`nbu2x1` of `mgmt-gw-app`): at `14:25:04.089` pcap capture on `eth1-m` fails with `unknown interface eth1-m: route ip+net: no such network interface`, and only **62ms later**, at `14:25:04.151`, the NI reconciler actually executes `create for DummyInterface/eth1-m` — i.e. the pcap-open was attempted before the dummy interface existed. `grep` over the full log buffer confirms `"Installing pcap on eth1-m"` appears exactly once, with no subsequent retry, even though `nbu2x1`'s TC-mirror rules were installed later at `14:25:19.820` — so the sniffer never got a working handle for that bridge, and `zedrouter` never reports an IP for vif1, exactly matching the app-info snapshot (vif0/`nbu1x1` has `IPAddrs:"10.60.10.150"`, vif1/`nbu2x1` has none) and the test's timeout.

This is a **known, recurring issue**: `live-ledger.md`'s top entry (run #42, same day) already root-caused this identical test/signature to the same `nistate/linux.go` defect (pcap-open failure never clears the retry guard so a later-attaching VIF's mirror capture is permanently stuck), and further back it was first seen via `TestAirGapSwitchNI` (run #31, 2026-09-09) and hit this same test verbatim on runs #38 (2026-09-10) and #39 (2026-09-11). This run (version `03335b43`, matching ledger run #41, which hadn't yet recorded this failure) is simply another recurrence — still unfixed on current master, and unrelated to the recent evetest/coverage-tooling commits at HEAD, none of which touch `zedrouter`/`nistate`.


#### TestVLANSubinterfacesOnTopOfLAGs

##### Failure

```

Timed out after 300.001s.
Expected to satisfy: App2 receives IP from VLAN 30 subnet
AppID:"023b0a2e-c419-4e94-b175-55c1a2ae34fe"  appVersion:"1"  AppName:"vlan30-bond-app"  bootTime:{seconds:1789225607  nanos:166512008}  state:RUNNING  network:{macAddr:"02:16:3e:00:00:02"  devName:"vif0"  defaultRouters:"<nil>"  dns:{}  localName:"nbu1x1"}  volumeRefs:"dac81afe-f0e6-47c3-95da-a1ef6c3e3486"  cluster_app_running:true
```

##### Claude's conclusion

###### Root cause

This is the same recurring pcap-open race in `pkg/pillar/nistate/linux.go` already documented in the ledger and in the earlier `TestMgmtTrafficRoutedViaApp` failure investigated in this run. Live `zedrouter` logs show that at `15:06:18.902` it attempted to install pcap capture on the mirror interface `vlan30bond-m` for NI `f58aa66f-b70a-4794-9916-26b2a9dc2834` (bridge `vlan30bond`), but failed at `15:06:18.905` with `unknown interface vlan30bond-m: route ip+net: no such network interface` — the reconciler didn't actually create `DummyInterface/vlan30bond-m` until `15:06:20.041`, 1.1s later. `AddVif(vlan30bond, nbu1x1, 02:16:3e:00:00:02, 023b0a2e-...)` for `vlan30-bond-app` happened later still, at `15:06:40.990`, but "Installing pcap on vlan30bond-m" appears exactly once in the whole run (the failed attempt) with no retry ever firing — matching the known `cancelPCAP` guard bug where a failed `pcap.OpenLive` never gets cleared, so ARP/DHCP snooping for that NI's lifetime is permanently broken.

This shares the exact same root cause as the `TestMgmtTrafficRoutedViaApp` failure already investigated earlier in this run, and as the earlier failure of this *same* test (`TestVLANSubinterfacesOnTopOfLAGs`) already recorded for run #42 in `live-ledger.md` — there it was App3 (`vlan30-eth2-app`) not getting an IP via mirror `bn2-m`; here it's App2 (`vlan30-bond-app`) not getting an IP via mirror `vlan30bond-m`, same defect, different VIF/mirror hit by the same startup race. This is a known, recurring issue tracked in the ledger since at least 2026-09-09 (first seen via `TestAirGapSwitchNI`), unfixed on current master (build `03335b43`).

### TestNodeClusterSuite: failure analysis

#### TestThreeNodesCluster

##### Failure

```
Timed out waiting for app "fc91ed20-8de9-457d-9979-45bb9635864d" to be scheduled in cluster "test-cluster"
```

##### Claude's conclusion

###### Root cause

The app (`container-app`, UUID `fc91ed20…`) is permanently stuck in `CREATING_VOLUME` and was never even scheduled as a pod/VMI (`zedkube` on edge-dev1 repeatedly reports `AppKubeStatus:NotInCluster` for it, and `kubectl get vmi` shows nothing). Live evidence shows `volumemgr`'s one-shot `virtctl image-upload` (`RolloutDiskToPVC`) got interrupted at ~92% after 96s (`13:49:24`) exactly when edge-dev1's embedded k3s restarted — `ps` on the live device shows `k3s init`/`k3s server` processes only ~9 min old, started at `13:49`, and `zedkube` logged apiserver `connection refused`/`apiserver not ready` from `13:49:54`–`13:50:04`. The immediate retry then failed instantly (`"Failed after 0.035623 seconds ... PVC not found"`, `13:49:54.296`) because the apiserver was still down, and `volumemgr` never retried the upload again (no further `virtctl args` invocation in the logs since). Separately, the CDI `cdi-upload-*` pod for the same PVC did complete successfully at `13:51:13` via the cluster's own CSI/CDI path, but `volumemgr`'s internal state machine never noticed and never advanced the app past `CREATING_VOLUME`, so nothing downstream (domainmgr scheduling the VMI) ever fires — matching the test's "timed out waiting for app to be scheduled" failure.

This is a new, previously-unrecorded failure signature (no `RolloutDiskToPVC`/`virtctl`/scheduling-timeout entries anywhere in `live-ledger.md`), though it's the same underlying class of problem the ledger already documents repeatedly since at least 2026-09-09/09-10: node-join churn on this cluster triggers transient k3s/etcd restarts and Longhorn storage flapping, previously manifesting as readiness timeouts or SSH-unreachability — here it instead hit a non-retrying corner of `volumemgr`'s image-upload path, permanently stalling volume creation. No other suite failure in this run to correlate with.


#### TestTieBreakerCluster

##### Failure

```
Timed out waiting for cluster nodes to become ready in cluster "test-cluster" (device "edge-dev1")
```

##### Claude's conclusion

###### Root cause

Live evidence on the paused devices shows this is the **same failure mechanism** as `TestThreeNodesCluster`, investigated earlier in this same run (recorded in `live-investigation.md`): chronic etcd/control-plane overload during node join causes k3s to crash and restart on the joining nodes, blowing through the harness's readiness-wait window. `TestTieBreakerCluster` began waiting at 14:09:48 for all three nodes, but `ps` on the live devices shows `k3s init`/`k3s server` only ~15 minutes old on edge-dev2 (started 14:26) and edge-dev3 (started 14:30) — i.e. both non-first nodes crash-restarted well into the wait. edge-dev2's `k3s.log` contains 2502 occurrences of `"apply request took too long"` and shows etcd raft connectivity to edge-dev3's peer (`2e154af3a8bff02b`) repeatedly flapping (`lost TCP streaming connection`, `dial tcp 10.244.244.4:2380: connect: connection refused`) right around the 14:29:37–14:30:35 restart window — the same etcd-apply-slowness/raft-instability signature (2351/2170 occurrences) seen on the equivalent nodes in `TestThreeNodesCluster`. `evetest cluster info` also shows storage health continuously flapping `HEALTHY`↔`DEGRADED`↔`FAILED` across all three nodes, matching the Longhorn-instability theme both from this run's earlier failure and the ledger.

This is the same root cause as the `TestThreeNodesCluster` failure earlier in this run, and both trace to the ledger's recurring "etcd apply-request/Longhorn instability during cluster join" issue class (documented there since at least 2026-09-09, though the exact "cluster nodes to become ready" timeout message itself first appeared only in this run's `TestThreeNodesCluster` entry, so it's not yet a long-standing named ledger entry beyond today). Notably this time all three nodes (not just two) failed to become ready in the 29-minute window, and by the time of this live investigation `kubectl get nodes` shows all three eventually reached `Ready` — again a slow-convergence near-miss rather than a permanently broken cluster, consistent with the underlying issue being control-plane/storage churn timing rather than a functional defect.


#### TestClusterToSingleConversion

##### Failure

```
Timed out waiting for cluster nodes to become ready in cluster "test-cluster" (device "edge-dev1")
```

##### Claude's conclusion

No prior ledger entry mentions this exact "Edge Node Certificate get fail during kube-init join decision" signature — this appears to be a new, previously-unrecorded failure mode. I have enough evidence for a solid root-cause conclusion.

###### Root cause

This is a **different bug** from the earlier `TestThreeNodesCluster`/`TestTieBreakerCluster` failures in this same run (which were caused by etcd/Longhorn slowness during join) — this time `edge-dev2` never even attempted to join the etcd-backed cluster. Live evidence on `edge-dev2`: `zedkube` received the correct `EdgeNodeClusterConfig` (join_server_ip `10.244.244.2`, cluster `test-cluster`) at `14:49:03.769`, but its `publishKubeConfigStatus` calls repeatedly failed with `"Edge Node Certificate get fail"` (`lookupEdgeNodeCert(...) not found`) until `14:50:03.829`, when the cert finally became available and decryption succeeded. Meanwhile `kube-init` (`/persist/kubelog/k3s-install.log`) didn't wait for that: it logged `"single-node config for edge-dev2: node-name only"` at `14:49:36` and started `k3s server` standalone (no `--server`/`--cluster-init`/etcd flags, `--etcd-servers=unix://kine.sock`) at `14:49:56` — roughly 27 seconds *before* the correctly-decrypted clustered `KubeConfig` was ready. `kube-init` never revisited that single-node decision once k3s was running, so `edge-dev2` bootstrapped its own isolated single-node k3s/kine cluster and never joined the `test-cluster` etcd quorum shared by `edge-dev1`/`edge-dev3` (confirmed by `kubectl get nodes` on `edge-dev1` showing only `edge-dev1`/`edge-dev3`, and `evetest cluster info` showing `edge-dev2` permanently stuck at `NODE_ADMISSION_JOINING` reporting only itself). This is exactly what the harness's "waiting for cluster nodes to become ready" 30-minute timeout caught (the wait started right when this test's own initial 3-node cluster formation began, at `14:49:25`).

This is a new, previously-unrecorded failure signature — no entry in `live-ledger.md` mentions `"Edge Node Certificate get fail"` or a node bootstrapping standalone via kine instead of joining. It's not the same root cause as the earlier `TestThreeNodesCluster`/`TestTieBreakerCluster` failures in this run (which converged, just slowly, due to etcd apply-slowness); nor is it the same as the two prior `TestClusterToSingleConversion` ledger entries (2026-09-08 node-removal race, 2026-09-10/09-11 config-confirmation-timeout-after-reboot) — this failure happens during initial 3-node formation, before any conversion logic runs, and is a genuine kube-init/zedkube race rather than a test-timeout-budget issue.

## [2026-09-12 -- 0.0.0-master-08a14977 (run #42)](https://github.com/milan-zededa/eve/actions/runs/34680667078)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/42/)

### TestApplicationConnectivitySuite: failure analysis

#### TestFlowLog

##### Failure

```
timed out after 5m0.000260039s (excluding download) waiting for app "f40b668a-6265-452c-a34a-bd02e150fc7f" (flowlog-test-app) on device "edge-dev" to reach RUNNING state (last state: RESOLVING_TAG)
```

##### Claude's conclusion

This confirms it: `flowlog-test-app` pulls `lfedge/evetest-ubuntu-ctr:1.0` from the same `192.168.170.2:5000` datastore that was previously root-caused as HTTP-only, hitting the identical hang.

Root cause: `flowlog-test-app` is stuck in `RESOLVING_TAG` (confirmed live via `evetest eve app-info`), with its tag-resolve TCP connection to the registry `192.168.170.2:5000` (image `lfedge/evetest-ubuntu-ctr:1.0`, per `evetest eve config`) sitting `ESTABLISHED` with a stalled, non-zero send queue — never completing or erroring. Live `curl` from the device confirms the registry is HTTP-only (`http://…/v2/` → clean `200`) while HTTPS immediately fails at the TLS layer (`wrong version number`); since the datastore isn't registered as `insecure`/localhost, `go-containerregistry`'s ping/resolve path only tries HTTPS and the pillar OCI resolve call uses `context.Background()` with no request deadline, so once a connection reaches `ESTABLISHED` without a fast TLS rejection it can hang indefinitely rather than falling back to HTTP.

This is the same root-cause family already documented twice in `live-ledger.md`: `TestLocalNetInstanceACLsWithFlowLog`/`acl-app-1` (run #31, 2026-09-09, never resolved) and `multi-port-app` (2026-09-09 entry, ~4m41s stuck before eventually resolving) — both against the identical `192.168.170.2:5000` registry with the identical HTTPS-probe-hang mechanism. It's unrelated to the earlier `TestFlowLog` conntrack-timing failures also recorded in the ledger (those were a different, already-fixed-understood flow-log-collection-timing issue, not a resolve hang). So this is a known, recurring issue dating back to at least 2026-09-09, now recurring on `TestFlowLog`/`flowlog-test-app` for what appears to be the first time under this specific test name.

### TestAppsSuite: failure analysis

_Paused for live investigation but nothing was recorded (see the job log)._

### TestDeviceConnectivitySuite: failure analysis

#### TestMgmtTrafficRoutedViaApp

##### Failure

```

Timed out after 180.001s.
Expected to satisfy: app reports 2 VIFs with the reserved IPs
AppID:"47bf124a-f85a-4117-9b6e-865457eca748"  appVersion:"1"  AppName:"mgmt-gw-app"  bootTime:{seconds:1789204455  nanos:230497512}  state:RUNNING  network:{macAddr:"02:16:3e:02:00:00"  devName:"vif0"  IPAddrs:"10.60.10.150"  IPAddrs:"fe80::16:3eff:fe02:0"  defaultRouters:"<nil>"  dns:{}  ipv4_up:true  localName:"nbu1x1"}  network:{macAddr:"02:16:3e:02:00:01"  devName:"vif1"  defaultRouters:"<nil>"  dns:{}  localName:"nbu2x1"}  volumeRefs:"e7120acb-63aa-4e9a-aa10-2a538b9f600e"  cluster_app_running:true
```

##### Claude's conclusion

I already have git log context from the system reminder (HEAD is `08a14977c`, unrelated evetest/coverage-tooling commits — nothing touching `nistate/linux.go` or `zedrouter`), and enough live evidence to confirm the diagnosis.

###### Root cause

`zedrouter`'s NI-state pcap capture on the mirror interface `eth1-m` (for NI `268734cb-9eca-4870-82f8-1f7c77384e85`, bridge `eth1`, which carries vif1/`nbu2x1` of `mgmt-gw-app`) failed at 09:14:01.967 with `unknown interface eth1-m: route ip+net: no such network interface`, because the reconciler didn't actually create `DummyInterface/eth1-m` until 09:14:02.342 — 375ms later. `pkg/pillar/nistate/linux.go` sets `ni.cancelPCAP` before the `sniffDNSandDHCP` goroutine runs, and when `pcap.OpenLive` fails inside that goroutine it just logs and returns without clearing `cancelPCAP`, so the retry guard never re-fires: even after vif1 (`nbu2x1`) attached at 09:14:12.469, no subsequent pcap-open attempt for `eth1-m` appears anywhere in the logs, which is exactly why `zedrouter` never reports an IP for vif1 and the test's `Eventually` (expecting 2 VIFs with IPs) timed out.

This is a **known, recurring issue since at least 2026-09-09** (first seen via `TestAirGapSwitchNI`, run #31) and has already hit this exact test twice before, verbatim: run #38 (2026-09-10) and run #39 (2026-09-11), both in `live-ledger.md`. Today's failure is the same signature again (same error string, same startup race, same `nistate/linux.go` "give up after first failed pcap open" defect), still unfixed on current master — not a new regression, and unrelated to the recent evetest/coverage-tooling commits at HEAD (`08a14977c` etc.), which don't touch `zedrouter`/`nistate`.

### TestLPSSuite: failure analysis

_Paused for live investigation but nothing was recorded (see the job log)._

### TestNodeClusterSuite: failure analysis

#### TestThreeNodesCluster

##### Failure

```
Timed out waiting for cluster nodes to become ready in cluster "test-cluster" (device "edge-dev2")
```

##### Claude's conclusion

I have enough evidence now.

**Root cause:** This is chronic control-plane/etcd slowness on the joining nodes, not a hard failure — the cluster eventually converged, just far too slowly. Live logs on `edge-dev2` show its embedded etcd logging "apply request took too long" continuously (2351 occurrences) from ~10:52 onward, culminating in a leader-election loss and `k3s exited: exit status 1` at 11:22:28, followed by an etcd `force-new-cluster` recovery restart at 11:23:06; `edge-dev3` shows the identical pattern (2170 occurrences), while `edge-dev1` (the first node to join) was comparatively healthy (427 occurrences, zero crashes). Storage health for the joining nodes flapped FAILED↔DEGRADED↔HEALTHY the entire time (matching the Longhorn-instability theme from the ledger's only prior `TestThreeNodesCluster` entry). The harness logged "Waiting for cluster nodes ... to become ready" every minute from 10:52:37 until it gave up at 11:21:37 (~29 minutes); live `evetest cluster info` right now shows all three nodes actually finished joining and reached Ready only around 11:22–11:24 — i.e. convergence completed just ~1-2 minutes after the test's timeout, a near-miss rather than a permanently broken cluster.

This is **not the same failure signature** as the one prior `TestThreeNodesCluster` ledger entry (run #32, 2026-09-09), which was about post-join SSH-app-reachability breaking due to Longhorn-driven k3s/VMI churn — today's is a pure join/readiness-timeout on two of the three nodes. I found no ledger entry matching this exact "Timed out waiting for cluster nodes to become ready" message, so this looks like a new (or at least previously unrecorded) manifestation of the same underlying etcd/Longhorn-instability class of problem, rather than a confirmed long-running known issue. No other suite failed in this run, so there's nothing else to correlate it with.


#### TestClusterToSingleConversion

##### Failure

```
Device "edge-dev3" failed to confirm the latest config (version 5): timed out waiting for LastProcessedConfig >= 2026-09-12 12:06:59.326359946 +0000 UTC
```

##### Claude's conclusion

###### Root cause

This is the same known, recurring issue documented in `live-ledger.md` for run #35 (2026‑09‑10) and again for run #40 (2026‑09‑11, config version 8): the test's `ApplyConfig` confirm step uses a fixed 2‑minute `deviceApplyConfigTimeout`, but converting `edge-dev3` out of the cluster forces a reboot + full pillar/zedbox re-bootstrap before it can publish an updated `LastProcessedConfig`, which routinely exceeds that budget.

Live evidence on `edge-dev3` confirms the identical mechanism: `/proc/uptime` shows the device rebooted only ~103s ago (boot ≈ 12:07:43, ~44s after fetching config version 5 at 12:06:59, matching the harness's timeout deadline of ~12:08:59), and at 12:09:19–12:09:21 — already past that deadline — logs show it's still deep in post-reboot bootstrap: `zedkube` cycling through leader-election/attestation states (`StateNonceWait`/`StateInternalQuoteWait`/`StateInternalEscrowWait`), and `domainmgr` only just re-subscribing to `EdgeNodeClusterConfig` at 12:09:21. This is not a new EVE/pillar regression — it's the harness racing a fixed 2‑minute confirm-timeout against an inherently reboot-bound cluster-withdrawal path, a known issue since at least 2026‑09‑10 (config version 8 that time, version 5 today, same signature both times). It's unrelated to the earlier `TestThreeNodesCluster` failure in this run (etcd/Longhorn join-readiness slowness), which is a distinct issue.

## [2026-09-12 -- 0.0.0-master-03335b43 (run #41)](https://github.com/milan-zededa/eve/actions/runs/34666428017)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/41/)

### TestApplicationConnectivitySuite: failure analysis

#### TestSwitchNIPortConfigRace

##### Failure

```

Told to stop trying after 0.028s.
vlan-switch-ni: Network instance is in error state
networkID:"6fbf579b-9845-431c-b202-5a96887d80e1"  networkVersion:"1"  instType:1  displayname:"vlan-switch-ni"  activated:true  CurrentUplinkIntf:"vlan100"  ports:"vlan100"  bridgeNum:2  bridgeName:"vlan100"  ipAssignments:{macAddress:"02:16:3e:00:00:02"  ipAddress:"10.53.100.181"}  vifs:{vifName:"nbu2x1"  macAddress:"02:16:3e:00:00:02"  appID:"738fcc7d-d855-4075-ae69-f1d3afc4c5b6"}  networkErr:{description:"failed items: BridgeFwdMask/vlan100 (failed to zero-out forwarding mask for bridge vlan100: open /sys/class/net/vlan100/bridge/group_fwd_mask: no such file or directory)"  timestamp:{seconds:1789186575  nanos:166894619}  severity:SEVERITY_ERROR}  state:ZNETINST_STATE_ERROR  mtu:1500
```

##### Claude's conclusion

Confirmed — bridge and `group_fwd_mask` (value `0x0`) are now present and healthy, matching every prior occurrence in the ledger where the failure was transient.

**Root cause:** Live logs at 04:16:15.157–.215 UTC show the NI reconciler deleting the old `bn2` bridge and creating `VLANBridge/vlan100` (`expectedBridgeID:0`) as part of this test's deliberate port-config churn; the `BridgeFwdMask/vlan100` item running against it at .215 failed with `open /sys/class/net/vlan100/bridge/group_fwd_mask: no such file or directory`, immediately followed at .291–.345 by the reconciler detecting a bridge-identity mismatch and redoing the bridge with `expectedBridgeID:14` — i.e. the bridge was transiently absent from sysfs mid-recreation. Live inspection now confirms `vlan100`'s `group_fwd_mask` is present and healthy (`0x0`), so the absence was transient, not a lasting device fault.

This is not a new bug — it's the same `TestSwitchNIPortConfigRace` defect recorded repeatedly in `live-ledger.md` (first exposed as a regression by this new test on 2026-09-03/2026-09-08, run #26, with a `TCMirror.Create`/"Parent Qdisc doesn't exists" variant; this identical `BridgeFwdMask`/`group_fwd_mask` symptom then recurred in runs #31, #32, #34–#36, and again in today's earlier run entry). Root cause is a lack of race-tolerance in the NI reconciler's `Create` paths for bridge-dependent items (`BridgeFwdMask`, `TCMirror`) when the bridge is transiently torn down/recreated during port reconfiguration — commit `8f277c37b` hardened `Delete` paths for this but not `Create`. This is a known, recurring, still-unfixed issue since at least 2026-09-08, reproducing again verbatim on today's build (`0.0.0-master-03335b43`).

### TestDeviceConnectivitySuite: failure analysis

#### TestActiveBackupBond

##### Failure

```
Failed to receive SDN tunnel properties: rpc error: code = Unavailable desc = unable to connect to SDN gRPC service on any of the uplink IPs ([192.168.170.6]): failed to establish tunnel to SDN: rpc error: code = Unavailable desc = connection error: desc = "transport: Error while dialing: dial tcp 192.168.170.6:50121: connect: no route to host"
```

##### Claude's conclusion

###### Root cause

This is the identical, recurring "SDN VM unreachable" infrastructure failure already documented multiple times in `live-ledger.md`, not a new bug or an EVE/pillar defect. Live `gotest.json` for this run shows SDN Proxmox VM 107 (`sdn-979b11a1`) was provisioned and powered on normally, obtaining uplink IP `192.168.170.6` at 04:17:11, but every gRPC connect retry from 04:17:25 through 04:22:11 (the full ~5-minute budget) failed with `no route to host` — never once succeeding — before `openTunnelToSDN` gave up. Live checks right now reproduce exactly the same signature seen in past occurrences: `evetest sdn status`/`net-model`/`logs` → "SDN client is not initialized", `evetest sdn ssh` → TCP connects but resets during SSH key exchange, and `evetest eve info`/`collect-info` → device "not onboarded"/"network model not applied" — i.e. the SDN VM's network path never actually came up, so nothing downstream (including the EVE device itself) could be configured.

This same "SDN VM gets uplink IP but gRPC/SSH stays unreachable for the entire retry window" pattern has appeared in the ledger three times before: first on 2026-09-10 (run #34, `TestZVolProvisionedSizeReported`), then again the same day (run #36, `TestNetworkAdapterPassthrough`), and again on 2026-09-11 (run #37). Each time it was judged a Proxmox/SDN-provisioning infra hiccup rather than a code-level bug, and it has now recurred a fourth time here on `TestActiveBackupBond` — so it's a known, recurring (though still not root-caused) infra issue dating back to 2026-09-10, unrelated to any pillar/EVE code change.

### TestStorageSuite: failure analysis

#### TestVolumes

##### Failure

```

Told to stop trying after 0.011s.
Stop waiting for: volume v-vhdx is created
Volume reports an error: Found error in content tree v-vhdx-image attached to volume v-vhdx: 
Size '0' provided in image config of 'v-vhdx.vhdx' is incorrect.
Download status (3276800 / 0). Aborting the download

uuid:"e57dbe1d-8021-4167-aab2-eb15c2c5671c"  displayName:"v-vhdx"  usage:{createTime:{seconds:-62135596800}  refCount:1  lastRefcountChangeTime:{seconds:1789188660  nanos:641262900}}  state:DOWNLOAD_STARTED  volumeErr:{description:"Found error in content tree v-vhdx-image attached to volume v-vhdx: \nSize '0' provided in image config of 'v-vhdx.vhdx' is incorrect.\nDownload status (3276800 / 0). Aborting the download\n"  timestamp:{seconds:1789188662  nanos:277398446}  severity:SEVERITY_NOTICE  entities:{entity:ENTITY_CONTENT_TREE  entity_id:"727b7fa3-06f5-4183-adfa-b5f1a8635fe6"}  retry_condition:"Will retry in 1m0s; have retried 0 times"}
```

##### Claude's conclusion

###### Root cause

Live `downloader` logs on the paused device show the exact same mechanism as prior occurrences: the datastore HTTP server ignored the Range request (`server ignored Range; skipping copiedBytes manually` at 04:51:01.086), then a progress update logged `Update progress for v-vhdx.vhdx: 3276800/0` (nonzero `currentSize`, zero `totalSize`) at 04:51:01.942, which trips the `currentSize > totalSize` check in `pkg/pillar/cmd/downloader/download.go`, aborting with `Size '0' provided in image config ... is incorrect` — identical to the failure text.

This is the same `TestVolumes`/`v-vhdx` bug recorded in `live-ledger.md` for runs #32, #35, #36 and #37 (2026-09-09 through 2026-09-11) — a known, recurring issue since at least 2026-09-09. However, the situation has changed: a fix (`2e809b3ee downloader: don't abort download when declared size is unknown (0)`) guarding the check with `totalSize > 0` was merged to master on 2026-09-11 and is present at current HEAD (`018ec1b29`). The device image actually under test here is `0.0.0-master-03335b43`, which I confirmed via `git merge-base` predates that fix commit — so this run simply used a stale EVE build from before the fix landed, not a regression of the fix itself. This is unrelated to the `018ec1b29` "personal testing overrides" commit; no other suite has been investigated yet in this run to compare for a shared root cause.

## [2026-09-12 -- 0.0.0-master-018ec1b2 (run #40)](https://github.com/milan-zededa/eve/actions/runs/34641374067)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/40/)

### TestAppsSuite: failure analysis

_Paused for live investigation but nothing was recorded (see the job log)._

### TestDeviceConnectivitySuite: failure analysis

#### TestLACPBond

##### Failure

```

Timed out after 300.000s.
Expected to satisfy: LACP bond has IP, no errors and reports LACP status
machineArch:"x86_64"  cpuArch:"x86_64"  platform:"x86_64"  ncpu:4  memory:7796  storage:40006  powerCycleCounter:-1  minfo:{manufacturer:"QEMU"  productName:"Standard PC (Q35 + ICH9, 2009)"  version:"pc-q35-10.1"  serialNumber:"3KCZCYIM"  UUID:"Not Settable"  biosVendor:"EDK II"  biosVersion:"unknown"  biosReleaseDate:"02/02/2022"}  assignableAdapters:{type:PhyIoNetEth  name:"ethernet0"  members:"ethernet0"  usedByBaseOS:true  ioAddressList:{macAddress:"da:39:78:d0:19:f2"}  usage:PhyIoUsageMgmtAndApps}  assignableAdapters:{type:PhyIoNetEth  name:"ethernet1"  members:"ethernet1"  usedByBaseOS:true  ioAddressList:{macAddress:"1e:68:56:62:e1:30"}  usage:PhyIoUsageMgmtAndApps}  assignableAdapters:{type:PhyIoNetEth  name:"ethernet2"  members:"ethernet2"  usedByBaseOS:true  ioAddressList:{macAddress:"ce:30:0f:9c:43:d9"}  usage:PhyIoUsageMgmtAndApps}  dns:{DNSservers:"127.0.0.1:53"  DNSsearch:"test."}  storageList:{mountPath:"/persist/patchEnvelopesCache"}  storageList:{mountPath:"/persist/tmp"}  storageList:{device:"vda9"  total:40953  partitionLabel:"P3"  partitionTypeGuid:"5f24425a-2dfa-11e8-a270-7b663faccc2c"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30059"}  storageList:{mountPath:"/persist/status"}  storageList:{device:"nbd0"}  storageList:{device:"nbd12"}  storageList:{mountPath:"/persist/pubsub-large"}  storageList:{device:"nbd7"}  storageList:{mountPath:"/config"  total:5}  storageList:{mountPath:"/hostfs"  total:273}  storageList:{device:"/persist/vector/data/buffer/v2/dev_upload_socket/buffer-data-0.dat"}  storageList:{mountPath:"/persist/memory-monitor/output"}  storageList:{mountPath:"/"  total:3898}  storageList:{mountPath:"/persist/clear"}  storageList:{device:"vda2"  total:10240  partitionLabel:"IMGA"  partitionTypeGuid:"5dfbf5f4-2848-4bac-aa5e-0d9a20b745a6"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30052"}  storageList:{mountPath:"/persist/vault/volumes"}  storageList:{mountPath:"/persist/agentdebug"}  storageList:{mountPath:"/persist/clear/volumes"}  storageList:{mountPath:"/persist/vault/downloader"}  storageList:{device:"nbd13"}  storageList:{mountPath:"/persist/netdump"}  storageList:{device:"nbd2"}  storageList:{device:"nbd8"}  storageList:{device:"nbd11"}  storageList:{device:"nbd10"}  storageList:{device:"vda"  total:65536}  storageList:{mountPath:"/persist/vault/verifier"}  storageList:{device:"vda4"  total:5  partitionLabel:"CONFIG"  partitionTypeGuid:"13307e62-cd9c-4920-8f9b-91b45828b798"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30054"}  storageList:{device:"nbd5"}  storageList:{mountPath:"/persist/certs"}  storageList:{mountPath:"/persist"  total:40006  storageLocation:true}  storageList:{mountPath:"/persist/nettrace"}  storageList:{mountPath:"/persist/ingested"}  storageList:{mountPath:"/persist/vault/containerd"}  storageList:{device:"nbd3"}  storageList:{device:"vda7"  total:2048  partitionLabel:"EFI System"  partitionTypeGuid:"c12a7328-f81f-11d2-ba4b-00a0c93ec93b"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30056"}  storageList:{device:"nbd15"}  storageList:{mountPath:"/persist/checkpoint"}  storageList:{device:"/persist/vector/data/buffer/v2/keep_sent_queue_socket/buffer-data-0.dat"}  storageList:{device:"nbd14"}  storageList:{device:"nbd4"}  storageList:{device:"nbd9"}  storageList:{device:"nbd1"}  storageList:{device:"vda1"  total:2048  partitionLabel:"EFI System"  partitionTypeGuid:"c12a7328-f81f-11d2-ba4b-00a0c93ec93b"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30051"}  storageList:{device:"vda3"  total:10240  partitionLabel:"IMGB"  partitionTypeGuid:"5dfbf5f4-2848-4bac-aa5e-0d9a20b745a6"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30053"}  storageList:{device:"nbd6"}  storageList:{mountPath:"/persist/newlog"}  storageList:{mountPath:"/persist/log"}  storageList:{mountPath:"/persist/patchEnvelopesUsageCache"}  storageList:{mountPath:"/persist/vault"}  storageList:{mountPath:"/persist/containerd-system-root"}  bootTime:{seconds:1789171040}  swList:{activated:true  partitionLabel:"IMGA"  partitionDevice:"/dev/vda2"  partitionState:"active"  status:INSTALLED  shortVersion:"0.0.0-pr34641374067-018ec1b2-kvm-amd64"  downloadProgress:100  userStatus:UPDATED}  swList:{partitionLabel:"IMGB"  partitionDevice:"/dev/vda3"  partitionState:"unused"  status:INITIAL}  HostName:"9874e052-5e46-40c8-930a-bca53e66b814"  lastRebootReason:"NORMAL: First boot of device - at 2026-09-11T23:58:09.375412097Z"  lastRebootTime:{seconds:1789171089  nanos:375412097}  systemAdapter:{status:{version:1  key:"zedagent"  timePriority:{seconds:1789171162  nanos:338361101}  lastSucceeded:{seconds:1789171171  nanos:422622362}  ports:{ifname:"eth0"  name:"ethernet0"  free:true  proxy:{}  macAddr:"da:39:78:d0:19:f2"  dns:{}  up:true  err:{timestamp:{seconds:1789171171  nanos:413708800}}  usage:PhyIoUsageMgmtAndApps  networkUUID:"00000000-0000-0000-0000-000000000000"  mtu:1500  config_source:{origin:NETWORK_CONFIG_ORIGIN_CONTROLLER  submitted_at:{seconds:1789171162  nanos:338361101}}  pnac_status:{}}  ports:{ifname:"eth1"  name:"ethernet1"  free:true  proxy:{}  macAddr:"da:39:78:d0:19:f2"  dns:{}  up:true  err:{timestamp:{seconds:1789171171  nanos:413714160}}  usage:PhyIoUsageMgmtAndApps  networkUUID:"00000000-0000-0000-0000-000000000000"  mtu:1500  config_source:{origin:NETWORK_CONFIG_ORIGIN_CONTROLLER  submitted_at:{seconds:1789171162  nanos:338361101}}  pnac_status:{}}  ports:{ifname:"bond1"  name:"lacp-bond"  isMgmt:true  free:true  dhcpType:4  subnet:"172.20.20.0/24"  domainname:"test."  proxy:{}  macAddr:"da:39:78:d0:19:f2"  IPAddrs:"172.20.20.123"  IPAddrs:"fe80::24e6:9348:b171:a1b3"  defaultRouters:"172.20.20.1"  dns:{DNSservers:"10.16.16.25"  DNSdomain:"test."}  up:true  location:{UnderlayIP:"62.96.37.222"  Hostname:"h-62.96.37.222.host.de.colt.net"  City:"Berlin"  Country:"DE"  Loc:"52.5244,13.4105"  Org:"AS8220 COLT Technology Services Group Limited"  Postal:"10119"}  err:{description:"interface bond1: no suitable IP address available"  timestamp:{seconds:1789171171  nanos:413792815}  severity:SEVERITY_ERROR}  networkUUID:"fa9a4ecb-dc22-4919-b06a-77aeab2c74c7"  mtu:1500  config_source:{origin:NETWORK_CONFIG_ORIGIN_CONTROLLER  submitted_at:{seconds:1789171162  nanos:338361101}}  pnac_status:{}  bond_status:{mode:BOND_MODE_802_3AD  mii_monitor:{enabled:true  polling_interval:100}  arp_monitor:{}  lacp:{lacp_rate:LACP_RATE_FAST  active_aggregator_id:1  partner_mac:"ca:fe:fe:80:68:22"  actor_key:9  partner_key:9}  members:{logicallabel:"ethernet0"  mii_up:true  lacp:{aggregator_id:1  actor_churn_state:BOND_LACP_CHURN_STATE_MONITORING  partner_churn_state:BOND_LACP_CHURN_STATE_MONITORING}}  members:{logicallabel:"ethernet1"  mii_up:true  lacp:{aggregator_id:1  actor_churn_state:BOND_LACP_CHURN_STATE_MONITORING  partner_churn_state:BOND_LACP_CHURN_STATE_MONITORING}}}}  ports:{ifname:"eth2"  name:"ethernet2"  isMgmt:true  free:true  dhcpType:4  subnet:"172.20.21.0/24"  domainname:"test."  proxy:{}  macAddr:"ce:30:0f:9c:43:d9"  IPAddrs:"172.20.21.146"  IPAddrs:"fe80::1f21:bf71:af5f:226a"  defaultRouters:"172.20.21.1"  dns:{DNSservers:"10.16.17.25"  DNSdomain:"test."}  up:true  location:{UnderlayIP:"62.96.37.222"  Hostname:"h-62.96.37.222.host.de.colt.net"  City:"Berlin"  Country:"DE"  Loc:"52.5244,13.4105"  Org:"AS8220 COLT Technology Services Group Limited"  Postal:"10119"}  err:{timestamp:{seconds:1789171171  nanos:422411285}}  usage:PhyIoUsageMgmtAndApps  networkUUID:"68825e53-e63d-4787-b0a8-9e88c8185204"  mtu:1500  config_source:{origin:NETWORK_CONFIG_ORIGIN_CONTROLLER  submitted_at:{seconds:1789171162  nanos:338361101}}  pnac_status:{}}}}  HSMStatus:NOTFOUND  dataSecAtRestInfo:{status:DATASEC_AT_REST_DISABLED  info:"TPM is either absent or not in use"  vaultList:{name:"Application Data Store"  status:DATASEC_AT_REST_DISABLED  vaultErr:{description:"TPM is either absent or not in use"  timestamp:{seconds:1789171141  nanos:367669346}  severity:SEVERITY_ERROR}  pcrStatus:PCR_DISABLED}}  sec_info:{sha_root_ca:"}[\xbe\x1c\xf8\x86ֿצ_\xdaE\xa7\xc9\x0c\xc0\x17*\xa4d4\xabv\xaa\xbeL\x19Z\x9bs6"}  configItemStatus:{configItems:{key:"app.allow.vnc"  value:{value:"true"}}  configItems:{key:"debug.default.loglevel"  value:{value:"debug"}}  configItems:{key:"debug.default.remote.loglevel"  value:{value:"debug"}}  configItems:{key:"debug.enable.console"  value:{value:"true"}}  configItems:{key:"debug.enable.ssh"  value:{value:"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAQC+H1RQUqHjFBJgGpslC73XsLz8Fg5WpNPble9naKyWz1Um8D2bOtQK/yguCImPeBYcH7/73z8dtC6d+dT0UF26+o7Vh6RN/U2X/5nkaZr7oM5QwwZTsD7Nd2Szww9wrRhXvpV0aFgUBDM9BIF1qBQxLNd+Jp8uttrgF3zj/cm7+SXllG54sv8WFBMfTX7J8cQ1jxLyp/Sc6PXK0zBaVzZwhmCCmI6CIzJK6ahMRgXm2vSP6doYibkB3ETSskaCXSHxDiZoaQK2ZY+GqNZkUusbau43MXVPTiJknXqUcmXhQmwyMSltQ3G54jcgn4TDObSQnW7vGdLI7zIEAHnk5D1BmKzQUBh5aRLdhBtb6T3uvVtAqGgnXWKD+d2GjMiy4G31zfIlArvC3G8LxwsDoxQL0XKaFjnEmXIptVXC68zq+laIM8YGDDOCEc6RfczP7lA4p6rv0gQUfTNqy0P3a4ulvIDb4hET1Gkh+Azkuw1do9NIhXPxDBmPdkTnKwJv6XelpdCPCw1QT5o7WdOkUgorf/e03jGDnnn1QBWpSPB9gLB/oDmT/Gzm8tCYaq7ggwYUq1fSMBvnbFaclH1KVcC2Gwn8UPLt9HHa/mGywuISZYl3gy7ztlKyAjHEZH053U7I8OaMvv/CFo9aR4Teeb5848REZYAes+yJIz3lJV1K5Q=="}}  configItems:{key:"newlog.allow.fastupload"  value:{value:"true"}}  configItems:{key:"timer.config.interval"  value:{value:"5"}}  configItems:{key:"timer.deviceinfo.interval"  value:{value:"30"}}  configItems:{key:"timer.download.retry"  value:{value:"60"}}  configItems:{key:"timer.location.app.interval"  value:{value:"20"}}  configItems:{key:"timer.location.cloud.interval"  value:{value:"300"}}  configItems:{key:"timer.metric.interval"  value:{value:"20"}}  configItems:{key:"timer.test.baseimage.update"  value:{value:"60"}}}  last_boot_reason:BOOT_REASON_FIRST  hardware_watchdog_present:true  capabilities:{HWAssistedVirtualization:true  IOVirtualization:true}  state:ZDEVICE_STATE_ONLINE  storage_info:{pool_name:"/persist"  storage_type:STORAGE_TYPE_INFO_EXT4  storage_state:STORAGE_STATUS_ONLINE  disks:{disk_name:{name:"/dev/vda9"  serial:"unknown"}  status:STORAGE_STATUS_ONLINE  smart_status:"failed"}}  attestation_info:{state:ATTESTATION_STATE_COMPLETE}  api_capability:API_CAPABILITY_DEFERRED_QUEUE_METRICS  optional_capabilities:{hw_inventory_support:true}  device_name:"edge-dev"
```

##### Claude's conclusion

This is a confirmed recurrence of a known, previously-diagnosed race condition — not a new bug.

**Root cause:** `/run/nim/DeviceNetworkStatus/global.json` on the live device shows `bond1` (lacp-bond) recorded `LastFailed: 2026-09-11T23:59:31.413Z` (matching the "no suitable IP address available" error in the failure snapshot) and `LastSucceeded: 2026-09-12T00:04:34.863Z` with `LastError` now empty — i.e. the bond actually came up and tested clean, but only **~3.8 seconds after** the test's own 300s timeout (23:59:31 + 300s = 00:04:31) had already fired. This is the identical mechanism documented in the ledger for run #26 (2026-09-08): `ethernet2` is a same-cost mgmt port that satisfies `requiredSuccessCount` first, so per `controllerconn.VerifyAllIntf` (pkg/pillar/controllerconn/send.go), `bond1`'s stale failed `TestResults` don't get refreshed until NIM's next full test rotation — which, with the default 300s `NetworkTestInterval` colliding with the test's own 300s timeout, is a coin-flip race. It reappeared once between then and now (no entry for it in the intervening runs #31-#39 in the ledger), so it's recurring but intermittent, not a persistent regression. Since the mechanism is a timing race rather than a functional defect, it is unrelated to the pending `018ec1b2` "personal testing overrides" commit at HEAD — there's no evidence tying this specific PR's changes to the failure.


#### TestVLANSubinterfacesOnTopOfLAGs

##### Failure

```

Timed out after 300.000s.
Expected to satisfy: App3 receives IP from VLAN 30 subnet
AppID:"d5a2aa8b-8ba2-4945-8eb3-7160fa012f0b"  appVersion:"1"  AppName:"vlan30-eth2-app"  bootTime:{seconds:1789172251  nanos:251841072}  state:RUNNING  network:{macAddr:"02:16:3e:00:00:03"  devName:"vif0"  defaultRouters:"<nil>"  dns:{}  localName:"nbu1x2"}  volumeRefs:"64fac71b-8a6f-4588-a60d-70a2d5e4ec3a"  cluster_app_running:true
```

##### Claude's conclusion

This confirms it's a well-established recurring bug, first surfaced 2026-09-09, seen across several different tests since.

###### Root cause

Confirmed via live logs: at `00:16:50.417` zedrouter attempted to open a pcap capture on the mirror interface `bn2-m` for NI `d61e084f-fb65-444a-b947-8ee6d627a00f` (`ni3-eth2`, the switch-NI on ethernet2 that carries the VLAN-30-tagged app), but failed with `unknown interface bn2-m: route ip+net: no such network interface` — the reconciler didn't actually create `DummyInterface/bn2-m` until 180ms later (`00:16:50.596`). Per the known bug in `pkg/pillar/nistate/linux.go`, `StartCollectingForNI` sets `ni.cancelPCAP` before the `sniffDNSandDHCP` goroutine runs, and when `pcap.OpenLive` fails inside it, the code just logs and returns without clearing `cancelPCAP`, so the retry guard never re-fires — no ARP/DHCP snooping ever runs again for that NI's lifetime. I confirmed this is exactly what happened live: the SDN's own `dnsmasq` log shows a completely successful DHCP exchange for `vlan30-eth2-app`'s MAC (`02:16:3e:00:00:03`), granting it `172.22.30.20` at 00:17:47 — but zedrouter's `NI State: Captured packet (DHCPv4...)` log (which fires for the sibling app on the other VLAN-30 NI, `vlan30-bond-app`, at the same moment) never fires for this app, so its `AppNetworkStatus` never gets an IP and the test's `Eventually` times out waiting for it.

This is the identical, previously-diagnosed "give up after first failed pcap open" defect in `nistate/linux.go`, first identified via `TestAirGapSwitchNI` on 2026-09-09 and since recurring under multiple different test names (`TestMgmtTrafficRoutedViaApp`, and now this one) — it isn't a defect specific to this test or a new bug, just another switch-NI test hitting the same startup race. It does not appear to be related to the earlier `TestLACPBond` failure investigated in this same run (that was a NIM test-timing/`NetworkTestInterval` race, unrelated to pcap/ARP-DHCP snooping). It's unrelated to the `018ec1b2` "personal testing overrides" commit at HEAD as well.

### TestDiagSuite: failure analysis

#### TestDiagOutput

##### Failure

```
failed to onboard device "edge-dev": context deadline exceeded
```

##### Claude's conclusion

###### Root cause analysis

**Live evidence:** The EVE device (`edge-dev`, MAC `da:39:78:d0:19:f2`) booted normally in its Proxmox VM at 22:47:xx and its bridge port on the SDN came up fine — but it only ever sent a single `DHCPDISCOVER`, which SDN's dnsmasq answered with a `DHCPOFFER(172.20.20.123)` at 22:47:42, logged in `dhcpsrv-network0.log`. No `DHCPREQUEST`/`DHCPACK` ever followed, no retry `DHCPDISCOVER` was seen in the following ~13 minutes, and `arp -an` for `172.20.20.123` in the SDN's router netns shows `<incomplete>` right now — i.e. the device never actually claimed or is using that address. Consequently the device never had a routable IP, `evetest eve ssh`/`collect-info` both fail with "failed to detect any IP address," Adam's controller logs (`adam-stdout`) show zero mentions of the device's serial (`VRF74HT3`) or onboard-cert CN, and `evetest status` still reports `EVE_DEVICE_STATE_UNDEFINED` with no interfaces — confirming the device genuinely never reached the controller to onboard, matching the reported `failed to onboard device "edge-dev": context deadline exceeded`.

The SDN side itself looks healthy (correct bridge/veth/netns topology, dnsmasq bound and answering correctly, network model applied without errors per `evetest sdn status`), so this points to a device-side failure — most likely the guest's DHCP client (nim) hanging or dying right after sending its first `DISCOVER`, before it could process the `OFFER` or retry. I could not confirm the exact pillar/nim internal state directly since no SSH path exists without an IP, and the device's serial console only carries kernel/containerd boot logs at that point (login prompt is reached, but no pillar/nim/zedagent output is visible via `console-output`).

This is the only failure in this run so far, and there is nothing about this specific "single DHCPDISCOVER, no follow-up, onboarding times out" signature in `live-ledger.md` — no prior entry mentions onboarding timeouts, DHCP stalls, or `TestDiagSuite`/`TestDiagOutput` at all. So this appears to be a **new, previously unseen failure mode**, not a recurrence of any known/tracked issue in the ledger.

## [2026-09-11 -- 0.0.0-master-e213aba5 (run #39)](https://github.com/milan-zededa/eve/actions/runs/34605755112)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/39/)

### TestAppsSuite: failure analysis

_Paused for live investigation but nothing was recorded (see the job log)._

### TestDeviceConnectivitySuite: failure analysis

#### TestMgmtTrafficRoutedViaApp

##### Failure

```

Timed out after 180.001s.
Expected to satisfy: app reports 2 VIFs with the reserved IPs
AppID:"4e8ef0c1-0a74-4326-907a-2230803a1f63"  appVersion:"1"  AppName:"mgmt-gw-app"  bootTime:{seconds:1789142604  nanos:452979381}  state:RUNNING  network:{macAddr:"02:16:3e:02:00:00"  devName:"vif0"  IPAddrs:"10.60.10.150"  IPAddrs:"fe80::16:3eff:fe02:0"  defaultRouters:"<nil>"  dns:{}  ipv4_up:true  localName:"nbu1x1"}  network:{macAddr:"02:16:3e:02:00:01"  devName:"vif1"  defaultRouters:"<nil>"  dns:{}  localName:"nbu2x1"}  volumeRefs:"cd663959-9de7-4115-bd16-5aecfa3fee5d"  cluster_app_running:true
```

##### Claude's conclusion

Confirmed the bug is still present unfixed on current master, matching the ledger's prior diagnosis.

###### Root cause

Live `zedrouter` logs show the identical race documented previously: at 16:03:10.956 `zedrouter` attempted to open a pcap capture on mirror interface `eth1-m` (bridge `eth1`, NI `069b9e62-c9eb-4fe2-81e3-0b3db6efbdd8`), but the reconciler didn't actually create `DummyInterface/eth1-m` until 16:03:11.081 — 78ms later — so the open failed with `unknown interface eth1-m: route ip+net: no such network interface`. `pkg/pillar/nistate/linux.go` sets `ni.cancelPCAP` before the `sniffDNSandDHCP` goroutine runs (lines 251-254/307-311), and when `pcap.OpenLive` fails inside that goroutine it only logs and returns without clearing `cancelPCAP`, so the `ni.cancelPCAP == nil` retry guard never re-fires — confirmed live: `vif1`/`nbu2x1` attached at 16:03:21.738 with no subsequent pcap retry log for `eth1-m` anywhere afterward, which is why `zedrouter` never reports an IP for that VIF and the test's `Eventually` (expecting 2 VIFs with IPs) timed out.

This is the same defect (unfixed on current master, confirmed via code read) already recorded in the ledger for this exact test in run #34 (2026-09-10), which itself traces back to the same `nistate/linux.go` "give up after first failed pcap open" bug first seen via `TestAirGapSwitchNI` in run #31 (2026-09-09). So this is a **known, recurring issue since at least 2026-09-09**, reproducing again verbatim today — not a new EVE/pillar regression. No other suite has been investigated in this run to compare for a shared root cause.

### TestLPSSuite: failure analysis

_Paused for live investigation but nothing was recorded (see the job log)._

## [2026-09-11 -- 0.0.0-master-0c954e83 (run #38)](https://github.com/milan-zededa/eve/actions/runs/34572329223)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/38/)

### TestApplicationConnectivitySuite: failure analysis

#### TestFlowLog

##### Failure

```

Timed out after 180.000s.
The function passed to Eventually failed at /evetest/tests/networking/netinst_test.go:2437 with:
expected an outbound flow record for the allowed HTTP ACE (2) from 10.50.0.2 to 10.17.17.25:80
Expected
    <*flowlog.FlowRecord | 0x0>: nil
not to be nil
```

##### Claude's conclusion

Good — only `TestFlowLog` failed in this run; every other subtest so far shows `pass`. No shared root cause with another failure in this run since there isn't one.

###### Root cause

This is the well-known **`TestFlowLog` conntrack-timeout-margin issue**, reproducing again with identical mechanics:

- Live evidence: `evetest eve flow-logs flowlog-test-app` shows the exact expected record now exists (`src:"10.50.0.2" srcPort:57822 dest:"10.17.17.25" destPort:80 protocol:6 aclId:2 ... action:ActionAccept`), with the TCP connection closing (`endTime`) at **10:05:51 UTC**. The test's `phase1-traffic-generated` checkpoint fired at **10:04:02 UTC**, so its 180s `Eventually` deadline hit at **10:07:02 UTC** — before the record could possibly become visible.
- The device's live `nf_conntrack_tcp_timeout_time_wait` is confirmed at **270s**, while `pkg/pillar/nistate/linux_flow.go:39` hardcodes `conntrackFlowExtraTimeout = 150`s, and `convertConntrackToFlow` (line 183) only turns a conntrack entry into a flow record once its *remaining* timeout drops below that 150s threshold — i.e. ~120s after the connection closes. That puts eligibility at ~10:07:51, a full 49s after the test already gave up. The device's `flowlogTask` log confirms this: it ran at 10:05:17 (before close) and again at 10:07:05 (3s after the test's deadline, still too early to have picked up the now-eligible record).
- This is a **known, recurring issue**, not new: the identical `TestFlowLog` failure/root cause is recorded in the ledger for runs #31, #32 (2026-09-09) and #33, #35, #36 (2026-09-10) — this is at least the **seventh consecutive reproduction** across three days, with the same unfixed test-timeout margin in `evetest/tests/networking/netinst_test.go` (line 2437/2486), not a pillar/EVE regression.
- No other test failed in this run, so there's no shared root cause to consider.

### TestNodeClusterSuite: failure analysis

#### TestClusterToSingleConversion

##### Failure

```
Device "edge-dev3" failed to confirm the latest config (version 8): timed out waiting for LastProcessedConfig >= 2026-09-11 12:22:13.098302288 +0000 UTC
```

##### Claude's conclusion

###### Root cause

Live evidence confirms the same mechanism already documented in the ledger: config version 8 (withdrawing `edge-dev3` from the cluster) was fetched by the device at 12:22:14, and the harness began waiting (with the fixed 2-minute `deviceApplyConfigTimeout`) for `LastProcessedConfig >= 2026-09-11 12:22:13.098`. That config change forces `edge-dev3` to reboot back to standalone mode — the harness itself observed the reboot completing at 12:23:26 — but pillar's bootstrap after the reboot is still ongoing well past the timeout: live `nim`/`zedagent` logs show the DPC with `TimePriority: 12:22:13.098302288` only reaching `DPC_SUCCESS` at 12:24:59, and `domainmgr`/`volumemgr` were still re-subscribing to `EdgeNodeClusterConfig`/`ContentTreeConfig` and zedagent was still reprocessing controller certs at 12:25:22–12:25:26 — over 3 minutes after the config was fetched, i.e. well past the 2-minute deadline (12:24:14) at which the test gave up.

This is the identical failure signature ("failed to confirm the latest config"/`LastProcessedConfig` timeout on the withdrawn node) recorded in `live-ledger.md` for run #35 on 2026-09-10, where the same conclusion was reached: the test's `ApplyConfig` confirm-timeout is a fixed 2 minutes, but this particular per-device cluster-withdrawal path is inherently reboot-bound and routinely exceeds that budget. So this is a **known, recurring issue since at least 2026-09-10**, reproducing again today — a test-timeout-margin bug in the harness (`conversion_test.go:212` / `edgedevice.go:280-327`), not a new EVE/pillar regression. It does not share a root cause with any other failure noted so far in this run (this is the only failure reported).

### TestStorageSuite: failure analysis

#### TestVolumes

##### Failure

```

Told to stop trying after 0.010s.
Stop waiting for: volume v-vhdx is created
Volume reports an error: Found error in content tree v-vhdx-image attached to volume v-vhdx: 
Size '0' provided in image config of 'v-vhdx.vhdx' is incorrect.
Download status (6422528 / 0). Aborting the download

uuid:"019206a0-8cf6-43cb-a0d6-235e9db5bfd7"  displayName:"v-vhdx"  usage:{createTime:{seconds:-62135596800}  refCount:1  lastRefcountChangeTime:{seconds:1789120802  nanos:884581647}}  state:DOWNLOAD_STARTED  volumeErr:{description:"Found error in content tree v-vhdx-image attached to volume v-vhdx: \nSize '0' provided in image config of 'v-vhdx.vhdx' is incorrect.\nDownload status (6422528 / 0). Aborting the download\n"  timestamp:{seconds:1789120804  nanos:160268221}  severity:SEVERITY_NOTICE  entities:{entity:ENTITY_CONTENT_TREE  entity_id:"4b483b47-6072-4fe9-8f9b-813d916ee25d"}  retry_condition:"Will retry in 1m0s; have retried 0 times"}
```

##### Claude's conclusion

Only `TestVolumes` ran/failed in this run so far — no other failure to share a root cause with. This confirms the pattern is identical to the recurring ledger issue. I have sufficient evidence.

###### Root cause

Live device logs confirm the same mechanism documented repeatedly in the ledger: at 10:00:02.985 the datastore HTTP server serving `v-vhdx.vhdx` ignored the Range request (`server ignored Range; skipping copiedBytes manually`), and ~900ms later `downloader` logged `Update progress for v-vhdx.vhdx: 6422528/0` — nonzero `currentSize` but zero `totalSize` — which trips the unconditional `currentSize > totalSize` check at `pkg/pillar/cmd/downloader/download.go:325`, aborting with `Size '0' provided in image config ... incorrect`. That error propagates through `doUpdateContentTree` → `doUpdateVol` → the volume's `volumeErr`, exactly matching this run's failure output. I confirmed the code on current `master` still has no `totalSize > 0` guard on that check.

This is the **same test/failure signature recorded in `live-ledger.md`** for runs #32 (2026-09-09), #35 and #36 (2026-09-10) — a **known, recurring issue since at least 2026-09-09**, now reproducing for a fourth consecutive day on the same build family (`0.0.0-master-551d4490`/`-...`). There's no other failure in this run to share a root cause with, since `TestVolumes` is the only test that has run so far. Per prior ledger notes, the fix (a `totalSize > 0` guard) exists only on unmerged branches, not on master — not a new EVE/pillar regression, just still unfixed.

## [2026-09-11 -- 0.0.0-master-551d4490 (run #37)](https://github.com/milan-zededa/eve/actions/runs/34548841403)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/37/)

### TestApplicationConnectivitySuite: failure analysis

#### TestSwitchNIPortConfigRace

##### Failure

```

Told to stop trying after 0.022s.
vlan-switch-ni: Network instance is in error state
networkID:"6d6d8b80-ac14-4134-a987-0eb059e38b48"  networkVersion:"1"  instType:1  displayname:"vlan-switch-ni"  activated:true  CurrentUplinkIntf:"vlan100"  ports:"vlan100"  bridgeNum:2  bridgeName:"vlan100"  ipAssignments:{macAddress:"02:16:3e:00:00:02"  ipAddress:"10.53.100.181"}  vifs:{vifName:"nbu2x1"  macAddress:"02:16:3e:00:00:02"  appID:"c93bdef3-4bb9-45dd-90f4-4f03f746e227"}  networkErr:{description:"failed items: BridgeFwdMask/vlan100 (failed to zero-out forwarding mask for bridge vlan100: open /sys/class/net/vlan100/bridge/group_fwd_mask: no such file or directory)"  timestamp:{seconds:1789097086  nanos:753521714}  severity:SEVERITY_ERROR}  state:ZNETINST_STATE_ERROR  mtu:1500
```

##### Claude's conclusion

This confirms the same known pattern from the ledger, now healed live (bridge and `group_fwd_mask`=0 present and healthy, exactly as in prior occurrences).

**Root cause:** Live logs show the reconciler creating `VLANBridge/vlan100` at 03:24:46.749 (`expectedBridgeID:0`), with the `BridgeFwdMask/vlan100` item run in that same pass failing at 03:24:46.863 with `open /sys/class/net/vlan100/bridge/group_fwd_mask: no such file or directory` — immediately followed at .919–.920 by the reconciler detecting a bridge-identity mismatch and redoing the bridge with `expectedBridgeID:14`, i.e. the bridge was transiently absent from sysfs mid-recreation during this test's deliberate port-config churn. Live inspection now confirms `vlan100`'s bridge and `group_fwd_mask` (value `0x0`) are present and healthy, confirming the absence was transient, not a lasting device fault. This is not a new bug and doesn't share a root cause with anything else in this run — it's the same `TestSwitchNIPortConfigRace` defect family recorded repeatedly in `live-ledger.md`: first as a regression caught by this brand-new test on 2026-09-03/08 (run #26, `TCMirror.Create`/`Parent Qdisc doesn't exists`), then this identical `BridgeFwdMask.Create`/`group_fwd_mask` symptom on 2026-09-09 (run #32) and again on 2026-09-10 (runs #35, #36) — a lack of race-tolerance in the reconciler's `Create` paths for bridge-dependent items when the bridge is transiently torn down/recreated during port reconfiguration (the `8f277c37b` commit hardened `Delete` paths for this but not `Create`). This is a known, recurring issue since at least 2026-09-08, still unfixed as of this build (`0.0.0-master-551d4490`), reproducing today verbatim.

### TestStorageSuite: failure analysis

#### TestVolumes

##### Failure

```

Told to stop trying after 0.010s.
Stop waiting for: volume v-vhdx is created
Volume reports an error: Found error in content tree v-vhdx-image attached to volume v-vhdx: 
Size '0' provided in image config of 'v-vhdx.vhdx' is incorrect.
Download status (1310720 / 0). Aborting the download

uuid:"8f75d5d0-4ca9-4f07-adad-0c1c5b620268"  displayName:"v-vhdx"  usage:{createTime:{seconds:-62135596800}  refCount:1  lastRefcountChangeTime:{seconds:1789088997  nanos:824377356}}  state:DOWNLOAD_STARTED  volumeErr:{description:"Found error in content tree v-vhdx-image attached to volume v-vhdx: \nSize '0' provided in image config of 'v-vhdx.vhdx' is incorrect.\nDownload status (1310720 / 0). Aborting the download\n"  timestamp:{seconds:1789089000  nanos:43427923}  severity:SEVERITY_NOTICE  entities:{entity:ENTITY_CONTENT_TREE  entity_id:"3c0f444a-9a03-4100-a179-0c0dbeaa54b1"}  retry_condition:"Will retry in 1m0s; have retried 0 times"}
```

##### Claude's conclusion

The unmerged fix branches (`dpc-fixes`, `evetest-ci`, `upgrade-test-change`) still exist remotely, unmerged into master, confirming the ledger's note. I have enough evidence now.

###### Root cause

Live device logs confirm the identical mechanism documented in the ledger: at 01:09:59.044 the datastore HTTP server serving `v-vhdx.vhdx` ignored the Range request (`server ignored Range; skipping copiedBytes manually`), and 627ms later `downloader` logged `Update progress for v-vhdx.vhdx: 1310720/0` — nonzero `currentSize` but zero `totalSize` — which immediately trips the unconditional `currentSize > totalSize` check at `pkg/pillar/cmd/downloader/download.go:325`, aborting with `Size '0' provided in image config ... incorrect`. That error then propagates up through `doUpdateContentTree` → `doUpdateVol` → the volume's `volumeErr`, exactly matching this run's failure output.

This is the **same test/failure signature recorded in `live-ledger.md` for run #36 (2026-09-10)**, which itself traced back to run #32 (2026-09-09) — so this is a **known, recurring issue since at least 2026-09-09**, now reproducing for at least a third consecutive day. I verified the code state is unchanged on current `master`: `download.go`'s `currentSize > totalSize` check still has no `totalSize > 0` guard, and the three fix commits (`b2c75e0df`, `4dce98f55`, `ed743e546`) remain only on unmerged branches (`dpc-fixes`, `evetest-ci`, `upgrade-test-change`), not on master or in the tested build (`0.0.0-master-551d4490`, same build as prior occurrences). There's no other failure in this run to share a root cause with. It's not a new EVE/pillar regression — it's the same unfixed downloader defect awaiting merge.

## [2026-09-10 -- 0.0.0-master-551d4490 (run #36)](https://github.com/milan-zededa/eve/actions/runs/34517740536)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/36/)

### TestApplicationConnectivitySuite: failure analysis

#### TestLocalNI

##### Failure

```
Failed to receive SDN tunnel properties: rpc error: code = Unavailable desc = unable to connect to SDN gRPC service on any of the uplink IPs ([192.168.170.5]): failed to establish tunnel to SDN: rpc error: code = Unavailable desc = connection error: desc = "transport: Error while dialing: dial tcp 192.168.170.5:50121: connect: no route to host"
```

##### Claude's conclusion

###### Root cause

Live evidence from `gotest.json` shows SDN VM `sdn-5231a41b` (Proxmox VM 109) was provisioned and powered on normally, getting uplink IP `192.168.170.5` at 20:08:35. The very first gRPC connect attempt at 20:08:55 got `i/o timeout` (host route up, VM not yet listening), but starting at 20:09:07 every retry for the next ~4m30s (through 20:13:37, when the test's budget expired) got `connect: no route to host` instead — a permanent, not transient, failure mode indicating the VM's network path died entirely after the initial boot phase, rather than the gRPC service just being slow to start. Live `evetest sdn status`/`ssh`/`logs` right now still fail (`SDN client is not initialized`, SSH `connection reset by peer`), confirming the SDN VM is still unreachable, not merely having been slow.

This is the same failure signature as run #34's `TestZVolProvisionedSizeReported` (also today, 2026-09-10): a freshly-provisioned SDN VM (VM 108, IP `192.168.170.4`) that got its uplink IP but then was never reachable over gRPC for the whole retry window, while an adjacent test's SDN VM in that same run connected fine after a few seconds. That prior instance was judged a one-off Proxmox/SDN-provisioning infra hiccup with no earlier ledger history. Seeing the identical pattern reproduce on a second, independently-provisioned SDN VM within the same nightly run suggests this may be an emerging recurring infra issue (first appeared today) rather than a true one-off — worth flagging to whoever owns the Proxmox/SDN broker infrastructure — but it is not an EVE/pillar code defect, and there's no other failure in this run yet to share a root cause with.


#### TestFlowLog

##### Failure

```

Timed out after 180.000s.
The function passed to Eventually failed at /evetest/tests/networking/netinst_test.go:2437 with:
expected an outbound flow record for the allowed HTTP ACE (2) from 10.50.0.2 to 10.17.17.25:80
Expected
    <*flowlog.FlowRecord | 0x0>: nil
not to be nil
```

##### Claude's conclusion

###### Root cause

Live flow-log evidence confirms the exact same pattern as the ledger's prior `TestFlowLog` failures: `evetest eve flow-logs flowlog-test-app` shows the expected record now exists — `src:"10.50.0.2" srcPort:55232 dest:"10.17.17.25" destPort:80 protocol:6 aclId:2 ... action:ActionAccept` — with the TCP connection closing (`endTime`) at 20:35:24 UTC. The test's `phase1-traffic-generated` checkpoint fired at 20:33:35 UTC, so the 180s `Eventually` deadline hit at 20:36:35 — only ~71s after the connection actually closed. The device's `nf_conntrack_tcp_timeout_time_wait` is (as before) 270s, deliberately larger than the flow-log code's `conntrackFlowExtraTimeout` (150s), so the conntrack entry only becomes flow-log-eligible ~120s after close (~20:37:24) plus up to another randomized sweep interval — comfortably past the test's remaining ~71s budget, so the record simply hadn't been collected/published yet when the test gave up.

This does **not** share a root cause with this run's earlier `TestLocalNI` failure (that was an SDN VM/Proxmox connectivity issue, unrelated). It **is** a known, recurring issue: identical `TestFlowLog` failures with this same message, same allowed-HTTP-ACE record, and the same conntrack-timeout-margin root cause are documented in the ledger for runs #31 and #32 (2026-09-09), and #33 and #35 (2026-09-10, same build `0.0.0-master-551d4490`) — this is at least the sixth consecutive reproduction, spanning three days, with no fix landed. It remains a test-timeout-margin bug in `evetest/tests/networking/netinst_test.go` (line 2437/2486), not a pillar/EVE regression.


#### TestNetworkAdapterPassthrough

##### Failure

```
Failed to receive SDN tunnel properties: rpc error: code = Unavailable desc = unable to connect to SDN gRPC service on any of the uplink IPs ([192.168.170.6]): failed to establish tunnel to SDN: rpc error: code = Unavailable desc = connection error: desc = "transport: Error while dialing: dial tcp 192.168.170.6:50121: connect: no route to host"
```

##### Claude's conclusion

I have enough evidence now.

###### Root cause

Live evidence (`gotest.json`) shows a fresh SDN Proxmox VM (VM 108) for `TestNetworkAdapterPassthrough` powered on at 21:41:06 and got uplink IP `192.168.170.6`, but every gRPC connect retry from 21:41:21 through 21:46:04 (~4m40s, the full retry budget) failed with `no route to host` — never once succeeding — before `openTunnelToSDN` gave up. Live checks right now confirm the SDN VM is still unreachable, not merely slow: `evetest sdn status` → `SDN client is not initialized`, `evetest sdn ssh`/`evetest eve ssh` → `Connection reset by peer` / `network model not applied`.

This is the exact same failure signature as this run's earlier `TestLocalNI` failure (SDN VM `192.168.170.5`, same "boots, gets IP, then permanently no-route-to-host" pattern) and shares a root cause with it — a freshly-provisioned SDN VM whose network path from the Proxmox broker never comes up, as opposed to the normal brief boot-time race (which typically resolves in a few seconds/retries, as seen for adjacent tests in this and prior runs). Per the ledger, this identical signature also occurred earlier today in run #34 (`TestZVolProvisionedSizeReported`, VM 108/`192.168.170.4`) and was judged a one-off Proxmox/SDN-provisioning infra hiccup at the time — but with three independent occurrences now within the same day (run #34, and two in this run #35: `TestLocalNI` and `TestNetworkAdapterPassthrough`), this looks like an emerging recurring Proxmox/SDN broker infrastructure problem rather than a true one-off, and worth escalating to whoever owns that infra. It is not an EVE/pillar code defect.

### TestLPSSuite: failure analysis

#### TestNetworkLocalChanges

##### Failure

```

Timed out after 153.516s.
The function passed to Eventually failed at /evetest/tests/lps/helpers_test.go:134 with:
Unexpected error:
    <*fmt.wrapError | 0xc001193a20>: 
    failed to execute script over SSH for app 0a67b56d-a4a2-45dd-aad7-a29bc34782bb: SSH dial to 172.20.20.123:2222 failed: ssh: handshake failed: read tcp 250.250.250.1:37314->172.20.20.123:2222: read: connection timed out (stderr: )
    {
        msg: "failed to execute script over SSH for app 0a67b56d-a4a2-45dd-aad7-a29bc34782bb: SSH dial to 172.20.20.123:2222 failed: ssh: handshake failed: read tcp 250.250.250.1:37314->172.20.20.123:2222: read: connection timed out (stderr: )",
        err: <*fmt.wrapError | 0xc001193a00>{
            msg: "SSH dial to 172.20.20.123:2222 failed: ssh: handshake failed: read tcp 250.250.250.1:37314->172.20.20.123:2222: read: connection timed out",
            err: <*fmt.wrapError | 0xc0011939e0>{
                msg: "ssh: handshake failed: read tcp 250.250.250.1:37314->172.20.20.123:2222: read: connection timed out",
                err: <*net.OpError | 0xc003b554a0>{
                    Op: "read",
                    Net: "tcp",
                    Source: <*net.TCPAddr | 0xc002c80a20>{
                        IP: [250, 250, 250, 1],
                        Port: 37314,
                        Zone: "",
                    },
                    Addr: <*net.TCPAddr | 0xc002c80a50>{
                        IP: [172, 20, 20, 123],
                        Port: 2222,
                        Zone: "",
                    },
                    Err: <*os.SyscallError | 0xc0011939c0>{
                        Syscall: "read",
                        Err: <syscall.Errno>0x6e,
                    },
                },
            },
        },
    }
occurred
```

##### Claude's conclusion

###### Root cause

Live device logs show the failure exactly coincides with the test's own network-revert step: at 21:34:50 the harness submitted the empty LPS config to revert eth0's local override, which at 21:34:51.98 triggered NIM/zedrouter to release/re-lease DHCP on both eth0/eth1 and briefly delete+recreate the local-NI's DNAT port-forward rules (`2222→10.11.12.128:22`) — deleted at 21:34:52.238, fully restored by 21:34:53.234. The app (`lps-app`) itself never rebooted or lost its IP (`evetest eve app-info` shows a single continuous `RUNNING` since boot), and no device-side errors/warnings appear anywhere in the following ~2.5 minutes — the device network stack was fully healthy again within ~1s of the revert. Yet the harness's SSH dial (source `250.250.250.1`, the SDN tunnel) stayed stuck on a single attempt for the full ~153s budget with a low-level TCP `read: connection timed out` rather than retrying, meaning one dial attempt landed in that ~1s reconfiguration window, the connection wedged, and — since the test's SSH helper has no shorter per-attempt timeout — it consumed nearly the entire `Eventually` budget before failing.

This is the same root-cause family already documented in the ledger for this exact test on 2026-09-09 (run #31): the LPS config-revert step's brief NAT/DHCP teardown racing a single un-timeout-bounded SSH dial attempt. It's a known, recurring issue since 2026-09-09 (this is its second occurrence), and it's a test-harness timeout/retry-margin gap rather than a new EVE/pillar defect — the device itself recovers well within a second.

### TestStorageSuite: failure analysis

#### TestVolumes

##### Failure

```

Told to stop trying after 0.011s.
Stop waiting for: volume v-vhdx is created
Volume reports an error: Found error in content tree v-vhdx-image attached to volume v-vhdx: 
Size '0' provided in image config of 'v-vhdx.vhdx' is incorrect.
Download status (2883584 / 0). Aborting the download

uuid:"29f444e7-9b7d-4c70-a8ec-a3a70c4eea1f"  displayName:"v-vhdx"  usage:{createTime:{seconds:-62135596800}  refCount:1  lastRefcountChangeTime:{seconds:1789067375  nanos:285616332}}  state:DOWNLOAD_STARTED  volumeErr:{description:"Found error in content tree v-vhdx-image attached to volume v-vhdx: \nSize '0' provided in image config of 'v-vhdx.vhdx' is incorrect.\nDownload status (2883584 / 0). Aborting the download\n"  timestamp:{seconds:1789067376  nanos:682866675}  severity:SEVERITY_NOTICE  entities:{entity:ENTITY_CONTENT_TREE  entity_id:"113b0b8e-a0bc-4681-99df-488dc61014b6"}  retry_condition:"Will retry in 1m0s; have retried 0 times"}
```

##### Claude's conclusion

This confirms the exact same mechanism identified in prior ledger entries. I have sufficient evidence now.

###### Root cause

Live logs confirm the identical mechanism seen in previous ledger entries: at 19:09:36.021 the datastore HTTP server for `v-vhdx.vhdx` ignored the Range request (`server ignored Range; skipping copiedBytes manually`), so `downloader` (`download.go:320`) logged `Update progress for v-vhdx.vhdx: 2883584/0` — nonzero `currentSize` but zero `totalSize` — and 386ms later hit the unconditional `currentSize > totalSize` check at `pkg/pillar/cmd/downloader/download.go:328`, aborting with `Size '0' provided in image config ... incorrect`. That error then propagated up through the blob/content-tree/volume status chain exactly as in `TestVolumes`'s prior failures.

This is the same test/failure signature recorded in `live-ledger.md` for run #32 (2026-09-09, same build family `551d4490`), and I confirmed the code state is unchanged: `download.go`'s `currentSize > totalSize` check still has no `totalSize > 0` guard on current `master`. The three "downloader: don't abort a download on a transient zero total size" fix commits (`b2c75e0df`, `4dce98f55`, `ed743e546`) still exist only on unmerged branches (`dpc-fixes`, `evetest-ci`, `upgrade-test-change`), not on master or the tested build. So this is a **known, recurring issue since at least 2026-09-09**, unfixed as of today's build — the fix branch containing the guard has still not been merged to master.

## [2026-09-10 -- 0.0.0-master-551d4490 (run #35)](https://github.com/milan-zededa/eve/actions/runs/34471430167)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/35/)

### TestApplicationConnectivitySuite: failure analysis

#### TestFlowLog

##### Failure

```

Timed out after 180.001s.
The function passed to Eventually failed at /evetest/tests/networking/netinst_test.go:2437 with:
expected an outbound flow record for the allowed HTTP ACE (2) from 10.50.0.2 to 10.17.17.25:80
Expected
    <*flowlog.FlowRecord | 0x0>: nil
not to be nil
```

##### Claude's conclusion

This is the exact same recurring issue seen in the ledger. Live evidence confirms it:

**Root cause:** The expected flow record now exists live (`evetest eve flow-logs flowlog-test-app`): `src:"10.50.0.2" srcPort:42158 dest:"10.17.17.25" destPort:80 protocol:6 aclId:2 ... action:ActionAccept`, with the TCP connection closing (`endTime`) at 13:36:11 UTC. The test's `phase1-traffic-generated` checkpoint fired at 13:34:22 UTC, so the 180s `Eventually` deadline hit at ~13:37:22 — only ~71s after the connection actually closed. Live `sysctl net.netfilter.nf_conntrack_tcp_timeout_time_wait` confirms it's set to 270s on the device, deliberately larger than the flow-log code's `conntrackFlowExtraTimeout` (150s), so the conntrack entry only becomes flow-log-eligible ~120s after close (~13:38:11), plus up to another randomized sweep interval — well past the test's remaining ~71s budget, so the record simply hadn't been collected/published yet when the test gave up.

This is a **known, recurring issue**, not a new bug: the identical `TestFlowLog` failure (same 180s timeout, same allowed-HTTP-ACE record, same `10.50.0.2 → 10.17.17.25:80` flow) is documented in `live-ledger.md` for run #31 (2026-09-09), run #32 (2026-09-09), and run #33 (2026-09-10, same build `551d4490`) — today's failure is the fourth consecutive reproduction with no fix landed. The root cause is a test-timeout-margin bug in `evetest/tests/networking/netinst_test.go` (line 2437) that doesn't account for the TIME_WAIT-eligibility delay baked into the device's sysctl tuning — it's a test-harness defect, not a pillar/EVE regression.


#### TestLocalNIWithMultiplePorts

##### Failure

```
timed out after 5m0.00110518s (excluding download) waiting for app "6252cd46-e8c6-4ccd-8b7a-27a05e399815" (multi-port-app) on device "edge-dev" to reach RUNNING state (last state: LOADING)
```

##### Claude's conclusion

###### Root cause

`multi-port-app`'s volume spent **4m41s stuck in `RESOLVING_TAG`** (14:11:48→14:16:27, per live `evetest eve logs`) with zero log activity from `zedbox`/`downloader` during that entire window — the resolve request against the OCI registry at `192.168.170.2:5000` simply hung. Once it finally unblocked, download (100% in ~5s) and image loading proceeded normally, but by then almost the whole 5-minute app-start budget was gone, so the test's `Eventually` expired while the app was still in `LOADING`, just before it reached `CREATING_VOLUME` at 14:16:59. Live probing confirms why: `curl http://192.168.170.2:5000/v2/` returns `200` in 6ms, while `curl https://192.168.170.2:5000/v2/` fails instantly with an SSL handshake error (exit 35) — the registry is plain-HTTP-only, but `go-containerregistry`'s ping logic tries HTTPS first for non-localhost hosts and the pillar download path (`context.Background()`, no per-request deadline) can leave that failed/hanging TLS attempt unresolved for minutes before falling back.

This is the exact same root-cause family already recorded in `live-ledger.md` for **run #31 (2026-09-09)**, `TestLocalNetInstanceACLsWithFlowLog` (`acl-app-1` against the same `192.168.170.2:5000` registry, stuck in `RESOLVING_TAG` indefinitely) — there it never resolved during the observation window, here it eventually resolved after ~4m41s, but it's the same HTTPS-probe-hang-against-an-HTTP-only-registry defect, not a new bug. It's a known/recurring issue since at least 2026-09-09, and does not overlap with this run's already-investigated `TestFlowLog` finding (conntrack/flow-log timing), which is unrelated.


#### TestSwitchNIPortConfigRace

##### Failure

```

Told to stop trying after 0.030s.
vlan-switch-ni: Network instance is in error state
networkID:"c2133a07-6e6f-4aed-bcbe-1925933158d6"  networkVersion:"1"  instType:1  displayname:"vlan-switch-ni"  activated:true  CurrentUplinkIntf:"vlan100"  ports:"vlan100"  bridgeNum:2  bridgeName:"vlan100"  ipAssignments:{macAddress:"02:16:3e:00:00:02"  ipAddress:"10.53.100.181"}  vifs:{vifName:"nbu2x1"  macAddress:"02:16:3e:00:00:02"  appID:"0705d853-2d52-48b1-a1ce-0d9dab5a5ba6"}  networkErr:{description:"failed items: BridgeFwdMask/vlan100 (failed to zero-out forwarding mask for bridge vlan100: open /sys/class/net/vlan100/bridge/group_fwd_mask: no such file or directory)"  timestamp:{seconds:1789053546  nanos:556760492}  severity:SEVERITY_ERROR}  state:ZNETINST_STATE_ERROR  mtu:1500
```

##### Claude's conclusion

Confirms the bridge and its `group_fwd_mask` (now `0`/healthy) are currently present — the absence at failure time was indeed transient. This is enough evidence.

###### Root cause

This is the same known, recurring `TestSwitchNIPortConfigRace` bug, now reproducing for at least the fifth time. Live logs show, at 15:19:06.545–.651 UTC, the reconciler tearing down the old `bn2` bridge and recreating it as `vlan100` (`VLANBridge/vlan100` create with `expectedBridgeID:0`) as part of this test's deliberate port-config churn; the `BridgeFwdMask/vlan100` item that ran in that same pass at .587 failed with `open /sys/class/net/vlan100/bridge/group_fwd_mask: no such file or directory`, immediately followed (.650–.651) by the reconciler detecting a bridge-identity mismatch and redoing the bridge with `expectedBridgeID:14` — i.e. the bridge was transiently absent from sysfs mid-recreation. Live inspection now confirms `vlan100`'s bridge and `group_fwd_mask` (value `0`) are present and healthy, confirming the absence was transient rather than a lasting device fault.

This is not a new bug: it's the identical `BridgeFwdMask/vlan100`/`group_fwd_mask` failure already documented in `live-ledger.md` for this exact test in run #31 (2026-09-09), run #32 (2026-09-09), and run #34 (2026-09-10, same build `551d4490`), and shares the same root-cause family as run #26's (2026-09-08) `TCMirror.Create`/`Parent Qdisc doesn't exists` variant — a lack of race-tolerance in the NI reconciler's `Create` paths for bridge-dependent items (`BridgeFwdMask`, `TCMirror`) when the underlying bridge is transiently torn down/recreated during port reconfiguration. So this has been a known, recurring, unfixed issue since at least 2026-09-08, and today's failure is a further reproduction of the same defect rather than a new one.

### TestNodeClusterSuite: failure analysis

#### TestClusterToSingleConversion

##### Failure

```
Device "edge-dev3" failed to confirm the latest config (version 8): timed out waiting for LastProcessedConfig >= 2026-09-10 12:29:22.915080761 +0000 UTC
```

##### Claude's conclusion

###### Root cause

`edge-dev3`'s reboot for the cluster→standalone conversion is the direct cause, but the real bug is in the test's timeout budget, not EVE. Live evidence: `edge-dev3`'s `ZInfoDevice` shows `lastRebootReason: "BootReasonKubeTransition, Transition from cluster mode to single node"` at 12:29:24 and `bootTime` 12:30:33 — i.e. the device correctly rebooted (as `conversion_test.go`'s own comments describe: kube-init sees the `EdgeNodeClusterConfig` disappear, marks `ConvertToSingleNode`, and must reboot before k3s state can be restored). Live device metrics now show `last_processed_config: {12:29:22.915080761}`, exactly matching the config timestamp the test was waiting for (`edgedevice.go:317`/`323`) — so the config *was* eventually confirmed, just not before the test gave up.

The test's `ApplyConfig(convertedCfg, true, true)` (`conversion_test.go:212`) uses the generic `waitUntilConfirmed` path, which is bound by the fixed `deviceApplyConfigTimeout = 2 * time.Minute` (`harness.go:97`, `edgedevice.go:280/300`). But this particular config change forces a reboot+k3s-restore cycle before zedagent can publish an updated `LastProcessedConfig` metric — the device only came back up at 12:30:33 (69s after fetching the config) and needed further boot-up time (zedbox init, ~758 goroutines, metrics not yet flushed) before the metric with the matching timestamp was actually published, landing only ~15-90s after the 12:31:23 deadline. The test author was clearly aware a reboot could take much longer (they later wait up to 20 minutes for `converted-node-standalone` at line 217-231), but didn't extend the immediate `ApplyConfig` confirm-timeout for this particular per-device withdrawal, so it's racing a fixed 2-minute budget against an inherently reboot-bound operation.

This is a distinct failure mode from the earlier `TestClusterToSingleConversion` entry in the ledger (2026-09-08, run #26: survivors never dropping `edge-dev3` from `kubectl get nodes` due to `drainAndDeleteNode`'s apiserver-unreachable race) — that was about node removal on the *survivors*, this is about the *converted node's* config-confirmation timing before drain/removal even matters. I found no prior ledger entry matching this "failed to confirm the latest config"/`LastProcessedConfig` signature, so this looks like a new, previously-unseen test-timeout-margin bug rather than a recurring one.

## [2026-09-10 -- 0.0.0-master-551d4490 (run #34)](https://github.com/milan-zededa/eve/actions/runs/34447233841)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/34/)

### TestApplicationConnectivitySuite: failure analysis

#### TestSwitchNIPortConfigRace

##### Failure

```

Told to stop trying after 0.025s.
vlan-switch-ni: Network instance is in error state
networkID:"65e91680-8cd8-4972-8532-55b5818ce801"  networkVersion:"1"  instType:1  displayname:"vlan-switch-ni"  activated:true  CurrentUplinkIntf:"vlan100"  ports:"vlan100"  bridgeNum:2  bridgeName:"vlan100"  ipAssignments:{macAddress:"02:16:3e:00:00:02"  ipAddress:"10.53.100.181"}  vifs:{vifName:"nbu2x1"  macAddress:"02:16:3e:00:00:02"  appID:"837cbe21-9970-47bb-96b4-e5093568be87"}  networkErr:{description:"failed items: BridgeFwdMask/vlan100 (failed to zero-out forwarding mask for bridge vlan100: open /sys/class/net/vlan100/bridge/group_fwd_mask: no such file or directory)"  timestamp:{seconds:1789036242  nanos:389414097}  severity:SEVERITY_ERROR}  state:ZNETINST_STATE_ERROR  mtu:1500
```

##### Claude's conclusion

###### Root cause

Live device logs (`evetest eve logs`) at 10:30:42.369–460 UTC show the identical sequence recorded in prior ledger entries: the NI reconciler recreates `vlan100`'s bridge for this test's deliberate port-config churn (`VLANBridge/vlan100` create with `expectedBridgeID:0`), and the `BridgeFwdMask/vlan100` item that runs against it at 10:30:42.440 fails with `open /sys/class/net/vlan100/bridge/group_fwd_mask: no such file or directory` — immediately followed (10:30:42.459–460) by the reconciler detecting a bridge-identity mismatch and redoing the create with `expectedBridgeID:14`, i.e. the bridge was transiently absent from sysfs mid-recreation. Live inspection now shows `vlan100`'s bridge and `group_fwd_mask` (value `0x0`) both present and healthy, confirming the absence was transient.

This is the same root-cause family already recorded in `live-ledger.md` for this exact test on 2026-09-08 (run #26, `TCMirror.Create`/`Parent Qdisc doesn't exists`) and 2026-09-09 (run #32, this identical `BridgeFwdMask.Create`/`group_fwd_mask` error, same device build family `551d4490`) — a lack of race-tolerance in the reconciler's `Create` paths for bridge-dependent items when the bridge is transiently torn down/recreated during port reconfiguration. So `TestSwitchNIPortConfigRace` has been a known, recurring issue since at least 2026-09-08, still unfixed as of this run's build, and today's failure reproduces the exact same `BridgeFwdMask/vlan100` symptom verbatim rather than being a new bug. No other suite failure has been investigated yet in this run to compare against.

### TestDeviceConnectivitySuite: failure analysis

#### TestMgmtTrafficRoutedViaApp

##### Failure

```

Timed out after 180.000s.
Expected to satisfy: app reports 2 VIFs with the reserved IPs
AppID:"a6dde0e9-0940-471c-b953-f6493499e93c"  appVersion:"1"  AppName:"mgmt-gw-app"  bootTime:{seconds:1789030219  nanos:474287193}  state:RUNNING  network:{macAddr:"02:16:3e:02:00:00"  devName:"vif0"  IPAddrs:"10.60.10.150"  IPAddrs:"fe80::16:3eff:fe02:0"  defaultRouters:"<nil>"  dns:{}  ipv4_up:true  localName:"nbu1x1"}  network:{macAddr:"02:16:3e:02:00:01"  devName:"vif1"  defaultRouters:"<nil>"  dns:{}  localName:"nbu2x1"}  volumeRefs:"ce6a124d-4623-40cf-ab57-d8ca059f0526"  cluster_app_running:true
```

##### Claude's conclusion

Confirmed the code still matches the previously-diagnosed bug (unfixed on master). I have enough evidence now.

###### Root cause

Live device logs show a startup race in NI state collection for `ni-lan` (NI `a26af772-b86e-46bc-af5e-8cf15a2e9689`, bridge `eth1`): at 08:50:04.285 `zedrouter` tried to open a pcap capture on the mirror interface `eth1-m` for ARP/DHCP snooping, but the `DummyInterface/eth1-m` wasn't actually created by the reconciler until 08:50:04.439 — 154ms later — so the open failed with `unknown interface eth1-m: route ip+net: no such network interface`. By contrast, `eth0-m` (used by `ni-wan`, whose VIF *did* get its IP reported) had its dummy interface created before the pcap attempt, so it succeeded. `pkg/pillar/nistate/linux.go` still has the bug that makes this permanent: `StartCollectingForNI`/`UpdateCollectingForNI` set `ni.cancelPCAP` before the `sniffDNSandDHCP` goroutine runs, and when `pcap.OpenLive` fails inside that goroutine it just logs and returns without clearing `cancelPCAP`, so the `ni.cancelPCAP == nil` retry guard never re-fires even after the VIF (`nbu2x1`) attaches later — confirmed live via `AddVif(eth1, nbu2x1, ...)` at 08:50:16 with no subsequent pcap retry log for `eth1-m` anywhere in the run. The bridge/DHCP path itself is healthy (SDN's `lan-network` has a static DHCP reservation `02:16:3e:02:00:01 → 10.60.20.150`, and the Linux bridge shows `nbu2x1` correctly forwarding on `eth1`), so this is purely an EVE-side observability/reporting gap, not a connectivity failure.

This is the identical root-cause defect already documented in `live-ledger.md` for `TestAirGapSwitchNI` in run #31 (2026-09-09) — same code path (`nistate/linux.go`'s permanent "give up after first failed pcap open" bug), same trigger (mirror dummy-interface creation racing the pcap-open attempt at NI startup). It's a known, recurring issue as of 2026-09-09, unfixed as of this build (`551d4490`), just now surfacing through a different test (`TestMgmtTrafficRoutedViaApp` expects the reported VIF IPs rather than the NI's `ipAssignments`).

### TestStorageSuite: failure analysis

#### TestZVolProvisionedSizeReported

##### Failure

```
Failed to receive SDN tunnel properties: rpc error: code = Unavailable desc = unable to connect to SDN gRPC service on any of the uplink IPs ([192.168.170.4]): failed to establish tunnel to SDN: rpc error: code = Unavailable desc = connection error: desc = "transport: Error while dialing: dial tcp 192.168.170.4:50121: connect: no route to host"
```

##### Claude's conclusion

No genuine matches — confirmed this is a first-time failure signature, not previously recorded in the ledger.

**Root cause:** This is a test-infrastructure failure, not an EVE/pillar code defect. `gotest.json` shows the broker provisioned a fresh SDN Proxmox VM (VM 108) at 07:19:04, powered it on, and got its uplink IP `192.168.170.4` at 07:19:06 — normal so far. But the subsequent `Failed to connect to SDN gRPC at 192.168.170.4:50121 (will retry): ... no route to host` warning then repeated every ~6s continuously for the entire ~4m46s retry budget (07:19:20 → 07:24:06) without a single success, at which point the test's `openTunnelToSDN` (setup.go:823) gave up with a `Fatalf`. For comparison, the immediately preceding test in this same run (`TestVolumeSizeAlignment`) hit the identical `no route to host` warning on its own freshly-booted SDN VM, but only for ~29s/3 retries before `Connected to SDN gRPC` succeeded at 07:15:03 — showing this warning is normally a brief, expected race during SDN VM boot, not a hard failure. Here, that boot-time race never resolved at all, indicating this particular SDN VM instance (or its network path from the broker on the Proxmox host) never actually came up correctly. Live `evetest sdn status`/`ssh`/`logs` all currently fail (`SDN client is not initialized`, SSH `connection reset by peer`), and `evetest eve ssh/logs` fail with "network model not applied"/"not onboarded" — all consistent with the SDN VM still being unreachable now, not merely slow. This doesn't share a root cause with any of the other failures already documented in the ledger, and there's no prior entry for this test or this SDN-gRPC/no-route-to-host signature, so it appears to be a new, one-off Proxmox/SDN-provisioning infra hiccup rather than a recurring or code-level issue.

## [2026-09-10 -- 0.0.0-master-551d4490 (run #33)](https://github.com/milan-zededa/eve/actions/runs/34414897742)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/33/)

### TestApplicationConnectivitySuite: failure analysis

#### TestFlowLog

##### Failure

```

Timed out after 180.000s.
The function passed to Eventually failed at /evetest/tests/networking/netinst_test.go:2437 with:
expected an outbound flow record for the allowed HTTP ACE (2) from 10.50.0.2 to 10.17.17.25:80
Expected
    <*flowlog.FlowRecord | 0x0>: nil
not to be nil
```

##### Claude's conclusion

###### Root cause

Live `evetest eve flow-logs flowlog-test-app` shows the exact expected record now exists: `src:"10.50.0.2" srcPort:39324 dest:"10.17.17.25" destPort:80 protocol:6 aclId:2 ... action:ActionAccept`, with the TCP connection closing (`endTime`) at 23:27:50 UTC. The test's `phase1-traffic-generated` checkpoint was reached at 23:26:01 UTC and the 180s `Eventually` deadline hit at 23:29:01 UTC — only ~71s after the connection actually closed. Live `sysctl net.netfilter.nf_conntrack_tcp_timeout_time_wait` confirms the device sets this to 270s, deliberately larger than the flow-log code's `conntrackFlowExtraTimeout` (150s), so the conntrack entry only becomes flow-log-eligible ~120s after close (≈23:29:50), plus up to another randomized sweep interval — comfortably past the test's 71s remaining budget, so the record simply hadn't been collected/published yet when the test gave up.

This is the identical root cause already documented twice in `live-ledger.md` for `TestFlowLog` earlier today (2026-09-09), in run #31 (build `68014620`) and run #32 (build `551d4490`): a test-timeout-margin bug in `evetest/tests/networking/netinst_test.go` that doesn't account for the TIME_WAIT-eligibility delay baked into the sysctl tuning, not a pillar/EVE defect. So this is a known, recurring issue as of today (2026-09-09), reproduced for a third consecutive time, with no fix landed between runs.

## [2026-09-09 -- 0.0.0-master-551d4490 (run #32)](https://github.com/milan-zededa/eve/actions/runs/34381743343)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/32/)

### TestApplicationConnectivitySuite: failure analysis

#### TestFlowLog

##### Failure

```

Timed out after 180.001s.
The function passed to Eventually failed at /evetest/tests/networking/netinst_test.go:2437 with:
expected an outbound flow record for the allowed HTTP ACE (2) from 10.50.0.2 to 10.17.17.25:80
Expected
    <*flowlog.FlowRecord | 0x0>: nil
not to be nil
```

##### Claude's conclusion

###### Root cause

Live `evetest eve flow-logs flowlog-test-app` shows the exact expected record now exists: `src:"10.50.0.2" srcPort:34618 dest:"10.17.17.25" destPort:80 protocol:6 aclId:2 ... action:ActionAccept`, with `startTime` 20:26:37 and `endTime` (connection close) 20:28:37 UTC. The test's 180s `Eventually` window ran from the "phase1-traffic-generated" checkpoint (20:26:48) to the timeout (20:29:48) — but the connection didn't close until 20:28:37, only ~71s before the deadline. Confirmed live: the device's `nf_conntrack_tcp_timeout_time_wait` is 270s, deliberately larger than the flow-log code's `conntrackFlowExtraTimeout` (150s), so a closed connection's conntrack entry only becomes eligible for flow-log collection ~120s after close, plus up to another ~120s until the next randomized flow-collection sweep — comfortably exceeding the remaining ~71s budget, so the record simply hadn't been collected/published yet when the test gave up (and indeed appears now, post-timeout).

This is the identical root cause already documented in `live-ledger.md` for `TestFlowLog` in run #31 earlier today (2026-09-09): a test-timeout-margin bug in `evetest/tests/networking/netinst_test.go` that doesn't account for the TIME_WAIT-eligibility delay baked into the sysctl tuning, not a pillar/EVE defect. So this is a known, recurring issue as of today, reproduced again on a later build (`0.0.0-master-551d4490` vs. run #31's `68014620`), with no code fix in between. It's unrelated to this run's other failure (`TestAirGapSwitchNI`, a pcap-retry bug in ARP/DHCP snooping).


#### TestSwitchNIPortConfigRace

##### Failure

```

Told to stop trying after 0.032s.
vlan-switch-ni: Network instance is in error state
networkID:"91e1d1e2-7eb7-4b50-8454-ff8296defec7"  networkVersion:"1"  instType:1  displayname:"vlan-switch-ni"  activated:true  CurrentUplinkIntf:"vlan100"  ports:"vlan100"  bridgeNum:1  bridgeName:"vlan100"  ipAssignments:{macAddress:"02:16:3e:00:00:02"  ipAddress:"10.53.100.181"}  vifs:{vifName:"nbu2x1"  macAddress:"02:16:3e:00:00:02"  appID:"2f2bc7da-f761-4a5a-9583-0be92ccf5631"}  networkErr:{description:"failed items: BridgeFwdMask/vlan100 (failed to zero-out forwarding mask for bridge vlan100: open /sys/class/net/vlan100/bridge/group_fwd_mask: no such file or directory)"  timestamp:{seconds:1788991264  nanos:441662193}  severity:SEVERITY_ERROR}  state:ZNETINST_STATE_ERROR  mtu:1500
```

##### Claude's conclusion

###### Root cause

Live device logs confirm this is the identical failure signature to earlier ledger entries. At 22:01:04.431 UTC the NI reconciler recreated the `vlan100` bridge as part of this test's deliberate port-config churn (`VLANBridge/vlan100` create, followed immediately by another delete/create cycle at .530–.531 that bumped `expectedBridgeID` from `0`→`14`), and the `BridgeFwdMask/vlan100` operation running in that same window hit `open /sys/class/net/vlan100/bridge/group_fwd_mask: no such file or directory` (surfaced to zedagent at 22:01:04.479) — i.e. the bridge was transiently absent from sysfs mid-recreation, exactly as in prior occurrences. Live inspection now (post-failure) shows `vlan100`'s bridge and its `group_fwd_mask` sysfs file both present and healthy, confirming the absence was transient.

This is the same root-cause family already recorded twice in `live-ledger.md` for this exact test: run #26 (2026-09-08, `TCMirror.Create` hitting `Parent Qdisc doesn't exists`) and run #31 (2026-09-09, `BridgeFwdMask.Create` hitting this identical `group_fwd_mask: no such file` error) — a general lack of race-tolerance in reconciler `Create` paths for bridge-dependent items when the bridge is transiently torn down/recreated during port reconfiguration. Today's failure reproduces run #31's exact symptom verbatim (same error string, same `BridgeFwdMask/vlan100` item, same device build family `551d4490` vs. `68014620`), so `TestSwitchNIPortConfigRace` is a known, recurring issue since at least 2026-09-08, still unfixed as of this build. It is unrelated to the other earlier-investigated failure in this run (`TestFlowLog`, a test-timeout-margin bug around conntrack/flow collection timing).

### TestStorageSuite: failure analysis

#### TestVolumes

##### Failure

```

Told to stop trying after 0.011s.
Stop waiting for: volume v-vhdx is created
Volume reports an error: Found error in content tree v-vhdx-image attached to volume v-vhdx: 
Size '0' provided in image config of 'v-vhdx.vhdx' is incorrect.
Download status (5242880 / 0). Aborting the download

uuid:"117674c7-4099-4bda-9af2-f203468cfb0f"  displayName:"v-vhdx"  usage:{createTime:{seconds:-62135596800}  refCount:1  lastRefcountChangeTime:{seconds:1788981946  nanos:579945337}}  state:DOWNLOAD_STARTED  volumeErr:{description:"Found error in content tree v-vhdx-image attached to volume v-vhdx: \nSize '0' provided in image config of 'v-vhdx.vhdx' is incorrect.\nDownload status (5242880 / 0). Aborting the download\n"  timestamp:{seconds:1788981948  nanos:121902620}  severity:SEVERITY_NOTICE  entities:{entity:ENTITY_CONTENT_TREE  entity_id:"cb40243a-62b1-4bae-8db2-bed9a829375e"}  retry_condition:"Will retry in 1m0s; have retried 0 times"}
```

##### Claude's conclusion

I have enough evidence for a root-cause writeup.

**Root cause:** Live device logs confirm the identical mechanism seen previously: the datastore HTTP server for `v-vhdx.vhdx` ignores the Range request (`server ignored Range; skipping copiedBytes manually`), so `downloader` sees `Update progress for v-vhdx.vhdx: 5242880/0` (nonzero `currentSize`, zero `totalSize`) and aborts with `Size '0' provided in image config ... incorrect` — exactly `pkg/pillar/cmd/downloader/download.go:325`'s unconditional `currentSize > totalSize` check, which I confirmed is still the code on master (no `totalSize > 0` guard present). This is the same root cause as the `TestVolumes` failure recorded in this same run's ledger entry for 2026-09-09 (run #31), which attributed it to a "stale test image" predating fix commit `ed743e546` — but that attribution was wrong (or the fix has since been reverted/never landed): none of the three "downloader: don't abort a download on a transient zero total size" commits I found in history (`b2c75e0df`, `4dce98f55`, `ed743e546`, dated 08-31/09-04/09-08) are ancestors of `master` or of today's device build `0.0.0-master-551d4490` — they all live only on unmerged branches (`dpc-fixes`, `evetest-ci`, `upgrade-test-change`). So this is a known, recurring issue as of at least 2026-09-09 (same-day ledger entry), and the actual state is that the fix has never been merged to master, not that the test image is merely stale — worth flagging to get that fix branch actually merged.

## [2026-09-09 -- 0.0.0-master-68014620 (run #31)](https://github.com/milan-zededa/eve/actions/runs/34323938056)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/31/)

### TestApplicationConnectivitySuite: failure analysis

#### TestAirGapSwitchNI

##### Failure

```

Timed out after 180.000s.
Expected to satisfy: switch-ni NI reports both static IP assignments
networkID:"29d99418-9eba-4d02-9bfd-6773b0cc1468"  networkVersion:"1"  instType:1  displayname:"switch-ni"  activated:true  bridgeNum:2  bridgeName:"bn2"  ipAssignments:{macAddress:"02:16:3e:00:02:01"}  ipAssignments:{macAddress:"02:16:3e:00:02:02"}  vifs:{vifName:"nbu2x1"  macAddress:"02:16:3e:00:02:02"  appID:"a2e96db2-80f5-4461-8346-20411d0f147a"}  vifs:{vifName:"nbu2x2"  macAddress:"02:16:3e:00:02:01"  appID:"1e66467d-89ce-43ab-84d3-7ae063f757b1"}  state:ZNETINST_STATE_ONLINE  mtu:1500
```

##### Claude's conclusion

No prior occurrences of `TestAirGapSwitchNI` in the ledger — this is a new finding, not a recurring known issue, and it shares no root cause with any other suite's failure in this run.

**Root cause:** Live device logs show that at 07:43:20.038 zedrouter's state collector tried to open a pcap capture on `bn2-m` (the mirror interface for the new `switch-ni` bridge) but failed immediately with `Cannot capture packets on bn2-m ...: unknown interface bn2-m: route ip+net: no such network interface` — because the NI reconciler didn't actually create the `bn2`/`bn2-m` bridge and its `TCMirror` rules until 07:43:36–39, ~16 seconds later. Code inspection of `pkg/pillar/nistate/linux.go` confirms the bug: `StartCollectingForNI` sets `ni.cancelPCAP` before launching the `sniffDNSandDHCP` goroutine, and when that goroutine's `pcap.OpenLive` fails it just logs and returns without clearing `ni.cancelPCAP`; `UpdateCollectingForNI`'s guard `ni.cancelPCAP == nil` therefore never re-triggers a retry even after VIFs are added and the mirror interface really exists. As a result no ARP/DHCP snooping ever ran for this NI's entire lifetime — confirmed live: `ip neigh`/`brctl` show `bn2-m` and the bridge are up and the two apps' pings succeeded (real L2 connectivity was fine), but `switch-ni`'s `ipAssignments` still only carry bare MAC addresses with no IP even now, and there is exactly one pcap-install log line for `bn2-m` in the whole run — the failed one. This looks like a start-order race introduced by the recent tc-mirror-based packet-sniffing rework (the same TCMirror/reconciler machinery flagged in an earlier ledger entry for a different test, `TestSwitchNIPortConfigRace`), but it manifests here as a distinct, previously-unseen bug: a permanent "give up after first failed pcap open" defect rather than that entry's `Create`/`Delete` error-tolerance asymmetry.


#### TestFlowLog

##### Failure

```

Timed out after 180.003s.
The function passed to Eventually failed at /evetest/tests/networking/netinst_test.go:2437 with:
expected an outbound flow record for the allowed HTTP ACE (2) from 10.50.0.2 to 10.17.17.25:80
Expected
    <*flowlog.FlowRecord | 0x0>: nil
not to be nil
```

##### Claude's conclusion

This is enough evidence for a solid root-cause writeup.

###### Root cause

The expected flow record does exist — `evetest eve flow-logs` shows it live: `src:"10.50.0.2" srcPort:58932 dest:"10.17.17.25" destPort:80 aclId:2 ... action:ActionAccept` with `StartTime` 08:00:52 and `StopTime` 08:02:51 — but it wasn't published in time for the test's 180s `Eventually` window (traffic generated ~08:00:52–08:01:02, timeout at 08:04:02). The device's `/etc/sysctl.d/02-eve.conf` sets `nf_conntrack_tcp_timeout_time_wait = 270`, deliberately larger than `nistate/linux_flow.go`'s `conntrackFlowExtraTimeout = 150` so a closed connection's conntrack entry only becomes eligible for flow-log collection once its remaining TTL drops below 150s — i.e. **120s after the TCP connection actually closes** — and is only then picked up by the next `flowCollectInterval` sweep (randomized 108–120s cadence). In the worst case (sweep tick just missed), that's up to ~240s after connection close before the record is collected/published, which exceeds the test's 3-minute (`flowLogTimeout`) margin; the test's own comment ("give it comfortable room for at least one full cycle plus margin") accounts only for the sweep cadence and not for this extra ~120s TIME_WAIT-eligibility delay baked into the sysctl tuning. This looks like a test-timeout-margin bug (`evetest/tests/networking/netinst_test.go`, added in `049e632cb`/`eb32c4367`) rather than a pillar/EVE defect. It does not share a root cause with this run's earlier `TestAirGapSwitchNI` failure (that was a pcap-open retry bug in ARP/DHCP snooping, unrelated to conntrack-based flow collection), and I found no prior `TestFlowLog` entries in `live-ledger.md`, so this doesn't appear to be a previously known/recurring issue.


#### TestLocalNetInstanceACLsWithFlowLog

##### Failure

```
timed out after 5m0.000260637s (excluding download) waiting for app "4c115736-19c0-49c5-af62-c3e7e467d263" (acl-app-1) on device "edge-dev" to reach RUNNING state (last state: RESOLVING_TAG)
```

##### Claude's conclusion

Still stuck in `RESOLVING_TAG`, confirming a genuine hang rather than a slow retry cycle. I have enough evidence for a root-cause writeup.

###### Root cause

`acl-app-1`'s volume never leaves `RESOLVING_TAG`: at 08:13:59 zedbox's `manifestsDescImg` began resolving `192.168.170.2:5000/lfedge/evetest-ubuntu-ctr:1.0` (via `go-containerregistry`'s `remote.Get`), and no further log line, error, or `ResolveStatus` for this object has appeared since — 10+ minutes later the device still reports `RESOLVING_TAG`. Live inspection of `/proc/net/tcp` on the device shows two TCP connections to `192.168.170.2:5000` stuck in `ESTABLISHED` with non-zero unflushed `tx_queue`, i.e. the resolve requests are hung mid-flight, not erroring and retrying. Because that registry (192.168.170.2:5000) is only registered as `insecure:false`/non-localhost, `go-containerregistry`'s ping logic (`transport/ping.go`) tries HTTPS only (no HTTP fallback, since the "localhost" heuristic doesn't match a bare IP) — and a manual `curl -v https://192.168.170.2:5000/v2/` from the device gets an immediate TLS-layer rejection (`wrong version number`, since the registry actually only speaks plain HTTP, confirmed via a working plaintext `curl http://.../v2/...`). The one-shot curl fails fast, but the actual pillar code path (`zedUpload`'s OCI datastore → `objectMetadata` in `pkg/pillar/cmd/downloader/download.go`) issues the request with `context.Background()` (no request/response deadline) and only TCP-connect/TLS-handshake timeouts on the transport, so once the connection reaches `ESTABLISHED` without a clean/prompt error this time, there is nothing to time it out — it can hang indefinitely (`retryTime` for a fresh resolve attempt is 600s and hasn't even been reached).

This is a distinct issue from both other failures already investigated in this run: `TestAirGapSwitchNI` (pcap-retry bug in ARP/DHCP snooping) and `TestFlowLog` (conntrack/flow-log collection timing), and it also doesn't match anything in `live-ledger.md` (which has no prior `RESOLVING_TAG`/image-resolve-hang entries) — this looks like a newly observed failure mode, either an HTTPS/HTTP registry-scheme mismatch or a registry-under-concurrency stall, exposed by the lack of a request timeout in the OCI tag-resolution path.


#### TestSwitchNIPortConfigRace

##### Failure

```

Told to stop trying after 0.020s.
vlan-switch-ni: Network instance is in error state
networkID:"c8b5dde3-26e4-4dac-b51b-19591640cba0"  networkVersion:"1"  instType:1  displayname:"vlan-switch-ni"  activated:true  CurrentUplinkIntf:"vlan100"  ports:"vlan100"  bridgeNum:2  bridgeName:"vlan100"  ipAssignments:{macAddress:"02:16:3e:00:00:02"  ipAddress:"10.53.100.181"}  vifs:{vifName:"nbu2x1"  macAddress:"02:16:3e:00:00:02"  appID:"c6863c6e-e40b-4a71-85d5-46c420797d5f"}  networkErr:{description:"failed items: BridgeFwdMask/vlan100 (failed to zero-out forwarding mask for bridge vlan100: open /sys/class/net/vlan100/bridge/group_fwd_mask: no such file or directory)"  timestamp:{seconds:1788948048  nanos:754981883}  severity:SEVERITY_ERROR}  state:ZNETINST_STATE_ERROR  mtu:1500
```

##### Claude's conclusion

###### Root cause

Live device logs (`evetest eve logs`) at 10:00:48.599–48.906 show `zedrouter`'s NI reconciler tearing down and recreating the switch NI's bridge during this test's deliberate port-config churn: it deletes the old `bn2`-named bridge's `TCMirror`/`BridgePort`/`TCIngress`/`BridgeFwdMask` items, then recreates the bridge (now itself named `vlan100`, since the vlan adapter is the sole/externally-bridged port), attaches `kvlan100`, and creates its `TCMirror` rules successfully — but the very next item, `BridgeFwdMask/vlan100` (10:00:48.818), fails with `open /sys/class/net/vlan100/bridge/group_fwd_mask: no such file or directory`, immediately followed by the reconciler noticing a bridge-identity mismatch (`expectedBridgeID:0` vs the new `14`) at 48.906 and redoing the `BridgePort` modifies — i.e. the bridge was transiently absent from sysfs mid-recreation when `BridgeFwdMask.Create` ran against it.

This is the same underlying defect class already recorded in the ledger for this exact test on 2026-09-08 (run #26): the reconciler's `Create` paths for bridge-dependent items lack tolerance for the bridge being transiently gone/recreated during the port-config race this test induces, whereas some `Delete` paths were already hardened against it (there it was `TCMirror.Create` hitting `Parent Qdisc doesn't exists`; here it's `BridgeFwdMask.Create` hitting a missing sysfs bridge attribute). So `TestSwitchNIPortConfigRace` is a known, recurring issue since at least 2026-09-08, and today's failure is the same root-cause family — a different symptom of the identical "no race-tolerance in Create for a mid-recreation bridge" gap — rather than a new, unrelated bug. It does not obviously relate to the other failures already investigated in this run (`TestAirGapSwitchNI`, `TestFlowLog`, `TestLocalNetInstanceACLsWithFlowLog`), which are distinct pcap-retry/flow-timing/registry-hang issues, though the earlier `TestAirGapSwitchNI` writeup itself already flagged this same TCMirror/reconciler mechanism as the source of that ledger entry.

### TestLPSSuite: failure analysis

#### TestNetworkLocalChanges

##### Failure

```

Timed out after 153.691s.
The function passed to Eventually failed at /evetest/tests/lps/helpers_test.go:134 with:
Unexpected error:
    <*fmt.wrapError | 0xc002c9a720>: 
    failed to execute script over SSH for app 43b27146-d561-4ea3-8904-cb42b39d7c78: SSH dial to 172.20.20.123:2222 failed: ssh: handshake failed: read tcp 250.250.250.1:55012->172.20.20.123:2222: read: connection timed out (stderr: )
    {
        msg: "failed to execute script over SSH for app 43b27146-d561-4ea3-8904-cb42b39d7c78: SSH dial to 172.20.20.123:2222 failed: ssh: handshake failed: read tcp 250.250.250.1:55012->172.20.20.123:2222: read: connection timed out (stderr: )",
        err: <*fmt.wrapError | 0xc002c9a700>{
            msg: "SSH dial to 172.20.20.123:2222 failed: ssh: handshake failed: read tcp 250.250.250.1:55012->172.20.20.123:2222: read: connection timed out",
            err: <*fmt.wrapError | 0xc002c9a6e0>{
                msg: "ssh: handshake failed: read tcp 250.250.250.1:55012->172.20.20.123:2222: read: connection timed out",
                err: <*net.OpError | 0xc0005cb180>{
                    Op: "read",
                    Net: "tcp",
                    Source: <*net.TCPAddr | 0xc001105950>{
                        IP: [250, 250, 250, 1],
                        Port: 55012,
                        Zone: "",
                    },
                    Addr: <*net.TCPAddr | 0xc001105980>{
                        IP: [172, 20, 20, 123],
                        Port: 2222,
                        Zone: "",
                    },
                    Err: <*os.SyscallError | 0xc002c9a660>{
                        Syscall: "read",
                        Err: <syscall.Errno>0x6e,
                    },
                },
            },
        },
    }
occurred
```

##### Claude's conclusion

###### Root cause

The failure lines up exactly with the test's own network-revert step: at 08:38:30 the test submitted the empty LPS config to revert eth0's local override, which at 08:38:31.99 triggered NIM to re-verify a new DevicePortConfig, causing zedrouter to briefly tear down and rebuild the local-ni port-forward — the DNAT rule for `2222→10.11.12.128:22` was deleted at 08:38:32.238 and only recreated at 08:38:32.970 (device logs, `ni-info`), with eth0 doing a DHCP release/re-lease in between. This ~0.7s gap is exactly when the harness's SSH dial to the app (source `250.250.250.1`) landed and apparently got stuck (a "read" timeout during the SSH banner handshake, not a refused/reset connection), and since that single stuck attempt has no short per-handshake deadline, it burned well past the 5s poll interval and the rest of the 2-minute `Eventually` budget, producing the reported 153.7s overrun. Live investigation confirms the device fully recovered within ~2s (`DPC verify: ... DPC_SUCCESS` at 08:38:33.958, DNAT rules present, `ni-info`/`ssh` all healthy now), the app (`lps-app`) never rebooted (single boot at 08:37:35, no restart), and there were no further NI/DPC events between 08:38:34 and 08:41:05 — i.e., this is a transient race triggered by the test's own local-config-revert step, not a lasting device defect. Checking `live-ledger.md` (this run's only ledger data, from 2026-09-08), there is no prior entry for `TestLPSSuite`/`TestNetworkLocalChanges` or this SSH-timeout signature, so this does not appear to be a previously known/recurring issue — it looks like a first-time occurrence of this particular race in the local-NI port-forward reconfiguration path.

### TestStorageSuite: failure analysis

#### TestVolumes

##### Failure

```

Told to stop trying after 0.010s.
Stop waiting for: volume v-vhdx is created
Volume reports an error: Found error in content tree v-vhdx-image attached to volume v-vhdx: 
Size '0' provided in image config of 'v-vhdx.vhdx' is incorrect.
Download status (6619136 / 0). Aborting the download

uuid:"c0454e34-3b41-4c49-914f-7da670194310" displayName:"v-vhdx" usage:{createTime:{seconds:-62135596800} refCount:1 lastRefcountChangeTime:{seconds:1788943309 nanos:15660506}} state:DOWNLOAD_STARTED volumeErr:{description:"Found error in content tree v-vhdx-image attached to volume v-vhdx: \nSize '0' provided in image config of 'v-vhdx.vhdx' is incorrect.\nDownload status (6619136 / 0). Aborting the download\n" timestamp:{seconds:1788943310 nanos:350481412} severity:SEVERITY_NOTICE entities:{entity:ENTITY_CONTENT_TREE entity_id:"eb9f9534-9c80-4cab-a7f2-05510b200f5e"} retry_condition:"Will retry in 1m0s; have retried 0 times"}
```

##### Claude's conclusion

Confirmed: device runs `0.0.0-master-68014620-kvm-amd64`, an ancestor of the fix commit `ed743e546` (merged 2026-08-31, after `68014620`). This is sufficient evidence.

###### Root cause

Live logs show `downloader` aborting the `v-vhdx.vhdx` download on its very first progress tick: `Update progress for v-vhdx.vhdx: 6619136/0` at 08:41:50.039, immediately followed by `Size '0' provided in image config ... incorrect` — the datastore HTTP server ignored the Range request (`server ignored Range; skipping copiedBytes manually`), so `totalSize` was reported as 0 while `currentSize` had already advanced, and `pkg/pillar/cmd/downloader/download.go`'s unconditional `currentSize > totalSize` check treated that as fatal. This is exactly the bug fixed by commit `ed743e546` ("downloader: don't abort a download on a transient zero total size", 2026-08-31), which guards the check with `totalSize > 0`. The device under test is running image `0.0.0-master-68014620-kvm-amd64`, and `68014620` is a git ancestor of `ed743e546` — i.e. the deployed image predates the fix, so this is a stale test-image issue, not a code regression. It shares no root cause with other entries in the ledger, and there is no prior TestStorageSuite/TestVolumes/vhdx entry in `live-ledger.md`, so this is a first-time-observed occurrence in the ledger (though the underlying bug itself is already fixed in current master).

## [2026-09-08 -- 0.0.0-master-0408b69f (run #26)](https://github.com/milan-zededa/eve/actions/runs/34208177401)

[Full report](https://milan-zededa.github.io/eve/test/master/runs/26/)

### TestApplicationConnectivitySuite: failure analysis

#### TestSwitchNIPortConfigRace

##### Failure

```

Told to stop trying after 0.020s.
vlan-switch-ni: Network instance is in error state
networkID:"aad9de64-d7dd-4dcc-b5d3-eed7db929a96"  networkVersion:"1"  instType:1  displayname:"vlan-switch-ni"  activated:true  CurrentUplinkIntf:"vlan100"  ports:"vlan100"  bridgeNum:2  bridgeName:"bn2"  ipAssignments:{macAddress:"02:16:3e:00:00:02"  ipAddress:"10.53.100.181"}  vifs:{vifName:"nbu2x1"  macAddress:"02:16:3e:00:00:02"  appID:"2e66850a-46b9-47eb-82db-36dc0c122f38"}  networkErr:{description:"failed items: TCMirror/tc-mirror/vlan100/2 (failed to delete tc-rule mirroring traffic from vlan100 to bn2-m: Error: Parent Qdisc doesn't exists.\nWe have an error talking to the kernel\n (exit status 2));TCMirror/tc-mirror/vlan100/1 (failed to add tc-rule mirroring traffic from vlan100 to bn2-m: Error: Parent Qdisc doesn't exists.\nWe have an error talking to the kernel\n (exit status 2))"  timestamp:{seconds:1788869114  nanos:554470855}  severity:SEVERITY_ERROR}  state:ZNETINST_STATE_ERROR  mtu:1500
```

##### Claude's conclusion

Now I have enough evidence to write the root cause summary.

###### Root cause

Live device logs (`evetest eve logs --tail=3000`) show the exact chain: at 12:05:13.744 attaching `vlan100` to bridge `bn2` failed with `too many levels of symbolic links` — a transient netlink race caused by `vlan100` being torn down/recreated concurrently by the port-config change this test deliberately induces. The reconciler nonetheless logged `TCIngress/vlan100` and its `TCMirror` rules as successfully created in that same pass (12:05:13.765). On the immediate retry (12:05:14.498–.516) the reconciler tried to delete the old `TCMirror/vlan100/2` rule and create the new `TCMirror/vlan100/1` rule, and both failed with `Parent Qdisc doesn't exists` because the ingress qdisc from the aborted attach never actually existed/was gone — driving the NI into `ZNETINST_STATE_ERROR`. Code inspection confirms the bug: `TCMirrorConfigurator.Delete` (`pkg/pillar/nireconciler/linuxitems/tcmirror.go:287-303`) explicitly tolerates `Parent Qdisc doesn't exists` and swallows the error, but `Create` (same file, lines 241-248) has no equivalent tolerance, so a benign race during port reconfiguration turns into a persistent NI error instead of a harmless retry.

This is not an old recurring flake — `TestSwitchNIPortConfigRace` is a brand-new regression test (commit `049e632cb`, added 2026-09-03) written specifically to exercise NIM/zedrouter port-config races, and the tc-mirror/tc-ingress mechanism itself is also recent (`cdc6a49ba`, "Implement packet sniffing with less overhead using tc-mirred"; `8f277c37b`, "Track NIM interface/bridge identity via IfInstanceID"). The latter commit added race-tolerance to the `Delete` paths of `TCIngress`/`TCMirror` but apparently missed doing the same for `TCMirror.Create`, which is exactly the gap this new test just caught. I found no ledger/known-issues file in the repo or in memory to confirm prior recurrences — this looks like the first time this specific race has been exposed.

### TestAppsSuite: failure analysis

#### TestHaltAfterImmediateDeactivate

##### Failure

```

application was not reported HALTED within 5m0s of being deactivated; a stop which does not escalate costs the whole 600s budget
Expected
<bool>: false
to be true
```

##### Claude's conclusion

No ledger file found in the repo, so I can't check a formal known-issues record — but the fix history itself makes the story clear.

###### Root cause

Live evidence: `evetest eve app-info` shows `halted-app` stuck in `HALTING` since 12:43:18, the QEMU process (pid 19838) is still alive and QMP `query-status` reports `"running": true`, and domainmgr's logs show `waitForDomainGone` polling with exponential backoff for the *entire* run without ever giving up at ~60s and escalating to a forced `DomainShutdown -F` — it kept waiting past 5+ minutes (heading toward the full 600s budget).

This is exactly the bug fixed by commit `b66b0040c` ("domainmgr: bound the graceful stop wait for default-mode guests", merged into master ~2026-09-07/08): an app with `VirtualizationMode` left unset defaults to `PV` (the enum's zero value), and prior to that fix, `shutdownBudget()` granted such guests the entire 10-minute budget for a graceful ACPI stop instead of the intended 60s (`gracefulShutdownWait`) before escalating — which is precisely what `TestHaltAfterImmediateDeactivate` (added in `33aff4a3d`, the very next commit) was written to catch. The device under test is running image `0.0.0-master-0408b69f`, a commit that is **not** an ancestor of `b66b0040c` in this checkout's history (HEAD is 58 commits past that fix) — i.e., the device's build predates the fix. So this isn't a regression in current code but a stale/outdated test image being exercised; rebuilding/redeploying EVE from current master should resolve it.

#### TestHaltUnresponsiveGuest

##### Failure

```

application was not reported HALTED within 5m0s of being deactivated; a stop which does not escalate costs the whole 600s budget
Expected
<bool>: false
to be true
```

##### Claude's conclusion

Same build (`0408b69f`) noted as stale in the earlier `TestHaltAfterImmediateDeactivate` investigation left in this artifact dir. I now have enough evidence for the root-cause summary.

###### Root cause

`unresponsive-app` (`bbb0cbc7…`) has been stuck in `HALTING` since 12:58:36. domainmgr's logs show it correctly tried the graceful ACPI stop, gave up after ~63s, and escalated to the forced stop, logging `DomainShutdown force-true ... Shutdown(force) succeeded` at 12:59:39 — but that "success" was false: the qemu process (pid 5702, unchanged since boot at 12:56:58) is still alive right now, healthy and responsive (`S` sleeping on `poll()`, 9 threads, not stuck in D-state), and its QMP socket still answers `query-status` with `"running": true`. `waitForDomainGone` has since polled that same live state for 6+ minutes, which is exactly what burns the full 600s force-stop budget the test's error message describes. This points to a bug in the forced-stop path (`terminateQemu`/`waitProcessGone` in `pkg/pillar/hypervisor/kvm.go`) reporting the QMP `quit`+SIGKILL sequence as successful without the qemu process actually terminating — worth checking whether `waitProcessGone`'s liveness probe or the pidfile read in `terminateQemu` has a false-positive path, since no "still alive after quit; sending SIGKILL" warning was ever logged despite the process surviving.

I found no formal known-issues ledger in the repo, so I can't confirm this as tracked, but this is a distinct bug from the one flagged in this same suite run: `TestHaltAfterImmediateDeactivate`'s leftover investigation notes (same artifact directory) diagnosed a different, already-fixed shutdown-budget bug (`b66b0040c`) on the same device build (`0.0.0-master-0408b69f`). That fix wouldn't explain this failure — here the escalation *did* fire on schedule, it just didn't actually kill the guest — so this looks like a separate, still-open defect in the forced-stop escalation itself.

### TestControllerFaultsSuite: failure analysis

#### TestDeferredQueueBacklogReported

##### Failure

```

device does not report the deferred queue at all
Expected
<*metrics.DeferredQueueMetric | 0x0>: nil
not to be nil
```

##### Claude's conclusion

I now have solid, corroborating live and repo evidence for the root cause.

**Root cause:** The device's running zedagent binary genuinely lacks the `DeferredQueue` metrics feature — `strings` on the live `zedbox` binary (`/containers/services/pillar/rootfs/opt/zededa/bin/zedbox`) shows the `controllerconn.DeferredQueue` queue-management code is present, but none of the reporting field names (`UndeliveredMsgCount`, `OldestUndeliveredMsg`, `DeferredQueueMetric`) appear anywhere, and the live `evetest eve metrics` output confirms no `deferredQueue:{...}` block is ever sent. That reporting was added in `pkg/pillar/cmd/zedagent/handlemetrics.go` by commit `3f4d338e7` ("Report what the controller is not being told", 2026‑09‑08 08:38:20), but the device reports version `0.0.0-master-0408b69f` — a commit hash that doesn't exist anywhere in this repo's history, i.e. the EVE image under test was built from a different/older checkout that predates that pillar change. This is not a test bug or a regression in the reconciler/agent logic; it's an image/pipeline staleness issue — the test is correctly detecting that the deployed EVE image doesn't yet contain the feature it's meant to verify. I found no ledger file in this repo to confirm whether this specific staleness pattern is a recurring known issue, so I can't speak to prior occurrences.

#### TestDeferredQueueDropsReported

##### Failure

```

device does not report the deferred queue at all
Expected
<*metrics.DeferredQueueMetric | 0x0>: nil
not to be nil
```

##### Claude's conclusion

No `deferredQueue` block anywhere in the metrics output, confirming the same root cause as the sibling test.

**Root cause:** The deployed EVE device (version `0.0.0-master-0408b69f`) predates commit `3f4d338e7` ("Report what the controller is not being told", merged 2026-09-08 08:38:20), which is the change that adds `DeferredQueueMetric` reporting in `pkg/pillar/cmd/zedagent/handlemetrics.go`. Live `evetest eve metrics` output confirms zedagent reports `zedcloud`, `newlog`, `flowlog`, etc., but never emits a `deferredQueue:{...}` block, so the test's expectation correctly reflects the deployed image's real behavior. This is the identical failure mode just diagnosed minutes earlier for the sibling test `TestDeferredQueueBacklogReported` in this same suite run (see `live-investigation.md` in this artifact dir) — an image/pipeline staleness issue (test image built from a commit that doesn't include the feature it's meant to verify), not a code regression or reconciler/agent bug.

### TestDeviceConnectivitySuite: failure analysis

#### TestLACPBond

##### Failure

```

Timed out after 300.001s.
Expected to satisfy: LACP bond has IP, no errors and reports LACP status
machineArch:"x86_64"  cpuArch:"x86_64"  platform:"x86_64"  ncpu:4  memory:7796  storage:40006  powerCycleCounter:-1  minfo:{manufacturer:"QEMU"  productName:"Standard PC (Q35 + ICH9, 2009)"  version:"pc-q35-10.1"  serialNumber:"HA9GXJDX"  UUID:"Not Settable"  biosVendor:"EDK II"  biosVersion:"unknown"  biosReleaseDate:"02/02/2022"}  assignableAdapters:{type:PhyIoNetEth  name:"ethernet0"  members:"ethernet0"  usedByBaseOS:true  ioAddressList:{macAddress:"da:39:78:d0:19:f2"}  usage:PhyIoUsageMgmtAndApps}  assignableAdapters:{type:PhyIoNetEth  name:"ethernet1"  members:"ethernet1"  usedByBaseOS:true  ioAddressList:{macAddress:"1e:68:56:62:e1:30"}  usage:PhyIoUsageMgmtAndApps}  assignableAdapters:{type:PhyIoNetEth  name:"ethernet2"  members:"ethernet2"  usedByBaseOS:true  ioAddressList:{macAddress:"ce:30:0f:9c:43:d9"}  usage:PhyIoUsageMgmtAndApps}  dns:{DNSservers:"127.0.0.1:53"  DNSsearch:"test."}  storageList:{device:"nbd0"}  storageList:{mountPath:"/persist/agentdebug"}  storageList:{device:"nbd1"}  storageList:{device:"vda3"  total:10240  partitionLabel:"IMGB"  partitionTypeGuid:"5dfbf5f4-2848-4bac-aa5e-0d9a20b745a6"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30053"}  storageList:{mountPath:"/persist/checkpoint"}  storageList:{device:"vda1"  total:2048  partitionLabel:"EFI System"  partitionTypeGuid:"c12a7328-f81f-11d2-ba4b-00a0c93ec93b"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30051"}  storageList:{mountPath:"/persist/log"}  storageList:{mountPath:"/persist/patchEnvelopesUsageCache"}  storageList:{device:"nbd7"}  storageList:{mountPath:"/persist/clear/volumes"}  storageList:{mountPath:"/persist/netdump"}  storageList:{device:"nbd11"}  storageList:{device:"nbd14"}  storageList:{device:"nbd3"}  storageList:{device:"nbd12"}  storageList:{mountPath:"/hostfs"  total:274}  storageList:{mountPath:"/persist/patchEnvelopesCache"}  storageList:{device:"nbd6"}  storageList:{mountPath:"/persist/status"}  storageList:{mountPath:"/persist/clear"}  storageList:{device:"nbd2"}  storageList:{device:"vda"  total:65536}  storageList:{device:"/persist/vector/data/buffer/v2/dev_upload_socket/buffer-data-0.dat"}  storageList:{mountPath:"/"  total:3898}  storageList:{mountPath:"/persist/memory-monitor/output"}  storageList:{device:"nbd4"}  storageList:{device:"vda4"  total:5  partitionLabel:"CONFIG"  partitionTypeGuid:"13307e62-cd9c-4920-8f9b-91b45828b798"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30054"}  storageList:{mountPath:"/persist/vault/volumes"}  storageList:{mountPath:"/persist/vault/containerd"}  storageList:{mountPath:"/persist/ingested"}  storageList:{mountPath:"/persist/containerd-system-root"}  storageList:{device:"nbd9"}  storageList:{device:"vda7"  total:2048  partitionLabel:"EFI System"  partitionTypeGuid:"c12a7328-f81f-11d2-ba4b-00a0c93ec93b"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30056"}  storageList:{device:"vda9"  total:40953  partitionLabel:"P3"  partitionTypeGuid:"5f24425a-2dfa-11e8-a270-7b663faccc2c"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30059"}  storageList:{mountPath:"/persist/vault/downloader"}  storageList:{device:"/persist/vector/data/buffer/v2/keep_sent_queue_socket/buffer-data-0.dat"}  storageList:{mountPath:"/persist/vault/verifier"}  storageList:{mountPath:"/persist/nettrace"}  storageList:{device:"nbd13"}  storageList:{mountPath:"/persist/tmp"}  storageList:{mountPath:"/persist/pubsub-large"}  storageList:{device:"nbd15"}  storageList:{device:"nbd10"}  storageList:{device:"vda2"  total:10240  partitionLabel:"IMGA"  partitionTypeGuid:"5dfbf5f4-2848-4bac-aa5e-0d9a20b745a6"  partitionUuid:"ad6871ee-31f9-4cf3-9e09-6f7a25c30052"}  storageList:{mountPath:"/config"  total:5}  storageList:{mountPath:"/persist/vault"}  storageList:{device:"nbd5"}  storageList:{mountPath:"/persist"  total:40006  storageLocation:true}  storageList:{mountPath:"/persist/newlog"}  storageList:{device:"nbd8"}  storageList:{mountPath:"/persist/certs"}  bootTime:{seconds:1788877532}  swList:{activated:true  partitionLabel:"IMGA"  partitionDevice:"/dev/vda2"  partitionState:"active"  status:INSTALLED  shortVersion:"0.0.0-master-0408b69f-kvm-amd64"  downloadProgress:100  userStatus:UPDATED}  swList:{partitionLabel:"IMGB"  partitionDevice:"/dev/vda3"  partitionState:"unused"  status:INITIAL}  HostName:"20fc5019-830a-49ab-826b-3e2ef3f45df3"  lastRebootReason:"NORMAL: First boot of device - at 2026-09-08T14:26:06.384114246Z"  lastRebootTime:{seconds:1788877566  nanos:384114246}  systemAdapter:{status:{version:1  key:"zedagent"  timePriority:{seconds:1788877614  nanos:755815376}  lastSucceeded:{seconds:1788877628  nanos:429909683}  ports:{ifname:"eth0"  name:"ethernet0"  free:true  proxy:{}  macAddr:"da:39:78:d0:19:f2"  dns:{}  up:true  err:{timestamp:{seconds:1788877628  nanos:429827204}}  usage:PhyIoUsageMgmtAndApps  networkUUID:"00000000-0000-0000-0000-000000000000"  mtu:1500  config_source:{origin:NETWORK_CONFIG_ORIGIN_CONTROLLER  submitted_at:{seconds:1788877614  nanos:755815376}}  pnac_status:{}}  ports:{ifname:"eth1"  name:"ethernet1"  free:true  proxy:{}  macAddr:"da:39:78:d0:19:f2"  dns:{}  up:true  err:{timestamp:{seconds:1788877628  nanos:429829986}}  usage:PhyIoUsageMgmtAndApps  networkUUID:"00000000-0000-0000-0000-000000000000"  mtu:1500  config_source:{origin:NETWORK_CONFIG_ORIGIN_CONTROLLER  submitted_at:{seconds:1788877614  nanos:755815376}}  pnac_status:{}}  ports:{ifname:"bond1"  name:"lacp-bond"  isMgmt:true  free:true  dhcpType:4  subnet:"172.20.20.0/24"  domainname:"test."  proxy:{}  macAddr:"da:39:78:d0:19:f2"  IPAddrs:"172.20.20.123"  IPAddrs:"fe80::f59d:937f:7084:84d9"  defaultRouters:"172.20.20.1"  dns:{DNSservers:"10.16.16.25"  DNSdomain:"test."}  up:true  err:{description:"interface bond1: no suitable IP address available"  timestamp:{seconds:1788877625  nanos:413544949}  severity:SEVERITY_ERROR}  networkUUID:"80fcb500-7b43-45e9-b578-70d9864d2884"  mtu:1500  config_source:{origin:NETWORK_CONFIG_ORIGIN_CONTROLLER  submitted_at:{seconds:1788877614  nanos:755815376}}  pnac_status:{}  bond_status:{mode:BOND_MODE_802_3AD  mii_monitor:{enabled:true  polling_interval:100}  arp_monitor:{}  lacp:{lacp_rate:LACP_RATE_FAST  active_aggregator_id:1  partner_mac:"ca:fe:fe:80:68:22"  actor_key:9  partner_key:9}  members:{logicallabel:"ethernet0"  mii_up:true  lacp:{aggregator_id:1  actor_churn_state:BOND_LACP_CHURN_STATE_MONITORING  partner_churn_state:BOND_LACP_CHURN_STATE_MONITORING}}  members:{logicallabel:"ethernet1"  mii_up:true  lacp:{aggregator_id:1  actor_churn_state:BOND_LACP_CHURN_STATE_MONITORING  partner_churn_state:BOND_LACP_CHURN_STATE_MONITORING}}}}  ports:{ifname:"eth2"  name:"ethernet2"  isMgmt:true  free:true  dhcpType:4  subnet:"172.20.21.0/24"  domainname:"test."  proxy:{}  macAddr:"ce:30:0f:9c:43:d9"  IPAddrs:"172.20.21.146"  IPAddrs:"fe80::e345:f0ce:989d:3171"  defaultRouters:"172.20.21.1"  dns:{DNSservers:"10.16.17.25"  DNSdomain:"test."}  up:true  err:{timestamp:{seconds:1788877628  nanos:429818405}}  usage:PhyIoUsageMgmtAndApps  networkUUID:"83859680-b3e6-433b-88f8-0b6f7cdb2154"  mtu:1500  config_source:{origin:NETWORK_CONFIG_ORIGIN_CONTROLLER  submitted_at:{seconds:1788877614  nanos:755815376}}  pnac_status:{}}}}  HSMStatus:NOTFOUND  dataSecAtRestInfo:{status:DATASEC_AT_REST_DISABLED  info:"TPM is either absent or not in use"  vaultList:{name:"Application Data Store"  status:DATASEC_AT_REST_DISABLED  vaultErr:{description:"TPM is either absent or not in use"  timestamp:{seconds:1788877603  nanos:200464800}  severity:SEVERITY_ERROR}  pcrStatus:PCR_DISABLED}}  sec_info:{sha_root_ca:"nY\xf8*b\x03\xd0G?\x84A\xb4\x98\x80\xa5n\x85\xe7\x18\x04\x17\xc2x'vg\x87z٥\x93y"}  configItemStatus:{configItems:{key:"app.allow.vnc"  value:{value:"true"}}  configItems:{key:"debug.default.loglevel"  value:{value:"debug"}}  configItems:{key:"debug.default.remote.loglevel"  value:{value:"debug"}}  configItems:{key:"debug.enable.console"  value:{value:"true"}}  configItems:{key:"debug.enable.ssh"  value:{value:"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAQC+H1RQUqHjFBJgGpslC73XsLz8Fg5WpNPble9naKyWz1Um8D2bOtQK/yguCImPeBYcH7/73z8dtC6d+dT0UF26+o7Vh6RN/U2X/5nkaZr7oM5QwwZTsD7Nd2Szww9wrRhXvpV0aFgUBDM9BIF1qBQxLNd+Jp8uttrgF3zj/cm7+SXllG54sv8WFBMfTX7J8cQ1jxLyp/Sc6PXK0zBaVzZwhmCCmI6CIzJK6ahMRgXm2vSP6doYibkB3ETSskaCXSHxDiZoaQK2ZY+GqNZkUusbau43MXVPTiJknXqUcmXhQmwyMSltQ3G54jcgn4TDObSQnW7vGdLI7zIEAHnk5D1BmKzQUBh5aRLdhBtb6T3uvVtAqGgnXWKD+d2GjMiy4G31zfIlArvC3G8LxwsDoxQL0XKaFjnEmXIptVXC68zq+laIM8YGDDOCEc6RfczP7lA4p6rv0gQUfTNqy0P3a4ulvIDb4hET1Gkh+Azkuw1do9NIhXPxDBmPdkTnKwJv6XelpdCPCw1QT5o7WdOkUgorf/e03jGDnnn1QBWpSPB9gLB/oDmT/Gzm8tCYaq7ggwYUq1fSMBvnbFaclH1KVcC2Gwn8UPLt9HHa/mGywuISZYl3gy7ztlKyAjHEZH053U7I8OaMvv/CFo9aR4Teeb5848REZYAes+yJIz3lJV1K5Q=="}}  configItems:{key:"newlog.allow.fastupload"  value:{value:"true"}}  configItems:{key:"timer.config.interval"  value:{value:"5"}}  configItems:{key:"timer.deviceinfo.interval"  value:{value:"30"}}  configItems:{key:"timer.download.retry"  value:{value:"60"}}  configItems:{key:"timer.location.app.interval"  value:{value:"20"}}  configItems:{key:"timer.location.cloud.interval"  value:{value:"300"}}  configItems:{key:"timer.metric.interval"  value:{value:"20"}}  configItems:{key:"timer.test.baseimage.update"  value:{value:"60"}}}  last_boot_reason:BOOT_REASON_FIRST  hardware_watchdog_present:true  capabilities:{HWAssistedVirtualization:true  IOVirtualization:true}  state:ZDEVICE_STATE_ONLINE  storage_info:{pool_name:"/persist"  storage_type:STORAGE_TYPE_INFO_EXT4  storage_state:STORAGE_STATUS_ONLINE  disks:{disk_name:{name:"/dev/vda9"  serial:"unknown"}  status:STORAGE_STATUS_ONLINE  smart_status:"failed"}}  attestation_info:{state:ATTESTATION_STATE_COMPLETE  error:{description:"[ATTEST] No escrow data"  timestamp:{seconds:1788877612  nanos:850268496}  severity:SEVERITY_ERROR}}  api_capability:API_CAPABILITY_APP_INSTANCE_NET_INTERFACE_CHANGE  optional_capabilities:{hw_inventory_support:true}  device_name:"edge-dev"
```

##### Claude's conclusion

###### Root cause

Live evidence confirms a race between NIM's port-testing rotation and the test's own polling timeout, not an actual bond/LACP failure. On the device, `bond1` (lacp-bond) and `ethernet2` are both management ports with `Cost:0`; per `controllerconn.VerifyAllIntf` (pkg/pillar/controllerconn/send.go:516-630), once one mgmt port satisfies `requiredSuccessCount`, remaining mgmt ports of the *same cost* are only dry-run tested and their `TestResults` are deliberately **not** updated (to avoid overwriting a previous full-test result) — this applies even when that stale prior result was a *failure*, not a success. At 14:27:05 both `bond1` and `ethernet2` still lacked an IP and were recorded as failed (`interface bond1: no suitable IP address available`); by 14:27:06-08 both actually came up and the overall DPC test passed, but only `ethernet2` got its `TestResults` refreshed (`LastSucceeded 14:27:08.43`), while `bond1`'s `LastFailed 14:27:05.41` stayed unrefreshed and kept reporting the stale error in device info. Checking `/run/nim/DeviceNetworkStatus/global.json` live shows `bond1`'s error only actually cleared at `14:32:10.32` — i.e. NIM didn't rotate back to fully re-test `bond1` until a full `NetworkTestInterval` (default 300s) had elapsed, which happened ~1s *after* the test's own 300s timeout fired at 14:32:09.287. So this is an inherent race: both the default DPC re-test rotation interval and the test's own timeout are 300s, making it a coin-flip whether the stale per-port error clears before the test gives up. I found no `ledger.md`/`live-ledger.md` in this run's artifacts to confirm prior recurrences, so I can't say how long this has been happening, but the mechanism is clearly reproducible by design whenever two mgmt ports share the same cost and one comes up fractionally later than the other.

### TestNodeClusterSuite: failure analysis

#### TestThreeNodesCluster

##### Failure

```

Timed out after 180.000s.
The function passed to Eventually failed at /evetest/tests/cluster/cluster_test.go:481 with:
Unexpected error:
<*fmt.wrapError | 0xc00042c0c0>: 
unable to reach app "907fce4b-1718-40c2-a8c0-5c69cf567581" SSH on device "edge-dev1": no reachable endpoint among [172.20.20.150:2222]
{
msg: "unable to reach app \"907fce4b-1718-40c2-a8c0-5c69cf567581\" SSH on device \"edge-dev1\": no reachable endpoint among [172.20.20.150:2222]",
err: <*errors.errorString | 0xc0017e7a00>{
s: "no reachable endpoint among [172.20.20.150:2222]",
},
}
occurred
```

##### Claude's conclusion

No ledger/known-issues file exists in this repo or in my memory, so I can't confirm this as a tracked recurring issue — treating it as a fresh finding.

###### Root cause

On `edge-dev1`, the Longhorn-backed local storage replica for the app's volume kept flapping OFFLINE/rebuilding (`storage.health` cycled HEALTHY → DEGRADED_1/2_REPLICA_AVAILABLE_NOT_REPLICATING → HEALTHY several times between 11:44–11:52), and this instability culminated in the local k3s server crashing and restarting on that node (`k3s init`/`k3s server` processes only ~1–2 min old at the time of investigation, vs. other node components running 25–34 min). Each k3s restart made the local apiserver (`127.0.0.1:6443`) unreachable (`zedkube` logs: `connection refused`, `apiserver not ready`, `checkAppsFailover: can't get pods apiserver not ready` at 11:52:35 onward), which in turn caused `zedkube`'s failover/reconciliation logic to repeatedly tear down and recreate the app's KubeVirt pod (three different `virt-launcher-container-app-907fc-*` pod names within about 2 minutes: `-0z2v9x` → `-0bzrzg` → `-0w5zqn`). Because the VMI kept being recreated, its network never stabilized long enough for the SSH port-forward to `172.20.20.150:2222` to become reachable before the test's 180s `Eventually` timed out — consistent with the observed `connection refused` retries in `gotest.json`. This looks like an EVE-K (kubevirt/longhorn) storage-instability issue rather than anything in the SSH-reachability check itself; I found no ledger or memory entry recording this as a previously-known recurring failure, so I can't say whether/since-when it has recurred before.

#### TestClusterToSingleConversion

##### Failure

```

Timed out after 600.000s.
No survivor of [edge-dev1 edge-dev2] reports a cluster of exactly [edge-dev1 edge-dev2] without "edge-dev3"
Expected
<bool>: false
to be true
```

##### Claude's conclusion

###### Root cause

`TestClusterToSingleConversion` withdrew `edge-dev3` from the cluster, and `edge-dev3` successfully rebooted into standalone mode (config version 5 confirmed, reboot at 12:49:10, checkpoint `converted-node-standalone` reached at 12:51:39). However, the remaining nodes `edge-dev1`/`edge-dev2` never dropped `edge-dev3` from their etcd/k3s node list — `kubectl get nodes` on `edge-dev1` still shows `edge-dev3` present (`NotReady,SchedulingDisabled`) at the time of investigation, and `zedkube`'s `getKubeNodeInfo` logs on `edge-dev1` show it continuously reporting `edge-dev3` (status 3/NotReady since 12:48:40) for the entire ~10-minute wait, so the "no survivor reports exactly [edge-dev1 edge-dev2]" check never passed before the 600s timeout.

The direct cause is visible in `edge-dev3`'s own logs at 12:48:18: when `zedagent` processed the cluster-config removal and called `drainAndDeleteNode` (`pkg/pillar/cmd/zedkube/drain.go`) to remove itself from the cluster before rebooting, its local k3s apiserver (`127.0.0.1:6443`) was already unreachable — `drainAndDeleteNode: can't get nodes ... dial tcp 127.0.0.1:6443: connect: connection refused`, immediately followed by the deferred delete also failing with the same `connection refused`. So `edge-dev3` was never actually deleted from the cluster's etcd state, leaving a stale Node object that the survivors kept reporting indefinitely — there's no retry/backoff in `drainAndDeleteNode` for this race (unlike `cordonAndDrainNode`, which has an explicit `drainSkipK8sAPINotReachableTimeout` fallback for API unreachability).

This node-deletion path was only added in commit `5e2f7121a` ("kubevirt: cluster config deletion missing node drain/delete", 2025-08-26), and it has this apiserver-unreachable race baked in from the start — I found no ledger/known-issues file in the repo to confirm whether this specific race has been seen/tracked before, so I can't say how long it's been recurring, but the code path is clearly exposed to this failure mode by design.

### TestUpgradeSuite: failure analysis

#### TestEVEUpgradeKubevirtToKubevirt

##### Failure

```
EVE upgrade to 0.0.0-master-0408b69f-kvm-amd64 failed: Upgrade to non EVE-k (0.0.0-master-0408b69f-kvm-amd64) from  EVE-k (0.0.0-master-66475696-k-amd64) is not supported
```

##### Claude's conclusion

Live check confirms the device is still healthy and running its original image `0.0.0-master-66475696-k-amd64` (EVE-k/kubevirt) — the upgrade was correctly rejected before any partition switch, so the device itself is not the problem.

**Root cause**: this is a bad test image, not a bug in EVE or the test harness's upgrade logic. The harness pulled `milan4zededa/eve:0.0.0-master-0408b69f-k-amd64` (a `-k-amd64`/kubevirt-tagged image, as expected for the `TestEVEUpgradeKubevirtToKubevirt` test), but `docker run ... version` on that image reported its actual embedded short version as `0.0.0-master-0408b69f-kvm-amd64` (gotest.json lines 576–586) — i.e. the image pushed under the `-k-amd64` tag is actually a plain-KVM build, not kubevirt. EVE's own upgrade guard on the device correctly detected this hypervisor mismatch and refused the downgrade path (`Upgrade to non EVE-k ... from EVE-k ... is not supported`), which is why the test failed safely with the device left healthy on its original image. The `milan4zededa/eve` repo is a personal/dev image namespace (not the standard CI-published one), strongly suggesting the `-k-amd64` tag for build `0408b69f` was mistakenly built/pushed as a kvm-flavored rootfs instead of kubevirt. I found no matching entry in memory for this specific failure, so I can't say whether it's recurring — worth checking whoever owns `milan4zededa/eve` about the `0408b69f` build tagging.
