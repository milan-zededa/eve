// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package components

import (
	"testing"
	"time"
)

// TestLonghornNodeMissingAction pins the grace period the Longhorn
// Node-missing self-heal waits out before recreating: a brief,
// transient disappearance (e.g. a watch-reflector resync elsewhere in
// the cluster) must not trigger a recreate, since recreating mints a
// fresh disk UUID every pre-existing replica on the node can never
// resolve again.
func TestLonghornNodeMissingAction(t *testing.T) {
	const grace = 45 * time.Second
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name         string
		missingSince time.Time
		now          time.Time
		wantRecreate bool
		wantNewSince time.Time
	}{
		{
			name:         "first sighting just starts the clock",
			missingSince: time.Time{},
			now:          t0,
			wantRecreate: false,
			wantNewSince: t0,
		},
		{
			name:         "still within grace",
			missingSince: t0,
			now:          t0.Add(grace - time.Second),
			wantRecreate: false,
			wantNewSince: t0,
		},
		{
			name:         "exactly at grace recreates",
			missingSince: t0,
			now:          t0.Add(grace),
			wantRecreate: true,
			wantNewSince: t0,
		},
		{
			name:         "well past grace recreates",
			missingSince: t0,
			now:          t0.Add(10 * time.Minute),
			wantRecreate: true,
			wantNewSince: t0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			recreate, newSince := longhornNodeMissingAction(c.missingSince, c.now, grace)
			if recreate != c.wantRecreate {
				t.Errorf("recreate = %v, want %v", recreate, c.wantRecreate)
			}
			if !newSince.Equal(c.wantNewSince) {
				t.Errorf("newMissingSince = %v, want %v", newSince, c.wantNewSince)
			}
		})
	}
}

// TestLonghornNodeMissingActionResolvesCleanly covers the caller's own
// reset-on-presence behavior at the type level: once the object is
// observed present again, the caller sets longhornNodeMissingSince back
// to zero (see LonghornIsReady), so the next absence is treated as a
// first sighting again rather than inheriting a stale clock.
func TestLonghornNodeMissingActionResolvesCleanly(t *testing.T) {
	const grace = 45 * time.Second
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Simulate: missing, then present (caller resets to zero), then
	// missing again later -- must restart the grace period, not
	// recreate immediately on the strength of the earlier sighting.
	_, since := longhornNodeMissingAction(time.Time{}, t0, grace)
	since = time.Time{} // caller's reset on a successful Get
	recreate, newSince := longhornNodeMissingAction(since, t0.Add(time.Hour), grace)
	if recreate {
		t.Error("a fresh absence after a resolved one must not recreate immediately")
	}
	if !newSince.Equal(t0.Add(time.Hour)) {
		t.Errorf("newMissingSince = %v, want the restarted clock", newSince)
	}
}
