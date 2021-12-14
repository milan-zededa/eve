// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package depgraph

// Graph is a dependency graph.
// The main use-case is to represent configuration items (network interfaces, routes,
// volumes, etc.) or any managed stateful objects (incl. processes, containers, files,
// etc.) as graph nodes and their dependencies as directed graph edges.
// For more information please see README.md.
type Graph interface {
	GraphR

	// PutNode adds a new node into the graph or update an existing one.
	// Note that PutNode may also move node from one subgraph to another.
	PutNode(*Node)
	// DelNode deletes an existing node from the graph.
	// Returns true if the node existed and was actually deleted.
	DelNode(NodeID) bool

	// PutSubGraph adds a new subgraph into this graph or update an existing
	// subgraph. This refers to a direct child of this graph, cannot add/update
	// a nested subgraphs.
	PutSubGraph(Graph)
	// DelSubGraph deletes existing subgraph. This refers to a direct child of this
	// graph, cannot delete a nested subgraphs.
	// Returns true if the subgraph existed and was actually deleted.
	DelSubGraph(name string) bool
	// EditSubGraph elevates read-only subgraph handle to read-write access.
	// Panics if the given graph is not actually subgraph (direct or nested)
	// of this graph.
	EditSubGraph(GraphR) Graph
	// EditParentGraph returns read-write handle to a (direct) parent graph
	// of this subgraph.
	EditParentGraph() Graph

	// PutPrivateData allows the user to store any data with the graph.
	// Note that it is also possible to store private data next to each node.
	// (see Node.PrivateData)
	PutPrivateData(interface{})
}

// GraphR : Read-only access to a dependency graph.
type GraphR interface {
	// Name assigned to the (sub)graph.
	Name() string
	// Description assigned to the (sub)graph.
	Description() string

	// Node returns a node inside the graph.
	// The function will look for the node also inside all the subgraphs.
	// Returns nil if node with such ID is not present in the graph.
	// Use NodeIDFor() to obtain ID for node that corresponds to a given item.
	Node(NodeID) (node Node, found bool)
	// Nodes returns an iterator for nodes inside this graph.
	// If inclSubGraphs is set to true, the iteration will include nodes
	// from subgraphs (both direct and nested).
	Nodes(inclSubGraphs bool) NodeIterator

	// SubGraph returns a read-only handle to a (direct, not nested) subgraph.
	SubGraph(name string) GraphR
	// SubGraphs returns an iterator for (direct) subgraphs of this graph.
	SubGraphs() GraphIterator
	// SubGraph returns a read-only handle to the (direct) parent graph.
	ParentGraph() GraphR

	// OutgoingEdges returns iterator for all outgoing edges of the given node.
	OutgoingEdges(NodeID) EdgeIterator
	// OutgoingEdges returns iterator for all incoming edges of the given node.
	IncomingEdges(NodeID) EdgeIterator
	// DetectCycle checks if the graph contains a cycle (which it should not,
	// dependency graph is supposed to be DAG) and the first one found is returned
	// as a list of IDs of nodes inside the cycle.
	DetectCycle() []NodeID

	// PrivateData returns whatever custom data has the user stored with the graph.
	PrivateData() interface{}
}

