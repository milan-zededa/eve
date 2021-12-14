// Copyright (c) 2021 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/lf-edge/eve/pkg/pillar/base"
)

const (
	// Forward-slash is used as a separator to build a path of nested clusters
	// as a string.
	clusterPathSep = "/"
)

type depGraph struct {
	*clusterSelector
	log *base.LogObject

	configurators map[string]Configurator // key = item type

	// All 3 maps below use item name as the key.
	nodes map[string]*node
	// Note that edges are kept even if the destination is missing.
	// If node from which an edge originates is removed, however,
	// then the edge is removed as well.
	outgoingEdges map[string]edges
	incomingEdges map[string]edges // inverted edges

	clusters map[string]*clusterInfo // key = cluster path

	// Nodes sorted lexicographically by clusterPath, then item Name.
	sortedNodes []*node

	plannedChanges plannedChanges
	syncIndex      int64 // to identify and log each run of Sync()
}

type node struct {
	name        string
	value       Item // never nil - nodes of deleted items are removed from the graph
	newValue    Item // used temporarily during Sync()
	needSync    bool // intended state = .newValue, actual state = .value
	state       ItemState
	lastOp      Operation
	lastErr     error
	clusterPath string
}

func (node *node) created() bool {
	// Note: node has state unknown if it was just added to the graph.
	return node.state != ItemStateUnknown &&
		node.state != ItemStatePending &&
		(node.state != ItemStateDeleting || node.lastOp != OperationDelete) &&
		(node.state != ItemStateRecreating || node.lastOp != OperationDelete) &&
		!node.failedToCreate()
}

func (node *node) failedToCreate() bool {
	return node.state == ItemStateFailure &&
		(node.lastOp == OperationCreate || node.lastOp == OperationRecreate)
}

func (node *node) getNewValue() Item {
	if node.needSync {
		return node.newValue
	}
	return node.value
}

type edge struct {
	// Edges always originate from nodes.
	// This field is therefore always defined.
	fromNode string // node.name

	// Currently edges always point to nodes, but later
	// we could add dependencies on e.g. clusters and instead of toNode
	// there would be toCluster defined.
	toNode string // node.name

	// Dependency represented by this edge.
	dependency Dependency
}

type edges []*edge

func remove(edges edges, edgeIndex int) edges {
	edges[edgeIndex] = edges[len(edges)-1]
	edges[len(edges)-1] = nil
	edges = edges[:len(edges)-1]
	return edges
}

type clusterInfo struct {
	name        string
	path        string
	nodeCount   int // includes nodes from sub-clusters
	description string
}

func parentClusterPath(subClusterPath string) string {
	pathPrefix := subClusterPath[:len(subClusterPath)-len(clusterPathSep)]
	index := strings.LastIndex(pathPrefix, clusterPathSep)
	if index == -1 {
		return ""
	}
	return pathPrefix[:index+1]
}

func clusterNameFromPath(clusterPath string) string {
	parentPath := parentClusterPath(clusterPath)
	return clusterPath[len(parentPath) : len(clusterPath)-len(clusterPathSep)]
}

// NewDepGraph returns a new instance of the dependency graph.
func NewDepGraph(log *base.LogObject) DepGraph {
	// top-level cluster has empty name and represents the graph as a whole
	topLevelClusterPath := clusterPathSep
	g := &depGraph{
		clusterSelector: &clusterSelector{
			clusterPath: topLevelClusterPath,
		},
		log:           log,
		configurators: make(map[string]Configurator),
		nodes:         make(map[string]*node),
		outgoingEdges: make(map[string]edges),
		incomingEdges: make(map[string]edges),
		clusters: map[string]*clusterInfo{
			topLevelClusterPath: {
				path: topLevelClusterPath,
			},
		},
	}
	g.clearPlannedChanges()
	g.clusterSelector.graph = g
	return g
}

