// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package depgraph

import "fmt"

// New creates a new instance of the dependency graph.
func New(args InitArgs) Graph {
	// TODO
	return nil
}

// ID of the node.
func (n Node) ID() NodeID {
	return NodeIDFor(n.Item)
}

// AsGraph returns subgraph containing only this node.
func (n Node) AsGraph() GraphR {
	if n.graphR == nil {
		panic(fmt.Sprintf("Node %s is not part of any graph", n.ID()))
	}
	// TODO
	return nil
}

// IsDepSatisfied checks if the dependency represented by the edge is satisfied.
func (e Edge) IsDepSatisfied() bool {
	if e.graphR == nil {
		panic(fmt.Sprintf("Edge %s->%s is not part of any graph",
			e.FromNode, e.ToNode))
	}
	// TODO
	return false
}