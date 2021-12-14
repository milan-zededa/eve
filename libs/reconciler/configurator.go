// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"context"

	"github.com/lf-edge/eve/libs/depgraph"
)

// Configurator implements Create, Modify and Delete operations for items of the same type.
// For Reconciler it is a "backend" which the graph calls as needed to sync the actual and
// the intended state.
type Configurator interface {
	// Create should create the item (e.g. create a Linux bridge with the given parameters).
	Create(ctx context.Context, item depgraph.Item) error
	// Modify should change the item to the new desired state (e.g. change interface IP address).
	Modify(ctx context.Context, oldItem, newItem depgraph.Item) (err error)
	// Delete should remove the item (e.g. stop process).
	Delete(ctx context.Context, item depgraph.Item) error
	// NeedsRecreate should return true if changing the item to the new desired state
	// requires the item to be completely re-created. Reconciler will then perform the change
	// as Delete(oldItem) followed by Create(newItem) instead of calling Modify.
	NeedsRecreate(oldItem, newItem depgraph.Item) (recreate bool)
}

// ContinueInBackground allows to run Create/Modify/Delete asynchronously.
// If changing the state of an item requires to perform a long-running task,
// such as downloading a large file from the Internet, it is recommended
// to continue this work in the background in a separate Go routine, in order
// to not block other *independent* state transitions.
// Note that Reconciler ensures that two items might change their state in parallel
// only if there are no dependencies between them, either direct or transitive.
// And if there are any restrictions for parallel execution besides item dependencies,
// synchronization primitives like mutexes are always an option.
//
// Example Usage:
//
//     func (c *MyConfigurator) Create(ctx context.Context, item depgraph.Item) error {
//         done := reconciler.ContinueInBackground(ctx)
//         go func() {
//		       // Remember to stop if ctx.Done() fires (return error if failed to complete)
//		       err := longRunningTask(ctx)
//		       done(err)
//          }
//	        // exit immediately with nil error
//          return nil
//     }
func ContinueInBackground(ctx context.Context) (done func(error)) {
	// TODO
	return nil
}

// ConfiguratorRegistry implements mapping between items and configurators that manage
// their state transitions.
type ConfiguratorRegistry interface {
	// GetConfigurator returns configurator registered for the given item.
	// Returns nil if there is no configurator registered.
	GetConfigurator(item depgraph.Item) Configurator
}

// DefaultRegistry implements ConfiguratorMapper.
// It maps configurators to items based on item types, i.e. one Configurator for each
// item type (excluding external items).
type DefaultRegistry struct {}

// Register configurator for a given item type.
func (r *DefaultRegistry) Register(configurator Configurator, itemType string) error {
	// TODO
	return nil
}

// GetConfigurator returns configurator registered for the given item.
// Returns nil if there is no configurator registered.
func (r *DefaultRegistry) GetConfigurator(item depgraph.Item) Configurator {
	// TODO
	return nil
}