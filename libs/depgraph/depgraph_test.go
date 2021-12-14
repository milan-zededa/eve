// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package depgraph_test

import (
	"testing"

	"github.com/lf-edge/eve/libs/depgraph"
	. "github.com/onsi/gomega"
)

func TestItemsWithoutDependencies(test *testing.T) {
	t := NewGomegaWithT(test)

	itemA := mockItem{
		name:     "A",
		itemType: "type1",
		attrs:    mockItemAttrs{intAttr: 10, strAttr: "abc"},
	}
	itemB := mockItem{
		name:     "B",
		itemType: "type1",
		attrs:    mockItemAttrs{boolAttr: true},
	}
	itemC := mockItem{
		name:     "C",
		itemType: "type2",
	}

	initArgs := depgraph.InitArgs{
		Name:        "Graph without edges",
		Description: "This graph has items with dependencies",
		Items:       []depgraph.Item{itemA, itemB, itemC},
	}
	g := depgraph.New(initArgs)
	t.Expect(g).ToNot(BeNil())
	t.Expect(g.Name()).To(Equal(initArgs.Name))
	t.Expect(g.Description()).To(Equal(initArgs.Description))
	t.Expect(g.SubGraphs().Len()).To(BeZero())
	t.Expect(g.ParentGraph()).To(BeNil())
	t.Expect(g.Nodes(true).Len()).To(BeZero())
	// TODO: continue
}
