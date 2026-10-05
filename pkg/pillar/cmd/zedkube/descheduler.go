// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build k

package zedkube

import (
	"os"
	"time"

	"github.com/lf-edge/eve/pkg/pillar/kubeapi"
	"github.com/lf-edge/eve/pkg/pillar/types"
)

// deschedulerBootWindow is how long after kubernetes comes ready the boot
// event may still fire. A node that never becomes healthy enough to
// receive apps should stop asking rather than retry for its whole uptime.
const deschedulerBootWindow = 30 * time.Minute

// deschedulePendingLogEvery bounds how often a run that cannot start yet
// says why.
const deschedulePendingLogEvery = time.Minute

// pendingJoinDeschedulerEventPath must match
// k3s.PendingJoinDeschedulerEventPath in kube-init's clustermode
// package (pkg/kube/kube-init/k3s/config.go), which cannot be imported
// from here -- kube-init and pillar are separate Go modules. Keep the
// two literals in sync by hand.
//
// Under /persist/vault/kube, not kube-init's private /var/lib
// bind-mount: /persist is what's shared with this container.
const pendingJoinDeschedulerEventPath = "/persist/vault/kube/rancher/k3s/pending-join-descheduler-event"

// deschedulerInputs is everything the pending decision reads.
type deschedulerInputs struct {
	events             types.VmiDescheduleConfig
	joinEventPending   bool
	triggeredSinceBoot bool
	bootWindowEnd      time.Time
	now                time.Time
}

// deschedulerReason names why a run is owed, for the log.
type deschedulerReason string

const (
	deschedulerReasonNone deschedulerReason = ""
	deschedulerReasonBoot deschedulerReason = "boot"
	deschedulerReasonJoin deschedulerReason = "join"
)

// deschedulerPending reports whether a descheduler run is owed, and why.
//
// The join event is a one-shot request recorded on disk by kube-init
// (see pendingJoinDeschedulerEventPath) rather than decided here: kube-
// init already checked events.OnJoin once, at the join, before writing
// it, so this needs no further gating against in.events.
//
// The boot event has no such writer gating it elsewhere: zedkube is the
// only place that knows kubernetes just became ready (bootWindowEnd), so
// in.events.OnBoot has to be read live, here, each tick.
func deschedulerPending(in deschedulerInputs) (deschedulerReason, bool) {
	if in.joinEventPending {
		return deschedulerReasonJoin, true
	}
	if in.events.OnBoot && !in.triggeredSinceBoot &&
		!in.bootWindowEnd.IsZero() && in.now.Before(in.bootWindowEnd) {
		return deschedulerReasonBoot, true
	}
	return deschedulerReasonNone, false
}

// runDeschedulerIfPending is the single place a pending trigger is acted
// on. Called from the app-status tick, which already runs while
// kubernetes is serving.
//
// The two reasons drive different actions, not a shared one: boot goes
// through the generic descheduler Job, which works there because a pod
// still on its preferred node's old failover really does violate its own
// (immutable) affinity once that node is healthy again. A join has no
// such violation to offer -- the running pod's own affinity still names
// wherever it was created, so nothing about it looks wrong to that Job.
// What actually moves it is reconcileJoinedVMIPlacement, which patches
// the VMIRS template to this node and then cycles it directly.
func (z *zedkube) runDeschedulerIfPending(wdFunc func()) {
	reason, pending := deschedulerPending(z.deschedulerInputs())
	if !pending {
		return
	}
	ready, why, err := kubeapi.IsDeschedulerReadyWithReason(log, z.nodeName)
	if err != nil {
		log.Errorf("runDeschedulerIfPending: readiness check: %v", err)
		return
	}
	if !ready {
		// Rate-limited: this retries every tick, and a node that needs a
		// minute to settle would otherwise say so six times a minute.
		if time.Since(z.deschedulePendingLogged) > deschedulePendingLogEvery {
			z.deschedulePendingLogged = time.Now()
			log.Noticef("runDeschedulerIfPending: %s run waiting to receive apps: %s",
				reason, why)
		}
		return
	}
	switch reason {
	case deschedulerReasonJoin:
		z.reconcileJoinedVMIPlacement(wdFunc)
		if err := os.Remove(pendingJoinDeschedulerEventPath); err != nil && !os.IsNotExist(err) {
			log.Errorf("runDeschedulerIfPending: remove join marker: %v", err)
		}
	case deschedulerReasonBoot:
		if err := kubeapi.TriggerDescheduler(log, z.nodeName); err != nil {
			log.Errorf("runDeschedulerIfPending: trigger: %v", err)
			return
		}
		z.triggeredSinceBoot = true
	}
	log.Noticef("runDeschedulerIfPending: descheduler triggered (%s)", reason)
}

// vmiDescheduleEvents is the operator's configured trigger set, read back
// from what handleVmiDescheduleEventsOverride published.
func (z *zedkube) vmiDescheduleEvents() types.VmiDescheduleConfig {
	if v, ok := z.pubKubeConfig.GetAll()["global"].(types.KubeConfig); ok {
		return v.VmiDescheduleEvents
	}
	return types.VmiDescheduleConfig{}
}

// deschedulerInputs snapshots what the decision reads, so the decision
// itself can be tested without a pubsub publication.
func (z *zedkube) deschedulerInputs() deschedulerInputs {
	return deschedulerInputs{
		events:             z.vmiDescheduleEvents(),
		joinEventPending:   joinDeschedulerEventPending(),
		triggeredSinceBoot: z.triggeredSinceBoot,
		bootWindowEnd:      z.deschedulerBootWindowEnd,
		now:                time.Now(),
	}
}

// joinDeschedulerEventPending reports whether kube-init left this node a
// join descheduler event to act on. See pendingJoinDeschedulerEventPath.
func joinDeschedulerEventPending() bool {
	_, err := os.Stat(pendingJoinDeschedulerEventPath)
	return err == nil
}
