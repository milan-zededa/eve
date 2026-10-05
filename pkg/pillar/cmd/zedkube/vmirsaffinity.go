// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build k

package zedkube

import (
	"context"

	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/hypervisor"
	"github.com/lf-edge/eve/pkg/pillar/kubeapi"
	"github.com/lf-edge/eve/pkg/pillar/types"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	virtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
)

// reconcileVMIRSAffinity is the general safety net: on every tick,
// independent of any join event, it corrects a VMIRS's node affinity
// once the node it currently names has been confirmed gone from the
// cluster for good -- e.g. hardware replaced, or the Node object
// otherwise removed -- not merely unreachable right now. A node that is
// only temporarily down keeps its Node object, so this cannot misfire
// the instant it reconnects.
//
// This does not cover, and is not a substitute for, the join event
// (reconcileJoinedVMIPlacement): a live DNID reassignment while the
// previously-designated node is still healthy and in the cluster will
// never satisfy nodeExists==false here, by design -- that case has its
// own authoritative trigger and needs no staleness gate.
//
// It performs no rescale: once the stale node's own Pod object is
// actually gone (because the node itself is gone), the VMIRS controller
// creates its replacement from the now-corrected template on its own,
// unlike the join case where the previous Pod is still very much
// running and must be cycled explicitly.
func (z *zedkube) reconcileVMIRSAffinity(wdFunc func()) {
	sub := z.subAppInstanceConfig
	items := sub.GetAll()
	if len(items) == 0 {
		return
	}
	if !anyDesignatedVMI(items) {
		// Nothing for this node to reconcile; skip the kubeconfig/client
		// construction cost on this tick.
		return
	}

	config, err := kubeapi.GetKubeConfig()
	if err != nil {
		log.Errorf("reconcileVMIRSAffinity: get kubeconfig: %v", err)
		return
	}
	virtClient, err := kubecli.GetKubevirtClientFromRESTConfig(config)
	if err != nil {
		log.Errorf("reconcileVMIRSAffinity: kubevirt client: %v", err)
		return
	}
	nodeClient, err := getKubeClientSet()
	if err != nil {
		log.Errorf("reconcileVMIRSAffinity: clientset: %v", err)
		return
	}

	reconcileVMIRSAffinityWithClient(z.nodeName, virtClient, nodeClient, items, wdFunc)
}

// anyDesignatedVMI reports whether any item is a VMI-backed app for which
// this node is the DNID, i.e. whether reconcileVMIRSAffinityWithClient would
// do any real work for these items.
func anyDesignatedVMI(items map[string]interface{}) bool {
	for _, item := range items {
		aiconfig := item.(types.AppInstanceConfig)
		if aiconfig.IsDesignatedNodeID && aiconfig.FixedResources.VirtualizationMode != types.NOHYPER {
			return true
		}
	}
	return false
}

// reconcileVMIRSAffinityWithClient is reconcileVMIRSAffinity's testable
// core. Patching only spec.template.spec.affinity does not disturb a
// VMI already running: a ReplicaSet controller consults the template
// only when creating a NEW replica to satisfy the desired count, never
// retroactively for a replica already running.
func reconcileVMIRSAffinityWithClient(nodeName string, virtClient kubecli.KubevirtClient,
	nodeClient kubernetes.Interface, items map[string]interface{}, wdFunc func()) {
	for _, item := range items {
		wdFunc()

		aiconfig := item.(types.AppInstanceConfig)
		if !aiconfig.IsDesignatedNodeID {
			continue
		}
		if aiconfig.FixedResources.VirtualizationMode == types.NOHYPER {
			// Native containers use a plain Kubernetes ReplicaSet/Pod
			// template (CreateReplicaPodConfig), not a VMIRS.
			continue
		}

		vmiRsName := base.GetAppKubeNameWithPurge(aiconfig.DisplayName,
			aiconfig.UUIDandVersion.UUID, aiconfig.PurgeCmd.Counter+aiconfig.LocalPurgeCmd.Counter)

		getCtx, getCancel := context.WithTimeout(context.Background(), kubeAPITimeout)
		existing, err := virtClient.ReplicaSet(kubeapi.EVEKubeNameSpace).Get(getCtx, vmiRsName, metav1.GetOptions{})
		getCancel()
		if err != nil {
			if !errors.IsNotFound(err) {
				log.Errorf("reconcileVMIRSAffinity: get vmirs %s: %v", vmiRsName, err)
			}
			// Not found: nothing to reconcile yet, Start() will create it
			// with the correct affinity for whichever node activates it.
			continue
		}

		// Gate 1: the recorded affinity actually disagrees with the DNID.
		staleNode := vmirsAffinityNode(existing)
		if staleNode == "" || staleNode == nodeName {
			continue
		}
		// Gate 2: that disagreement is permanent, not a live node merely
		// being unreachable right now.
		if nodeExists(nodeClient, staleNode) {
			continue
		}

		existing.Spec.Template.Spec.Affinity = hypervisor.SetKubeAffinity(nodeName, aiconfig.AffinityType)
		updateCtx, updateCancel := context.WithTimeout(context.Background(), kubeAPITimeout)
		_, err = virtClient.ReplicaSet(kubeapi.EVEKubeNameSpace).Update(updateCtx, existing, metav1.UpdateOptions{})
		updateCancel()
		if err != nil {
			log.Errorf("reconcileVMIRSAffinity: update vmirs %s affinity: %v", vmiRsName, err)
			continue
		}
		log.Noticef("reconcileVMIRSAffinity: updated vmirs %s affinity to node %s (node %s no longer in cluster)",
			vmiRsName, nodeName, staleNode)
	}
}

