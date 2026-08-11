// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package cluster_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	// revive:disable:dot-imports
	. "github.com/onsi/gomega"

	eveconfig "github.com/lf-edge/eve-api/go/config"
	"github.com/lf-edge/eve-api/go/evecommon"
	eveinfo "github.com/lf-edge/eve-api/go/info"
	"github.com/lf-edge/eve/evetest"
	"github.com/lf-edge/eve/evetest/netmodels"
	"github.com/lf-edge/eve/pkg/pillar/types"
	uuid "github.com/satori/go.uuid"
)

// failoverCycleCountParamKey is the key for the parameter controlling how
// many times TestTwoNodeHACluster repeats its failover/recovery cycle.
const failoverCycleCountParamKey = "CLUSTER_FAILOVER_CYCLE_COUNT"

var failoverCycleCountParam = evetest.TestParameterDefinition{
	Key:          failoverCycleCountParamKey,
	DefaultValue: uint32(1),
	Description: evetest.TestParameterDescription{
		Summary: "Number of times to repeat the non-seed/seed failover and quorum-recovery cycle",
		Default: "1",
	},
}

// appNodeName returns the k3s node name currently hosting the app (matched
// by display name, allowing for Kubernetes' hash suffix - mirrors
// edgecluster.go's unexported findAppNodeName), or "" if not yet reported.
func appNodeName(info *eveinfo.ZInfoKubeCluster, appDisplayName string) string {
	prefix := appDisplayName + "-"
	matches := func(name string) bool {
		return name == appDisplayName || strings.HasPrefix(name, prefix)
	}
	for _, app := range info.GetEveApps() {
		if matches(app.GetName()) && app.GetNodeName() != "" {
			return app.GetNodeName()
		}
	}
	for _, vm := range info.GetEveVmApps() {
		if matches(vm.GetName()) && vm.GetNodeName() != "" {
			return vm.GetNodeName()
		}
	}
	return ""
}

// witnessJoined returns a predicate checking that ZInfoKubeCluster.Witness
// reports the expected witness IP in JOINED state. The witness carries no
// separate recovery/convergence status of its own: unlike a node it never
// holds a partial share of the cluster's data, so JOINED already means
// caught up.
func witnessJoined(witnessIP string) func(*eveinfo.ZInfoKubeCluster) bool {
	return func(info *eveinfo.ZInfoKubeCluster) bool {
		w := info.GetWitness()
		if w == nil || w.GetWitnessIp() != witnessIP {
			return false
		}
		return w.GetState() == eveinfo.WitnessEtcdState_WITNESS_ETCD_STATE_JOINED
	}
}

// nodeRecoveryConverged returns a predicate checking that the named node's
// KubeNodeInfo.Recovery reports the expected applied generation, with no
// error and no convergence in progress.
func nodeRecoveryConverged(
	nodeName string, wantGeneration uint32) func(*eveinfo.ZInfoKubeCluster) bool {
	return func(info *eveinfo.ZInfoKubeCluster) bool {
		for _, node := range info.GetNodes() {
			if node.GetName() != nodeName {
				continue
			}
			rec := node.GetRecovery()
			return rec.GetAppliedGeneration() == wantGeneration &&
				!rec.GetConverging() && rec.GetError() == nil
		}
		return false
	}
}

// getAppBootTime reads the "btime" line of /proc/stat inside the app,
// the kernel's boot time as a fixed Unix timestamp.
// Use to detect whether the app was restarted.
//
// The SSH call is retried within a short Eventually: it runs right after
// powering a cluster node off/on, and a transient connection race at that
// moment (e.g. "unexpected packet in response to channel open") must not be
// mistaken for an app restart.
func getAppBootTime(t *WithT, cluster *evetest.EdgeCluster, appUUID uuid.UUID,
	auth evetest.AuthMethod, timeout time.Duration) int64 {
	var bootTime int64
	t.Eventually(func(t Gomega) {
		output, _, err := cluster.RunShellScriptInsideApp(appUUID, auth,
			"awk '/^btime/{print $2}' /proc/stat", timeout, 0)
		t.Expect(err).ToNot(HaveOccurred())
		parsed, parseErr := strconv.ParseInt(strings.TrimSpace(output), 10, 64)
		t.Expect(parseErr).ToNot(HaveOccurred())
		bootTime = parsed
	}, time.Minute, 3*time.Second).Should(Succeed())
	return bootTime
}

