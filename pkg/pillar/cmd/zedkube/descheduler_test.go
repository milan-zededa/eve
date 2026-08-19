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
// Recording an event and deciding separately is the point of it: an event
// that happened before the operator's config was processed used to be
// lost for good, because neither recurs — a boot happens once, and a
// designation change is only observed at the moment it happens.
func TestDeschedulerPending(t *testing.T) {
	now := time.Now()
	windowOpen := now.Add(10 * time.Minute)
	windowShut := now.Add(-1 * time.Minute)

	boot := types.VmiDescheduleConfig{OnBoot: true}
	join := types.VmiDescheduleConfig{OnJoin: true}
	both := types.VmiDescheduleConfig{OnBoot: true, OnJoin: true}

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
		in:         deschedulerInputs{events: join, bootWindowEnd: windowOpen, now: now},
		wantReason: deschedulerReasonNone, wantOwed: false,
	}, {
		name: "join owed",
		in: deschedulerInputs{events: join, designationChanged: true,
			bootWindowEnd: windowOpen, now: now},
		wantReason: deschedulerReasonJoin, wantOwed: true,
	}, {
		// The config may be processed after the change was recorded; the
		// change is still owed when it arrives.
		name: "designation change owed after the boot event is done and the window shut",
		in: deschedulerInputs{events: both, designationChanged: true,
			triggeredSinceBoot: true, bootWindowEnd: windowShut, now: now},
		wantReason: deschedulerReasonJoin, wantOwed: true,
	}, {
		name: "designation changed but not configured",
		in: deschedulerInputs{events: boot, designationChanged: true,
			triggeredSinceBoot: true, bootWindowEnd: windowOpen, now: now},
		wantReason: deschedulerReasonNone, wantOwed: false,
	}, {
		name:       "nothing owed",
		in:         deschedulerInputs{events: both, triggeredSinceBoot: true, now: now},
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
