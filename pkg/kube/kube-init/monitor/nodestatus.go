// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package monitor

import (
	"context"
	"encoding/json"
	"log"

	"github.com/lf-edge/eve/pkg/kube/kube-init/encconfig"
	"github.com/lf-edge/eve/pkg/kube/kube-init/kubeclient"
	"github.com/lf-edge/eve/pkg/kube/kube-init/kubectlx"
	"github.com/lf-edge/eve/pkg/kube/kube-init/quorum"
	"github.com/lf-edge/eve/pkg/kube/kube-init/witnessstatus"
	"github.com/lf-edge/eve/pkg/pillar/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// lastStamped is the annotation set last written, so an unchanged tick
// costs nothing. Recovery status changes rarely and the tick is every
// 15 seconds; patching regardless would be a steady stream of writes
// into etcd for no new information.
var lastStamped map[string]string

// StampNodeStatus publishes what only this node knows onto its own Node
// object: how far it has converged to the controller's recovery
// generation, and, on the device hosting it, the witness's status.
//
// This is the route node-uuid already takes. The elected cluster-info
// reporter builds its message from the kube API, so anything it cannot
// read there has to be put there by the node that knows it, and a
// witness has no Node object of its own to carry its own.
func (m *Monitor) StampNodeStatus(ctx context.Context) {
	annotations := map[string]string{}

	// zedagent rejects a generation increase that does not name a
	// survivor before kube-init ever sees it, so there is no intent or
	// applied marker for the block below to notice - the rejection has
	// to be read back from the config that carries it instead.
	var recoveryErr string
	if cfg, ok := encconfig.Get(); ok {
		recoveryErr = cfg.QuorumRecoveryError
	}

	if applied, recorded, err := quorum.ReadApplied(); err != nil {
		log.Printf("warning: read applied generation: %v", err)
	} else if recorded || recoveryErr != "" {
		status := types.KubeQuorumRecoveryStatus{
			AppliedGeneration: applied.Counter,
		}
		if recorded {
			// When it got there, so a reader can tell a node that converged
			// a moment ago from one that has been at this generation since
			// it was first recorded.
			if at, recorded, err := quorum.AppliedAt(); err != nil {
				log.Printf("warning: read applied timestamp: %v", err)
			} else if recorded {
				status.LastTransitionAt = at
			}
			// An intent on disk means a convergence is under way, and its
			// generation is the one being worked towards rather than the
			// one reached.
			if in, found, err := quorum.ReadIntent(); err != nil {
				log.Printf("warning: read recovery intent: %v", err)
			} else if found {
				status.Converging = true
				status.LastTransitionAt = in.StartedAt
			}
		}
		if recoveryErr != "" {
			status.Error.SetErrorDescription(types.ErrorDescription{Error: recoveryErr})
		}
		if encoded, err := json.Marshal(status); err != nil {
			log.Printf("warning: encode recovery status: %v", err)
		} else {
			annotations[types.NodeRecoveryAnnotation] = string(encoded)
		}
	}

	if witnessstatus.Received() {
		status := witnessstatus.Get()
		if encoded, err := json.Marshal(status); err != nil {
			log.Printf("warning: encode witness status: %v", err)
		} else {
			annotations[types.NodeWitnessAnnotation] = string(encoded)
		}
	}

	if len(annotations) == 0 || sameAnnotations(annotations, lastStamped) {
		return
	}
	patch, err := kubectlx.BuildMergeLabelPatch(nil, annotations)
	if err != nil {
		log.Printf("warning: build node status patch: %v", err)
		return
	}
	nodes := kubeclient.Default().Clientset.CoreV1().Nodes()
	if _, err := nodes.Patch(ctx, m.deviceName, k8stypes.MergePatchType,
		patch, metav1.PatchOptions{}); err != nil {
		log.Printf("warning: stamp node status: %v", err)
		return
	}
	lastStamped = annotations
}

func sameAnnotations(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