// RegisterConfigurator makes association between a Configurator and items
// of the given type. When changes are made to the intended configuration for
// these items, DepGraph will know where to look for the implementation
// of the Create, Modify and Delete operations and will call them from Sync().
// Additionally, Configurator is used by DepGraph to learn the dependencies of an item.
func (g *depGraph) RegisterConfigurator(configurator Configurator, itemType string) error {
	_, duplicate := g.configurators[itemType]
	if duplicate {
		err := fmt.Errorf("multiple configurators registered for item type: %s",
			itemType)
		g.log.Error(err)
		return err
	}
	g.configurators[itemType] = configurator
	return nil
}

type syncMetrics struct {
	createCount int
	deleteCount int
	modifyCount int
}

// Sync performs all Create/Modify/Delete operations (provided by Configurators)
// to get the (new) intended and the actual (previous intended) state in-sync.
// Currently, it is assumed that the actual state is the same as the intended state
// of the last Sync() and the new intended state additionally contains all the changes
// that were made to the graph since.
func (g *depGraph) Sync(ctx context.Context) (err error) {
	if len(g.plannedChanges.clusters) == 0 &&
		len(g.plannedChanges.items) == 0 {
		// nothing to do
		return nil
	}

	defer g.clearPlannedChanges()
	syncIndex := g.syncIndex
	g.syncIndex++

	// 1. Sync cluster info
	g.syncClusterInfo()

	// 2. Sync item changes
	metrics, errs := g.syncItems(ctx)

	// 3. GC unused clusters
	for clusterPath, cluster := range g.clusters {
		if cluster.nodeCount == 0 {
			g.log.Noticef("Removing unused graph cluster %s", cluster.path)
			delete(g.clusters, clusterPath)
		}
	}

	// 4. Log summary info
	g.log.Noticef("Executed DepGraph.Sync() #%d: %dx Create, %dx Modify, %dx Delete; %d errors",
		syncIndex, metrics.createCount, metrics.modifyCount, metrics.deleteCount, len(errs))
	err = nil
	if len(errs) > 0 {
		var errMsgs []string
		for _, err := range errs {
			errMsgs = append(errMsgs, err.Error())
		}
		err = errors.New(strings.Join(errMsgs, "; "))
	}
	return err
}

