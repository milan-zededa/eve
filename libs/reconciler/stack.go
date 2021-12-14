// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

// A simple stack used for DFS graph traversal.

package reconciler

import "fmt"

// Item inserted into the stack for processing inside Reconciler.Reconcile().
type stackedItem struct {
	itemType  string
	itemName  string
	postOrder bool
}

// stack is a simple LIFO queue for item changes.
type stack struct {
	stackedItems []stackedItem
}

// newStack : returns a new instance of the stack.
func newStack() *stack {
	return &stack{
		stackedItems: make([]stackedItem, 0, 16),
	}
}

// isEmpty : will return a boolean indicating whether there are any elements on the stack.
func (s *stack) isEmpty() bool {
	return len(s.stackedItems) == 0
}

// push : Adds an element on the stack.
func (s *stack) push(item stackedItem) *stack {
	s.stackedItems = append(s.stackedItems, item)
	return s
}

// pop : removes an element from the stack and returns its value.
func (s *stack) pop() (stackedItem, error) {
	if len(s.stackedItems) == 0 {
		return stackedItem{}, fmt.Errorf("stack is empty")
	}
	element := s.stackedItems[len(s.stackedItems)-1]
	s.stackedItems = s.stackedItems[:len(s.stackedItems)-1]
	return element, nil
}
