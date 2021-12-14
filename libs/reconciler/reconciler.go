// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lf-edge/eve/libs/depgraph"
)

// Reconciler implements state reconciliation using two dependency graphs,
// one modeling the current state and the other the intended state.
// For more information, please refer to README.md.
type Reconciler interface {
	// Reconcile : run state reconciliation. The function makes state transitions
	// (using Configurators) to get from the currentState (closer) to the intended
	// state. The function updates the currentState graph to reflect all the performed
	// changes.
	// Some state transitions may continue running asynchronously in the background,
	// see comments for the returned Status, and refer to README.md for even more detailed
	// documentation.
	Reconcile(ctx context.Context,
		currentState depgraph.Graph, intendedState depgraph.GraphR) Status
}

// reconciler implements Reconciler API
type reconciler struct {
	CR ConfiguratorRegistry
}

// New creates a new Reconciler.
// Note that reconciler is a stateless object and so there is no need to keep it
// after Reconcile() returns. Even if there are some async operations running
// in the background, you can resume the reconciliation with a new instance
// of Reconciler, just keep the graph with the current state (do not rebuild
// from scratch).
func New(cr ConfiguratorRegistry) Reconciler {
	return &reconciler{CR: cr}
}

// Status of a state reconciliation as returned by Reconcile().
type Status struct {
	// Err : non-nil if any state transition failed.
	Err error
	// NewCurrentState : updated graph with the current state.
	// If current state was passed as nil, this contains a newly created graph.
	NewCurrentState depgraph.Graph
	// OperationLog : log of all executed operations.
	OperationLog OperationLog
	// AsyncOpsInProgress : true if any state transition still continues running
	// asynchronously. When at least one of the asynchronous operations finalizes,
	// the returned channel ReadyToResume will fire.
	AsyncOpsInProgress bool
	// ReadyToResume : Fires when at least one of the asynchronous operations from
	// a previous reconciliation finalizes. Use this channel only until the next
	// reconciliation (even if the next reconciliation is for a different subgraph),
	// then replace it with the newly returned Status.ReadyToResume.
	// Returns name of the (sub)graph ready to continue reconciling.
	// This may be useful if you do selective reconciliations with subgraphs.
	ReadyToResume <-chan string
	// CancelAsyncOps : send cancel signal to all asynchronously running operations.
	// They will receive the signal through ctx.Done() and should respect it.
	CancelAsyncOps context.CancelFunc
	// WaitForAsyncOps : wait for all asynchronously running operations to complete.
	WaitForAsyncOps sync.WaitGroup
}

// OperationLog : log of all operations executed during a single Reconcile().
// Operations are ordered by StartTime.
type OperationLog []OpLogEntry

// OpLogEntry : log entry for a single operation executed during Reconcile().
// InProgress is returned as true and EndTime as zero value if the operation
// continues running asynchronously.
type OpLogEntry struct {
	Item       depgraph.Item
	Operation  depgraph.Operation
	StartTime  time.Time
	EndTime    time.Time
	InProgress bool
	Err        error
}

// String : a multi-line description of all executed operations during a single Reconcile().
func (l OperationLog) String() string {
	var ops []string
	for _, op := range l {
		var inProgress string
		if op.InProgress {
			inProgress = " (in-progress)"
		}
		var withError string
		if op.Err != nil {
			withError = " with error " + op.Err.Error()
		}
		ops = append(ops, fmt.Sprintf("[%v - %v]%s %s item type:%s name:%s%s",
			op.StartTime, op.EndTime, inProgress, strings.Title(op.Operation.String()),
			op.Item.Type(), op.Item.Name(), withError))
	}
	return strings.Join(ops, "\n")
}

// Reconcile : run state reconciliation. The function makes state transitions
// (using Configurators) to get from the currentState (closer) to the intended
// state. The function updates the currentState graph to reflect all the performed
// changes.
func (r *reconciler) Reconcile(ctx context.Context,
	currentState depgraph.Graph, intendedState depgraph.GraphR) Status {

	// TODO
	return Status{}
}