// Sync all pending item changes.
// Create/Modify/Delete operations are performed in two stages:
//   1. First all Delete + Modify operations are executed (incl. the first half of the Recreate).
//   2. Next all (Re)Create operations are carried out.
// In both cases the nodes are traversed using DFS and the operations are executed
// in the forward or reverse topological order with respect to the dependencies.
// In the first stage, Delete/Modify operations are run in the DFS post-order, while
// in the seconds stage Create operations are lined up in the DFS pre-order.
// A simple stack structure is used to remember nodes which are being visited
// (recursion is intentionally avoided). In the first stage, each node is inserted into
// the stack only a constant number of times, whereas in the second stage a node
// could be added into the stack once for every outgoing edge to re-check dependencies
// of a pending item. Cumulatively, this gives us a time complexity O(V + E), where V
// represents the set of nodes and E the set of edges. In practise, the number of dependencies
// a configuration item will have is constant, hence the complexity can be simplified to O(V).
// The sparsity of the graph is the reason why DFS was selected over BFS.
func (g *depGraph) syncItems(ctx context.Context) (metrics syncMetrics, errs []error) {
	var err error
	// Initialize stacks, node.newValue and node.needSync.
	// Also add new nodes into the graph (with ItemStateUnknown for now).
	stage1Stack := newStack()
	stage2Stack := newStack()
	for _, itemChange := range g.plannedChanges.items {
		itemName := itemChange.itemName
		newValue := itemChange.newValue
		newClusterPath := itemChange.clusterPath
		node, exists := g.nodes[itemName]
		if !exists && newValue == nil {
			// delete of non-existing item is NOOP
			continue
		}
		if exists {
			if newValue != nil && node.value.External() != newValue.External() {
				panic(fmt.Sprintf("External-mismatch for item %s", itemName))
			}
			if newValue != nil && node.value.Type() != newValue.Type() {
				panic(fmt.Sprintf("Type-mismatch for item %s", itemName))
			}
			node.newValue = newValue
			node.needSync = true
			g.updateNodeClusterPath(itemName, newClusterPath)
		} else {
			var deps []Dependency
			if !newValue.External() {
				deps = g.getConfigurator(newValue.Type()).DependsOn(newValue)
				validateDeps(deps)
			}
			g.addNewNode(itemName, newClusterPath, newValue, deps)
		}
		stage1Stack.push(stackedItem{itemName: itemName})
	}

	// Stage 1: Run Delete + Modify operations
	// From every node to be deleted, run DFS and delete all nodes that
	// depend on it in the DFS *post-order*.
	// At this stage, a node state may change only in this direction:
	//  Created -> Deleting/Recreating/Modifying -> Failure/Pending/<Deleted>
	// Only at the transition from Created to Deleting/Recreating/Modifying
	// we trigger DFS from the node.
	for !stage1Stack.isEmpty() {
		item, _ := stage1Stack.pop()
		itemName := item.itemName
		node := g.nodes[itemName]
		itemType := node.value.Type()
		external := node.value.External()

		// Explicit item removal.
		if node.getNewValue() == nil {
			if !node.created() {
				// Item is not created, just remove node from the graph.
				g.removeNode(node)
				continue
			}
			if item.postOrder {
				// ready for Delete (items depending on this were already traversed)
				if !external {
					err = g.getConfigurator(itemType).Delete(ctx, node.value)
					metrics.deleteCount++
					if err != nil {
						node.lastOp = OperationDelete
						node.lastErr = err
						node.state = ItemStateFailure
						node.needSync = false
						errs = append(errs, err)
						continue
					}
				}
				g.removeNode(node)
				continue
			}
			// Delete after all items that depends on it are removed first.
			node.state = ItemStateDeleting
			stage1Stack.push(stackedItem{itemName: itemName, postOrder: true})
			g.schedulePreDelOps(node, stage1Stack)
			continue
		}

		// Update outgoing edges if needed.
		if node.needSync && !node.value.Equal(node.newValue) {
			var deps []Dependency
			if !external {
				deps = g.getConfigurator(itemType).DependsOn(node.getNewValue())
				validateDeps(deps)
			}
			g.updateNodeEdges(itemName, deps)
		}

		// Delete due to unsatisfied dependencies.
		if !g.hasSatisfiedDeps(node) {
			if !node.created() {
				// Cannot create due to a missing dependency.
				node.state = ItemStatePending
				if node.needSync {
					node.value = node.newValue
					node.needSync = false
				}
				continue
			}
			if item.postOrder {
				err = g.getConfigurator(itemType).Delete(ctx, node.value)
				metrics.deleteCount++
				node.lastOp = OperationDelete
				node.lastErr = err
				if err == nil {
					node.state = ItemStatePending
					if node.needSync {
						node.value = node.newValue
					}
				} else {
					node.state = ItemStateFailure
					errs = append(errs, err)
				}
				node.needSync = false
				continue
			}
			// Delete after all items that depends on it are removed first.
			node.state = ItemStateDeleting
			stage1Stack.push(stackedItem{itemName: itemName, postOrder: true})
			g.schedulePreDelOps(node, stage1Stack)
			continue
		}

		// Handle first half of the Recreate
		if g.needToRecreate(node) {
			if item.postOrder {
				err = g.getConfigurator(itemType).Delete(ctx, node.value)
				metrics.deleteCount++
				node.lastOp = OperationDelete
				node.lastErr = err
				if err == nil {
					if node.needSync {
						node.value = node.newValue
					}
					// Create is carried out in the next stage.
					stage2Stack.push(stackedItem{itemName: itemName})
				} else {
					node.state = ItemStateFailure
					errs = append(errs, err)
				}
				node.needSync = false
				continue
			}
			// Delete after all items that depends on it are removed first.
			node.state = ItemStateRecreating
			stage1Stack.push(stackedItem{itemName: itemName, postOrder: true})
			g.schedulePreDelOps(node, stage1Stack)
			continue
		}

		// Handle item modification.
		if node.needSync && node.created() {
			if node.value.Equal(node.newValue) {
				node.value = node.newValue
				node.needSync = false
				continue
			}
			if item.postOrder {
				err = nil
				if !external {
					err = g.getConfigurator(itemType).Modify(ctx, node.value, node.newValue)
					metrics.modifyCount++
					node.lastOp = OperationModify
					node.lastErr = err
				}
				node.needSync = false
				if err == nil {
					node.value = node.newValue
					// Some pending items might be now ready to be created.
					// (keep ItemStateModifying)
					stage2Stack.push(stackedItem{itemName: itemName})
				} else {
					node.state = ItemStateFailure
					errs = append(errs, err)
				}
				continue
			}
			node.state = ItemStateModifying
			stage1Stack.push(stackedItem{itemName: itemName, postOrder: true})
			g.schedulePreModifyOps(node, stage1Stack)
			continue
		}

		// Create is processed in the next stage.
		if !node.created() {
			stage2Stack.push(stackedItem{itemName: itemName})
			continue
		}
	}

	// Stage 2: Run (Re)Create operations
	// From every node to be created or that has been modified, run DFS and maybe
	// create some pending nodes that depend on it in the DFS *pre-order*.
	// At this stage, a node state may change only in this direction:
	//  Pending/Recreating/Modifying -> Created/Failure
	for !stage2Stack.isEmpty() {
		item, _ := stage2Stack.pop()
		itemName := item.itemName
		node := g.nodes[itemName]
		itemType := node.value.Type()
		external := node.value.External()

		// Handle (Re)Create.
		if !node.created() {
			if !g.hasSatisfiedDeps(node) {
				continue
			}
			err = nil
			if !external {
				err = g.getConfigurator(itemType).Create(ctx, node.getNewValue())
				metrics.createCount++
				if node.state == ItemStateRecreating {
					node.lastOp = OperationRecreate
				} else {
					node.lastOp = OperationCreate
				}
				node.lastErr = err
			}
			if node.needSync {
				node.value = node.newValue
				node.needSync = false
			}
			if err == nil {
				node.state = ItemStateCreated
				g.schedulePostPutOps(node, stage2Stack)
			} else {
				node.state = ItemStateFailure
				errs = append(errs, err)
			}
			continue
		}

		// Schedule possible Create operations that follow from a Modify
		if node.state == ItemStateModifying {
			g.schedulePostPutOps(node, stage2Stack)
			node.state = ItemStateCreated
		}

		if node.needSync {
			panic(fmt.Sprintf("node %s is unexpectedly not in-sync", node.name))
		}
	}
	return metrics, errs
}

