# Evetest Nightly Ledger

One section per nightly run with at least one failing suite.

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