// nodeExists reports whether a Node object named nodeName is currently
// registered with the cluster. Any error other than a clean "not found" is
// treated as still existing (fail safe): the caller only acts on affinity
// staleness once it can positively confirm the referenced node is gone for
// good, never on an ambiguous API error.
func nodeExists(clientset kubernetes.Interface, nodeName string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), kubeAPITimeout)
	defer cancel()
	_, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err == nil {
		return true
	}
	return !errors.IsNotFound(err)
}

// reconcileJoinedVMIPlacement is the handler for a pending join
// descheduler event (see pendingJoinDeschedulerEventPath). It runs
// exactly once per actual join, with the marker itself as the sole
// trigger and sole authorization -- unlike reconcileVMIRSAffinity's
// general per-tick safety net above, there is deliberately no staleness
// gate here, since the controller only ever reassigns an app's
// designated node as part of a node joining.
//
// For every VMI-backed app now designated to this node, it rewrites the
// VMIRS template affinity (if it still names a different node) and,
// only when it just did so, cycles the VMIRS so KubeVirt actually
// creates the VMI here. Patching the template without also cycling it
// would be a no-op: a running VMI's own affinity is immutable, so
// nothing else would ever revisit it to pick up the new template.
func (z *zedkube) reconcileJoinedVMIPlacement(wdFunc func()) {
	sub := z.subAppInstanceConfig
	items := sub.GetAll()

	config, err := kubeapi.GetKubeConfig()
	if err != nil {
		log.Errorf("reconcileJoinedVMIPlacement: get kubeconfig: %v", err)
		return
	}
	virtClient, err := kubecli.GetKubevirtClientFromRESTConfig(config)
	if err != nil {
		log.Errorf("reconcileJoinedVMIPlacement: kubevirt client: %v", err)
		return
	}

	listCtx, listCancel := context.WithTimeout(context.Background(), kubeAPITimeout)
	vmiList, err := virtClient.VirtualMachineInstance(kubeapi.EVEKubeNameSpace).List(listCtx, metav1.ListOptions{})
	listCancel()
	if err != nil {
		log.Errorf("reconcileJoinedVMIPlacement: list VMIs: %v", err)
		return
	}

	reconcileAndRescaleVMIs(z.nodeName, virtClient, vmiList, items, wdFunc)
}

