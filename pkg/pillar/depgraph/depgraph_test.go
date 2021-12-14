// Copyright (c) 2021 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package depgraph_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"

	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/depgraph"
)

type testCtx struct {
	graph  depgraph.DepGraph
	opsLog *operationsLog
}

func newTestCtx(graph depgraph.DepGraph) *testCtx {
	return &testCtx{
		graph:  graph,
		opsLog: &operationsLog{},
	}
}

func (ctx *testCtx) syncGraph() error {
	ctx.opsLog.clear()
	return ctx.graph.Sync(context.Background())
}

func (ctx *testCtx) addConfigurator(forItemType string, itemDepsClb mockItemDepsClb) error {
	return ctx.graph.RegisterConfigurator(&mockConfigurator{
		itemType:    forItemType,
		opsLog:      ctx.opsLog,
		itemDepsClb: itemDepsClb,
	}, forItemType)
}

func (ctx *testCtx) executedOps() []opLog {
	return ctx.opsLog.log
}

// test context is accessed by matchers (see depgraph_matchers_test.go).
var ctx *testCtx
var log = base.NewSourceLogObject(logrus.StandardLogger(), "test", 0)

// Items: A, B, C
// Without dependencies
func TestItemsWithoutDependencies(t *testing.T) {
	g := NewGomegaWithT(t)
	ctx = newTestCtx(depgraph.NewDepGraph(log))
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(ctx.executedOps()).To(BeEmpty())

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
	g.Expect(ctx.addConfigurator("type1", nil)).To(Succeed())
	g.Expect(ctx.addConfigurator("type2", nil)).To(Succeed())

	// 1. Create all three items
	ctx.graph.Item(itemA.name).Put(itemA)
	ctx.graph.Item(itemB.name).Put(itemB)
	ctx.graph.Item(itemC.name).Put(itemC)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeCreated())
	g.Expect(itemB).To(BeCreated())
	g.Expect(itemC).To(BeCreated())
	g.Expect(ctx.executedOps()).To(HaveLen(3))

	summary, exists := ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.Item).To(BeEquivalentTo(itemA))
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))

	// 2. Modify itemB
	itemB.modifiableAttrs.intAttr++
	ctx.graph.Item(itemB.name).Put(itemB)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemB).To(BeModified())
	g.Expect(ctx.executedOps()).To(HaveLen(1))

	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.Item).To(BeEquivalentTo(itemB))
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationModify))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))

	// 3. Put the same itemB, should not trigger Modify
	ctx.graph.Item(itemB.name).Put(itemB)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemB).ToNot(BeModified())
	g.Expect(ctx.executedOps()).To(BeEmpty())

	// 4. Delete itemA and itemC
	ctx.graph.Item(itemA.name).Del()
	ctx.graph.Item(itemC.name).Del()

	// not applied until Sync is called
	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))

	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeDeleted())
	g.Expect(itemB).ToNot(BeDeleted())
	g.Expect(itemC).To(BeDeleted())
	g.Expect(ctx.executedOps()).To(HaveLen(2))

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeFalse())
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	summary, exists = ctx.graph.Item(itemC.name).Get()
	g.Expect(exists).To(BeFalse())
}

func depFromStrAttr(_, modifiableAttrs mockItemAttrs) []depgraph.Dependency {
	return []depgraph.Dependency{
		&depgraph.ItemIsCreated{
			ItemName:    modifiableAttrs.strAttr,
			Description: "dependency for testing",
		},
	}
}

func depFromStrAttrWithRecreate(_, modifiableAttrs mockItemAttrs) []depgraph.Dependency {
	return []depgraph.Dependency{
		&depgraph.ItemIsCreated{
			ItemName:             modifiableAttrs.strAttr,
			RecreateWhenModified: true,
			Description:          "dependency for testing",
		},
	}
}

func depFromSliceAttr(_, modifiableAttrs mockItemAttrs) (deps []depgraph.Dependency) {
	for _, item := range modifiableAttrs.sliceAttr {
		deps = append(deps, &depgraph.ItemIsCreated{
			ItemName:    item,
			Description: "dependency for testing",
		})
	}
	return deps
}

// Items: A, B, C
// Dependencies: A->C, B->C
func TestDependencyItemIsCreated(t *testing.T) {
	g := NewGomegaWithT(t)
	ctx = newTestCtx(depgraph.NewDepGraph(log))

	itemA := mockItem{
		name:     "A",
		itemType: "type1",
		modifiableAttrs: mockItemAttrs{
			// simulate reference to a dependency
			strAttr: "C",
		},
	}
	itemB := mockItem{
		name:     "B",
		itemType: "type1",
		modifiableAttrs: mockItemAttrs{
			// simulate reference to a dependency
			strAttr: "C",
		},
	}
	itemC := mockItem{
		name:     "C",
		itemType: "type2",
	}

	g.Expect(ctx.addConfigurator("type1", depFromStrAttr)).To(Succeed())
	g.Expect(ctx.addConfigurator("type2", nil)).To(Succeed())

	// 1. Create all three items
	ctx.graph.Item(itemA.name).Put(itemA)
	ctx.graph.Item(itemB.name).Put(itemB)
	ctx.graph.Item(itemC.name).Put(itemC)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeCreated().After(itemC))
	g.Expect(itemB).To(BeCreated().After(itemC))
	g.Expect(itemC).To(BeCreated())
	g.Expect(ctx.executedOps()).To(HaveLen(3))

	summary, exists := ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.Item).To(BeEquivalentTo(itemA))
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.Item).To(BeEquivalentTo(itemB))
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemC.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.Item).To(BeEquivalentTo(itemC))
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))

	// 2. Modify itemC, dependent items A and B should remain unchanged
	//    (ItemIsCreated.RecreateWhenModified is not set)
	itemC.modifiableAttrs.boolAttr = true
	ctx.graph.Item(itemC.name).Put(itemC)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemC).To(BeModified())
	g.Expect(ctx.executedOps()).To(HaveLen(1))

	// 2. Delete itemC, dependent items A and B should be removed and marked as pending
	ctx.graph.Item(itemC.name).Del()
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeDeleted().Before(itemC))
	g.Expect(itemB).To(BeDeleted().Before(itemC))
	g.Expect(itemC).To(BeDeleted())
	g.Expect(ctx.executedOps()).To(HaveLen(3))

	summary, exists = ctx.graph.Item(itemC.name).Get()
	g.Expect(exists).To(BeFalse())

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationDelete))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStatePending))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationDelete))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStatePending))
}

