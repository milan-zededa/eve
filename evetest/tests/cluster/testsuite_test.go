// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package cluster_test

import (
	"testing"

	"github.com/lf-edge/eve/evetest"
)

// TestNodeClusterSuite is the top-level entry point for cluster tests.
// It reuses the evetest harness (Adam controller, SDN, broker) across
// the subtests for efficiency wherever possible. All subtests pin the device
// to the Kubevirt hypervisor (aka eve-k).
//
// The single-node subtests run before the three-node one, and the happy-path
// purge runs before the fault-injecting VMIRS test, so a failure in the
// ordinary app lifecycle is not masked by chaos.
//
// Every cluster subtest re-creates its devices, because
// clusterDeviceRequirements sets CreateFromScratchWithLiveImage, which
// maybeReuseDevices always rejects. No subtest inherits cluster state from
// the one before it.
//
// TestClusterToSingleConversion is ordered after TestThreeNodesCluster
// regardless, since it converts a node out of a cluster and reads better
// as a variation following the plain three-node formation it starts from.
//
// TestTwoNodeHACluster runs last. It requests its own two-device,
// SeparateClusterPort topology, distinct from the three-device one used
// above, and is placed last simply because it has different requirements
// from all the other tests.
//
// Test parameters
// ---------------
//   - TPM (bool) via evetest.TPMParameter(). The suite passes the same
//     TPM choice to all subtests.
func TestNodeClusterSuite(test *testing.T) {
	evetest.Init(test)
	defer evetest.Close()

	// Define configurable parameters available for the test suite.
	evetest.DefineTestParameters(
		evetest.TPMParameter(),
	)

	evetest.RunTestSuite(
		evetest.TestCase{
			Test: TestSingleNodeCluster,
		},
		evetest.TestCase{
			Test: TestAppInstancePurge,
		},
		evetest.TestCase{
			Test: TestVMIRSStrandedReplicasRecovery,
		},
		evetest.TestCase{
			Test: TestThreeNodesCluster,
		},
		evetest.TestCase{
			Test: TestTieBreakerCluster,
		},
		evetest.TestCase{
			Test: TestClusterToSingleConversion,
		},
		evetest.TestCase{
			Test: TestTwoNodeHACluster,
		},
	)
}