func (g *depGraph) syncClusterInfo() {
	for _, cluster := range g.plannedChanges.clusters {
		info, exists := g.clusters[cluster.path]
		if exists {
			info.description = cluster.description
			continue
		}
		info = &clusterInfo{
			name:        cluster.name,
			path:        cluster.path,
			nodeCount:   0,
			description: cluster.description,
		}
		g.clusters[cluster.path] = info
		// ensure that parent clusters also have an entry
		for {
			parentPath := parentClusterPath(info.path)
			if parentPath == "" {
				break
			}
			info, exists = g.clusters[parentPath]
			if exists {
				break
			}
			info = &clusterInfo{
				name: clusterNameFromPath(parentPath),
				path: parentPath,
			}
			g.clusters[parentPath] = info
		}
	}
}

// If changingNode is about to be deleted, iterate over nodes that depend on it
// and check if they need to be deleted first because their dependencies will no
// longer be satisfied.
func (g *depGraph) schedulePreDelOps(changingNode *node, stack *stack) {
	for _, edge := range g.incomingEdges[changingNode.name] {
		switch edge.dependency.(type) {
		case *ItemIsCreated:
			nodeWithDep := g.nodes[edge.fromNode]
			if nodeWithDep.created() {
				// Removal of changingNode breaks dependencies of nodeWithDep.
				stack.push(stackedItem{itemName: nodeWithDep.name})
			}
		default:
			panic("Unsupported dependency type")
		}
	}
}

