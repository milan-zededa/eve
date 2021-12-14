// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package reconciler_test

import (
	"fmt"
	"reflect"

	"github.com/onsi/gomega/format"
	"github.com/onsi/gomega/types"

	"github.com/lf-edge/eve/libs/depgraph"
	"github.com/lf-edge/eve/libs/reconciler"
)

// BeMockItem checks if expectation matches the given mock Item.
func BeMockItem(item mockItem) types.GomegaMatcher {
	return &mockItemMatcher{expItem: item}
}

// OperationMatcher : matcher for synchronous operation
type OperationMatcher interface {
	types.GomegaMatcher
	WithError(errMsg string) types.GomegaMatcher
	Before(item mockItem) RefOperationMatcherWithAsync
	After(item mockItem) RefOperationMatcher
}

// AsyncOperationMatcher : matcher for asynchronous operation
type AsyncOperationMatcher interface {
	types.GomegaMatcher
	After(item mockItem) RefOperationMatcher
}

// RefOperationMatcher : reference another synchronous operation and check
// the relative ordering.
type RefOperationMatcher interface {
	types.GomegaMatcher
	IsCreated() types.GomegaMatcher
	IsDeleted() types.GomegaMatcher
	IsModified() types.GomegaMatcher
	IsRecreated() types.GomegaMatcher
}

// RefOperationMatcher : reference another operation and check the relative ordering.
type RefOperationMatcherWithAsync interface {
	RefOperationMatcher
	IsBeingCreated() types.GomegaMatcher
	IsBeingDeleted() types.GomegaMatcher
	IsBeingModified() types.GomegaMatcher
	IsBeingRecreated() types.GomegaMatcher
}

func BeCreated() OperationMatcher {
	return &opMatcher{
		expOp: depgraph.OperationCreate,
	}
}

func BeDeleted() OperationMatcher {
	return &opMatcher{
		expOp: depgraph.OperationDelete,
	}
}

func BeModified() OperationMatcher {
	return &opMatcher{
		expOp: depgraph.OperationModify,
	}
}

func BeRecreated() OperationMatcher {
	return &opMatcher{
		expOp: depgraph.OperationRecreate,
	}
}

func BeingCreated() AsyncOperationMatcher {
	return &opMatcher{
		expOp:         depgraph.OperationCreate,
		expInProgress: true,
	}
}

func BeingDeleted() AsyncOperationMatcher {
	return &opMatcher{
		expOp:         depgraph.OperationDelete,
		expInProgress: true,
	}
}

func BeingModified() AsyncOperationMatcher {
	return &opMatcher{
		expOp:         depgraph.OperationModify,
		expInProgress: true,
	}
}

func BeingRecreated() AsyncOperationMatcher {
	return &opMatcher{
		expOp:         depgraph.OperationRecreate,
		expInProgress: true,
	}
}

// mockItemMatcher implements types.GomegaMatcher.
type mockItemMatcher struct {
	expItem mockItem
}

func (m *mockItemMatcher) Match(actual interface{}) (success bool, err error) {
	item, ok := actual.(mockItem)
	if !ok {
		return false, fmt.Errorf("OperationMatcher expects a mock Item")
	}
	return item.itemType == m.expItem.itemType &&
			item.name == m.expItem.name &&
			item.isExternal == m.expItem.isExternal &&
			reflect.DeepEqual(item.staticAttrs, m.expItem.staticAttrs) &&
			reflect.DeepEqual(item.modifiableAttrs, m.expItem.modifiableAttrs),
		nil
}

func (m *mockItemMatcher) FailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("Expected\n%s\nto be mock item\n%s",
		format.Object(actual, 1),
		format.Object(m.expItem, 1))
}

func (m *mockItemMatcher) NegatedFailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("Expected\n%s\nto NOT be mock item\n%s",
		format.Object(actual, 1),
		format.Object(m.expItem, 1))
}

// opMatcher implements OperationMatcher
type opMatcher struct {
	expOp         depgraph.Operation
	expBefore     *expectedOp
	expAfter      *expectedOp
	expError      string
	expInProgress bool
}

type expectedOp struct {
	item       mockItem
	op         depgraph.Operation
	inProgress bool
}

