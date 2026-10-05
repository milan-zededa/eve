// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package quorum converges this node to the controller's quorum-loss
// recovery generation.
//
// Losing quorum is not something a device can fix on its own: with a
// majority of etcd members gone, the survivors cannot elect a leader or
// agree to evict anyone, so no amount of local retrying helps. The
// controller breaks the tie by naming a new seed and incrementing
// EdgeNodeCluster.quorum_recovery_generation. The node now named by
// join_server_ip forces its etcd into a single-member cluster; every
// other current or former member has its cluster config withdrawn
// outright instead and rejoins later through the ordinary join workflow,
// so this package never acts on their behalf.
package quorum

import (
	"fmt"

	"github.com/lf-edge/eve/pkg/kube/kube-init/k3s"
)

// Action is what a node must do to converge to a generation.
type Action int

// Actions.
const (
	// ActionNone: already converged, or a first sighting that only
	// needs recording.
	ActionNone Action = iota
	// ActionPromote: force this node's etcd into a single-member
	// cluster and become the sole bootstrap.
	ActionPromote
)

// String returns the action name, which is also what the intent marker
// records.
func (a Action) String() string {
	switch a {
	case ActionPromote:
		return "promote"
	default:
		return "none"
	}
}

// parseAction reads back what String wrote.
func parseAction(s string) (Action, error) {
	switch s {
	case "promote":
		return ActionPromote, nil
	case "none":
		return ActionNone, nil
	}
	return ActionNone, fmt.Errorf("unknown recovery action %q", s)
}

// Inputs is what the decision is made from.
type Inputs struct {
	// Want is the generation the controller is asking for.
	Want k3s.Generation
	// Applied is what this node last converged to, and Recorded says
	// whether it has ever recorded one.
	Applied  k3s.Generation
	Recorded bool
	// IsSeed is true when this node owns join_server_ip, which is the
	// controller's way of naming the survivor to promote.
	IsSeed bool
	// HasEtcdState is true when this node holds an initialised etcd
	// data directory, meaning it was a member of some cluster.
	HasEtcdState bool
}

// Decide returns the action that converges this node to Inputs.Want.
//
// Comparison is by inequality, never "newer than". The counter is a
// uint32 that a controller-side restore or a device reprovision can
// move backwards, and a node that ignored a decrease would keep state
// the rest of the cluster has discarded.
//
// Only the seed ever has anything to do here. A non-seed node seeing a
// generation it has not recorded means the controller withdrew its
// cluster config without this node noticing yet, or has not withdrawn
// it as expected - either way there is no action for this package to
// take on its behalf, since it converges by leaving and rejoining
// through the ordinary join workflow, not by wiping in place.
func Decide(in Inputs) Action {
	if in.Recorded && in.Applied == in.Want {
		return ActionNone
	}
	if !in.IsSeed {
		return ActionNone
	}
	if !in.Recorded {
		// Never recorded. Two very different situations look the same
		// here, and getting them the wrong way round is expensive.
		//
		// Counter 0 means no recovery has ever happened for this
		// cluster, so there is nothing to converge to: this is either a
		// node joining for the first time or, just as common, a node
		// that was already a healthy member before this code existed
		// and is meeting the marker for the first time after an EVE
		// upgrade. Acting on it would reset a working cluster.
		//
		// Above 0 a recovery has happened. A seed with no record but
		// with etcd state was already a member before this reset: its
		// data belongs to the cluster as it was before, so it still
		// has to promote. Without etcd state there is nothing to reset
		// from, so recording is enough.
		if in.Want.Counter == 0 || !in.HasEtcdState {
			return ActionNone
		}
	}
	return ActionPromote
}