// Items: A, B, C
// Dependencies: A->C, B->C (with RecreateWhenModified)
func TestRecreateWhenModified(t *testing.T) {
	g := NewGomegaWithT(t)
	ctx = newTestCtx(depgraph.NewDepGraph(log))

	itemA := mockItem{
		name:     "A",
		itemType: "type1",
		modifiableAttrs: mockItemAttrs{
			// simulate reference to a dependency
			strAttr: "C",
		},
	}
	itemB := mockItem{
		name:     "B",
		itemType: "type1",
		modifiableAttrs: mockItemAttrs{
			// simulate reference to a dependency
			strAttr: "C",
		},
	}
	itemC := mockItem{
		name:     "C",
		itemType: "type2",
	}

	g.Expect(ctx.addConfigurator("type1", depFromStrAttrWithRecreate)).To(Succeed())
	g.Expect(ctx.addConfigurator("type2", nil)).To(Succeed())

	// 1. Create all three items
	ctx.graph.Item(itemA.name).Put(itemA)
	ctx.graph.Item(itemB.name).Put(itemB)
	ctx.graph.Item(itemC.name).Put(itemC)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeCreated().After(itemC))
	g.Expect(itemB).To(BeCreated().After(itemC))
	g.Expect(itemC).To(BeCreated())
	g.Expect(ctx.executedOps()).To(HaveLen(3))

	// 2. Modify itemC, dependent items A and B should be re-created
	//    (ItemIsCreated.RecreateWhenModified is set)
	itemC.modifiableAttrs.boolAttr = true
	ctx.graph.Item(itemC.name).Put(itemC)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeRecreated())
	g.Expect(itemB).To(BeRecreated())
	g.Expect(itemA).To(BeDeleted().Before(itemC).IsModified())
	g.Expect(itemA).To(BeCreated().After(itemC).IsModified())
	g.Expect(itemB).To(BeDeleted().Before(itemC).IsModified())
	g.Expect(itemB).To(BeCreated().After(itemC).IsModified())
	g.Expect(itemC).To(BeModified())
	// Recreate = 2 ops (Delete + Create)
	g.Expect(ctx.executedOps()).To(HaveLen(5))

	summary, exists := ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationRecreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationRecreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemC.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationModify))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))

	// 3. Put the same itemC, should not trigger Modify or Recreate
	ctx.graph.Item(itemC.name).Put(itemC)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).ToNot(BeRecreated())
	g.Expect(itemB).ToNot(BeRecreated())
	g.Expect(itemC).ToNot(BeModified())
	g.Expect(ctx.executedOps()).To(BeEmpty())
}

// Items: A, B
// Dependencies: A->B
// Scenario: re-create of B should be surrounded by delete+create of A
func TestRecreate(t *testing.T) {
	g := NewGomegaWithT(t)
	ctx = newTestCtx(depgraph.NewDepGraph(log))

	itemA := mockItem{
		name:     "A",
		itemType: "type1",
		modifiableAttrs: mockItemAttrs{
			// simulate reference to a dependency
			strAttr: "B",
		},
	}
	itemB := mockItem{
		name:     "B",
		itemType: "type2",
		staticAttrs: mockItemAttrs{
			intAttr: 10,
		},
	}

	g.Expect(ctx.addConfigurator("type1", depFromStrAttr)).To(Succeed())
	g.Expect(ctx.addConfigurator("type2", nil)).To(Succeed())

	// 1. Create both items
	ctx.graph.Item(itemA.name).Put(itemA)
	ctx.graph.Item(itemB.name).Put(itemB)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeCreated().After(itemB))
	g.Expect(itemB).To(BeCreated())
	g.Expect(ctx.executedOps()).To(HaveLen(2))

	// 2. Make modification to itemB which requires re-create
	itemB.staticAttrs.intAttr++
	ctx.graph.Item(itemB.name).Put(itemB)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeDeleted().Before(itemB).IsRecreated())
	g.Expect(itemB).To(BeRecreated())
	g.Expect(itemA).To(BeCreated().After(itemB).IsRecreated())
	g.Expect(ctx.executedOps()).To(HaveLen(4))

	summary, exists := ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationRecreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
}