// If changingNode is about to be modified, iterate over nodes that depend on it
// and check if they need to be deleted or recreated.
func (g *depGraph) schedulePreModifyOps(changingNode *node, stack *stack) {
	for _, edge := range g.incomingEdges[changingNode.name] {
		switch dep := edge.dependency.(type) {
		case *ItemIsCreated:
			nodeWithDep := g.nodes[edge.fromNode]
			if nodeWithDep.created() {
				if dep.MustSatisfy != nil && !dep.MustSatisfy(changingNode.getNewValue()) {
					// Modification of changingNode breaks dependencies of nodeWithDep.
					stack.push(stackedItem{itemName: nodeWithDep.name})
					continue
				}
				if dep.RecreateWhenModified {
					stack.push(stackedItem{itemName: nodeWithDep.name})
					continue
				}
			}
		default:
			panic("Unsupported dependency type")
		}
	}
}

// Schedule items to be (re)processed after one of their dependencies was (Re)Created
// or Modified.
func (g *depGraph) schedulePostPutOps(node *node, stack *stack) {
	for _, edge := range g.incomingEdges[node.name] {
		switch edge.dependency.(type) {
		case *ItemIsCreated:
			nodeWithDep := g.nodes[edge.fromNode]
			if !nodeWithDep.created() && node.state != ItemStateFailure {
				stack.push(stackedItem{itemName: nodeWithDep.name})
			}
		default:
			panic("Unsupported dependency type")
		}
	}
}

func (g *depGraph) getConfigurator(itemType string) Configurator {
	configurator := g.configurators[itemType]
	if configurator == nil {
		panic(fmt.Sprintf("Missing configurator for item type %s", itemType))
	}
	return configurator
}

// RenderDOT returns DOT description of the graph content. This can be visualized
// with Graphviz and used for troubleshooting purposes.
func (g *depGraph) RenderDOT() (dot string, err error) {
	renderer := &dotRenderer{graph: g}
	return renderer.render()
}

func (g *depGraph) clearPlannedChanges() {
	g.plannedChanges.clusters = make(map[string]clusterInfo)
	g.plannedChanges.items = make(map[string]itemChange)
}

// Return index in the array g.sortedNodes at which the given node should be.
// Note that g.sortedNodes is ordered lexicographically first by node.clusterPath,
// then node.Name.
func (g *depGraph) findNodeIndex(nodeName, clusterPath string) (nodeIndex int) {
	return sort.Search(len(g.sortedNodes), func(i int) bool {
		node := g.sortedNodes[i]
		return (node.clusterPath == clusterPath && node.name == nodeName) ||
			(node.clusterPath == clusterPath && node.name > nodeName) ||
			node.clusterPath > clusterPath
	})
}

