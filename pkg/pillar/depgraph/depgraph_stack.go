// Copyright (c) 2021 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

// A simple stack of planned (not yet sync-ed) item changes.

package depgraph

import "fmt"

// Changes not yet submitted with Sync().
type plannedChanges struct {
	items    map[string]itemChange  // key = item name
	clusters map[string]clusterInfo // key = cluster path; nodeCount not used here
}

type itemChange struct {
	itemName    string
	newValue    Item // nil represents Del operation
	clusterPath string
}

// Item inserted into the stack for processing inside DepGraph.Sync().
type stackedItem struct {
	itemName  string
	postOrder bool
}

// stack is a simple LIFO queue for item changes.
type stack struct {
	itemChanges []stackedItem
}

// newStack : returns a new instance of the stack.
func newStack() *stack {
	return &stack{
		itemChanges: make([]stackedItem, 0, 16),
	}
}

// isEmpty : will return a boolean indicating whether there are any elements on the stack.
func (s *stack) isEmpty() bool {
	return len(s.itemChanges) == 0
}

// push : Adds an element on the stack.
func (s *stack) push(item stackedItem) *stack {
	s.itemChanges = append(s.itemChanges, item)
	return s
}

// pop : removes an element from the stack and returns its value.
func (s *stack) pop() (stackedItem, error) {
	if len(s.itemChanges) == 0 {
		return stackedItem{}, fmt.Errorf("stack is empty")
	}
	element := s.itemChanges[len(s.itemChanges)-1]
	s.itemChanges = s.itemChanges[:len(s.itemChanges)-1]
	return element, nil
}