// Items: A, B, C
// Dependencies: A->B->C
func TestTransitiveDependencies(t *testing.T) {
	g := NewGomegaWithT(t)
	ctx = newTestCtx(depgraph.NewDepGraph(log))

	itemA := mockItem{
		name:     "A",
		itemType: "type1",
		modifiableAttrs: mockItemAttrs{
			// simulate reference to a dependency
			strAttr: "B",
		},
	}
	itemB := mockItem{
		name:     "B",
		itemType: "type2",
		modifiableAttrs: mockItemAttrs{
			// simulate reference to a dependency
			strAttr: "C",
		},
	}
	itemC := mockItem{
		name:     "C",
		itemType: "type3",
	}

	g.Expect(ctx.addConfigurator("type1", depFromStrAttr)).To(Succeed())
	g.Expect(ctx.addConfigurator("type2", depFromStrAttr)).To(Succeed())
	g.Expect(ctx.addConfigurator("type3", nil)).To(Succeed())

	// 1. Try to create only itemA and itemB at first
	ctx.graph.Item(itemA.name).Put(itemA)
	ctx.graph.Item(itemB.name).Put(itemB)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).ToNot(BeCreated())
	g.Expect(itemB).ToNot(BeCreated())
	g.Expect(ctx.executedOps()).To(BeEmpty())

	summary, exists := ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationUnknown))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStatePending))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationUnknown))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStatePending))

	// 2. Now create itemC; itemA should be created transitively
	ctx.graph.Item(itemC.name).Put(itemC)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeCreated().After(itemB))
	g.Expect(itemB).To(BeCreated().After(itemC))
	g.Expect(itemC).To(BeCreated())
	g.Expect(ctx.executedOps()).To(HaveLen(3))

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemC.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))

	// 3. Delete itemC, both itemA and itemB should be removed and marked as pending
	ctx.graph.Item(itemC.name).Del()
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeDeleted().Before(itemB))
	g.Expect(itemB).To(BeDeleted().Before(itemC))
	g.Expect(itemC).To(BeDeleted())
	g.Expect(ctx.executedOps()).To(HaveLen(3))

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationDelete))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStatePending))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationDelete))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStatePending))
	summary, exists = ctx.graph.Item(itemC.name).Get()
	g.Expect(exists).To(BeFalse())
}

// Items: A, B
// Dependencies: A->B, A->C (but C is never created)
func TestUnsatisfiedDependency(t *testing.T) {
	g := NewGomegaWithT(t)
	ctx = newTestCtx(depgraph.NewDepGraph(log))

	itemA := mockItem{
		name:     "A",
		itemType: "type1",
		modifiableAttrs: mockItemAttrs{
			// simulate references to a dependency
			sliceAttr: []string{"B", "C"},
		},
	}
	itemB := mockItem{
		name:     "B",
		itemType: "type2",
	}

	g.Expect(ctx.addConfigurator("type1", depFromSliceAttr)).To(Succeed())
	g.Expect(ctx.addConfigurator("type2", nil)).To(Succeed())

	// 1. Create both items; itemA will remain pending
	ctx.graph.Item(itemA.name).Put(itemA)
	ctx.graph.Item(itemB.name).Put(itemB)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).ToNot(BeCreated())
	g.Expect(itemB).To(BeCreated())
	g.Expect(ctx.executedOps()).To(HaveLen(1))

	summary, exists := ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationUnknown))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStatePending))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))

	// 2. Remove both items, NOOP for itemA
	ctx.graph.Item(itemA.name).Del()
	ctx.graph.Item(itemB.name).Del()
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).ToNot(BeDeleted())
	g.Expect(itemB).To(BeDeleted())
	g.Expect(ctx.executedOps()).To(HaveLen(1))

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeFalse())
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeFalse())
}

// Items: A, B
// Dependencies: A->B (with MustSatisfy defined to check the content of B)
func TestMustSatisfy(t *testing.T) {
	g := NewGomegaWithT(t)
	ctx = newTestCtx(depgraph.NewDepGraph(log))

	const magicValue = 42
	itemA := mockItem{
		name:     "A",
		itemType: "type1",
		modifiableAttrs: mockItemAttrs{
			// simulate reference to a dependency
			strAttr: "B",
			// dependency must have this attribute
			intAttr: magicValue,
		},
	}
	itemB := mockItem{
		name:     "B",
		itemType: "type2",
	}

	depWithMustSatisfy := func(_, modifiableAttrs mockItemAttrs) []depgraph.Dependency {
		return []depgraph.Dependency{
			&depgraph.ItemIsCreated{
				ItemName: modifiableAttrs.strAttr,
				MustSatisfy: func(item depgraph.Item) bool {
					return modifiableAttrs.intAttr == item.(mockItem).modifiableAttrs.intAttr
				},
			},
		}
	}

	g.Expect(ctx.addConfigurator("type1", depWithMustSatisfy)).To(Succeed())
	g.Expect(ctx.addConfigurator("type2", nil)).To(Succeed())

	// 1. Create both items; itemA will remain pending (MustSatisfy returns false for itemB)
	ctx.graph.Item(itemA.name).Put(itemA)
	ctx.graph.Item(itemB.name).Put(itemB)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).ToNot(BeCreated())
	g.Expect(itemB).To(BeCreated())
	g.Expect(ctx.executedOps()).To(HaveLen(1))

	summary, exists := ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationUnknown))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStatePending))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))

	// 2. Modify itemB, it should now satisfy itemA dependency
	itemB.modifiableAttrs.intAttr = magicValue
	ctx.graph.Item(itemB.name).Put(itemB)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeCreated().After(itemB).IsModified())
	g.Expect(itemB).To(BeModified())
	g.Expect(ctx.executedOps()).To(HaveLen(2))

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationModify))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
}

