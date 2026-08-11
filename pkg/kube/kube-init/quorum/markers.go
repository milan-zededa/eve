// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package quorum

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lf-edge/eve/pkg/kube/kube-init/state"
)

// On-disk records, under /var/lib so they live on the same
// vault-backed storage as the etcd data they describe and survive the
// reboots a recovery involves. Vars for test override.
var (
	// appliedPath records the generation this node has fully converged
	// to. Written only after etcd is healthy again.
	appliedPath = "/var/lib/quorum-recovery-applied"

	// intentPath records a convergence that has begun. Written before
	// anything destructive happens.
	intentPath = "/var/lib/quorum-recovery-intent"
)

// Intent is a convergence in progress.
type Intent struct {
	Generation Generation
	Action     Action
	StartedAt  time.Time
}

// String is the on-disk form: generation, action, start time.
func (i Intent) String() string {
	return fmt.Sprintf("%s %s %d", i.Generation, i.Action, i.StartedAt.Unix())
}

// ReadApplied returns the last generation this node converged to.
func ReadApplied() (g Generation, recorded bool, err error) {
	raw, err := os.ReadFile(appliedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Generation{}, false, nil
		}
		return Generation{}, false, fmt.Errorf("read %s: %w", appliedPath, err)
	}
	return ParseGeneration(string(raw))
}

// WriteApplied records convergence. Call only once the node is healthy
// in the recovered cluster: this is what stops the action repeating.
func WriteApplied(g Generation) error {
	if err := state.AtomicWriteFile(appliedPath, []byte(g.String()+"\n"), 0644); err != nil {
		return fmt.Errorf("write %s: %w", appliedPath, err)
	}
	return nil
}

// AppliedAt is when the recorded generation was written, which for a
// converged node is when it got there. Taken from the marker's mtime
// rather than its contents so the record survives a restart without
// widening the on-disk format.
func AppliedAt() (t time.Time, recorded bool, err error) {
	fi, err := os.Stat(appliedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("stat %s: %w", appliedPath, err)
	}
	return fi.ModTime(), true, nil
}

// ReadIntent returns a convergence that began and has not been
// committed, if any.
func ReadIntent() (in Intent, found bool, err error) {
	raw, err := os.ReadFile(intentPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Intent{}, false, nil
		}
		return Intent{}, false, fmt.Errorf("read %s: %w", intentPath, err)
	}
	fields := strings.Fields(string(raw))
	if len(fields) != 4 {
		return Intent{}, false, fmt.Errorf("malformed intent %q", strings.TrimSpace(string(raw)))
	}
	g, _, err := ParseGeneration(fields[0] + " " + fields[1])
	if err != nil {
		return Intent{}, false, err
	}
	action, err := parseAction(fields[2])
	if err != nil {
		return Intent{}, false, err
	}
	secs, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		return Intent{}, false, fmt.Errorf("malformed intent time %q", fields[3])
	}
	return Intent{
		Generation: g,
		Action:     action,
		StartedAt:  time.Unix(secs, 0),
	}, true, nil
}

// WriteIntent records that a convergence is starting. Must be called
// before the first destructive step.
//
// The pair exists because neither marker alone is safe. Recording only
// on completion means a crash after the wipe repeats it on the next
// boot, and the second wipe destroys a member that had already rejoined.
// Recording only up front means a crash before the work is done leaves
// the node believing it converged, holding etcd state from a cluster
// that no longer exists, and nothing ever revisits it.
func WriteIntent(in Intent) error {
	if err := state.AtomicWriteFile(intentPath, []byte(in.String()+"\n"), 0644); err != nil {
		return fmt.Errorf("write %s: %w", intentPath, err)
	}
	return nil
}

// ClearIntent removes the in-progress record. Call after WriteApplied.
func ClearIntent() error {
	if err := os.Remove(intentPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", intentPath, err)
	}
	return nil
}

// Resume returns the action to take on boot when a previous attempt did
// not finish.
//
// The action comes from the intent, never from re-deciding against the
// current cluster status. A config push landing mid-reset would
// otherwise restart the reset against a newer generation on the next
// reboot, leaving the database half-reset with no record of which
// attempt it belongs to.
func Resume() (in Intent, found bool, err error) {
	in, found, err = ReadIntent()
	if err != nil || !found {
		return Intent{}, false, err
	}
	applied, recorded, err := ReadApplied()
	if err != nil {
		return Intent{}, false, err
	}
	// An intent left over from a generation already applied is
	// finished work whose marker was not cleared.
	if recorded && applied == in.Generation {
		return Intent{}, false, ClearIntent()
	}
	return in, true, nil
}
