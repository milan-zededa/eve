// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build k

package zedkube

import (
	"context"
	"reflect"

	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/hypervisor"
	"github.com/lf-edge/eve/pkg/pillar/kubeapi"
	"github.com/lf-edge/eve/pkg/pillar/types"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kubevirt.io/client-go/kubecli"
)

// reconcileVMIRSAffinity corrects the node affinity recorded in an
// AppInstance's VMI ReplicaSet when it no longer matches this node, but only
// for apps whose cluster DNID is currently assigned to this node.
//
// The VMIRS's affinity is set once, by whichever node happens to create it
// (see hypervisor.CreateReplicaVMIConfig), and is never revisited by that
// creation path afterward. A cluster DNID reassignment (e.g. node
// replacement) does not update it, so it can keep pointing at a node that no
// longer runs (or never ran) this app.
//
// Gating on "am I the DNID node" -- rather than "am I the node currently
// running this app" -- is deliberate: a temporary failover (e.g. the DNID
// node reboots and the app runs elsewhere for a while, with DNID unchanged)
// must NOT rewrite the affinity to the failover node, since the descheduler
// (EnsureVMsDeschedulerAnnotated / RemovePodsViolatingNodeAffinity) relies on
// the affinity still pointing at the true home node to move the app back
// once it recovers. Only the actual DNID node ever corrects the affinity, and
// it does so unconditionally on its own periodic check, independent of
// whether it happens to be running the app right now.
//
// Patching only spec.template.spec.affinity does not disturb a VMI already
// running: a ReplicaSet controller consults the template only when creating
// a NEW replica to satisfy the desired count, never retroactively for a
// replica already running.
//
// wdFunc is invoked once per app so the watchdog budget resets between
// per-app API calls; see checkAppsFailover for the same pattern. Without it,
// N apps each incurring a kubeAPITimeout-bounded Get+Update could together
// exceed the agent's errorTime budget.
func (z *zedkube) reconcileVMIRSAffinity(wdFunc func()) {
	sub := z.subAppInstanceConfig
	items := sub.GetAll()
	if len(items) == 0 {
		return
	}
	designated := anyDesignatedVMI(items)
	// Logged on change rather than per tick: whether this node is the
	// designated one for any VMI app decides whether it reconciles
	// affinity at all, so a silent "no" is the first thing to rule out
	// when an app does not move.
	if designated != z.lastAnyDesignatedVMI || !z.anyDesignatedVMILogged {
		// The join event fires on the true->false->true edge only, and
		// only once this process has already seen a baseline: the first
		// sighting after this agent starts is this device's own boot,
		// which the boot event already covers, not a designation this
		// process watched happen.
		if z.anyDesignatedVMILogged && designated && !z.lastAnyDesignatedVMI {
			z.designationChanged = true
		}
		z.lastAnyDesignatedVMI = designated
		z.anyDesignatedVMILogged = true
		log.Noticef("reconcileVMIRSAffinity: this node is the designated node "+
			"for at least one VMI app: %t (of %d app configs)", designated, len(items))
	}
	if !designated {
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

	reconcileVMIRSAffinityWithClient(z.nodeName, virtClient, items, wdFunc)
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

// Rewrites the affinity of every VMIRS this node owns that no longer
// matches its app config. Only the template is rewritten: a running
// pod's affinity is immutable, so the app moves when the descheduler
// evicts it, not here.
func reconcileVMIRSAffinityWithClient(nodeName string, virtClient kubecli.KubevirtClient,
	items map[string]interface{}, wdFunc func()) {
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

		desired := hypervisor.SetKubeAffinity(nodeName, aiconfig.AffinityType)
		if reflect.DeepEqual(existing.Spec.Template.Spec.Affinity, desired) {
			continue
		}

		existing.Spec.Template.Spec.Affinity = desired
		updateCtx, updateCancel := context.WithTimeout(context.Background(), kubeAPITimeout)
		_, err = virtClient.ReplicaSet(kubeapi.EVEKubeNameSpace).Update(updateCtx, existing, metav1.UpdateOptions{})
		updateCancel()
		if err != nil {
			log.Errorf("reconcileVMIRSAffinity: update vmirs %s affinity: %v", vmiRsName, err)
			continue
		}
		log.Noticef("reconcileVMIRSAffinity: updated vmirs %s affinity (DNID reassignment)",
			vmiRsName)
	}
}

// rescaleDesignatedVMIs cycles the VMIRS of every VMI-backed app newly
// designated to this node whose VMI is not already running here, forcing
// KubeVirt to create a fresh one from the template reconcileVMIRSAffinity
// already rewrote.
//
// Nothing else moves it: the running VMI's own affinity is immutable and
// still names wherever it was created, so it is not violating anything
// the generic descheduler Job would ever notice, and the scheduler does
// not revisit a pod that is already running. A direct scale-cycle is the
// only way left to make it move.
func (z *zedkube) rescaleDesignatedVMIs(wdFunc func()) {
	sub := z.subAppInstanceConfig
	items := sub.GetAll()
	if len(items) == 0 {
		return
	}

	config, err := kubeapi.GetKubeConfig()
	if err != nil {
		log.Errorf("rescaleDesignatedVMIs: get kubeconfig: %v", err)
		return
	}
	virtClient, err := kubecli.GetKubevirtClientFromRESTConfig(config)
	if err != nil {
		log.Errorf("rescaleDesignatedVMIs: kubevirt client: %v", err)
		return
	}

	listCtx, listCancel := context.WithTimeout(context.Background(), kubeAPITimeout)
	vmiList, err := virtClient.VirtualMachineInstance(kubeapi.EVEKubeNameSpace).List(listCtx, metav1.ListOptions{})
	listCancel()
	if err != nil {
		log.Errorf("rescaleDesignatedVMIs: list VMIs: %v", err)
		return
	}

	for _, item := range items {
		wdFunc()

		aiconfig := item.(types.AppInstanceConfig)
		if !aiconfig.IsDesignatedNodeID {
			continue
		}
		if aiconfig.FixedResources.VirtualizationMode == types.NOHYPER {
			continue
		}

		vmiRsName := base.GetAppKubeNameWithPurge(aiconfig.DisplayName,
			aiconfig.UUIDandVersion.UUID, aiconfig.PurgeCmd.Counter+aiconfig.LocalPurgeCmd.Counter)
		if vmi := findAppVMI(vmiList.Items, vmiRsName); vmi != nil && vmi.Status.NodeName == z.nodeName {
			// Already here - the running VMI's own affinity was already
			// satisfied by wherever it was created, or a previous cycle
			// already landed it.
			continue
		}

		log.Noticef("rescaleDesignatedVMIs: cycling vmirs %s onto newly designated node %s",
			vmiRsName, z.nodeName)
		if err := kubeapi.DetachUtilVmirsReplicaReset(log, vmiRsName); err != nil {
			log.Errorf("rescaleDesignatedVMIs: cycle vmirs %s: %v", vmiRsName, err)
		}
	}
}