// 3 scenarios where dependencies change with Modify.
func TestModifiedDependencies(t *testing.T) {
	// Scenario 1
	// Items: A, B, C
	// Dependencies: initially A->B, then A->C
	g := NewGomegaWithT(t)
	ctx = newTestCtx(depgraph.NewDepGraph(log))

	itemA := mockItem{
		name:     "A",
		itemType: "type1",
		modifiableAttrs: mockItemAttrs{
			// simulate reference to a dependency
			strAttr: "B",
		},
	}
	itemB := mockItem{
		name:     "B",
		itemType: "type2",
	}
	itemC := mockItem{
		name:     "C",
		itemType: "type2",
	}

	g.Expect(ctx.addConfigurator("type1", depFromStrAttr)).To(Succeed())
	g.Expect(ctx.addConfigurator("type2", nil)).To(Succeed())

	// 1.1 Create itemA and itemB at first
	ctx.graph.Item(itemA.name).Put(itemA)
	ctx.graph.Item(itemB.name).Put(itemB)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeCreated().After(itemB))
	g.Expect(itemB).To(BeCreated())
	g.Expect(ctx.executedOps()).To(HaveLen(2))

	summary, exists := ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))

	// 1.2. Modify itemA such then it now depends on non-existing itemC
	itemA.modifiableAttrs.strAttr = "C"
	ctx.graph.Item(itemA.name).Put(itemA)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeDeleted())
	g.Expect(ctx.executedOps()).To(HaveLen(1))

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationDelete))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStatePending))

	// 1.3 Create itemC and the pending itemA
	ctx.graph.Item(itemC.name).Put(itemC)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeCreated().After(itemC))
	g.Expect(itemC).To(BeCreated())
	g.Expect(ctx.executedOps()).To(HaveLen(2))

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemC.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))

	// 1.4 Remove itemB; should have no effect on itemA
	ctx.graph.Item(itemB.name).Del()
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).ToNot(BeDeleted())
	g.Expect(itemB).To(BeDeleted())
	g.Expect(ctx.executedOps()).To(HaveLen(1))

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeFalse())

	// Scenario 2
	// Items: A, B - changed to A', B'
	// Dependencies: A->B, then A'->B' (A must be recreated, there is no other way)
	ctx = newTestCtx(depgraph.NewDepGraph(log))

	itemA = mockItem{
		name:     "A",
		itemType: "type1",
		modifiableAttrs: mockItemAttrs{
			// simulate reference to a dependency
			strAttr: "B",
			// dependency must have this attribute
			intAttr: 1,
		},
	}
	itemB = mockItem{
		name:     "B",
		itemType: "type2",
		modifiableAttrs: mockItemAttrs{
			intAttr: 1,
		},
	}

	depWithMustSatisfy := func(_, modifiableAttrs mockItemAttrs) []depgraph.Dependency {
		return []depgraph.Dependency{
			&depgraph.ItemIsCreated{
				ItemName: modifiableAttrs.strAttr,
				MustSatisfy: func(item depgraph.Item) bool {
					return modifiableAttrs.intAttr == item.(mockItem).modifiableAttrs.intAttr
				},
			},
		}
	}

	g.Expect(ctx.addConfigurator("type1", depWithMustSatisfy)).To(Succeed())
	g.Expect(ctx.addConfigurator("type2", nil)).To(Succeed())

	// 2.1. Create both items
	ctx.graph.Item(itemA.name).Put(itemA)
	ctx.graph.Item(itemB.name).Put(itemB)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeCreated())
	g.Expect(itemB).To(BeCreated())
	g.Expect(ctx.executedOps()).To(HaveLen(2))

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))

	// 2.2. Modify both items - dependencies are also updated
	// However, because modifications are done one after the other, itemA will be recreated.
	itemA.modifiableAttrs.intAttr = 2
	itemB.modifiableAttrs.intAttr = 2
	ctx.graph.Item(itemA.name).Put(itemA)
	ctx.graph.Item(itemB.name).Put(itemB)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeRecreated())
	g.Expect(itemA).To(BeDeleted().Before(itemB).IsModified())
	g.Expect(itemA).To(BeCreated().After(itemB).IsModified())
	g.Expect(itemB).To(BeModified())
	g.Expect(ctx.executedOps()).To(HaveLen(3))

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationModify))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))

	// Scenario 3
	// Items: initially A, B, C; later A' (modified A) and C (B removed)
	// Dependencies: A->B, A'->C
	ctx = newTestCtx(depgraph.NewDepGraph(log))

	itemA = mockItem{
		name:     "A",
		itemType: "type1",
		modifiableAttrs: mockItemAttrs{
			// simulate reference to a dependency
			strAttr: "B",
		},
	}
	itemB = mockItem{
		name:     "B",
		itemType: "type2",
	}
	itemC = mockItem{
		name:     "C",
		itemType: "type2",
	}

	g.Expect(ctx.addConfigurator("type1", depFromStrAttr)).To(Succeed())
	g.Expect(ctx.addConfigurator("type2", nil)).To(Succeed())

	// 3.1. Create all items
	ctx.graph.Item(itemA.name).Put(itemA)
	ctx.graph.Item(itemB.name).Put(itemB)
	ctx.graph.Item(itemC.name).Put(itemC)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeCreated().After(itemB))
	g.Expect(itemB).To(BeCreated())
	g.Expect(itemC).To(BeCreated())
	g.Expect(ctx.executedOps()).To(HaveLen(3))

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemC.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))

	// 3.2. Delete itemB but also modify itemA to depend on itemC now
	itemA.modifiableAttrs.strAttr = "C"
	ctx.graph.Item(itemA.name).Put(itemA)
	ctx.graph.Item(itemB.name).Del()
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeModified().Before(itemB).IsDeleted())
	g.Expect(itemB).To(BeDeleted())
	g.Expect(ctx.executedOps()).To(HaveLen(2))

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationModify))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeFalse())
	summary, exists = ctx.graph.Item(itemC.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
}

