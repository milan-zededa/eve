// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package monitor

import "testing"

// TestSameAnnotations guards the check that keeps an unchanged tick from
// writing. Recovery status changes rarely and the tick runs every 15
// seconds, so patching regardless would be a steady stream of writes
// into etcd carrying no new information.
func TestSameAnnotations(t *testing.T) {
	cases := []struct {
		name string
		a, b map[string]string
		want bool
	}{
		{"both empty", map[string]string{}, map[string]string{}, true},
		{"identical", map[string]string{"k": "v"}, map[string]string{"k": "v"}, true},
		{"value differs", map[string]string{"k": "v"}, map[string]string{"k": "w"}, false},
		{"key added", map[string]string{"k": "v"},
			map[string]string{"k": "v", "j": "u"}, false},
		{"key removed", map[string]string{"k": "v", "j": "u"},
			map[string]string{"k": "v"}, false},
		{"against nil", map[string]string{"k": "v"}, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sameAnnotations(c.a, c.b); got != c.want {
				t.Errorf("sameAnnotations(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}
