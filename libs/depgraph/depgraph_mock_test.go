// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package depgraph_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/lf-edge/eve/libs/depgraph"
)

// log entry for a single (mock) operation
type opLog struct {
	item  mockItem
	op    depgraph.Operation
	err   error
	index int
}

// log of operations for a single DepGraph.Sync() call
type operationsLog struct {
	log []opLog
}

func (l *operationsLog) recordOp(item mockItem, op depgraph.Operation, err error) {
	l.log = append(l.log, opLog{
		item:  item,
		op:    op,
		err:   err,
		index: len(l.log),
	})
}

func (l *operationsLog) clear() {
	l.log = []opLog{}
}

func (l *operationsLog) find(item mockItem, op depgraph.Operation) *opLog {
	var deleted bool
	for _, logEntry := range l.log {
		if logEntry.item.Name() != item.Name() {
			continue
		}
		if op == logEntry.op ||
			(op == depgraph.OperationRecreate &&
				deleted && logEntry.op == depgraph.OperationCreate) {
			return &logEntry
		}
		deleted = logEntry.op == depgraph.OperationDelete
	}
	return nil
}

func (l *operationsLog) String() string {
	var ops []string
	for _, op := range l.log {
		var withError string
		if op.err != nil {
			withError = " with error " + op.err.Error()
		}
		ops = append(ops, fmt.Sprintf("%s item %s%s",
			strings.Title(op.op.String()), op.item.name, withError))
	}
	return strings.Join(ops, "\n")
}

type mockItemDepsClb func(staticAttrs, modifiableAttrs mockItemAttrs) []depgraph.Dependency

type mockConfigurator struct {
	itemType    string
	opsLog      *operationsLog
	itemDepsClb mockItemDepsClb
}

func (m *mockConfigurator) Create(ctx context.Context, item depgraph.Item) (err error) {
	mItem, ok := item.(mockItem)
	if !ok {
		panic("mockConfigurator only works with mockItem")
	}
	if item.Type() != m.itemType {
		panic("DepGraph called wrong Configurator")
	}
	if item.External() {
		panic("external item should not have configurator associated")
	}
	if mItem.failToCreate {
		err = errors.New("failed to create")
	}
	if m.opsLog != nil {
		m.opsLog.recordOp(mItem, depgraph.OperationCreate, err)
	}
	return err
}

func (m *mockConfigurator) Modify(ctx context.Context, oldItem, newItem depgraph.Item) (err error) {
	if oldItem.Name() != newItem.Name() {
		panic("Modify called between different items")
	}
	if newItem.Type() != m.itemType {
		panic("DepGraph called wrong Configurator")
	}
	_, ok := oldItem.(mockItem)
	if !ok {
		panic("mockConfigurator only works with mockItem")
	}
	mNewItem, ok := newItem.(mockItem)
	if !ok {
		panic("mockConfigurator only works with mockItem")
	}
	if oldItem.Equal(newItem) {
		panic("Modify called for item which has not changed")
	}
	if newItem.External() {
		panic("external item should not have configurator associated")
	}
	if mNewItem.failToCreate {
		err = errors.New("failed to modify")
	}
	if m.opsLog != nil {
		m.opsLog.recordOp(mNewItem, depgraph.OperationModify, err)
	}
	return err
}

func (m *mockConfigurator) Delete(ctx context.Context, item depgraph.Item) (err error) {
	mItem, ok := item.(mockItem)
	if !ok {
		panic("mockConfigurator only works with mockItem")
	}
	if item.Type() != m.itemType {
		panic("DepGraph called wrong Configurator")
	}
	if item.External() {
		panic("external item should not have configurator associated")
	}
	if mItem.failToDelete {
		err = errors.New("failed to delete")
	}
	if m.opsLog != nil {
		m.opsLog.recordOp(mItem, depgraph.OperationDelete, err)
	}
	return err
}

func (m *mockConfigurator) NeedsRecreate(oldItem, newItem depgraph.Item) (recreate bool) {
	if newItem.Type() != m.itemType {
		panic("DepGraph called wrong Configurator")
	}
	mOldItem, ok := oldItem.(mockItem)
	if !ok {
		panic("mockConfigurator only works with mockItem")
	}
	mNewItem, ok := newItem.(mockItem)
	if !ok {
		panic("mockConfigurator only works with mockItem")
	}
	return !reflect.DeepEqual(mOldItem.staticAttrs, mNewItem.staticAttrs)
}

func (m *mockConfigurator) DependsOn(item depgraph.Item) []depgraph.Dependency {
	mItem, ok := item.(mockItem)
	if !ok {
		panic("mockConfigurator only works with mockItem")
	}
	if m.itemDepsClb != nil {
		return m.itemDepsClb(mItem.staticAttrs, mItem.modifiableAttrs)
	}
	return []depgraph.Dependency{}
}

type mockItemAttrs struct {
	intAttr   int
	strAttr   string
	boolAttr  bool
	sliceAttr []string
}

type mockItem struct {
	name            string
	itemType        string
	isExternal      bool
	staticAttrs     mockItemAttrs // change of these requires purge
	modifiableAttrs mockItemAttrs // can be changed by Modify
	failToCreate    bool          // enable to simulate failed Create/Modify
	failToDelete    bool          // enable to simulate failed Delete
}

func (m mockItem) Name() string {
	return m.name
}

func (m mockItem) Label() string {
	return m.name
}

func (m mockItem) Type() string {
	return m.itemType
}

func (m mockItem) Equal(m2 depgraph.Item) bool {
	return reflect.DeepEqual(m.modifiableAttrs, m2.(mockItem).modifiableAttrs) &&
		reflect.DeepEqual(m.staticAttrs, m2.(mockItem).staticAttrs)
}

func (m mockItem) External() bool {
	return m.isExternal
}

func (m mockItem) String() string {
	return fmt.Sprintf("item %s with attrs: %v; %v",
		m.name, m.modifiableAttrs, m.staticAttrs)
}
