// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package depgraph

import "context"

// DepGraph is a dependency graph [1].
// In EVE, the main use-case is to represent configuration items (interfaces, bridges,
// domains, etc.) as graph nodes and their dependencies as directed graph edges.
// For example, if there are nodes A and B with edge A->B, and the dependency is of type
// ItemIsCreated, then B should be created before A. Conversely, the removal of these
// two items should proceed in the opposite order, i.e. A should be removed first
// (think of it as "A cannot exist without B").
//
// The graph as a whole is used to represent the intended state of configuration items.
// However, the graph not only allows to store the configuration, but also to execute
// synchronization procedure (Sync() method), which performs all configuration changes,
// in an order that respects dependencies, to get from the current state to a new
// intended state. The actual configuration changes are delegated to Configurators,
// each registered with a specific type of configuration items (returned by Item.Type();
// type can be for example "Linux bridge").
// Configurator implements Create, Modify and Delete operations and also describes
// dependencies for a configuration item, which DepGraph uses to build graph edges.
// Apart from state synchronization, operation ordering and execution, the graph also
// keeps some state information for every item, for example the last error value.
//
// In order to make changes to the graph content (the intended state), first select
// a single item or a subset of the graph using Selector and call Put/Del methods
// as defined by ItemOps and ClusterOps. Once the new intended state is constructed,
// run Sync() to apply the changes down to the current system state through Configurators.
// The graph is NOT thread-safe. It is supposed to be used exclusively from within
// the main loop of a microservice.
//
// The concept of item clustering is borrowed from Graphviz [2]. Here the clusters are
// used to group related items and allow to select and edit them together.
// For example, all components of a network instance (bridge, routes, dnsmasq, etc.)
// can be grouped into one cluster with the name of the network instance. Then,
// if the network instance is removed, the intended state can be updated as easily as:
//     graph.Cluster(<NI-name>).Del()
// Also, the entire content of a cluster can be replaced with:
//     graph.Cluster(<name>).Put(<new-content>)
// DepGraph will run a "diff" algorithm to automatically determine which items should
// be created/modified/deleted.
// Clusters can be also nested and thus compose a hierarchical tree structure.
// This is very similar to directory structure of a filesystem if you think of clusters
// as directories and items as files.
// Top-level cluster has empty name and represents the graph as a whole.
// Currently, clustering is not related to and does not affect dependencies.
//
// The graph content can be exported into DOT [3] and then visualized for example
// using Graphviz [4]. Item clustering also helps here because related items are drawn
// together and contained within a rectangle.
//
// DepGraph is a generic solution for a common problem. The current implementation
// is very simple with a plenty of room for optimizations and improvements. For example,
// Configurators could be extended with Read operations (i.e. provide complete CRUD),
// and DepGraph could implement a full reconciliation between the actual and the intended
// state (similar to what Controllers in Kubernetes do). Currently, DepGraph assumes that
// the actual state is the same as the intended state of the last Sync() - but this could
// be wrong assumption if some operations failed or if the state changed somehow due to external
// factors. Additionally, DepGraph could automatically retry or revert failed operations.
// Also, since Dependency is just an interface, new kinds of dependencies with different semantic
// could be added as needed. But the main point is that all this functionality only needs
// to be implemented and maintained in one place.
//
// [1]: https://en.wikipedia.org/wiki/Dependency_graph
// [2]: https://graphviz.org/Gallery/directed/cluster.html
// [3]: https://en.wikipedia.org/wiki/DOT_(graph_description_language)
// [4]: https://graphviz.org/
type DepGraph interface {
	// Methods to select a single item or a subset of the graph and make changes to it.
	Selector

	// RegisterConfigurator makes association between a Configurator and items
	// of the given type. When changes are made to the intended configuration for
	// these items, DepGraph will know where to look for the implementation
	// of the Create, Modify and Delete operations and will call them from Sync().
	// Additionally, Configurator is used by DepGraph to learn the dependencies of an item.
	RegisterConfigurator(configurator Configurator, itemType string) error

	// Sync performs all Create/Modify/Delete operations (provided by Configurators)
	// to get the (new) intended and the actual (previous intended) state in-sync.
	// Currently, it is assumed that the actual state is the same as the intended state
	// of the last Sync() and the new intended state additionally contains all the changes
	// that were made to the graph since.
	// Operations are performed in the topological order with respect to the dependencies.
	// It is recommended to call this at the end of a microservice main loop, i.e.:
	//
	// graph := NewDepGraph()
	// graph.RegisterConfigurator(configurator, itemType)
	// ...
	// for {
	//     select {
	//         case <-sub1:
	//             ... // potentially make changes to the intended state
	//         case <-sub2:
	//             ...
	//     }
	//     graph.Sync()
	// }
	Sync(ctx context.Context) error

	// RenderDOT returns DOT description of the graph content. This can be visualized
	// with Graphviz and used for troubleshooting purposes.
	RenderDOT() (dot string, err error)
}