// Items (clustered): [A, B, [C, D]]
// Dependencies: C->A, D->B
func TestClusters(t *testing.T) {
	g := NewGomegaWithT(t)
	ctx = newTestCtx(depgraph.NewDepGraph(log))

	itemA := mockItem{
		name:     "A",
		itemType: "type1",
	}
	itemB := mockItem{
		name:     "B",
		itemType: "type1",
	}
	itemC := mockItem{
		name:     "C",
		itemType: "type2",
		modifiableAttrs: mockItemAttrs{
			// simulate reference to a dependency
			strAttr: "A",
		},
	}
	itemD := mockItem{
		name:     "D",
		itemType: "type2",
		modifiableAttrs: mockItemAttrs{
			// simulate reference to a dependency
			strAttr: "B",
		},
	}

	g.Expect(ctx.addConfigurator("type1", nil)).To(Succeed())
	g.Expect(ctx.addConfigurator("type2", depFromStrAttr)).To(Succeed())

	// 1. Create clusters
	var nestedClusterDescr = "Nested cluster"
	nestedCluster := depgraph.Cluster{
		Name:        "nested-cluster",
		Description: nestedClusterDescr,
		Items:       []depgraph.Item{itemC, itemD},
		SubClusters: nil,
	}
	var clusterDescr = "cluster with itemA, itemB and one nested cluster"
	cluster := depgraph.Cluster{
		Name:        "cluster",
		Description: clusterDescr,
		Items:       []depgraph.Item{itemA, itemB},
		SubClusters: []depgraph.Cluster{nestedCluster},
	}
	ctx.graph.Cluster(cluster.Name).Put(cluster)

	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeCreated())
	g.Expect(itemB).To(BeCreated())
	g.Expect(itemC).To(BeCreated().After(itemA))
	g.Expect(itemD).To(BeCreated().After(itemB))
	g.Expect(ctx.executedOps()).To(HaveLen(4))

	summary, exists := ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	g.Expect(summary.ClusterPath).To(Equal("/cluster/"))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	g.Expect(summary.ClusterPath).To(Equal("/cluster/"))
	summary, exists = ctx.graph.Item(itemC.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	g.Expect(summary.ClusterPath).To(Equal("/cluster/nested-cluster/"))
	summary, exists = ctx.graph.Item(itemD.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	g.Expect(summary.ClusterPath).To(Equal("/cluster/nested-cluster/"))

	clusterSummary, exists := ctx.graph.Cluster(cluster.Name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(clusterSummary.Name).To(Equal(cluster.Name))
	g.Expect(clusterSummary.Description).To(Equal(clusterDescr))
	g.Expect(clusterSummary.ClusterPath).To(Equal("/cluster/"))
	g.Expect(clusterSummary.Items).To(HaveLen(2))
	g.Expect(clusterSummary.Items[0].Item.Name()).To(Equal(itemA.name))
	g.Expect(clusterSummary.Items[1].Item.Name()).To(Equal(itemB.name))
	g.Expect(clusterSummary.SubClusters).To(HaveLen(1))
	g.Expect(clusterSummary.SubClusters[0].Name).To(Equal(nestedCluster.Name))
	g.Expect(clusterSummary.SubClusters[0].Description).To(Equal(nestedClusterDescr))
	g.Expect(clusterSummary.SubClusters[0].Items).To(HaveLen(2))
	g.Expect(clusterSummary.SubClusters[0].Items[0].Item.Name()).To(Equal(itemC.name))
	g.Expect(clusterSummary.SubClusters[0].Items[1].Item.Name()).To(Equal(itemD.name))

	// 2. Change (replace) content of the cluster
	itemA.staticAttrs.boolAttr = true
	cluster.Items = []depgraph.Item{itemA}
	clusterDescr = "cluster with itemA and one nested cluster"
	cluster.Description = clusterDescr
	ctx.graph.Cluster(cluster.Name).Put(cluster)

	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeRecreated())
	g.Expect(itemB).To(BeDeleted())
	g.Expect(itemC).To(BeRecreated())
	g.Expect(itemC).To(BeDeleted().Before(itemA).IsRecreated())
	g.Expect(itemC).To(BeCreated().After(itemA).IsRecreated())
	g.Expect(itemD).To(BeDeleted().Before(itemB).IsDeleted())
	g.Expect(ctx.executedOps()).To(HaveLen(6))

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationRecreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	g.Expect(summary.ClusterPath).To(Equal("/cluster/"))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeFalse())
	summary, exists = ctx.graph.Item(itemC.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	g.Expect(summary.ClusterPath).To(Equal("/cluster/nested-cluster/"))
	summary, exists = ctx.graph.Item(itemD.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationDelete))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStatePending))
	g.Expect(summary.ClusterPath).To(Equal("/cluster/nested-cluster/"))

	clusterSummary, exists = ctx.graph.Cluster(cluster.Name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(clusterSummary.Name).To(Equal(cluster.Name))
	g.Expect(clusterSummary.Description).To(Equal(clusterDescr))
	g.Expect(clusterSummary.ClusterPath).To(Equal("/cluster/"))
	g.Expect(clusterSummary.Items).To(HaveLen(1))
	g.Expect(clusterSummary.Items[0].Item.Name()).To(Equal(itemA.name))
	g.Expect(clusterSummary.SubClusters).To(HaveLen(1))
	g.Expect(clusterSummary.SubClusters[0].Name).To(Equal(nestedCluster.Name))
	g.Expect(clusterSummary.SubClusters[0].Description).To(Equal(nestedClusterDescr))
	g.Expect(clusterSummary.SubClusters[0].Items).To(HaveLen(2))
	g.Expect(clusterSummary.SubClusters[0].Items[0].Item.Name()).To(Equal(itemC.name))
	g.Expect(clusterSummary.SubClusters[0].Items[1].Item.Name()).To(Equal(itemD.name))

	clusterSummary, exists = ctx.graph.Cluster(cluster.Name).Cluster(nestedCluster.Name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(clusterSummary.Description).To(Equal(nestedClusterDescr))
	g.Expect(clusterSummary.ClusterPath).To(Equal("/cluster/nested-cluster/"))
	g.Expect(clusterSummary.Items).To(HaveLen(2))
	g.Expect(clusterSummary.Items[0].Item.Name()).To(Equal(itemC.name))
	g.Expect(clusterSummary.Items[1].Item.Name()).To(Equal(itemD.name))
	g.Expect(clusterSummary.SubClusters).To(HaveLen(0))

	// 3. Apply the same content again
	ctx.graph.Cluster(cluster.Name).Put(depgraph.Cluster{Name: "cluster"}) // overridden below
	ctx.graph.Cluster(cluster.Name).Put(cluster)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(ctx.executedOps()).To(BeEmpty())

	// 4. Remove entire (nested) cluster
	ctx.graph.Cluster(cluster.Name).Cluster(nestedCluster.Name).Del()
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemC).To(BeDeleted())
	g.Expect(itemD).ToNot(BeDeleted()) // was pending
	g.Expect(ctx.executedOps()).To(HaveLen(1))

	clusterSummary, exists = ctx.graph.Cluster(cluster.Name).Get()
	g.Expect(exists).To(BeTrue())
	clusterSummary, exists = ctx.graph.Cluster(cluster.Name).Cluster(nestedCluster.Name).Get()
	g.Expect(exists).To(BeFalse())

	summary, exists = ctx.graph.Item(itemC.name).Get()
	g.Expect(exists).To(BeFalse())
	summary, exists = ctx.graph.Item(itemD.name).Get()
	g.Expect(exists).To(BeFalse())

	// 5. Access item through a cluster
	summary, exists = ctx.graph.Cluster(cluster.Name).Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.Item.Name()).To(Equal(itemA.name))

	// 6. Move itemA to another cluster (the top-level one)
	ctx.graph.Item(itemA.name).Put(itemA)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(ctx.executedOps()).To(BeEmpty())
	summary, exists = ctx.graph.Cluster(cluster.Name).Item(itemA.name).Get()
	g.Expect(exists).To(BeFalse())
	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	// original cluster is no longer used and so it should be GCed
	clusterSummary, exists = ctx.graph.Cluster(cluster.Name).Get()
	g.Expect(exists).To(BeFalse())

	// 7. Graph as a whole is the top-level cluster
	clusterSummary, exists = ctx.graph.Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(clusterSummary.Name).To(BeEmpty())
	g.Expect(clusterSummary.Description).To(BeEmpty())
	g.Expect(clusterSummary.ClusterPath).To(Equal("/"))
	g.Expect(clusterSummary.Items).To(HaveLen(1))
	g.Expect(clusterSummary.Items[0].Item.Name()).To(Equal(itemA.name))
	g.Expect(clusterSummary.SubClusters).To(HaveLen(0))
}