// verifyAppReachable checks SSH (port-fwd 2222->22) and outbound
// connectivity (curl to an SDN HTTP endpoint) for the app, wherever in the
// cluster it currently runs.
func verifyAppReachable(t *WithT, cluster *evetest.EdgeCluster, appUUID uuid.UUID,
	auth evetest.AuthMethod, sshTimeout time.Duration) {
	log := evetest.Logger()
	log.Infof("Verifying app %q is reachable...", appUUID)
	t.Eventually(func(t Gomega) {
		output, _, err := cluster.RunShellScriptInsideApp(appUUID, auth,
			"hostname", sshTimeout, 0)
		t.Expect(err).ToNot(HaveOccurred())
		t.Expect(output).To(ContainSubstring(appUUID.String()))
	}, 3*time.Minute, 3*time.Second).Should(Succeed())

	output, _, err := cluster.RunShellScriptInsideApp(appUUID, auth,
		"curl -sS http://http-server.test/helloworld", sshTimeout, 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(output).To(ContainSubstring("Hello world!"))
}

// diskMarkerPath is where writeDiskMarker/verifyDiskMarker read and write,
// on the same Longhorn-backed root volume as the rest of the app's
// filesystem. Checking its content after a failover proves the write made
// it onto the node the app lands on next - i.e. the surviving node already
// had a synced Longhorn replica - not merely that the running process
// state carried over.
const diskMarkerPath = "/root/ha-disk-marker.txt"

// writeDiskMarker writes a one-line, caller-supplied marker to
// diskMarkerPath inside the app and fsyncs it, then reads it back and
// returns the exact content written, for later comparison by
// verifyDiskMarker. marker should be unique per call (e.g. the name of the
// checkpoint it is establishing a baseline for) so a failed comparison
// names which write is missing.
func writeDiskMarker(t *WithT, cluster *evetest.EdgeCluster, appUUID uuid.UUID,
	auth evetest.AuthMethod, timeout time.Duration, marker string) string {
	content := fmt.Sprintf("ha-disk-marker:%s:%d", marker, time.Now().UTC().UnixNano())
	script := fmt.Sprintf("printf '%s' > %s && sync && cat %s",
		content, diskMarkerPath, diskMarkerPath)
	output, _, err := cluster.RunShellScriptInsideApp(appUUID, auth, script, timeout, 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(strings.TrimSpace(output)).To(Equal(content))
	return content
}

// verifyDiskMarker checks that diskMarkerPath still holds exactly
// wantContent, proving data written before a node failover is still present
// afterward: the surviving/rescheduled-onto node's Longhorn replica, not
// just the pod definition, carried the write across.
func verifyDiskMarker(t *WithT, cluster *evetest.EdgeCluster, appUUID uuid.UUID,
	auth evetest.AuthMethod, timeout time.Duration, wantContent string) {
	output, _, err := cluster.RunShellScriptInsideApp(appUUID, auth,
		fmt.Sprintf("cat %s", diskMarkerPath), timeout, 0)
	t.Expect(err).ToNot(HaveOccurred())
	t.Expect(strings.TrimSpace(output)).To(Equal(wantContent),
		"disk marker content changed or went missing after failover - "+
			"the write did not survive on the node the app is now running on")
}

// TestTwoNodeHACluster verifies EVE-k's 2-node HA (witness) feature: two K3s
// server nodes plus a lightweight etcd-only witness co-located with the
// seed/bootstrap node, tolerating the loss of either physical node - either
// automatically (non-seed dies, quorum survives) or via a controller-driven
// quorum recovery (seed dies, quorum lost).
//
// Network model
// -------------
//   - netmodels.SeparateClusterPort(devName[:]...) -- a dedicated cluster
//     port per device (same shape as the 3-node cluster test, but built
//     for exactly the 2 devices requested here, so it's a distinct
//     network model instance from the 3-node test's - this rules out
//     device-VM reuse between the two tests for now).
//
// Device configuration
// --------------------
//   - Two RequireEdgeDevice entries via clusterDeviceRequirements (same as
//     the 3-node test: Kubevirt, fresh image, ext4 by default), but with
//     MinCPUs/MinRAMInMiB raised to 8/16384. The 3-node test's default 4/8
//     floor spreads etcd, kubevirt, Longhorn, multus and traefik across
//     three real voting members; a quorum recovery here collapses to a
//     single voting member, so the survivor alone carries all of that plus
//     whatever the dead node was running, on the same per-node floor. At
//     4/8 that was enough to make etcd too slow to serve its own
//     leader-election lease renewals in time, crashing k3s outright.
//   - ClusterConfig with two ClusterNode entries: edge-dev1 (seed,
//     BootstrapNode=true, 10.244.244.2/24) and edge-dev2 (non-seed,
//     10.244.244.3/24), ClusterInterface="ethernet1".
//   - Witness configured via SetWitness(10.244.244.5) - co-locates with
//     whichever device currently owns JoinServerIp (initially edge-dev1).
//   - Container app (lfedge/evetest-ubuntu-ctr:1.0), PREFERRED affinity,
//     DesignatedNodeName=edge-dev2 (the non-seed). The app is deliberately
//     kept away from the seed so that losing the seed (and the co-located
//     witness) never interrupts it, only the control plane; losing the
//     non-seed instead causes a short, fully automatic reschedule +
//     failback via K3s and pkg/kube's descheduler. Unlike the seed role,
//     which follows join_server_ip on its own, the app's designated node
//     does not move by itself: the controller repoints it explicitly
//     after the quorum recovery below, once the old seed has rejoined as
//     the new non-seed.
//
// Test parameters
// ---------------
//   - TPM via evetest.TPMParameter().
//   - FILESYSTEM (ext4|zfs, defaults to ext4) via evetest.FilesystemParameter().
//   - CLUSTER_FAILOVER_CYCLE_COUNT (uint32, defaults to 1): how many times
//     phases 8-19 repeat. Each cycle ends with the seed and non-seed roles
//     swapped between the two devices, so the next cycle exercises the
//     same steps with the physical devices reversed. The quorum recovery
//     generation keeps counting up across cycles (cycle N expects
//     generation N) rather than resetting. Checkpoint names from phases
//     8-19 get a "-cycle-N" suffix when this is set above 1.
//
// Phases
// ------
//  1. setup-done
//  2. initial-config-applied
//  3. nodes-are-ready
//  4. witness-is-ready: ZInfoKubeCluster.Witness reports JOINED at the
//     configured IP, with no recovery error/in-progress.
//  5. app-config-is-submitted / app-is-deployed: app lands on the non-seed
//     node.
//  6. app-verified-on-non-seed: SSH (hostname) + outbound curl checks;
//     captures a boot-time baseline (/proc/stat's btime) for later
//     no-downtime checks.
//  7. disk-marker-written: a unique line is written to a file on the app's
//     Longhorn-backed root volume and fsync'd. Every later app-move
//     checkpoint (9, 11, 19) re-reads this same file and expects the exact
//     same content, proving the node the app lands on next already had (or,
//     for the rejoined old seed, correctly rebuilt) a synced Longhorn
//     replica - not just that the running process state carried over.
//  8. non-seed-powered-off
//  9. app-failed-over-to-seed: quorum survives (seed+witness=2 of 3), K3s
//     reschedules the app onto the seed automatically; SSH+curl and the
//     disk marker re-verified there. Boot time changes here (expected -
//     the pod restarted elsewhere).
//  10. non-seed-powered-on
//  11. app-failed-back-to-non-seed: pkg/kube's descheduler moves the app
//     back to its preferred (non-seed) node once it's healthy again;
//     SSH+curl and the disk marker re-verified. Boot time changes again
//     (last expected restart; re-baselined here for the no-downtime checks
//     below).
//  12. seed-powered-off: witness dies with it (2 of 3 votes gone) - quorum
//     lost, but the app was never on the seed.
//  13. no-downtime-confirmed: the app's boot time is compared against the
//     step-11 baseline and must be unchanged - proves the control-plane
//     outage never touched the running app.
//  14. k3s-unresponsive-confirmed: `eve exec kube kubectl get nodes` over
//     SSH to the surviving (non-seed) device's EVE host must fail/timeout
//     - the local apiserver can't serve requests without quorum.
//  15. quorum-recovery-triggered: TriggerQuorumRecovery(edge-dev2) promotes
//     the non-seed to be the new seed, and withdraws the dead seed's
//     cluster config in the same config change, so it is never told to
//     converge to the new generation at all.
//  16. cluster-reset-completed: the new seed's KubeNodeInfo.Recovery
//     reaches the new generation with no error, and the witness has
//     followed the new seed (JOINED again). Boot time is re-checked
//     against the step-11/13 baseline - the reset itself must not
//     interrupt the app either.
//  17. old-seed-powered-on: expects the power-on reboot plus one more -
//     with no cluster config to find, it converts back to single-node the
//     same way TestClusterToSingleConversion does.
//  18. old-seed-converted-to-single / old-seed-rejoined: once it reports
//     itself as a lone ready node, it is rejoined as a plain (non-
//     bootstrap) member through the ordinary join workflow, and the
//     controller repoints the app's DesignatedNodeName at it in the same
//     config change - the app is placed by node id alone, so nothing else
//     would move it here.
//  19. app-moved-to-new-non-seed: the join event fires once edge-dev1
//     reports its cluster ready, moving the app there; final SSH+curl
//     checks, plus a final disk-marker check - the old seed's Longhorn
//     replica was wiped along with everything else in steps 16-18, so
//     finding the original marker here proves Longhorn rebuilt it correctly
//     from the surviving node's copy before the app started.
//
// Suite placement
// ---------------
//   - TestNodeClusterSuite. Like the other cluster tests this runs only on eve-k
func TestTwoNodeHACluster(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()

	// Define configurable parameters available for the test.
	evetest.DefineTestParameters(
		evetest.TPMParameter(),
		evetest.FilesystemParameter(),
		failoverCycleCountParam,
	)

	// Get parameter values set for this test execution.
	withTPM := evetest.GetTPMParameterValue()
	filesystem := evetest.GetFilesystemParameterValue()
	failoverCycles := evetest.GetTestParameter[uint32](failoverCycleCountParamKey)

	// Set up the test harness and specify the test prerequisites.
	const numNodes = 2
	var requiredDevices [numNodes]evetest.Requirement
	var devName [numNodes]string
	for i := 0; i < numNodes; i++ {
		devName[i] = fmt.Sprintf("edge-dev%d", i+1)
		req := clusterDeviceRequirements(devName[i], withTPM, filesystem, false)
		// Raised past the 4 vCPU/8 GiB floor: a quorum recovery leaves the
		// survivor as the cluster's sole etcd voter, running the whole
		// stack (kubevirt, Longhorn, multus, traefik) alone rather than
		// sharing it with peers the way the 3-node test's nodes do.
		req.MinCPUs = 8
		req.MinRAMInMiB = 16384
		requiredDevices[i] = req
	}
	requiredNetModel := evetest.RequireNetworkModel{
		NetworkModel: netmodels.SeparateClusterPort(devName[:]...),
	}
	var requirements []evetest.Requirement
	requirements = append(requirements, requiredDevices[:]...)
	requirements = append(requirements, requiredNetModel)
	evetest.Setup(requirements...)
	evetest.Checkpoint("setup-done")

	// Build the cluster configuration: edge-dev1 is the seed/bootstrap,
	// edge-dev2 is the non-seed.
	const witnessIP = "10.244.244.5"
	nodes := [numNodes]evetest.ClusterNode{
		{
			DevName:          devName[0],
			ClusterIP:        evetest.IPAddressWithPrefix("10.244.244.2/24"),
			ClusterInterface: "ethernet1",
			BootstrapNode:    true,
		},
		{
			DevName:          devName[1],
			ClusterIP:        evetest.IPAddressWithPrefix("10.244.244.3/24"),
			ClusterInterface: "ethernet1",
		},
	}
	clusterConfig := evetest.NewEdgeClusterConfig(
		eveconfig.ClusterType_CLUSTER_TYPE_REPLICATED_STORAGE,
		nodes[:]...,
	)
	clusterConfig.SetWitness(evetest.IPAddress(witnessIP))

	// The app only moves when the descheduler runs, and both moves this test
	// makes need it: back to the non-seed once that node returns, which is
	// the boot event, and onto the old seed once it rejoins as the new
	// non-seed after the quorum recovery, which is the join event - a live
	// single-to-cluster transition never restarts zedkube, so it needs its
	// own trigger distinct from boot. Set in the config the devices onboard
	// with, since the boot watcher is launched once just after k3s comes
	// ready and a value arriving later does not re-trigger it.
	//
	// "join" is spelled out because its constant arrives with the pillar
	// change this test exercises, and evetest builds against a published
	// release; swap it for types.VmiDescheduleEventJoin after the next
	// pillar bump.
	cfgProps := types.NewConfigItemValueMap()
	cfgProps.SetGlobalValueString(types.KubernetesVmiDescheduleEvents,
		types.VmiDescheduleEventBoot+",join")
	clusterConfig.SetConfigProperties(cfgProps)

	// Configure network adapters and networks (applied to all devices).
	dhcpNet := clusterConfig.AddNetwork(
		evetest.DHCPNetworkConfig{
			NetworkType: evecommon.NetworkType_V4Only,
		})
	noIPNet := clusterConfig.AddNetwork(evetest.NoIPNetworkConfig{})
	clusterConfig.AddNetworkAdapter(
		evetest.NetworkAdapterConfig{
			LogicalLabel:  "ethernet0",
			PhysicalLabel: "eth0",
			InterfaceName: "eth0",
			NetworkUUID:   dhcpNet,
			Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
		})
	clusterConfig.AddNetworkAdapter(
		evetest.NetworkAdapterConfig{
			LogicalLabel:  "ethernet1",
			PhysicalLabel: "eth1",
			InterfaceName: "eth1",
			NetworkUUID:   noIPNet,
			Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageShared,
		})

	// Apply the initial configuration to each device in parallel.
	cluster := evetest.NewEdgeCluster("test-two-node-ha-cluster")
	cluster.ApplyConfig(clusterConfig, true, true)
	evetest.Checkpoint("initial-config-applied")

	cluster.WaitUntilNodesAreReady(30 * time.Minute)
	evetest.Checkpoint("nodes-are-ready")

	cluster.WaitUntilClusterInfoSatisfies(5*time.Minute,
		fmt.Sprintf("witness to join at %s", witnessIP), witnessJoined(witnessIP))
	evetest.Checkpoint("witness-is-ready")

	// Deploy an application into the cluster, preferring the non-seed node.
	const appDisplayName = "container-app"
	niUUID := clusterConfig.AddNetworkInstance(evetest.LocalNetworkInstanceConfig{
		DisplayName: "local-ni",
		Port:        "ethernet0",
		Subnet:      evetest.IPSubnet("10.11.12.0/24"),
		DHCPRange: types.IPRange{
			Start: evetest.IPAddress("10.11.12.2"),
			End:   evetest.IPAddress("10.11.12.254"),
		},
		Gateway:       evetest.IPAddress("10.11.12.1"),
		EnableFlowlog: true,
		MTU:           1500,
		ForwardLLDP:   false,
	})
	appConfig := evetest.ApplicationInstanceConfig{
		DisplayName: appDisplayName,
		Activate:    true,
		Image: evetest.DockerContainer{
			ImageName: "lfedge/evetest-ubuntu-ctr",
			Tag:       "1.0",
		},
		CPUs:        1,
		MemoryBytes: 500 * evetest.MiB,
		NetworkAdapters: []evetest.AppNetworkAdapter{
			evetest.VirtualNetworkAdapter{
				LogicalLabel:        "vif0",
				NetworkInstanceUUID: niUUID,
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
	}
	// Placed by plain node id. A pod's affinity is immutable once it is
	// running, so moving the app once the non-seed role changes hands is
	// the controller's job, not something the initial placement can do on
	// its own: after the quorum recovery below, the controller repoints
	// DesignatedNodeName at whichever device is the non-seed then, and the
	// join event moves the running app to match.
	//
	// DesignatedNodeName also names the node that downloads the volume,
	// and is the node the app first lands on.
	appUUID := clusterConfig.AddApplication(evetest.ClusterApplicationInstanceConfig{
		ApplicationInstanceConfig: appConfig,
		DesignatedNodeName:        devName[1], // non-seed
		Affinity:                  eveconfig.AffinityType_AFFINITY_TYPE_PREFERRED,
	})
	cluster.ApplyConfig(clusterConfig, true, true)
	log := evetest.Logger()
	log.Infof("Submitted config with container application UUID=%v", appUUID)
	evetest.Checkpoint("app-config-is-submitted")

	timeoutExcludingDownload := 10 * time.Minute
	cluster.WaitUntilAppIsRunning(appUUID, timeoutExcludingDownload)
	cluster.WaitUntilClusterInfoSatisfies(1*time.Minute,
		fmt.Sprintf("app %q to be reported on non-seed %q", appDisplayName, devName[1]),
		func(info *eveinfo.ZInfoKubeCluster) bool {
			return appNodeName(info, appDisplayName) == devName[1]
		})
	evetest.Checkpoint("app-is-deployed")

	appAuth := evetest.UsernamePasswordAuth{
		Username: "root",
		Password: "testpassword",
	}
	sshTimeout := 20 * time.Second
	verifyAppReachable(t, cluster, appUUID, appAuth, sshTimeout)
	evetest.Checkpoint("app-verified-on-non-seed")

	// Write a disk marker now, before any failover. Every app-move checkpoint
	// below re-checks this same content, proving the write survives on
	// whichever node the app is rescheduled or moved to next - i.e. that
	// node's Longhorn replica was already in sync, not a fresh empty volume.
	diskMarker := writeDiskMarker(t, cluster, appUUID, appAuth, sshTimeout, "initial")
	evetest.Checkpoint("disk-marker-written")

	// Phases 8-19 repeat failoverCycles times, with the seed/non-seed roles
	// swapped between the two devices at the end of each cycle (see "Test
	// parameters" above).
	curSeed, curNonSeed := devName[0], devName[1]
	var recoveryGeneration uint32
	for cycle := uint32(1); cycle <= failoverCycles; cycle++ {
		checkpointSuffix := ""
		if failoverCycles > 1 {
			checkpointSuffix = fmt.Sprintf("-cycle-%d", cycle)
		}
		checkpoint := func(name string) {
			evetest.Checkpoint(name + checkpointSuffix)
		}

		// Kill the non-seed node. Quorum survives (seed + witness = 2 of 3),
		// so K3s reschedules the app onto the seed automatically.
		nonSeedDevice := evetest.GetEdgeDevice(curNonSeed)
		nonSeedDevice.PowerOff()
		checkpoint("non-seed-powered-off")

		rescheduleTimeout := 10 * time.Minute // no image download needed, just a K8s reschedule
		cluster.WaitUntilClusterInfoSatisfies(rescheduleTimeout,
			fmt.Sprintf("app %q to be rescheduled onto seed %q", appDisplayName, curSeed),
			func(info *eveinfo.ZInfoKubeCluster) bool {
				return appNodeName(info, appDisplayName) == curSeed
			})
		verifyAppReachable(t, cluster, appUUID, appAuth, sshTimeout)
		verifyDiskMarker(t, cluster, appUUID, appAuth, sshTimeout, diskMarker)
		checkpoint("app-failed-over-to-seed")

		// Bring the non-seed node back. pkg/kube's descheduler should move the
		// app back to its preferred node once it's healthy again.
		nonSeedDevice.PowerOn(true)
		checkpoint("non-seed-powered-on")

		failbackTimeout := 10 * time.Minute // node health checks + descheduler trigger
		cluster.WaitUntilClusterInfoSatisfies(failbackTimeout,
			fmt.Sprintf("app %q to be moved back onto non-seed %q", appDisplayName, curNonSeed),
			func(info *eveinfo.ZInfoKubeCluster) bool {
				return appNodeName(info, appDisplayName) == curNonSeed
			})
		verifyAppReachable(t, cluster, appUUID, appAuth, sshTimeout)
		verifyDiskMarker(t, cluster, appUUID, appAuth, sshTimeout, diskMarker)
		checkpoint("app-failed-back-to-non-seed")

		// Re-baseline boot time here. Valid only through the seed-death/
		// quorum-recovery checks below (steps 11-15) - the app is
		// deliberately moved again afterward (once its DesignatedNodeName is
		// swapped in step 18), which restarts it and invalidates this
		// baseline for anything checked after that point.
		bootTimeBaseline := getAppBootTime(t, cluster, appUUID, appAuth, sshTimeout)

		// Kill the seed node. The co-located witness dies with it - 2 of 3
		// votes gone, quorum lost - but the app was never running on the seed.
		seedDevice := evetest.GetEdgeDevice(curSeed)
		seedDevice.PowerOff()
		checkpoint("seed-powered-off")

		// Confirm the app was never interrupted: its boot time must be
		// unchanged.
		latestBootTime := getAppBootTime(t, cluster, appUUID, appAuth, sshTimeout)
		t.Expect(latestBootTime).To(Equal(bootTimeBaseline),
			"app boot time changed - the seed outage caused unexpected downtime")
		checkpoint("no-downtime-confirmed")

		// Confirm the control plane is stuck without quorum: kubectl against
		// the surviving node's local apiserver must fail/timeout.
		_, _, err := nonSeedDevice.RunShellScript(
			"eve exec kube kubectl get nodes", 30*time.Second, 0)
		t.Expect(err).To(HaveOccurred(),
			"kubectl succeeded despite lost quorum - k3s apiserver should be unresponsive")
		checkpoint("k3s-unresponsive-confirmed")

		// Recover: promote the non-seed to be the new seed. TriggerQuorumRecovery
		// also withdraws the dead seed's cluster config outright in the same
		// config change, so it converts back to single-node on its own - the
		// same mechanism TestClusterToSingleConversion exercises.
		//
		// The recovery generation is a cluster-wide counter that keeps
		// incrementing across cycles, not a per-cycle value.
		recoveryGeneration++
		clusterConfig.TriggerQuorumRecovery(curNonSeed)
		// Published without waiting: the old seed is powered off until step 16
		// and could never fetch, and the survivor needs no separate wait since
		// the convergence check below cannot pass until it has acted on this.
		cluster.ApplyConfig(clusterConfig, false, false)
		checkpoint("quorum-recovery-triggered")

		// Covers the whole chain, not just the reset: config to cluster
		// status, a graceful k3s stop, the datastore snapshot, the forced
		// reset itself, then a full configure/start/ready cycle where ready
		// means the node and its kube-system pods, and the witness
		// rejoining behind it. On top of that sits a fixed reporting
		// latency of roughly a minute, since the status is stamped on a
		// 15s health tick and collected on zedkube's 30s one.
		//
		// An estimate rather than a measurement, pending a hardware run.
		recoveryTimeout := 10 * time.Minute
		cluster.WaitUntilClusterInfoSatisfies(recoveryTimeout,
			fmt.Sprintf("quorum recovery (generation %d) to complete on new seed %q",
				recoveryGeneration, curNonSeed),
			func(info *eveinfo.ZInfoKubeCluster) bool {
				return nodeRecoveryConverged(curNonSeed, recoveryGeneration)(info) &&
					witnessJoined(witnessIP)(info)
			})
		latestBootTime = getAppBootTime(t, cluster, appUUID, appAuth, sshTimeout)
		t.Expect(latestBootTime).To(Equal(bootTimeBaseline),
			"app boot time changed - the quorum recovery caused unexpected app downtime")
		checkpoint("cluster-reset-completed")

		// Bring the old seed back. Its cluster config was withdrawn above, so
		// once it fetches that, kube-init converts it back to a standalone
		// node exactly as TestClusterToSingleConversion does: mark
		// ConvertToSingleNode, reboot, and restore the pre-cluster /var/lib.
		// That reboot is required behaviour, not a crash - declare it, or
		// Close reports the conversion working as a failure.
		seedDevice.ExpectAdditionalReboots(1)
		seedDevice.PowerOn(true)
		checkpoint("old-seed-powered-on")

		// The device boot, vault unseal and install come first, then the
		// conversion's own reboot and the restore before k3s ever starts in
		// cluster mode again.
		//
		// An estimate rather than a measurement, pending a hardware run.
		conversionTimeout := 20 * time.Minute
		cluster.WaitUntilClusterInfoSatisfies(conversionTimeout,
			fmt.Sprintf("old seed %q to report itself as a standalone node", curSeed),
			func(info *eveinfo.ZInfoKubeCluster) bool {
				return soleNodeReady(info, curSeed)
			})
		checkpoint("old-seed-converted-to-single")

		// Rejoin the old seed as a plain (non-bootstrap) member through the
		// same mechanism any device joins for the first time - a rejoin is
		// not a distinct operation - and repoint the app's designated node at
		// it in the same config change: the controller has to say explicitly
		// that curSeed is the non-seed again, since nothing else determines
		// where the app runs.
		clusterConfig.RestoreClusterConfig(curSeed)
		clusterConfig.UpdateApplication(appUUID, evetest.ClusterApplicationInstanceConfig{
			ApplicationInstanceConfig: appConfig,
			DesignatedNodeName:        curSeed,
			Affinity:                  eveconfig.AffinityType_AFFINITY_TYPE_PREFERRED,
		})
		cluster.ApplyConfig(clusterConfig, true, true)
		checkpoint("old-seed-rejoin-triggered")

		// Same budget as the earlier cluster formation: a fresh join plus full
		// node readiness.
		rejoinTimeout := 30 * time.Minute
		cluster.WaitUntilNodesAreReady(rejoinTimeout)
		checkpoint("old-seed-rejoined")

		// The join event fires once curSeed reports its own cluster ready,
		// which is what lets the app move here without a device reboot: unlike
		// boot, join has to be its own trigger because a live single-to-cluster
		// transition never restarts zedkube. Nothing could have moved the app
		// before this point: the descheduler evicts a pod violating its
		// affinity only once some other node actually satisfies it, and before
		// the join above curSeed was not yet a node the app could run on.
		finalMoveTimeout := 20 * time.Minute
		cluster.WaitUntilClusterInfoSatisfies(finalMoveTimeout,
			fmt.Sprintf("app %q to move to new non-seed %q", appDisplayName, curSeed),
			func(info *eveinfo.ZInfoKubeCluster) bool {
				return appNodeName(info, appDisplayName) == curSeed
			})
		verifyAppReachable(t, cluster, appUUID, appAuth, sshTimeout)
		// The old seed was wiped back to single-node state in the steps above, so
		// its Longhorn replica here was rebuilt from scratch from the surviving
		// node's copy after rejoining, not left over from before. Finding the
		// original marker proves that rebuild completed correctly before the app
		// started on it.
		verifyDiskMarker(t, cluster, appUUID, appAuth, sshTimeout, diskMarker)
		checkpoint("app-moved-to-new-non-seed")

		// The old seed is now the non-seed and vice versa; the next cycle
		// repeats the same steps with the physical devices reversed.
		curSeed, curNonSeed = curNonSeed, curSeed
	}
}