// Add new node into the graph together with all outgoing edges representing dependencies.
// Increases node-counters of all parent clusters.
// Will panic if the node already exist or if parent clusters do not exist.
func (g *depGraph) addNewNode(nodeName, clusterPath string, value Item, deps []Dependency) *node {
	// add to depGraph.nodes
	if _, exists := g.nodes[nodeName]; exists {
		panic(fmt.Sprintf("node %s is already present in the graph", nodeName))
	}
	node := &node{
		name:        nodeName,
		value:       value,
		clusterPath: clusterPath,
	}
	g.nodes[nodeName] = node
	// add to depGraph.sortedNodes
	nodeIndex := g.findNodeIndex(nodeName, clusterPath)
	g.sortedNodes = append(g.sortedNodes, nil)
	if nodeIndex < len(g.sortedNodes)-1 {
		copy(g.sortedNodes[nodeIndex+1:], g.sortedNodes[nodeIndex:])
	}
	g.sortedNodes[nodeIndex] = node
	// add edge for every dependency
	if len(g.outgoingEdges[nodeName]) > 0 {
		panic(fmt.Sprintf("node %s already has some outgoing edges", nodeName))
	}
	for _, itemDep := range deps {
		g.addNewEdge(nodeName, itemDep)
	}
	// increase node-counters of parent clusters
	parentCluster := clusterPath
	for parentCluster != "" {
		g.clusters[parentCluster].nodeCount++
		parentCluster = parentClusterPath(parentCluster)
	}
	return node
}

// Removes node from the graph, including all incoming and outgoing
// edges and decreases node-counters of parent clusters.
func (g *depGraph) removeNode(node *node) {
	// remove from depGraph.nodes
	delete(g.nodes, node.name)
	// remove from depGraph.sortedNodes
	nodeIndex := g.findNodeIndex(node.name, node.clusterPath)
	if nodeIndex >= len(g.sortedNodes) || g.sortedNodes[nodeIndex].name != node.name {
		panic(fmt.Sprintf("node %s is not present in depGraph.sortedNodes",
			node.name))
	}
	if nodeIndex < len(g.sortedNodes)-1 {
		copy(g.sortedNodes[nodeIndex:], g.sortedNodes[nodeIndex+1:])
	}
	g.sortedNodes[len(g.sortedNodes)-1] = nil
	g.sortedNodes = g.sortedNodes[:len(g.sortedNodes)-1]
	// decrease node-counters of parent clusters
	parentCluster := node.clusterPath
	for parentCluster != "" {
		g.clusters[parentCluster].nodeCount--
		parentCluster = parentClusterPath(parentCluster)
	}
	// remove all outgoing edges
	for _, edge := range g.outgoingEdges[node.name] {
		// remove it from incomingEdges of the opposite node
		g.removeIncomingEdge(edge)
	}
	delete(g.outgoingEdges, node.name)
}

func (g *depGraph) removeIncomingEdge(edge *edge) {
	if edge.toNode == "" {
		// Currently this should not be reachable,
		// but later we may have edges pointing to something else
		// than nodes (e.g. clusters), so let's not put panic in here.
		return
	}
	for i, inEdge := range g.incomingEdges[edge.toNode] {
		// compare pointers
		if inEdge == edge {
			g.incomingEdges[edge.toNode] = remove(
				g.incomingEdges[edge.toNode], i)
			return
		}
	}
}

func (g *depGraph) addNewEdge(nodeName string, dep Dependency) {
	switch dep := dep.(type) {
	case *ItemIsCreated:
		edge := &edge{
			fromNode:   nodeName,
			toNode:     dep.ItemName,
			dependency: dep,
		}
		g.outgoingEdges[nodeName] = append(
			g.outgoingEdges[nodeName], edge)
		g.incomingEdges[dep.ItemName] = append(
			g.incomingEdges[dep.ItemName], edge)
	default:
		panic("Unsupported dependency type")
	}
}

// Update the node's outgoing edges.
// Will panic if the node does not exist.
func (g *depGraph) updateNodeEdges(nodeName string, newDeps []Dependency) {
	_, exists := g.nodes[nodeName]
	if !exists {
		panic(fmt.Sprintf("node %s is not present in the graph", nodeName))
	}
	// Remove obsolete edges and update existing ones.
	edges := g.outgoingEdges[nodeName]
	for i := 0; i < len(edges); {
		var found bool
		edge := edges[i]
		for _, newDep := range newDeps {
			if edge.dependency.dependencyKey() == newDep.dependencyKey() {
				edge.dependency = newDep
				found = true
				break
			}
		}
		if !found {
			edges = remove(edges, i)
			g.removeIncomingEdge(edge)
		} else {
			i++
		}
	}
	g.outgoingEdges[nodeName] = edges
	// Add new edges.
	for _, newDep := range newDeps {
		var found bool
		for _, edge := range edges {
			if edge.dependency.dependencyKey() == newDep.dependencyKey() {
				found = true
				break
			}
		}
		if !found {
			g.addNewEdge(nodeName, newDep)
		}
	}
}

