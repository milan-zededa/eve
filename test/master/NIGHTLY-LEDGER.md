# Evetest Nightly Ledger

One section per nightly run with at least one failing suite.

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