func (m *opMatcher) Match(actual interface{}) (success bool, err error) {
	item, ok := actual.(mockItem)
	if !ok {
		return false, fmt.Errorf("OperationMatcher expects a mock Item")
	}
	opLog := m.findOp(item, m.expOp)
	if opLog == nil {
		return false, nil
	}
	if m.expInProgress && !opLog.EndTime.IsZero() {
		return false, nil
	}
	var opErr string
	if opLog.Err != nil {
		opErr = opLog.Err.Error()
	}
	if m.expError != opErr {
		return false, nil
	}
	if m.expBefore != nil {
		opLog2 := m.findOp(m.expBefore.item, m.expBefore.op)
		if opLog2 == nil {
			return false, nil
		}
		if m.expBefore.inProgress && !opLog2.EndTime.IsZero() {
			return false, nil
		}
		if opLog.EndTime.After(opLog2.StartTime) {
			return false, nil
		}
	}
	if m.expAfter != nil {
		opLog2 := m.findOp(m.expAfter.item, m.expAfter.op)
		if opLog2 == nil {
			return false, nil
		}
		if opLog2.EndTime.After(opLog.StartTime) {
			return false, nil
		}
	}
	return true, nil
}

func (m *opMatcher) findOp(item mockItem, op depgraph.Operation) *reconciler.OpLogEntry {
	var deleted bool
	for _, logEntry := range status.OperationLog {
		if logEntry.Item.Name() != item.Name() {
			continue
		}
		if op == logEntry.Operation ||
			(op == depgraph.OperationRecreate &&
				deleted && logEntry.Operation == depgraph.OperationCreate) {
			return &logEntry
		}
		deleted = logEntry.Operation == depgraph.OperationDelete
	}
	return nil
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
	if m.expInProgress {
		expVerb = "being"
	} else {
		expVerb = "to be"
	}
	if negated {
		expVerb = "NOT " + expVerb
	}
	expOp = expOpToString(m.expOp)
	expErr = "successfully"
	if m.expError != "" {
		expErr = fmt.Sprintf("with error %s", m.expError)
	}
	if m.expBefore != nil {
		being := "being "
		if !m.expBefore.inProgress {
			being = ""
		}
		expOrder = fmt.Sprintf("before item\n%s is %s%s",
			format.Object(m.expBefore.item, 1),
			being, expOpToString(m.expBefore.op))
	}
	if m.expAfter != nil {
		expOrder = fmt.Sprintf("after item\n%s is %s",
			format.Object(m.expAfter.item, 1),
			expOpToString(m.expAfter.op))
	}
	actualOps := fmt.Sprintf("Executed operations:\n%v", status.OperationLog)
	return fmt.Sprintf("Expected\n%s\n%s %s %s %s\n%s",
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

func (m *opMatcher) Before(item mockItem) RefOperationMatcherWithAsync {
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

func (m *opMatcher) IsBeingCreated() types.GomegaMatcher {
	if m.expBefore != nil {
		m.expBefore.op = depgraph.OperationCreate
		m.expBefore.inProgress = true
	}
	if m.expAfter != nil {
		m.expAfter.op = depgraph.OperationCreate
		m.expAfter.inProgress = true
	}
	return m
}

func (m *opMatcher) IsBeingDeleted() types.GomegaMatcher {
	if m.expBefore != nil {
		m.expBefore.op = depgraph.OperationDelete
		m.expBefore.inProgress = true
	}
	if m.expAfter != nil {
		m.expAfter.op = depgraph.OperationDelete
		m.expAfter.inProgress = true
	}
	return m
}

func (m *opMatcher) IsBeingModified() types.GomegaMatcher {
	if m.expBefore != nil {
		m.expBefore.op = depgraph.OperationModify
		m.expBefore.inProgress = true
	}
	if m.expAfter != nil {
		m.expAfter.op = depgraph.OperationModify
		m.expAfter.inProgress = true
	}
	return m
}

func (m *opMatcher) IsBeingRecreated() types.GomegaMatcher {
	if m.expBefore != nil {
		m.expBefore.op = depgraph.OperationRecreate
		m.expBefore.inProgress = true
	}
	if m.expAfter != nil {
		m.expAfter.op = depgraph.OperationRecreate
		m.expAfter.inProgress = true
	}
	return m
}
