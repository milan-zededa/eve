// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package quorum

import "testing"

func gen(id string, n uint32) Generation { return Generation{ClusterID: id, Counter: n} }

// TestDecide is the contract for when a node converges. The expensive
// mistakes are in both directions: acting when it should not resets a
// healthy cluster, and not acting when it should leaves a node holding
// etcd state from a cluster that no longer exists.
func TestDecide(t *testing.T) {
	cases := []struct {
		name string
		in   Inputs
		want Action
	}{
		{
			name: "converged",
			in: Inputs{Want: gen("a", 1), Applied: gen("a", 1), Recorded: true,
				HasEtcdState: true},
			want: ActionNone,
		},
		{
			name: "seed promotes on a bump",
			in: Inputs{Want: gen("a", 1), Applied: gen("a", 0), Recorded: true,
				IsSeed: true, HasEtcdState: true},
			want: ActionPromote,
		},
		{
			// The node powered off across the increment. It has a
			// record from when it joined, so the difference is visible
			// even though it was absent for the change - but with only
			// two physical nodes, seeing this at all as a non-seed means
			// the controller has not withdrawn its cluster config yet,
			// which is not this package's problem to solve.
			name: "non-seed does nothing on a bump",
			in: Inputs{Want: gen("a", 1), Applied: gen("a", 0), Recorded: true,
				HasEtcdState: true},
			want: ActionNone,
		},
		{
			// A counter that moves backwards still means the cluster
			// changed under this node. Treating only increases as real
			// would leave a seed on state everyone else discarded.
			name: "seed promotes even on a decrease",
			in: Inputs{Want: gen("a", 1), Applied: gen("a", 3), Recorded: true,
				IsSeed: true, HasEtcdState: true},
			want: ActionPromote,
		},
		{
			name: "seed promotes for a different cluster id",
			in: Inputs{Want: gen("b", 0), Applied: gen("a", 0), Recorded: true,
				IsSeed: true, HasEtcdState: true},
			want: ActionPromote,
		},
		{
			// First boot of a fresh node. Nothing to converge to.
			name: "first sighting at generation 0 only records",
			in:   Inputs{Want: gen("a", 0)},
			want: ActionNone,
		},
		{
			// The upgrade case, and the one that would hurt most: a
			// healthy member meeting the marker for the first time
			// after an EVE upgrade. It has etcd state and no record,
			// but no recovery has ever happened, so acting here would
			// reset a working cluster.
			name: "healthy member upgrading into this code only records",
			in:   Inputs{Want: gen("a", 0), IsSeed: true, HasEtcdState: true},
			want: ActionNone,
		},
		{
			// A node with no etcd state has nothing to reset from,
			// however far the counter has moved.
			name: "fresh seed joining after a recovery only records",
			in:   Inputs{Want: gen("a", 2), IsSeed: true},
			want: ActionNone,
		},
		{
			// The narrow gap: a seed whose first boot under this code
			// happens only after a recovery. No record, but its etcd
			// belongs to the cluster as it was before.
			name: "seed that missed the record still promotes",
			in:   Inputs{Want: gen("a", 1), IsSeed: true, HasEtcdState: true},
			want: ActionPromote,
		},
		{
			// A non-seed with no record needs no nuance at all: nothing
			// this package does ever applies to it.
			name: "non-seed that missed the record still does nothing",
			in:   Inputs{Want: gen("a", 1), HasEtcdState: true},
			want: ActionNone,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Decide(c.in); got != c.want {
				t.Errorf("Decide() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestGenerationRoundTrip(t *testing.T) {
	cases := []Generation{
		{ClusterID: "7c9e6679-7425-40de-944b-e07fc1f90ae7", Counter: 0},
		{ClusterID: "7c9e6679-7425-40de-944b-e07fc1f90ae7", Counter: 4294967295},
	}
	for _, want := range cases {
		got, recorded, err := ParseGeneration(want.String())
		if err != nil || !recorded || got != want {
			t.Errorf("round trip of %v gave (%v, %v, %v)", want, got, recorded, err)
		}
	}
}

// TestParseGenerationEmptyIsNotAnError: an absent marker is the normal
// state on a first boot, not a failure to report.
func TestParseGenerationEmptyIsNotAnError(t *testing.T) {
	for _, in := range []string{"", "   ", "\n"} {
		got, recorded, err := ParseGeneration(in)
		if err != nil || recorded || got != (Generation{}) {
			t.Errorf("ParseGeneration(%q) = (%v, %v, %v)", in, got, recorded, err)
		}
	}
}

func TestParseGenerationRejectsMalformed(t *testing.T) {
	for _, in := range []string{"cluster-a", "cluster-a x", "cluster-a -1", "cluster-a 1 2 3"} {
		if _, _, err := ParseGeneration(in); err == nil {
			t.Errorf("ParseGeneration(%q) accepted malformed input", in)
		}
	}
}

func TestActionRoundTrip(t *testing.T) {
	for _, want := range []Action{ActionNone, ActionPromote} {
		got, err := parseAction(want.String())
		if err != nil || got != want {
			t.Errorf("round trip of %v gave (%v, %v)", want, got, err)
		}
	}
	if _, err := parseAction("nonsense"); err == nil {
		t.Error("parseAction accepted an unknown action")
	}
}