// reconcileAndRescaleVMIs is reconcileJoinedVMIPlacement's testable core:
// given already-constructed clients and inputs, patch+cycle every
// VMI-backed app now designated to nodeName.
//
// wdFunc is invoked once per app so the watchdog budget resets between
// per-app API calls; see checkAppsFailover for the same pattern. Without
// it, N apps each incurring kubeAPITimeout-bounded Get+Update+cycle
// calls could together exceed the agent's errorTime budget.
func reconcileAndRescaleVMIs(nodeName string, virtClient kubecli.KubevirtClient,
	vmiList *virtv1.VirtualMachineInstanceList, items map[string]interface{}, wdFunc func()) {
	for _, item := range items {
		wdFunc()

		aiconfig := item.(types.AppInstanceConfig)
		if !aiconfig.IsDesignatedNodeID {
			continue
		}
		if aiconfig.FixedResources.VirtualizationMode == types.NOHYPER {
			// Native containers use a plain Kubernetes ReplicaSet/Pod
			// template (CreateReplicaPodConfig), not a VMIRS.
			continue
		}

		vmiRsName := base.GetAppKubeNameWithPurge(aiconfig.DisplayName,
			aiconfig.UUIDandVersion.UUID, aiconfig.PurgeCmd.Counter+aiconfig.LocalPurgeCmd.Counter)

		patched, err := patchVMIRSAffinity(virtClient, nodeName, vmiRsName, aiconfig.AffinityType)
		if err != nil {
			log.Errorf("reconcileAndRescaleVMIs: patch vmirs %s: %v", vmiRsName, err)
			continue
		}
		if !patched {
			// Already correct, or no VMIRS yet for Start() to have
			// created -- either way, nothing to cycle.
			continue
		}
		if vmi := findAppVMI(vmiList.Items, vmiRsName); vmi != nil && vmi.Status.NodeName == nodeName {
			// A previous cycle already landed it here.
			continue
		}

		log.Noticef("reconcileAndRescaleVMIs: cycling vmirs %s onto newly joined node %s",
			vmiRsName, nodeName)
		if err := kubeapi.DetachUtilVmirsReplicaReset(log, vmiRsName); err != nil {
			log.Errorf("reconcileAndRescaleVMIs: cycle vmirs %s: %v", vmiRsName, err)
		}
	}
}

// patchVMIRSAffinity rewrites vmiRsName's template affinity to nodeName
// when it currently names a different node. Returns whether it actually
// patched anything, so the caller can skip a pointless rescale cycle
// when the affinity already matched (or there is no VMIRS yet).
//
// Patching only spec.template.spec.affinity does not disturb a VMI
// already running: a ReplicaSet controller consults the template only
// when creating a NEW replica to satisfy the desired count, never
// retroactively for a replica already running.
func patchVMIRSAffinity(virtClient kubecli.KubevirtClient, nodeName, vmiRsName string,
	affinityType types.Affinity) (bool, error) {
	getCtx, getCancel := context.WithTimeout(context.Background(), kubeAPITimeout)
	existing, err := virtClient.ReplicaSet(kubeapi.EVEKubeNameSpace).Get(getCtx, vmiRsName, metav1.GetOptions{})
	getCancel()
	if err != nil {
		if errors.IsNotFound(err) {
			// Nothing to reconcile yet, Start() will create it with the
			// correct affinity for whichever node activates it.
			return false, nil
		}
		return false, err
	}

	staleNode := vmirsAffinityNode(existing)
	if staleNode == "" || staleNode == nodeName {
		return false, nil
	}

	existing.Spec.Template.Spec.Affinity = hypervisor.SetKubeAffinity(nodeName, affinityType)
	updateCtx, updateCancel := context.WithTimeout(context.Background(), kubeAPITimeout)
	_, err = virtClient.ReplicaSet(kubeapi.EVEKubeNameSpace).Update(updateCtx, existing, metav1.UpdateOptions{})
	updateCancel()
	if err != nil {
		return false, err
	}
	log.Noticef("patchVMIRSAffinity: updated vmirs %s affinity to node %s (was %s)",
		vmiRsName, nodeName, staleNode)
	return true, nil
}

// vmirsAffinityNode extracts the kubernetes.io/hostname value from the EVE-set
// node affinity in a VMIRS template spec. EVE encodes the owner node via
// hypervisor.SetKubeAffinity using either
// preferredDuringSchedulingIgnoredDuringExecution or
// requiredDuringSchedulingIgnoredDuringExecution. Returns "" if neither is
// present or the hostname matchExpression is absent.
func vmirsAffinityNode(vmirs *virtv1.VirtualMachineInstanceReplicaSet) string {
	if vmirs.Spec.Template == nil {
		return ""
	}
	aff := vmirs.Spec.Template.Spec.Affinity
	if aff == nil || aff.NodeAffinity == nil {
		return ""
	}
	na := aff.NodeAffinity
	for _, pref := range na.PreferredDuringSchedulingIgnoredDuringExecution {
		for _, expr := range pref.Preference.MatchExpressions {
			if expr.Key == "kubernetes.io/hostname" && len(expr.Values) > 0 {
				return expr.Values[0]
			}
		}
	}
	if req := na.RequiredDuringSchedulingIgnoredDuringExecution; req != nil {
		for _, term := range req.NodeSelectorTerms {
			for _, expr := range term.MatchExpressions {
				if expr.Key == "kubernetes.io/hostname" && len(expr.Values) > 0 {
					return expr.Values[0]
				}
			}
		}
	}
	return ""
}