// Item is something that can be created, modified and deleted, essentially a stateful
// object. This could be for example a network interface, volume instance, configuration
// file, etc. In Graph, each item is represented as a single graph node.
// Beware that items are stored inside the graph and their content should not change
// in any other way than through the Graph APIs. It is recommended to implement the Item
// interface with *value* receivers (or alternatively pass *copied* item values to the graph).
type Item interface {
	// Name should return a unique string identifier for the item instance.
	// Name is not allowed to contain the forward slash character ("/").
	// It is required for the name to be unique only within item instances of the
	// same type (see Type()). A globally unique item identifier is therefore
	// a combination of the item type and the item name.
	Name() string
	// Label is an optional alternative name that does not have to be unique.
	// It is only used in the graph visualization as the label for the graph node
	// that represents the item. If empty string is returned, Item.Name() is used
	// for labeling instead.
	Label() string
	// Type should return the name of the item type.
	// Type is not allowed to contain the forward slash character ("/").
	// This is something like reflect.TypeOf(item).Name(), but potentially much more
	// human-readable.
	// For example, type could be "Linux bridge".
	Type() string
	// Equal compares this and the other item instance (of the same type and name)
	// for equivalency. For the purposes of state reconciliation (see libs/reconciler),
	// Equal determines if the current and the new intended state of an item is equal,
	// or if a state transition is needed.
	Equal(Item) bool
	// External should return true for items which are not managed (created/modified/deleted)
	// by the caller/owner. This could be used for items created by other management agents
	// or to represent system notifications (e.g. interface link is up).
	// For reconciliation, the presence of external items in the graph is used only for
	// dependency purposes (e.g. create A only after another microservice created B).
	External() bool
	// String should return a human-readable description of the item instance.
	// (e.g. a network interface configuration)
	String() string
	// Dependencies returns a list of all dependencies that have to be satisfied before
	// the item can be created (i.e. dependencies in the returned list are AND-ed).
	// Should be empty for external item (see Item.External()).
	Dependencies() []Dependency
}

// Node of a dependency graph. Stores an instance of the Item alongside
// some state information, useful for reconciliation purposes.
// It is up to the graph user to fill the content of the Node as needed
// and then to call Graph.PutNode(node).
// Note that Node implements methods ID() and AsGraph() to present itself
// as a single-node read-only graph.
type Node struct {
	Item Item

	// State, LastOperation and LastError can be used for state
	// reconciliation purposes.
	State         ItemState
	LastOperation Operation
	LastError     error

	// PrivateData for the user of the graph to store anything.
	PrivateData interface{}

	// Internal, non-nil if the node is part of a graph.
	graphR GraphR
}

// NodeID is a unique identifier of a node inside an entire graph.
// It is based on the ID of the item inside.
// Obtain with Node.ID() or using function NodeIDFor().
// Currently it is a string, but do not rely on it. Use NodeID.String()
// if you need a printable representation.
// The NodeID type is guaranteed to be comparable and therefore can be used
// as a map key.
type NodeID string

// NodeIDFor returns corresponding ID for node representing a given item.
func NodeIDFor(item Item) NodeID {
	return NodeID(item.Type() + "/" + item.Name())
}

// String returns string representation of the NodeID.
// Note that currently NodeID IS a string, but there is also an idea
// of using a 64 byte hashes instead (to be compatible with gonum graphs [1]
// for example), so avoid doing "string(nodeID)" and instead call this function.
//
// [1] https://pkg.go.dev/gonum.org/v1/gonum/graph#Directed
func (id NodeID) String() string {
	return string(id)
}

// Edge represents a directed edge of a dependency graph.
// To check if the edge represents satisfied dependency, call Edge.IsDepSatisfied().
type Edge struct {
	FromNode NodeID
	ToNode   NodeID
	// Dependency represented by this edge.
	Dependency Dependency

	// Internal, non-nil if the edge is part of a graph.
	graphR GraphR
}

// Dependency which is considered satisfied if the referenced Item is already created
// and MustSatisfy returns true for that item or is nil.
type Dependency struct {
	// Item which must be already created, referenced by item ID.
	Item RequiredItem
	// MustSatisfy : used if the required item must not only exist but also satisfy
	// a certain condition. For example, a network route may depend on a specific network
	// interface to exist and also to have a specific IP address assigned. MustSatisfy can
	// check for the presence of the IP address.
	// This function may get called quite often so keep it lightweight.
	MustSatisfy func(Item) bool
	// Description : optional description of the dependency.
	Description string
	// Attributes : some additional attributes that may be helpful in special cases
	// to further describe a dependency.
	Attributes  DependencyAttributes
}

// DependencyAttributes : some additional attributes that may be helpful in special cases
// to further describe a dependency.
type DependencyAttributes struct {
	// RecreateWhenModified : items that have this dependency should be recreated whenever
	// the required item changes (through Modify).
	RecreateWhenModified bool
	// AutoDeletedByExternal : items that have this dependency are automatically/externally
	// deleted (by other agents or by the managed system itself) whenever the required
	// *external* item is deleted. If the required item is not external (Item.External()
	// returns false), this dependency attribute should be ignored.
	AutoDeletedByExternal bool
}

