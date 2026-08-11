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
func getAppBootTime(t *WithT, cluster *evetest.EdgeCluster, appUUID uuid.UUID,
	auth evetest.AuthMethod, timeout time.Duration) int64 {
	output, _, err := cluster.RunShellScriptInsideApp(appUUID, auth,
		"awk '/^btime/{print $2}' /proc/stat", timeout, 0)
	t.Expect(err).ToNot(HaveOccurred())
	bootTime, parseErr := strconv.ParseInt(strings.TrimSpace(output), 10, 64)
	t.Expect(parseErr).ToNot(HaveOccurred())
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
//     the 3-node test: Kubevirt, fresh image, ext4 by default).
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
//  7. non-seed-powered-off
//  8. app-failed-over-to-seed: quorum survives (seed+witness=2 of 3), K3s
//     reschedules the app onto the seed automatically; SSH+curl
//     re-verified there. Boot time changes here (expected - the pod
//     restarted elsewhere).
//  9. non-seed-powered-on
//  10. app-failed-back-to-non-seed: pkg/kube's descheduler moves the app
//     back to its preferred (non-seed) node once it's healthy again;
//     SSH+curl re-verified. Boot time changes again (last expected
//     restart; re-baselined here for the no-downtime checks below).
//  11. seed-powered-off: witness dies with it (2 of 3 votes gone) - quorum
//     lost, but the app was never on the seed.
//  12. no-downtime-confirmed: the app's boot time is compared against the
//     step-10 baseline and must be unchanged - proves the control-plane
//     outage never touched the running app.
//  13. k3s-unresponsive-confirmed: `eve exec kube kubectl get nodes` over
//     SSH to the surviving (non-seed) device's EVE host must fail/timeout
//     - the local apiserver can't serve requests without quorum.
//  14. quorum-recovery-triggered: TriggerQuorumRecovery(edge-dev2) promotes
//     the non-seed to be the new seed, and withdraws the dead seed's
//     cluster config in the same config change, so it is never told to
//     converge to the new generation at all.
//  15. cluster-reset-completed: the new seed's KubeNodeInfo.Recovery
//     reaches the new generation with no error, and the witness has
//     followed the new seed (JOINED again). Boot time is re-checked
//     against the step-10/12 baseline - the reset itself must not
//     interrupt the app either.
//  16. old-seed-powered-on: expects the power-on reboot plus one more -
//     with no cluster config to find, it converts back to single-node the
//     same way TestClusterToSingleConversion does.
//  17. old-seed-converted-to-single / old-seed-rejoined: once it reports
//     itself as a lone ready node, it is rejoined as a plain (non-
//     bootstrap) member through the ordinary join workflow, and the
//     controller repoints the app's DesignatedNodeName at it in the same
//     config change - the app is placed by node id alone, so nothing else
//     would move it here.
//  18. app-moved-to-new-non-seed: the join event fires once edge-dev1
//     reports its cluster ready, moving the app there; final SSH+curl
//     checks.
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
	)

	// Get parameter values set for this test execution.
	withTPM := evetest.GetTPMParameterValue()
	filesystem := evetest.GetFilesystemParameterValue()

	// Set up the test harness and specify the test prerequisites.
	const numNodes = 2
	var requiredDevices [numNodes]evetest.Requirement
	var devName [numNodes]string
	for i := 0; i < numNodes; i++ {
		devName[i] = fmt.Sprintf("edge-dev%d", i+1)
		requiredDevices[i] = clusterDeviceRequirements(devName[i], withTPM, filesystem, false)
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

	// Kill the non-seed node. Quorum survives (seed + witness = 2 of 3),
	// so K3s reschedules the app onto the seed automatically.
	nonSeedDevice := evetest.GetEdgeDevice(devName[1])
	nonSeedDevice.PowerOff()
	evetest.Checkpoint("non-seed-powered-off")

	rescheduleTimeout := 10 * time.Minute // no image download needed, just a K8s reschedule
	cluster.WaitUntilClusterInfoSatisfies(rescheduleTimeout,
		fmt.Sprintf("app %q to be rescheduled onto seed %q", appDisplayName, devName[0]),
		func(info *eveinfo.ZInfoKubeCluster) bool {
			return appNodeName(info, appDisplayName) == devName[0]
		})
	verifyAppReachable(t, cluster, appUUID, appAuth, sshTimeout)
	evetest.Checkpoint("app-failed-over-to-seed")

	// Bring the non-seed node back. pkg/kube's descheduler should move the
	// app back to its preferred node once it's healthy again.
	nonSeedDevice.PowerOn(true)
	evetest.Checkpoint("non-seed-powered-on")

	failbackTimeout := 10 * time.Minute // node health checks + descheduler trigger
	cluster.WaitUntilClusterInfoSatisfies(failbackTimeout,
		fmt.Sprintf("app %q to be moved back onto non-seed %q", appDisplayName, devName[1]),
		func(info *eveinfo.ZInfoKubeCluster) bool {
			return appNodeName(info, appDisplayName) == devName[1]
		})
	verifyAppReachable(t, cluster, appUUID, appAuth, sshTimeout)
	evetest.Checkpoint("app-failed-back-to-non-seed")

	// Re-baseline boot time here. Valid only through the seed-death/
	// quorum-recovery checks below (steps 11-15) - the app is
	// deliberately moved again afterward (once its DesignatedNodeName is
	// swapped in step 18), which restarts it and invalidates this
	// baseline for anything checked after that point.
	bootTimeBaseline := getAppBootTime(t, cluster, appUUID, appAuth, sshTimeout)

	// Kill the seed node. The co-located witness dies with it - 2 of 3
	// votes gone, quorum lost - but the app was never running on the seed.
	seedDevice := evetest.GetEdgeDevice(devName[0])
	seedDevice.PowerOff()
	evetest.Checkpoint("seed-powered-off")

	// Confirm the app was never interrupted: its boot time must be
	// unchanged.
	latestBootTime := getAppBootTime(t, cluster, appUUID, appAuth, sshTimeout)
	t.Expect(latestBootTime).To(Equal(bootTimeBaseline),
		"app boot time changed - the seed outage caused unexpected downtime")
	evetest.Checkpoint("no-downtime-confirmed")

	// Confirm the control plane is stuck without quorum: kubectl against
	// the surviving node's local apiserver must fail/timeout.
	_, _, err := nonSeedDevice.RunShellScript(
		"eve exec kube kubectl get nodes", 30*time.Second, 0)
	t.Expect(err).To(HaveOccurred(),
		"kubectl succeeded despite lost quorum - k3s apiserver should be unresponsive")
	evetest.Checkpoint("k3s-unresponsive-confirmed")

	// Recover: promote the non-seed to be the new seed. TriggerQuorumRecovery
	// also withdraws the dead seed's cluster config outright in the same
	// config change, so it converts back to single-node on its own - the
	// same mechanism TestClusterToSingleConversion exercises.
	const firstRecoveryGeneration = 1
	clusterConfig.TriggerQuorumRecovery(devName[1])
	// Published without waiting: the old seed is powered off until step 16
	// and could never fetch, and the survivor needs no separate wait since
	// the convergence check below cannot pass until it has acted on this.
	cluster.ApplyConfig(clusterConfig, false, false)
	evetest.Checkpoint("quorum-recovery-triggered")

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
			firstRecoveryGeneration, devName[1]),
		func(info *eveinfo.ZInfoKubeCluster) bool {
			return nodeRecoveryConverged(devName[1], firstRecoveryGeneration)(info) &&
				witnessJoined(witnessIP)(info)
		})
	latestBootTime = getAppBootTime(t, cluster, appUUID, appAuth, sshTimeout)
	t.Expect(latestBootTime).To(Equal(bootTimeBaseline),
		"app boot time changed - the quorum recovery caused unexpected app downtime")
	evetest.Checkpoint("cluster-reset-completed")

	// Bring the old seed back. Its cluster config was withdrawn above, so
	// once it fetches that, kube-init converts it back to a standalone
	// node exactly as TestClusterToSingleConversion does: mark
	// ConvertToSingleNode, reboot, and restore the pre-cluster /var/lib.
	// That reboot is required behaviour, not a crash - declare it, or
	// Close reports the conversion working as a failure.
	seedDevice.ExpectAdditionalReboots(1)
	seedDevice.PowerOn(true)
	evetest.Checkpoint("old-seed-powered-on")

	// The device boot, vault unseal and install come first, then the
	// conversion's own reboot and the restore before k3s ever starts in
	// cluster mode again.
	//
	// An estimate rather than a measurement, pending a hardware run.
	conversionTimeout := 20 * time.Minute
	cluster.WaitUntilClusterInfoSatisfies(conversionTimeout,
		fmt.Sprintf("old seed %q to report itself as a standalone node", devName[0]),
		func(info *eveinfo.ZInfoKubeCluster) bool {
			return soleNodeReady(info, devName[0])
		})
	evetest.Checkpoint("old-seed-converted-to-single")

	// Rejoin the old seed as a plain (non-bootstrap) member through the
	// same mechanism any device joins for the first time - a rejoin is
	// not a distinct operation - and repoint the app's designated node at
	// it in the same config change: the controller has to say explicitly
	// that devName[0] is the non-seed again, since nothing else
	// determines where the app runs.
	clusterConfig.RestoreClusterConfig(devName[0])
	clusterConfig.UpdateApplication(appUUID, evetest.ClusterApplicationInstanceConfig{
		ApplicationInstanceConfig: appConfig,
		DesignatedNodeName:        devName[0],
		Affinity:                  eveconfig.AffinityType_AFFINITY_TYPE_PREFERRED,
	})
	cluster.ApplyConfig(clusterConfig, true, true)
	evetest.Checkpoint("old-seed-rejoin-triggered")

	// Same budget as the earlier cluster formation: a fresh join plus full
	// node readiness.
	rejoinTimeout := 30 * time.Minute
	cluster.WaitUntilNodesAreReady(rejoinTimeout)
	evetest.Checkpoint("old-seed-rejoined")

	// The join event fires once devName[0] reports its own cluster ready,
	// which is what lets the app move here without a device reboot: unlike
	// boot, join has to be its own trigger because a live single-to-cluster
	// transition never restarts zedkube. Nothing could have moved the app
	// before this point: the descheduler evicts a pod violating its
	// affinity only once some other node actually satisfies it, and before
	// the join above devName[0] was not yet a node the app could run on.
	finalMoveTimeout := 20 * time.Minute
	cluster.WaitUntilClusterInfoSatisfies(finalMoveTimeout,
		fmt.Sprintf("app %q to move to new non-seed %q", appDisplayName, devName[0]),
		func(info *eveinfo.ZInfoKubeCluster) bool {
			return appNodeName(info, appDisplayName) == devName[0]
		})
	verifyAppReachable(t, cluster, appUUID, appAuth, sshTimeout)
	evetest.Checkpoint("app-moved-to-new-non-seed")
}
