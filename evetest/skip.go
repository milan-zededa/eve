// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package evetest

import (
	"strings"

	"github.com/lf-edge/eve/evetest/constants"
	"github.com/lf-edge/eve/pkg/pillar/utils/generics"
	"github.com/spf13/viper"
)

// skippedTestNames returns the list of test/suite names listed in
// EVETEST_SKIP, or nil if the variable is unset or empty.
func skippedTestNames() []string {
	raw := viper.GetString(constants.SkipEnv)
	if raw == "" {
		return nil
	}
	var names []string
	for _, name := range strings.Split(raw, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// matchedSkipName implements EVETEST_SKIP: it reports whether any of the
// given names is listed there, and if so, which one matched. Callers pass
// both a test's own name and, for a variant executed as part of a suite, the
// name of the underlying test function it was derived from (see
// TestCase.Test in RunTestSuite) -- EVETEST_SKIP may list either one, the
// latter skipping every variant of that subtest at once.
func matchedSkipName(names ...string) (matched string, skip bool) {
	skipped := skippedTestNames()
	for _, name := range names {
		if name != "" && generics.ContainsItem(skipped, name) {
			return name, true
		}
	}
	return "", false
}
