// Copyright (c) 2023 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package utils

// EqualSlices returns true if the two slices are equal in size
// and items they contain.
// This function can be used if slice items are comparable
// (operator "==" can be used).
func EqualSlices[Type comparable](list1, list2 []Type) bool {
	return EqualSlicesFn(list1, list2, func(a, b Type) bool {
		return a == b
	})
}

// EqualSlicesFn returns true if the two slices are equal in size
// and items they contain.
// Two slice items are compared using the provided "equal" callback.
func EqualSlicesFn[Type any](list1, list2 []Type, equal func(a, b Type) bool) bool {
	if len(list1) != len(list2) {
		return false
	}
	for i := range list1 {
		if !equal(list1[i], list2[i]) {
			return false
		}
	}
	return true
}
