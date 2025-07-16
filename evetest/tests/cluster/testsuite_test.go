// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package cluster_test

import (
	"testing"

	"github.com/lf-edge/eve/evetest"
)

func TestNodeClusterSuite(test *testing.T) {
	evetest.Init(test)
	defer evetest.Close()

	evetest.RunTestSuite(
		evetest.TestCase{
			Test: TestSingleNodeCluster,
		},
		evetest.TestCase{
			Test: TestThreeNodesCluster,
		},
	)
}