// Update the destination cluster of the node.
// Will panic if the node does not exist.
func (g *depGraph) updateNodeClusterPath(nodeName, newClusterPath string) {
	node, exists := g.nodes[nodeName]
	if !exists {
		panic(fmt.Sprintf("node %s is not present in the graph", nodeName))
	}
	// Decrease node-counters of the previous parent clusters.
	// Increase node-counters of the new parent clusters.
	if node.clusterPath != newClusterPath {
		parentCluster := node.clusterPath
		for parentCluster != "" {
			g.clusters[parentCluster].nodeCount--
			parentCluster = parentClusterPath(parentCluster)
		}
		node.clusterPath = newClusterPath
		parentCluster = node.clusterPath
		for parentCluster != "" {
			g.clusters[parentCluster].nodeCount++
			parentCluster = parentClusterPath(parentCluster)
		}
	}
}

// Check if node has all the dependencies satisfied.
func (g *depGraph) hasSatisfiedDeps(node *node) bool {
	if node.value.External() {
		return true
	}
	for _, edge := range g.outgoingEdges[node.name] {
		if !g.isSatisfiedDep(edge) {
			return false
		}
	}
	return true
}

// Check if the dependency represented by the edge is satisfied.
func (g *depGraph) isSatisfiedDep(edge *edge) bool {
	switch dep := edge.dependency.(type) {
	case *ItemIsCreated:
		depNode, exists := g.nodes[edge.toNode]
		if !exists {
			return false
		}
		if !depNode.created() {
			return false
		}
		if depNode.state == ItemStateRecreating ||
			depNode.state == ItemStateDeleting {
			return false
		}
		if depNode.getNewValue() == nil {
			return false
		}
		if dep.MustSatisfy != nil {
			if !dep.MustSatisfy(depNode.value) {
				return false
			}
			if depNode.needSync && !dep.MustSatisfy(depNode.getNewValue()) {
				return false
			}
		}
	default:
		panic("Unsupported dependency type")
	}
	return true
}

// Returns true if this node needs to be Re-created.
func (g *depGraph) needToRecreate(node *node) bool {
	if node.state == ItemStateRecreating {
		return true
	}
	if node.value.External() || !node.created() {
		return false
	}
	for _, edge := range g.outgoingEdges[node.name] {
		switch dep := edge.dependency.(type) {
		case *ItemIsCreated:
			depNode, exists := g.nodes[edge.toNode]
			if exists && depNode.state == ItemStateModifying {
				if dep.RecreateWhenModified {
					return true
				}
			}
		default:
			panic("Unsupported dependency type")
		}
	}
	modify := node.needSync && node.created() && !node.value.Equal(node.newValue)
	if modify {
		itemType := node.value.Type()
		return g.getConfigurator(itemType).NeedsRecreate(node.value, node.newValue)
	}
	return false
}

func validateDeps(deps []Dependency) {
	// Check if dependency is supported.
	for _, dep := range deps {
		switch dep.(type) {
		case *ItemIsCreated:
			// OK
		default:
			panic("Unsupported dependency type")
		}
	}
	// Multiple ItemIsCreated pointing to the same item are not allowed.
	for i := 0; i < len(deps); i++ {
		for j := i + 1; j < len(deps); j++ {
			if deps[i].dependencyKey() == deps[j].dependencyKey() {
				// Strictly speaking this is a programming error,
				// so let's just lazily put panic in here.
				panic(fmt.Sprintf("Duplicate dependencies (%s)",
					deps[i].dependencyKey()))
			}
		}
	}
}
