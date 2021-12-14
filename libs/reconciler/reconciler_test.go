// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package reconciler_test

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/lf-edge/eve/libs/depgraph"
	"github.com/lf-edge/eve/libs/reconciler"
)

// Reconciliation status is accessed by matchers (see matchers_test.go).
var status reconciler.Status

// NodeIDFor : just a shortcut to avoid repeating "depgraph."
var NodeIDFor = depgraph.NodeIDFor

func addConfigurator(registry *reconciler.DefaultRegistry, forItemType string) error {
	return registry.Register(&mockConfigurator{itemType: forItemType}, forItemType)
}

// Items: A, B, C
// Without dependencies
func TestItemsWithoutDependencies(test *testing.T) {
	t := NewGomegaWithT(test)

	itemA := mockItem{
		name:            "A",
		itemType:        "type1",
		modifiableAttrs: mockItemAttrs{intAttr: 10, strAttr: "abc"},
	}
	itemB := mockItem{
		name:            "B",
		itemType:        "type1",
		modifiableAttrs: mockItemAttrs{boolAttr: true},
	}
	itemC := mockItem{
		name:     "C",
		itemType: "type2",
	}

	reg := &reconciler.DefaultRegistry{}
	t.Expect(addConfigurator(reg, "type1")).To(Succeed())
	t.Expect(addConfigurator(reg, "type2")).To(Succeed())

	// 0. Empty content of the intended state
	intent := depgraph.New(depgraph.InitArgs{
		Name:        "TestGraph",
		Description: "Graph for testing",
	})

	r := reconciler.New(reg)
	t.Expect(r).ToNot(BeNil())
	status = r.Reconcile(context.Background(), nil, intent)
	t.Expect(status.Err).To(BeNil())
	t.Expect(status.OperationLog).To(BeEmpty())
	t.Expect(status.AsyncOpsInProgress).To(BeFalse())
	t.Expect(status.CancelAsyncOps).To(BeNil())
	t.Expect(status.ReadyToResume).To(BeNil())
	t.Expect(status.NewCurrentState).ToNot(BeNil())

	current := status.NewCurrentState
	t.Expect(current.Name()).To(Equal(intent.Name()))
	t.Expect(current.Description()).To(Equal(intent.Description()))
	t.Expect(current.Nodes(true).Len()).To(BeZero())
	t.Expect(current.SubGraphs().Len()).To(BeZero())

	// 1. Create all three items
	intent.PutNode(&depgraph.Node{Item: itemA})
	intent.PutNode(&depgraph.Node{Item: itemB})
	intent.PutNode(&depgraph.Node{Item: itemC})

	r = reconciler.New(reg)
	status = r.Reconcile(context.Background(), current, intent)
	t.Expect(status.Err).To(BeNil())
	t.Expect(status.AsyncOpsInProgress).To(BeFalse())
	t.Expect(status.NewCurrentState).To(BeIdenticalTo(current))
	t.Expect(itemA).To(BeCreated())
	t.Expect(itemB).To(BeCreated())
	t.Expect(itemC).To(BeCreated())
	t.Expect(status.OperationLog).To(HaveLen(3))

	node, exists := current.Node(NodeIDFor(itemA))
	t.Expect(exists).To(BeTrue())
	t.Expect(node.Item).To(BeMockItem(itemA))
	t.Expect(node.LastOperation).To(Equal(depgraph.OperationCreate))
	t.Expect(node.LastError).To(BeNil())
	t.Expect(node.State).To(Equal(depgraph.ItemStateCreated))

	// 2. Modify itemB
	itemB.modifiableAttrs.intAttr++
	intent.PutNode(&depgraph.Node{Item: itemB})

	// let's try to reuse previous reconciler...
	status = r.Reconcile(context.Background(), current, intent)
	t.Expect(status.Err).To(BeNil())
	t.Expect(status.AsyncOpsInProgress).To(BeFalse())
	t.Expect(status.NewCurrentState).To(BeIdenticalTo(current))
	t.Expect(itemB).To(BeModified())
	t.Expect(status.OperationLog).To(HaveLen(1))

	node, exists = current.Node(NodeIDFor(itemB))
	t.Expect(exists).To(BeTrue())
	t.Expect(node.Item).To(BeMockItem(itemB))
	t.Expect(node.LastOperation).To(Equal(depgraph.OperationModify))
	t.Expect(node.LastError).To(BeNil())
	t.Expect(node.State).To(Equal(depgraph.ItemStateCreated))

	// 3. Put the same itemB, should not trigger Modify
	intent.PutNode(&depgraph.Node{Item: itemB})

	r = reconciler.New(reg)
	status = r.Reconcile(context.Background(), current, intent)
	t.Expect(status.Err).To(BeNil())
	t.Expect(status.AsyncOpsInProgress).To(BeFalse())
	t.Expect(status.NewCurrentState).To(BeIdenticalTo(current))
	t.Expect(status.OperationLog).To(BeEmpty())

	// 4. Delete itemA and itemC
	intent.DelNode(NodeIDFor(itemA))
	intent.DelNode(NodeIDFor(itemC))

	r = reconciler.New(reg)
	status = r.Reconcile(context.Background(), current, intent)
	t.Expect(status.Err).To(BeNil())
	t.Expect(status.AsyncOpsInProgress).To(BeFalse())
	t.Expect(status.NewCurrentState).To(BeIdenticalTo(current))
	t.Expect(itemA).To(BeDeleted())
	t.Expect(itemB).ToNot(BeDeleted())
	t.Expect(itemC).To(BeDeleted())
	t.Expect(status.OperationLog).To(HaveLen(2))

	_, exists = current.Node(NodeIDFor(itemA))
	t.Expect(exists).To(BeFalse())
	_, exists = current.Node(NodeIDFor(itemB))
	t.Expect(exists).To(BeTrue())
	_, exists = current.Node(NodeIDFor(itemC))
	t.Expect(exists).To(BeFalse())
}

