// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build k

package zedkube

import (
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

// deschedulerInputs is everything the pending decision reads.
type deschedulerInputs struct {
	events             types.VmiDescheduleConfig
	designationChanged bool
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
// Both events are recorded as facts when they happen and evaluated here,
// rather than each deciding for itself at the moment it occurs. An event
// that fires before the operator's config has been processed would
// otherwise be lost for good: neither recurs on its own, since a boot
// happens once and a designation change is only observed at the moment
// it happens.
//
// The boot event is bounded by a window opened when kubernetes came
// ready. A designation change is not: this node either still is or is
// not the app's designated node, and that stays true until it changes
// again.
func deschedulerPending(in deschedulerInputs) (deschedulerReason, bool) {
	if in.events.OnJoin && in.designationChanged {
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
// (immutable) affinity once that node is healthy again. A designation
// change has no such violation to offer - the running pod's own affinity
// still names wherever it was created, so nothing about it looks wrong to
// that Job. What actually moves it is rescaleDesignatedVMIs, a direct
// scale-cycle of the specific VMIRS this node was just designated for.
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
		z.rescaleDesignatedVMIs(wdFunc)
		z.designationChanged = false
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
		designationChanged: z.designationChanged,
		triggeredSinceBoot: z.triggeredSinceBoot,
		bootWindowEnd:      z.deschedulerBootWindowEnd,
		now:                time.Now(),
	}
}