// Items: A, B, External (obviously external item)
// Dependencies: A->External, B->External
func TestExternalItems(t *testing.T) {
	g := NewGomegaWithT(t)
	ctx = newTestCtx(depgraph.NewDepGraph(log))

	itemA := mockItem{
		name:     "A",
		itemType: "type1",
		modifiableAttrs: mockItemAttrs{
			// simulate reference to a dependency
			strAttr: "External",
		},
	}
	itemB := mockItem{
		name:     "B",
		itemType: "type1",
		modifiableAttrs: mockItemAttrs{
			// simulate reference to a dependency
			strAttr: "External",
		},
	}
	itemExt := mockItem{
		name:       "External",
		itemType:   "type2",
		isExternal: true,
	}

	g.Expect(ctx.addConfigurator("type1", depFromStrAttrWithRecreate)).To(Succeed())
	// No configurator for type2 - those items are created externally

	// 1. Try to create itemA and itemB; both will remain pending (waiting for external item)
	ctx.graph.Item(itemA.name).Put(itemA)
	ctx.graph.Item(itemB.name).Put(itemB)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).ToNot(BeCreated())
	g.Expect(itemB).ToNot(BeCreated())
	g.Expect(ctx.executedOps()).To(BeEmpty())

	summary, exists := ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationUnknown))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStatePending))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationUnknown))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStatePending))
	summary, exists = ctx.graph.Item(itemExt.name).Get()
	g.Expect(exists).To(BeFalse())

	// 2. External item was created
	ctx.graph.Item(itemExt.name).Put(itemExt)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeCreated())
	g.Expect(itemB).To(BeCreated())
	g.Expect(ctx.executedOps()).To(HaveLen(2))

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemExt.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationUnknown)) // managed externally
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))

	// 3. External item was modified
	itemExt.modifiableAttrs.strAttr = "modified"
	ctx.graph.Item(itemExt.name).Put(itemExt)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeRecreated())
	g.Expect(itemB).To(BeRecreated())
	g.Expect(ctx.executedOps()).To(HaveLen(4))

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationRecreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationRecreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemExt.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationUnknown))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))

	// 4. External item was deleted
	ctx.graph.Item(itemExt.name).Del()
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeDeleted())
	g.Expect(itemB).To(BeDeleted())
	g.Expect(ctx.executedOps()).To(HaveLen(2))

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationDelete))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStatePending))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationDelete))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStatePending))
	summary, exists = ctx.graph.Item(itemExt.name).Get()
	g.Expect(exists).To(BeFalse())
}

