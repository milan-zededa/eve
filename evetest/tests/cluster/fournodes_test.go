// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package cluster_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	// revive:disable:dot-imports
	. "github.com/onsi/gomega"

	"github.com/lf-edge/eve/evetest"
	"github.com/lf-edge/eve/evetest/netmodels"
)

// TestFourNodesCluster extends TestThreeNodesCluster: once the three-node
// cluster is healthy, a fourth EVE-K device is added to it as a worker (Agent)
// node, and the test verifies that the node joins and then stays in the
// cluster.
//
// The controller-side shape mirrors what a real controller sends when a node is
// added to a running cluster: the new node is listed as an Agent
// (EdgeNodeCluster.is_worker_node) and every node's config carries
// master_node_uuids with the three original nodes. The "stays in the cluster"
// part matters because the node first appears Ready and can be removed again
// shortly after (see EV-2854), so a single readiness check is not enough.
//
// Network model
// -------------
//   - netmodels.SeparateClusterPort for four devices, the same topology as
//     TestThreeNodesCluster with one more eth0/eth1 pair on the shared bridges.
//
// Device configuration
// --------------------
//   - Four RequireEdgeDevice entries built via clusterDeviceRequirements.
//   - The initial cluster config is newThreeNodeClusterConfig for the first
//     three devices. The fourth device is onboarded but not configured.
//   - EdgeClusterConfig.AddNode adds the fourth device with ClusterIP
//     10.244.244.5 on ethernet1 and Worker=true.
//
// Test parameters
// ---------------
//   - TPM via evetest.TPMParameter().
//   - FILESYSTEM (ext4|zfs, defaults to ext4) via evetest.FilesystemParameter().
//
// Phases
// ------
//  1. three-nodes-are-ready: the three-node cluster forms, as in
//     TestThreeNodesCluster.
//  2. fourth-node-config-applied: AddNode with Worker=true, re-apply to all
//     four devices.
//  3. four-nodes-are-ready: WaitUntilNodesAreReady over all four devices.
//  4. fourth-node-stays: Kubernetes, asked through the first node, keeps
//     listing all four nodes as Ready for several minutes, and the EVE API
//     still reports all four afterwards.
//
// Suite placement
// ---------------
//   - TestNodeClusterSuite, after the three-node tests. Its four-device
//     requirements match no other test, so it does not reuse their devices.
func TestFourNodesCluster(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()

	evetest.DefineTestParameters(
		evetest.TPMParameter(),
		evetest.FilesystemParameter(),
	)
	withTPM := evetest.GetTPMParameterValue()
	filesystem := evetest.GetFilesystemParameterValue()

	const numNodes = 4
	devNames := make([]string, numNodes)
	var requirements []evetest.Requirement
	for i := 0; i < numNodes; i++ {
		devNames[i] = fmt.Sprintf("edge-dev%d", i+1)
		requirements = append(requirements,
			clusterDeviceRequirements(devNames[i], withTPM, filesystem, false))
	}
	requirements = append(requirements, evetest.RequireNetworkModel{
		NetworkModel: netmodels.SeparateClusterPort(devNames...),
	})
	evetest.Setup(requirements...)
	evetest.Checkpoint("setup-done")

	log := evetest.Logger()
	clusterConfig := newThreeNodeClusterConfig([3]string(devNames[:3]))
	cluster := evetest.NewEdgeCluster("test-cluster")
	cluster.ApplyConfig(clusterConfig, true, true)
	cluster.WaitUntilNodesAreReady(30 * time.Minute)
	evetest.Checkpoint("three-nodes-are-ready")

	clusterConfig.AddNode(evetest.ClusterNode{
		DevName:          devNames[3],
		ClusterIP:        evetest.IPAddressWithPrefix("10.244.244.5/24"),
		ClusterInterface: "ethernet1",
		Worker:           true,
	})
	cluster.ApplyConfig(clusterConfig, true, true)
	evetest.Checkpoint("fourth-node-config-applied")

	cluster.WaitUntilNodesAreReady(30 * time.Minute)
	evetest.Checkpoint("four-nodes-are-ready")

	// EV-2854: the node was removed 1-3 minutes after it first reported Ready.
	log.Infof("Verifying that all four nodes stay in the cluster")
	first := evetest.GetEdgeDevice(devNames[0])
	t.Consistently(func() ([]string, error) {
		return readyKubeNodeNames(first)
	}, 6*time.Minute, 15*time.Second).Should(ConsistOf(devNames),
		"Kubernetes should keep reporting all four nodes as Ready")
	cluster.WaitForFreshNodesReady(10 * time.Minute)
	evetest.Checkpoint("fourth-node-stays")
}

// readyKubeNodeNames returns the sorted names of the Kubernetes nodes that
// report Ready, as seen from dev. An error means kubectl was unreachable and
// must be retried by the caller.
func readyKubeNodeNames(dev *evetest.EdgeDevice) ([]string, error) {
	const jsonpath = `{range .items[*]}{.metadata.name}{" "}` +
		`{range .status.conditions[?(@.type=="Ready")]}{.status}{end}{"\n"}{end}`
	out, err := dev.RunKubectl("get nodes -o jsonpath='"+jsonpath+"'", 30*time.Second)
	if err != nil {
		return nil, err
	}
	var ready []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == "True" {
			ready = append(ready, fields[0])
		}
	}
	return ready, nil
}
