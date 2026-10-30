// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package types_test

import (
	"testing"

	"github.com/lf-edge/eve-api/go/info"
	"github.com/lf-edge/eve/pkg/pillar/types"
)

// TestClusterNodeConnectionToProto pins what is reported for each
// quorum state: only a lost quorum marks the cluster as down, as
// readiness of the node itself is not this report's business.
func TestClusterNodeConnectionToProto(t *testing.T) {
	cases := []struct {
		name       string
		conn       types.ClusterNodeConnection
		wantQuorum info.ZInfoClusterNodeQuorum
		wantStatus info.ZInfoClusterNodeStatus
		wantError  bool
	}{
		{
			name:       "unknown",
			conn:       types.ClusterNodeConnection{ClusterID: "c1"},
			wantQuorum: info.ZInfoClusterNodeQuorum_Z_INFO_CLUSTER_NODE_QUORUM_UNSPECIFIED,
			wantStatus: info.ZInfoClusterNodeStatus_Z_INFO_CLUSTER_NODE_STATUS_UNSPECIFIED,
		},
		{
			name:       "quorum established",
			conn:       types.ClusterNodeConnection{ClusterID: "c1", Quorum: types.ClusterQuorumEstablished},
			wantQuorum: info.ZInfoClusterNodeQuorum_Z_INFO_CLUSTER_NODE_QUORUM_ESTABLISHED,
			wantStatus: info.ZInfoClusterNodeStatus_Z_INFO_CLUSTER_NODE_STATUS_UNSPECIFIED,
		},
		{
			name: "lost quorum",
			conn: types.ClusterNodeConnection{
				ClusterID: "c1",
				Quorum:    types.ClusterQuorumLost,
			},
			wantQuorum: info.ZInfoClusterNodeQuorum_Z_INFO_CLUSTER_NODE_QUORUM_LOST,
			wantStatus: info.ZInfoClusterNodeStatus_Z_INFO_CLUSTER_NODE_STATUS_DOWN,
			wantError:  true,
		},
	}
	cases[2].conn.Error.SetErrorDescription(types.ErrorDescription{Error: "etcdserver: no leader"})
	for _, c := range cases {
		got := c.conn.ZInfoClusterNode()
		if got.GetClusterId() != "c1" {
			t.Errorf("%s: cluster id = %q, want c1", c.name, got.GetClusterId())
		}
		if got.GetQuorum() != c.wantQuorum {
			t.Errorf("%s: quorum = %v, want %v", c.name, got.GetQuorum(), c.wantQuorum)
		}
		if got.GetNodeStatus() != c.wantStatus {
			t.Errorf("%s: node status = %v, want %v", c.name, got.GetNodeStatus(), c.wantStatus)
		}
		if (got.GetError() != nil) != c.wantError {
			t.Errorf("%s: error set = %v, want %v", c.name, got.GetError() != nil, c.wantError)
		}
	}
}
