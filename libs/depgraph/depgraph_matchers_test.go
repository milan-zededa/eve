// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package depgraph_test

import (
	"fmt"

	"github.com/onsi/gomega/format"
	"github.com/onsi/gomega/types"

	"github.com/lf-edge/eve/libs/depgraph"
)

// OperationMatcher : match operation inside operationsLog.
type OperationMatcher interface {
	types.GomegaMatcher
	WithError(errMsg string) types.GomegaMatcher
	Before(item mockItem) RefOperationMatcher
	After(item mockItem) RefOperationMatcher
}

// RefOperationMatcher : reference another operation and check the relative ordering.
type RefOperationMatcher interface {
	types.GomegaMatcher
	IsCreated() types.GomegaMatcher
	IsDeleted() types.GomegaMatcher
	IsModified() types.GomegaMatcher
	IsRecreated() types.GomegaMatcher
}

type expectedOp struct {
	item mockItem
	op   depgraph.Operation
}

// BeCreated : Expect item to be created inside DepGraph.Sync().
func BeCreated() OperationMatcher {
	return &opMatcher{
		expOp: depgraph.OperationCreate,
	}
}

// BeDeleted : Expect item to be deleted inside DepGraph.Sync().
func BeDeleted() OperationMatcher {
	return &opMatcher{
		expOp: depgraph.OperationDelete,
	}
}

// BeModified : Expect item to be modified inside DepGraph.Sync().
func BeModified() OperationMatcher {
	return &opMatcher{
		expOp: depgraph.OperationModify,
	}
}

// BeRecreated : Expect item to be re-created inside DepGraph.Sync().
func BeRecreated() OperationMatcher {
	return &opMatcher{
		expOp: depgraph.OperationRecreate,
	}
}

// opMatcher implements OperationMatcher
type opMatcher struct {
	expOp     depgraph.Operation
	expBefore *expectedOp
	expAfter  *expectedOp
	expError  string
}

func (m *opMatcher) Match(actual interface{}) (success bool, err error) {
	item, ok := actual.(mockItem)
	if !ok {
		return false, fmt.Errorf("OperationMatcher expects a mock Item")
	}
	opLog := ctx.opsLog.find(item, m.expOp)
	if opLog == nil {
		return false, nil
	}
	var opErr string
	if opLog.err != nil {
		opErr = opLog.err.Error()
	}
	if m.expError != opErr {
		return false, nil
	}
	if m.expBefore != nil {
		opLog2 := ctx.opsLog.find(m.expBefore.item, m.expBefore.op)
		if opLog2 == nil {
			return false, nil
		}
		if opLog.index > opLog2.index {
			return false, nil
		}
	}
	if m.expAfter != nil {
		opLog2 := ctx.opsLog.find(m.expAfter.item, m.expAfter.op)
		if opLog2 == nil {
			return false, nil
		}
		if opLog.index < opLog2.index {
			return false, nil
		}
	}
	return true, nil
}

func expOpToString(expOp depgraph.Operation) string {
	switch expOp {
	case depgraph.OperationCreate:
		return "created"
	case depgraph.OperationDelete:
		return "deleted"
	case depgraph.OperationModify:
		return "modified"
	case depgraph.OperationRecreate:
		return "recreated"
	}
	return "<unknown>"
}

func (m *opMatcher) failureMessage(actual interface{}, negated bool) (message string) {
	var expVerb, expOp, expErr, expOrder string
	expVerb = "be"
	if negated {
		expVerb = "NOT " + expVerb
	}
	expOp = expOpToString(m.expOp)
	expErr = "successfully"
	if m.expError != "" {
		expErr = fmt.Sprintf("with error %s", m.expError)
	}
	if m.expBefore != nil {
		expOrder = fmt.Sprintf("before item\n%s is %s",
			format.Object(m.expBefore.item, 1),
			expOpToString(m.expBefore.op))
	}
	if m.expAfter != nil {
		expOrder = fmt.Sprintf("after item\n%s is %s",
			format.Object(m.expAfter.item, 1),
			expOpToString(m.expAfter.op))
	}
	actualOps := fmt.Sprintf("Actual Sync operations:\n%v", ctx.opsLog)
	return fmt.Sprintf("Expected\n%s\nto %s %s %s %s\n%s",
		format.Object(actual, 1), expVerb, expOp, expErr, expOrder,
		actualOps)

}

func (m *opMatcher) FailureMessage(actual interface{}) (message string) {
	return m.failureMessage(actual, false)
}

func (m *opMatcher) NegatedFailureMessage(actual interface{}) (message string) {
	return m.failureMessage(actual, true)
}

func (m *opMatcher) WithError(errMsg string) types.GomegaMatcher {
	m.expError = errMsg
	return m
}

func (m *opMatcher) Before(item mockItem) RefOperationMatcher {
	m.expBefore = &expectedOp{
		item: item,
		op:   m.expOp,
	}
	return m
}

func (m *opMatcher) After(item mockItem) RefOperationMatcher {
	m.expAfter = &expectedOp{
		item: item,
		op:   m.expOp,
	}
	return m
}

func (m *opMatcher) IsCreated() types.GomegaMatcher {
	if m.expBefore != nil {
		m.expBefore.op = depgraph.OperationCreate
	}
	if m.expAfter != nil {
		m.expAfter.op = depgraph.OperationCreate
	}
	return m
}

func (m *opMatcher) IsModified() types.GomegaMatcher {
	if m.expBefore != nil {
		m.expBefore.op = depgraph.OperationModify
	}
	if m.expAfter != nil {
		m.expAfter.op = depgraph.OperationModify
	}
	return m
}

func (m *opMatcher) IsDeleted() types.GomegaMatcher {
	if m.expBefore != nil {
		m.expBefore.op = depgraph.OperationDelete
	}
	if m.expAfter != nil {
		m.expAfter.op = depgraph.OperationDelete
	}
	return m
}

func (m *opMatcher) IsRecreated() types.GomegaMatcher {
	if m.expBefore != nil {
		m.expBefore.op = depgraph.OperationRecreate
	}
	if m.expAfter != nil {
		m.expAfter.op = depgraph.OperationRecreate
	}
	return m
}