// Items: A, B, C
// Dependencies: A->B->C
// Scenario: some Create/Modify/Delete operations fail
func TestFailures(t *testing.T) {
	g := NewGomegaWithT(t)
	ctx = newTestCtx(depgraph.NewDepGraph(log))

	itemA := mockItem{
		name:     "A",
		itemType: "type1",
		modifiableAttrs: mockItemAttrs{
			// simulate reference to a dependency
			strAttr: "B",
		},
	}
	itemB := mockItem{
		name:     "B",
		itemType: "type1",
		modifiableAttrs: mockItemAttrs{
			// simulate reference to a dependency
			strAttr: "C",
		},
	}
	itemC := mockItem{
		name:     "C",
		itemType: "type2",
	}

	g.Expect(ctx.addConfigurator("type1", depFromStrAttr)).To(Succeed())
	g.Expect(ctx.addConfigurator("type2", nil)).To(Succeed())

	// 1. itemB will fail to be created
	itemB.failToCreate = true
	itemB.failToDelete = true // prepare for scenario 4.
	ctx.graph.Item(itemA.name).Put(itemA)
	ctx.graph.Item(itemB.name).Put(itemB)
	ctx.graph.Item(itemC.name).Put(itemC)
	g.Expect(ctx.syncGraph()).ToNot(Succeed())
	g.Expect(itemA).ToNot(BeCreated())
	g.Expect(itemB).To(BeCreated().WithError("failed to create"))
	g.Expect(itemC).To(BeCreated().Before(itemB))
	g.Expect(ctx.executedOps()).To(HaveLen(2))

	summary, exists := ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationUnknown))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStatePending))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(MatchError("failed to create"))
	g.Expect(summary.State).To(Equal(depgraph.ItemStateFailure))
	summary, exists = ctx.graph.Item(itemC.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))

	// 2. Next attempt to create itemB is successful
	itemB.failToCreate = false
	itemB.modifiableAttrs.boolAttr = true
	ctx.graph.Item(itemB.name).Put(itemB)
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeCreated().After(itemB))
	g.Expect(itemB).To(BeCreated())
	g.Expect(ctx.executedOps()).To(HaveLen(2))

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemC.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))

	// 3. Simulate failure to modify C
	itemC.failToCreate = true
	itemC.modifiableAttrs.strAttr = "modified"
	ctx.graph.Item(itemC.name).Put(itemC)

	g.Expect(ctx.syncGraph()).ToNot(Succeed())
	g.Expect(itemC).To(BeModified().WithError("failed to modify"))
	g.Expect(ctx.executedOps()).To(HaveLen(1))

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationCreate))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStateCreated))
	summary, exists = ctx.graph.Item(itemC.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationModify))
	g.Expect(summary.LastError).To(MatchError("failed to modify"))
	g.Expect(summary.State).To(Equal(depgraph.ItemStateFailure))

	// 4. Simulate failure to re-create itemB (delete fails)
	itemB.staticAttrs.strAttr = "modified"
	ctx.graph.Item(itemB.name).Put(itemB)

	g.Expect(ctx.syncGraph()).ToNot(Succeed())
	g.Expect(itemA).To(BeDeleted().Before(itemB))
	g.Expect(itemB).To(BeDeleted().WithError("failed to delete"))
	g.Expect(ctx.executedOps()).To(HaveLen(2))

	summary, exists = ctx.graph.Item(itemA.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationDelete))
	g.Expect(summary.LastError).To(BeNil())
	g.Expect(summary.State).To(Equal(depgraph.ItemStatePending))
	summary, exists = ctx.graph.Item(itemB.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationDelete))
	g.Expect(summary.LastError).To(MatchError("failed to delete"))
	g.Expect(summary.State).To(Equal(depgraph.ItemStateFailure))
	summary, exists = ctx.graph.Item(itemC.name).Get()
	g.Expect(exists).To(BeTrue())
	g.Expect(summary.LastOp).To(Equal(depgraph.OperationModify))
	g.Expect(summary.LastError).To(MatchError("failed to modify"))
	g.Expect(summary.State).To(Equal(depgraph.ItemStateFailure))
}