// Selector allows to select a single item or a subset of the graph and make changes to it.
// Note that changes do not take effect until DepGraph.Sync() is called.
type Selector interface {
	// Note: Top-level cluster has empty name and covers the entire graph.
	ClusterOps
	// Item : Select item with the specified name.
	Item(name string) ItemOps
	// Cluster : Select (sub-)cluster with the specified name.
	Cluster(name string) Selector
}

// ItemOps : operations to change/get the intended state of a single item.
// Note that changes do not take effect until DepGraph.Sync() is called.
type ItemOps interface {
	// Put : Create/Modify item.
	Put(item Item)
	// Del : Delete item.
	Del()
	// Get : Get summary info for the item from DepGraph.
	// This summary corresponds to the item state after the last Sync().
	Get() (itemSummary ItemSummary, exists bool)
}

// ClusterOps : operations to change/get the intended state of all items inside
// a particular cluster.
// Note that changes do not take effect until DepGraph.Sync() is called.
type ClusterOps interface {
	// Put : Replace all items and sub-clusters inside this cluster.
	Put(cluster Cluster)
	// Del : Delete entire cluster with all items and sub-clusters it contains.
	Del()
	// Get : Get summary info for all items in the cluster from DepGraph.
	// This summary corresponds to the state after the last Sync().
	Get() (clusterSummary ClusterSummary, exists bool)
}

// Item is something that can be created, modified and deleted.
// This could be for example a network interface, volume instance, configuration file, etc.
// In DepGraph, each item is represented as a single graph node.
// Note that the term "node" is already overused in EVE and intentionally not used here.
// Beware that items are stored inside the graph and their content should not change
// in any other way than through the DepGraph APIs. It is recommended to implement the Item
// interface with *value* receivers (or alternatively pass *copied* item values to the graph).
type Item interface {
	// Name should return a unique string identifier for the item instance.
	// The name should be unique across *all* items (of all item types and in all clusters).
	Name() string
	// Label is an optional alternative name that does not have to be unique.
	// It is only used in the graph visualization as the label for the graph node
	// that represents the item. If empty string is returned, Item.Name() is used
	// for labeling instead.
	Label() string
	// Type groups item instances handled by the same Configurator.
	// For example, type could be "Linux bridge".
	Type() string
	// Equal compares this and the other item instance (of the same name)
	// for equivalency.
	// If the current and the new intended state of an item is equal, then DepGraph
	// will not call Modify.
	Equal(Item) bool
	// External should return true for items which are not created/modified/deleted
	// by this instance of DepGraph using a Configurator (i.e. do not register
	// Configurator with external items).
	// This could be used for items created by other microservices.
	// Presence of external items in the graph is used only for dependency purposes.
	// (e.g. create A only after another microservice created B).
	External() bool
	// String should return a human-readable description of the item instance.
	// (e.g. a network interface configuration)
	String() string
}

// Dependency of an item.
// The interface methods are intentionally lower-case because Dependency implementation
// can only come from the DepGraph package itself.
type Dependency interface {
	// Dependencies of a given item should each have a unique key, otherwise they are
	// treated as duplicates.
	// In other words, Configurator.DependsOn() should not return multiple dependencies
	// with the same key.
	dependencyKey() string
	//  String should return a human-readable description of the dependency.
	String() string
}

// ItemIsCreated is a dependency which is satisfied if Item with the name ItemName
// is already created and MustSatisfy returns true for that item or is nil.
type ItemIsCreated struct {
	// ItemName : name of an item which must be already created.
	ItemName string
	// MustSatisfy : used if the referenced item must not only exist but also satisfy
	// a certain condition. For example, a network route may depend on a specific network
	// interface to exist and also to have a specific IP address assigned. MustSatisfy can
	// check for the presence of the IP address.
	// This function may get called quite often so keep it lightweight.
	MustSatisfy func(Item) bool
	// RecreateWhenModified : Whenever the referenced item changes (through Modify), recreate
	// the items that depend on it.
	RecreateWhenModified bool
	// Description : optional description of the dependency.
	Description string
}

func (dep ItemIsCreated) dependencyKey() string {
	return dep.ItemName
}