// Items: A, B, C
// Dependencies: A->C, B->C
func TestDependencyItemIsCreated(test *testing.T) {
	t := NewGomegaWithT(test)

	itemA := mockItem{
		name:            "A",
		itemType:        "type1",
		modifiableAttrs: mockItemAttrs{intAttr: 10, strAttr: "abc"},
		deps:            []depgraph.Dependency{
			{
				Item: depgraph.RequiredItem{
					Type: "type2",
					Name: "C",
				},
			},
		},
	}
	itemB := mockItem{
		name:            "B",
		itemType:        "type1",
		modifiableAttrs: mockItemAttrs{boolAttr: true},
		deps:            []depgraph.Dependency{
			{
				Item: depgraph.RequiredItem{
					Type: "type2",
					Name: "C",
				},
			},
		},
	}
	itemC := mockItem{
		name:     "C",
		itemType: "type2",
	}

	reg := &reconciler.DefaultRegistry{}
	t.Expect(addConfigurator(reg, "type1")).To(Succeed())
	t.Expect(addConfigurator(reg, "type2")).To(Succeed())

	// 1. Create all three items
	intent := depgraph.New(depgraph.InitArgs{
		Name:        "TestGraph",
		Description: "Graph for testing",
		Items:       []depgraph.Item{
			itemA, itemB, itemC,
		},
	})

	r := reconciler.New(reg)
	status = r.Reconcile(context.Background(), nil, intent)
	t.Expect(status.Err).To(BeNil())
	t.Expect(status.AsyncOpsInProgress).To(BeFalse())
	t.Expect(itemA).To(BeCreated().After(itemC))
	t.Expect(itemB).To(BeCreated().After(itemC))
	t.Expect(itemC).To(BeCreated())
	t.Expect(status.OperationLog).To(HaveLen(3))
	t.Expect(status.NewCurrentState).ToNot(BeNil())

	current := status.NewCurrentState
	t.Expect(current.Name()).To(Equal(intent.Name()))
	t.Expect(current.Description()).To(Equal(intent.Description()))
	t.Expect(current.Nodes(true).Len()).To(HaveLen(3))
	t.Expect(current.SubGraphs().Len()).To(BeZero())

	node, exists := current.Node(NodeIDFor(itemA))
	t.Expect(exists).To(BeTrue())
	t.Expect(node.Item).To(BeMockItem(itemA))
	t.Expect(node.LastOperation).To(Equal(depgraph.OperationCreate))
	t.Expect(node.LastError).To(BeNil())
	t.Expect(node.State).To(Equal(depgraph.ItemStateCreated))
	node, exists = current.Node(NodeIDFor(itemB))
	t.Expect(exists).To(BeTrue())
	t.Expect(node.Item).To(BeMockItem(itemB))
	t.Expect(node.LastOperation).To(Equal(depgraph.OperationCreate))
	t.Expect(node.LastError).To(BeNil())
	t.Expect(node.State).To(Equal(depgraph.ItemStateCreated))
	node, exists = current.Node(NodeIDFor(itemC))
	t.Expect(exists).To(BeTrue())
	t.Expect(node.Item).To(BeMockItem(itemC))
	t.Expect(node.LastOperation).To(Equal(depgraph.OperationCreate))
	t.Expect(node.LastError).To(BeNil())
	t.Expect(node.State).To(Equal(depgraph.ItemStateCreated))

	// 2. Modify itemC, dependent items A and B should remain unchanged
	//    (Dependency.Attributes.RecreateWhenModified is not set)
	itemC.modifiableAttrs.boolAttr = true
	intent.PutNode(&depgraph.Node{Item: itemC})

	r = reconciler.New(reg)
	status = r.Reconcile(context.Background(), current, intent)
	t.Expect(status.Err).To(BeNil())
	t.Expect(status.AsyncOpsInProgress).To(BeFalse())
	t.Expect(status.NewCurrentState).To(BeIdenticalTo(current))
	t.Expect(itemC).To(BeModified())
	t.Expect(status.OperationLog).To(HaveLen(1))

	// 3. Delete itemC, dependent items A and B should be removed and marked as pending
	intent.DelNode(NodeIDFor(itemC))

	r = reconciler.New(reg)
	status = r.Reconcile(context.Background(), current, intent)
	t.Expect(status.Err).To(BeNil())
	t.Expect(status.AsyncOpsInProgress).To(BeFalse())
	t.Expect(status.NewCurrentState).To(BeIdenticalTo(current))
	t.Expect(itemA).To(BeDeleted().Before(itemC))
	t.Expect(itemB).To(BeDeleted().Before(itemC))
	t.Expect(itemC).To(BeDeleted())
	t.Expect(status.OperationLog).To(HaveLen(3))

	node, exists = current.Node(NodeIDFor(itemC))
	t.Expect(exists).To(BeFalse())

	node, exists = current.Node(NodeIDFor(itemA))
	t.Expect(exists).To(BeTrue())
	t.Expect(node.Item).To(BeMockItem(itemA))
	t.Expect(node.LastOperation).To(Equal(depgraph.OperationDelete))
	t.Expect(node.LastError).To(BeNil())
	t.Expect(node.State).To(Equal(depgraph.ItemStatePending))
	node, exists = current.Node(NodeIDFor(itemB))
	t.Expect(exists).To(BeTrue())
	t.Expect(node.Item).To(BeMockItem(itemB))
	t.Expect(node.LastOperation).To(Equal(depgraph.OperationDelete))
	t.Expect(node.LastError).To(BeNil())
	t.Expect(node.State).To(Equal(depgraph.ItemStatePending))
}

// TODO: more tests...