// Items (clustered): [A, B, [C, D]]
// Dependencies: C->A, D->B
func TestDOTRendering(t *testing.T) {
	g := NewGomegaWithT(t)
	ctx = newTestCtx(depgraph.NewDepGraph(log))

	itemA := mockItem{
		name:     "A",
		itemType: "type1",
	}
	itemB := mockItem{
		name:     "B",
		itemType: "type1",
	}
	itemC := mockItem{
		name:     "C",
		itemType: "type2",
		modifiableAttrs: mockItemAttrs{
			// simulate reference to a dependency
			strAttr: "A",
		},
	}
	itemD := mockItem{
		name:     "D",
		itemType: "type2",
		modifiableAttrs: mockItemAttrs{
			// simulate reference to a dependency
			strAttr: "B",
		},
	}

	g.Expect(ctx.addConfigurator("type1", nil)).To(Succeed())
	g.Expect(ctx.addConfigurator("type2", depFromStrAttr)).To(Succeed())

	// 1. Create clusters
	nestedCluster := depgraph.Cluster{
		Name:        "nested",
		Description: "Nested cluster",
		Items:       []depgraph.Item{itemC, itemD},
		SubClusters: nil,
	}
	cluster := depgraph.Cluster{
		Name:        "with-nested",
		Description: "cluster with itemA, itemB and one nested cluster",
		Items:       []depgraph.Item{itemA, itemB},
		SubClusters: []depgraph.Cluster{nestedCluster},
	}
	ctx.graph.Cluster(cluster.Name).Put(cluster)
	g.Expect(ctx.syncGraph()).To(Succeed())

	// 2. Render DOT description of the graph
	dot, err := ctx.graph.RenderDOT()
	g.Expect(err).To(BeNil())
	g.Expect(dot).ToNot(BeEmpty())
	g.Expect(dot).To(ContainSubstring("subgraph cluster_with_nested {"))
	g.Expect(dot).To(ContainSubstring("subgraph cluster_nested {"))
	g.Expect(dot).To(ContainSubstring("C -> A"))
	g.Expect(dot).To(ContainSubstring("D -> B"))
	g.Expect(dot).To(ContainSubstring("tooltip = \"item A with attrs: {0  false []}; {0  false []}\""))
	g.Expect(dot).To(ContainSubstring("tooltip = \"item B with attrs: {0  false []}; {0  false []}\""))
	g.Expect(dot).To(ContainSubstring("tooltip = \"item C with attrs: {0 A false []}; {0  false []}\""))
	g.Expect(dot).To(ContainSubstring("tooltip = \"item D with attrs: {0 B false []}; {0  false []}\""))
	g.Expect(dot).To(ContainSubstring("tooltip = \"Nested cluster\""))
	g.Expect(dot).To(ContainSubstring("tooltip = \"cluster with itemA, itemB and one nested cluster\""))
	g.Expect(strings.Count(dot, "{")).To(Equal(strings.Count(dot, "}")))

	// 3. Even unmet dependencies should be shown in the DOT rendering
	ctx.graph.Item(itemA.name).Del()
	g.Expect(ctx.syncGraph()).To(Succeed())
	g.Expect(itemA).To(BeDeleted())
	g.Expect(itemC).To(BeDeleted().Before(itemA))
	g.Expect(ctx.executedOps()).To(HaveLen(2))

	dot, err = ctx.graph.RenderDOT()
	g.Expect(err).To(BeNil())
	g.Expect(dot).ToNot(BeEmpty())
	g.Expect(dot).To(ContainSubstring("subgraph cluster_with_nested {"))
	g.Expect(dot).To(ContainSubstring("subgraph cluster_nested {"))
	g.Expect(dot).To(ContainSubstring("C -> A"))
	g.Expect(dot).To(ContainSubstring("D -> B"))
	g.Expect(dot).To(ContainSubstring("tooltip = \"<missing>\""))
	g.Expect(dot).To(ContainSubstring("tooltip = \"item B with attrs: {0  false []}; {0  false []}\""))
	g.Expect(dot).To(ContainSubstring("tooltip = \"item C with attrs: {0 A false []}; {0  false []}\""))
	g.Expect(dot).To(ContainSubstring("tooltip = \"item D with attrs: {0 B false []}; {0  false []}\""))
	g.Expect(dot).To(ContainSubstring("tooltip = \"Nested cluster\""))
	g.Expect(dot).To(ContainSubstring("tooltip = \"cluster with itemA, itemB and one nested cluster\""))
	g.Expect(strings.Count(dot, "{")).To(Equal(strings.Count(dot, "}")))
}

func BenchmarkDepGraph100(b *testing.B) {
	for n := 0; n < b.N; n++ {
		perfTest(100)
	}
}

func BenchmarkDepGraph1000(b *testing.B) {
	for n := 0; n < b.N; n++ {
		perfTest(1000)
	}
}

func BenchmarkDepGraph10000(b *testing.B) {
	for n := 0; n < b.N; n++ {
		perfTest(10000)
	}
}

func BenchmarkDepGraph100000(b *testing.B) {
	for n := 0; n < b.N; n++ {
		perfTest(100000)
	}
}

// Perf test proves that the DepGraph.Sync() complexity is linear with respect
// to the number of nodes. Actually, it is O(V+E), but each node has only
// a constant number of edges in this benchmark, reflecting a realistic
// use-case.
func perfTest(numOfItems int) {
	ctx = newTestCtx(depgraph.NewDepGraph(log))
	ctx.addConfigurator("item-type", depFromSliceAttr)
	const numOfDeps = 10
	for i := 0; i < numOfItems; i++ {
		item := mockItem{
			name:     strconv.Itoa(i),
			itemType: "item-type",
		}
		item.modifiableAttrs.sliceAttr = make([]string, 0, numOfDeps)
		for j := i + 1; j < numOfItems && j <= i+numOfDeps; j++ {
			item.modifiableAttrs.sliceAttr = append(
				item.modifiableAttrs.sliceAttr, strconv.Itoa(j))
		}
		ctx.graph.Item(item.name).Put(item)
	}
	ctx.syncGraph()
}