// RequiredItem : item required due to some dependency.
type RequiredItem struct {
	Type string
	Name string
}

// ItemState : state of an item inside the dependency graph.
type ItemState int

const (
	// ItemStateUnknown : item state is not known.
	ItemStateUnknown ItemState = iota
	// ItemStateCreated : item is successfully created.
	ItemStateCreated
	// ItemStatePending : item cannot be yet created because one or more
	// dependencies are not satisfied.
	ItemStatePending
	// ItemStateFailure : last Create/Modify/Delete operation failed.
	// Expect to find non-nil Node.LastError.
	ItemStateFailure
	// ItemStateCreating : item is being created.
	// This can be used as an intermittent state during reconciliation
	// or for asynchronous Create.
	ItemStateCreating
	// ItemStateDeleting : item is being removed.
	// This can be used as an intermittent state during reconciliation
	// or for asynchronous Delete.
	ItemStateDeleting
	// ItemStateModifying : item is being modified.
	// This can be used as an intermittent state during reconciliation
	// or for asynchronous Modify.
	ItemStateModifying
	// ItemStateRecreating : item is being re-created.
	// This can be used as an intermittent state during reconciliation
	// or for asynchronous Create after Delete.
	ItemStateRecreating
)

// String returns string representation of the item state.
func (s ItemState) String() string {
	switch s {
	case ItemStateUnknown:
		return "unknown"
	case ItemStateCreated:
		return "created"
	case ItemStatePending:
		return "pending"
	case ItemStateFailure:
		return "failure"
	case ItemStateCreating:
		return "creating"
	case ItemStateDeleting:
		return "deleting"
	case ItemStateModifying:
		return "modifying"
	case ItemStateRecreating:
		return "recreating"
	}
	return ""
}

// Operation : operation done over an item.
type Operation int

const (
	// OperationUnknown : unknown operation
	OperationUnknown Operation = iota
	// OperationCreate : Create() operation
	OperationCreate
	// OperationDelete : Delete() operation
	OperationDelete
	// OperationModify : Modify() operation
	OperationModify
	// OperationRecreate : Delete() followed by Create()
	OperationRecreate
)

// String returns string representation of the operation.
func (o Operation) String() string {
	switch o {
	case OperationUnknown:
		return "unknown"
	case OperationCreate:
		return "create"
	case OperationDelete:
		return "delete"
	case OperationModify:
		return "modify"
	case OperationRecreate:
		return "recreate"
	}
	return ""
}

// NodeIterator iterates nodes of a graph.
// Nodes are ordered lexicographically first by subgraphs (in DFS order)
// and secondly by node ID.
type NodeIterator interface {
	Iterator

	// Node returns the current Node from the iterator.
	Node() Node
}

// EdgeIterator iterates outgoing or incoming edges of a node.
// The order of edges is undefined.
type EdgeIterator interface {
	Iterator

	// Edge returns the current Edge from the iterator.
	Edge() Edge
}

// GraphIterator iterates subgraphs of a graph.
// The order of subgraphs is undefined.
type GraphIterator interface {
	Iterator

	// SubGraph returns the current subgraph from the iterator.
	SubGraph() GraphR
}

// Iterator : a common iterator interface.
type Iterator interface {
	// Next advances the iterator and returns whether the next call
	// to the Node()/Edge()/... method will return a non-nil value.
	// Next should be called prior to any call to the iterator's
	// item retrieval method after the iterator has been obtained or reset.
	Next() bool

	// Len returns the number of items remaining in the iterator.
	Len() int

	// Reset returns the iterator to its start position.
	Reset()
}

// InitArgs : input arguments to use with the (sub)graph constructor New().
type InitArgs struct {
	// Name of the graph.
	Name string
	// Description for the graph.
	Description string
	Items       []Item
	Subgraphs   []InitArgs
	// PrivateData for the user of the graph to store anything.
	PrivateData interface{}
}
