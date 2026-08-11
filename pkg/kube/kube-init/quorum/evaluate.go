// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package quorum

import (
	"log"
	"time"

	"github.com/lf-edge/eve/pkg/kube/kube-init/encconfig"
	"github.com/lf-edge/eve/pkg/kube/kube-init/k3s"
)

// Evaluate reports the action this node owes the controller's recovery
// generation, if any.
//
// Resuming an action already begun takes precedence over deciding
// afresh, so a crash mid-action cannot change its mind about what it
// was doing.
//
// A device with no cluster config has nothing to converge to, which is
// not the same as being at generation zero, so it is left alone.
//
// When nothing is owed, the current generation is recorded as the
// baseline. That is what makes a later change visible to a node that
// was powered off when it happened: without a record, absence and
// agreement look identical.
func Evaluate() (in Intent, needed bool, err error) {
	cs, err := k3s.GetClusterStatus()
	if err != nil {
		return Intent{}, false, nil //nolint:nilerr // not clustered
	}
	if resumed, found, err := Resume(); err != nil {
		return Intent{}, false, err
	} else if found {
		log.Printf("resuming %s for generation %s", resumed.Action, resumed.Generation)
		return resumed, true, nil
	}

	applied, recorded, err := ReadApplied()
	if err != nil {
		return Intent{}, false, err
	}
	want, isSeed := controllerIntent(cs)
	action := Decide(Inputs{
		Want:         want,
		Applied:      applied,
		Recorded:     recorded,
		IsSeed:       isSeed,
		HasEtcdState: k3s.EtcdClusterInitialized(),
	})
	if action == ActionNone {
		if !recorded || applied != want {
			if err := WriteApplied(want); err != nil {
				return Intent{}, false, err
			}
			log.Printf("recorded quorum-recovery baseline %s", want)
		}
		return Intent{}, false, nil
	}
	return Intent{Generation: want, Action: action, StartedAt: time.Now()}, true, nil
}

// controllerIntent is the generation this node owes and whether it is the
// seed, taken from the config zedagent publishes rather than the status
// zedkube mirrors from it.
//
// A recovery is asked for when the cluster has lost quorum, which is when
// zedkube's own Kubernetes calls stall and its status stops being
// refreshed: on a two-node cluster the promotion took nine minutes to
// arrive that way, against two seconds for the config. Everything after
// the reset may wait for zedkube — the refreshed status, the witness veth
// NIM plugs from it, the witness itself — because the reset is what makes
// those possible again. The reset must not.
//
// The status still supplies what the reset runs with, the decrypted token
// above all, which only zedkube can produce. That is safe: a recovery
// preserves the token, so the last status published before quorum was
// lost still describes it.
//
// Falls back to the status when no config has arrived, which is the shape
// every other reader here assumes.
func controllerIntent(cs *k3s.ClusterStatus) (want Generation, isSeed bool) {
	want = Generation{
		ClusterID: cs.ClusterID,
		Counter:   cs.QuorumRecoveryGeneration,
	}
	isSeed = cs.IsBootstrapNode
	if !encconfig.Present() {
		return want, isSeed
	}
	cfg, ok := encconfig.Get()
	if !ok {
		return want, isSeed
	}
	return Generation{
		ClusterID: cfg.ClusterID.UUID.String(),
		Counter:   cfg.QuorumRecoveryGeneration,
	}, cfg.BootstrapNode
}