// String returns dependency description.
func (dep ItemIsCreated) String() string {
	return dep.Description
}

// Cluster allows to group items which are in some way related to each other.
// For example, all components of the same application (domain, volumes, VIFs),
// could be grouped under one cluster.
// This is mostly used to simplify graph modifications. For example, cluster
// as whole, with all the items it contains, can be removed with one call
// (to ClusterOps.Del()) or the content can be replaced with a new set of items
// (using ClusterOps.Put()). When the content is replaced, DepGraph will run a "diff"
// algorithm to automatically determine which items should be created/modified/deleted
// (i.e. you do not have to implement "diff" yourself).
// Clusters can be also nested and thus compose a hierarchical tree structure.
// This is very similar to directory structure of a filesystem if you think of clusters
// as directories and items as files.
// Top-level cluster has empty name and represents the graph as a whole.
// Currently, clustering is not related to and does not affect dependencies.
type Cluster struct {
	// Name of the cluster.
	// Should be unique at least within the parent cluster.
	// Name is not allowed to contain the forward slash character ("/").
	Name string
	// Some human-readable explanation of the cluster's purpose.
	Description string
	Items       []Item
	SubClusters []Cluster
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
	// Expect to find non-nil LastError inside ItemSummary.
	ItemStateFailure
	// ItemStateDeleting : item is being removed. This is only an intermittent
	// item state used during the DepGraph.Sync() procedure. It is never
	// returned by ItemOps.Get() or ClusterOps.Get().
	ItemStateDeleting
	// ItemStateModifying : item is being modified. This is only an intermittent
	// item state used during the DepGraph.Sync() procedure. It is never
	// returned by ItemOps.Get() or ClusterOps.Get().
	ItemStateModifying
	// ItemStateRecreating : item is being re-created. This is only an intermittent
	// item state used during the DepGraph.Sync() procedure. It is never
	// returned by ItemOps.Get() or ClusterOps.Get().
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
	case ItemStateDeleting:
		return "deleting"
	case ItemStateModifying:
		return "modifying"
	case ItemStateRecreating:
		return "recreating"
	}
	return ""
}

// Operation : operation done over an item through a Configurator.
type Operation int

const (
	// OperationUnknown : unknown operation
	OperationUnknown Operation = iota
	// OperationCreate : Configurator.Create() was called
	OperationCreate
	// OperationDelete : Configurator.Delete() was called
	OperationDelete
	// OperationModify : Configurator.Modify() was called
	OperationModify
	// OperationRecreate : Configurator.Delete() followed by Configurator.Create() was called
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

// ItemSummary encapsulates summary information for a single item.
type ItemSummary struct {
	Item        Item
	State       ItemState
	LastOp      Operation
	LastError   error
	ClusterPath string // Path to the item's parent cluster
}

// ClusterSummary encapsulates summary information for all items and sub-clusters
// contained by a cluster.
type ClusterSummary struct {
	Name        string
	Description string
	// ClusterPath is the path to this cluster from the top of the graph.
	// Forward-slash is used to separate names of the clusters that the path
	// consists of.
	// (just like absolute path to a directory in the Unix-like systems).
	ClusterPath string
	// Items are sorted by their names.
	Items []ItemSummary
	// Nested clusters are sorted by their names.
	SubClusters []ClusterSummary
}

// Configurator implements Create, Modify and Delete operations for items of the same type.
// For DepGraph it is a "backend" which the graph calls as needed to sync the actual and
// the intended state.
// Additionally, Configurator is used by DepGraph to learn the dependencies of an item.
type Configurator interface {
	// Create should create the item (e.g. create a Linux bridge with the given parameters).
	Create(ctx context.Context, item Item) error
	// Modify should change the item to the new desired state (e.g. change interface IP address).
	Modify(ctx context.Context, oldItem, newItem Item) (err error)
	// Delete should remove the item (e.g. stop application domain).
	Delete(ctx context.Context, item Item) error
	// NeedsRecreate should return true if changing the item to the new desired state
	// requires the item to be completely re-created. DepGraph will then perform the change
	// as Delete(oldItem) followed by Create(newItem) instead of calling Modify.
	NeedsRecreate(oldItem, newItem Item) (recreate bool)
	// DependsOn returns a list of all dependencies that have to be satisfied before
	// the item can be created (i.e. dependencies in the returned list are AND-ed).
	// Item which cannot be created due to unsatisfied dependencies will be internally marked
	// as pending with ItemStatePending (DepGraph allows to obtain the current state of items).
	DependsOn(item Item) []Dependency
}
