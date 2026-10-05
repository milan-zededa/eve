// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build k

package zedkube

import (
	"testing"
	"time"

	"github.com/lf-edge/eve/pkg/pillar/types"
)

// TestDeschedulerPending covers the decision both trigger events share.
//
// The join event is already a one-shot fact by the time it reaches this
// decision (kube-init only wrote it because OnJoin was configured at the
// join), so unlike boot it needs no events check here — only its own
// presence matters, and it takes priority over a simultaneously-pending
// boot. The boot event has no such writer gating it elsewhere, so
// events.OnBoot is still read live, here, each tick.
func TestDeschedulerPending(t *testing.T) {
	now := time.Now()
	windowOpen := now.Add(10 * time.Minute)
	windowShut := now.Add(-1 * time.Minute)

	boot := types.VmiDescheduleConfig{OnBoot: true}

	cases := []struct {
		name       string
		in         deschedulerInputs
		wantReason deschedulerReason
		wantOwed   bool
	}{{
		name:       "boot owed while the window is open",
		in:         deschedulerInputs{events: boot, bootWindowEnd: windowOpen, now: now},
		wantReason: deschedulerReasonBoot, wantOwed: true,
	}, {
		name: "boot satisfied once triggered",
		in: deschedulerInputs{events: boot, triggeredSinceBoot: true,
			bootWindowEnd: windowOpen, now: now},
		wantReason: deschedulerReasonNone, wantOwed: false,
	}, {
		name:       "boot expires with the window",
		in:         deschedulerInputs{events: boot, bootWindowEnd: windowShut, now: now},
		wantReason: deschedulerReasonNone, wantOwed: false,
	}, {
		// Zero means WaitForKubernetes has not returned: there is no
		// cluster to act on yet.
		name:       "boot waits for kubernetes",
		in:         deschedulerInputs{events: boot, now: now},
		wantReason: deschedulerReasonNone, wantOwed: false,
	}, {
		name:       "boot not configured",
		in:         deschedulerInputs{now: now},
		wantReason: deschedulerReasonNone, wantOwed: false,
	}, {
		name:       "join owed",
		in:         deschedulerInputs{joinEventPending: true, now: now},
		wantReason: deschedulerReasonJoin, wantOwed: true,
	}, {
		// Join takes priority even when a boot run is also pending.
		name: "join owed ahead of a pending boot",
		in: deschedulerInputs{events: boot, joinEventPending: true,
			bootWindowEnd: windowOpen, now: now},
		wantReason: deschedulerReasonJoin, wantOwed: true,
	}, {
		name:       "nothing owed",
		in:         deschedulerInputs{events: boot, triggeredSinceBoot: true, now: now},
		wantReason: deschedulerReasonNone, wantOwed: false,
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reason, owed := deschedulerPending(c.in)
			if owed != c.wantOwed || reason != c.wantReason {
				t.Errorf("deschedulerPending() = (%q, %t), want (%q, %t)",
					reason, owed, c.wantReason, c.wantOwed)
			}
		})
	}
}
