// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package quorum

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func stageMarkers(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	origApplied, origIntent := appliedPath, intentPath
	appliedPath = filepath.Join(dir, "applied")
	intentPath = filepath.Join(dir, "intent")
	t.Cleanup(func() { appliedPath, intentPath = origApplied, origIntent })
}

func TestAppliedRoundTrip(t *testing.T) {
	stageMarkers(t)

	if _, recorded, err := ReadApplied(); err != nil || recorded {
		t.Fatalf("absent marker gave (%v, %v); want not recorded, no error", recorded, err)
	}
	want := gen("cluster-a", 2)
	if err := WriteApplied(want); err != nil {
		t.Fatalf("WriteApplied: %v", err)
	}
	got, recorded, err := ReadApplied()
	if err != nil || !recorded || got != want {
		t.Errorf("ReadApplied = (%v, %v, %v), want %v recorded", got, recorded, err, want)
	}
}

func TestIntentRoundTrip(t *testing.T) {
	stageMarkers(t)

	want := Intent{
		Generation: gen("cluster-a", 1),
		Action:     ActionPromote,
		// Truncated: the on-disk form carries whole seconds.
		StartedAt: time.Unix(time.Now().Unix(), 0),
	}
	if err := WriteIntent(want); err != nil {
		t.Fatalf("WriteIntent: %v", err)
	}
	got, found, err := ReadIntent()
	if err != nil || !found {
		t.Fatalf("ReadIntent = (%v, %v, %v)", got, found, err)
	}
	if got.Generation != want.Generation || got.Action != want.Action ||
		!got.StartedAt.Equal(want.StartedAt) {
		t.Errorf("ReadIntent = %+v, want %+v", got, want)
	}
}

// TestResumeReturnsRecordedAction: the action must come from the
// intent, not from re-deciding. A config push landing mid-reset would
// otherwise restart the reset against a newer generation across a
// reboot, leaving a half-reset database.
func TestResumeReturnsRecordedAction(t *testing.T) {
	stageMarkers(t)

	want := Intent{Generation: gen("cluster-a", 1), Action: ActionPromote,
		StartedAt: time.Unix(1000, 0)}
	if err := WriteIntent(want); err != nil {
		t.Fatalf("WriteIntent: %v", err)
	}
	got, found, err := Resume()
	if err != nil || !found {
		t.Fatalf("Resume = (%v, %v, %v)", got, found, err)
	}
	if got.Action != ActionPromote {
		t.Errorf("Resume action = %v, want promote", got.Action)
	}
}

// TestResumeIgnoresFinishedWork: an intent for a generation already
// applied is a marker that outlived its work. Re-running it would
// repeat the reset on a node that has already recovered.
func TestResumeIgnoresFinishedWork(t *testing.T) {
	stageMarkers(t)

	g := gen("cluster-a", 1)
	if err := WriteIntent(Intent{Generation: g, Action: ActionPromote,
		StartedAt: time.Unix(1000, 0)}); err != nil {
		t.Fatalf("WriteIntent: %v", err)
	}
	if err := WriteApplied(g); err != nil {
		t.Fatalf("WriteApplied: %v", err)
	}
	_, found, err := Resume()
	if err != nil || found {
		t.Errorf("Resume = (found %v, err %v), want no work to resume", found, err)
	}
	if _, err := os.Stat(intentPath); err == nil {
		t.Error("a stale intent should have been cleared")
	}
}

// TestResumeAfterACrashMidAction: applied still names the previous
// generation, so the work is unfinished and has to be picked up.
func TestResumeAfterACrashMidAction(t *testing.T) {
	stageMarkers(t)

	if err := WriteApplied(gen("cluster-a", 0)); err != nil {
		t.Fatalf("WriteApplied: %v", err)
	}
	if err := WriteIntent(Intent{Generation: gen("cluster-a", 1),
		Action: ActionPromote, StartedAt: time.Unix(1000, 0)}); err != nil {
		t.Fatalf("WriteIntent: %v", err)
	}
	got, found, err := Resume()
	if err != nil || !found {
		t.Fatalf("Resume = (%v, %v, %v), want the unfinished action", got, found, err)
	}
	if got.Action != ActionPromote || got.Generation != gen("cluster-a", 1) {
		t.Errorf("Resume = %+v, want the recorded promote at generation 1", got)
	}
}

func TestClearIntentIsIdempotent(t *testing.T) {
	stageMarkers(t)
	if err := ClearIntent(); err != nil {
		t.Errorf("clearing an absent intent: %v", err)
	}
	if err := WriteIntent(Intent{Generation: gen("a", 1), Action: ActionPromote,
		StartedAt: time.Unix(1, 0)}); err != nil {
		t.Fatalf("WriteIntent: %v", err)
	}
	if err := ClearIntent(); err != nil {
		t.Fatalf("ClearIntent: %v", err)
	}
	if err := ClearIntent(); err != nil {
		t.Errorf("second ClearIntent: %v", err)
	}
}

func TestReadIntentRejectsMalformed(t *testing.T) {
	stageMarkers(t)
	for _, raw := range []string{"garbage", "a 1 promote", "a 1 nonsense 5", "a x promote 5"} {
		if err := os.WriteFile(intentPath, []byte(raw), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, _, err := ReadIntent(); err == nil {
			t.Errorf("ReadIntent accepted %q", raw)
		}
	}
}
