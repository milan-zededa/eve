// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"fmt"
	"strings"
)

// clusterSelector implements Selector interface.
type clusterSelector struct {
	graph       *depGraph
	clusterPath string
}

// itemSelector implements ItemOps interface.
type itemSelector struct {
	graph       *depGraph
	itemName    string
	clusterPath string
}

// Item : Select item with the specified name.
func (s *clusterSelector) Item(name string) ItemOps {
	return &itemSelector{
		graph:       s.graph,
		itemName:    name,
		clusterPath: s.clusterPath,
	}
}

// Cluster : Select (sub-)cluster with the specified name.
func (s *clusterSelector) Cluster(name string) Selector {
	return &clusterSelector{
		graph:       s.graph,
		clusterPath: s.clusterPath + name + clusterPathSep,
	}
}

// Put : Replace all items and sub-clusters inside this cluster.
func (s *clusterSelector) Put(cluster Cluster) {
	if clusterNameFromPath(s.clusterPath) != cluster.Name {
		panic("cluster name mismatch")
	}
	// Clear (set for Delete) all current items in this cluster.
	// For some, the nil value will be replaced with a new (or the same as before)
	// value in s.putCluster() below.
	_, alreadyChanged := s.graph.plannedChanges.clusters[s.clusterPath]
	if alreadyChanged {
		for itemName, item := range s.graph.plannedChanges.items {
			if strings.HasPrefix(item.clusterPath, s.clusterPath) {
				item.newValue = nil
				s.graph.plannedChanges.items[itemName] = item
			}
		}
	} else {
		s.Del()
	}
	s.putCluster(cluster, s.clusterPath)
}

func (s *clusterSelector) putCluster(cluster Cluster, clusterPath string) {
	s.graph.plannedChanges.clusters[clusterPath] = clusterInfo{
		name:        cluster.Name,
		path:        clusterPath,
		description: cluster.Description,
	}
	for _, item := range cluster.Items {
		s.graph.plannedChanges.items[item.Name()] = itemChange{
			itemName:    item.Name(),
			newValue:    item,
			clusterPath: clusterPath,
		}
	}
	for _, subCluster := range cluster.SubClusters {
		s.putCluster(subCluster, clusterPath+subCluster.Name+clusterPathSep)
	}
}

// Del : Delete entire cluster with all items and sub-clusters it contains.
func (s *clusterSelector) Del() {
	firstNode := s.graph.findNodeIndex("", s.clusterPath)
	for i := firstNode; i < len(s.graph.sortedNodes); i++ {
		node := s.graph.sortedNodes[i]
		if !strings.HasPrefix(node.clusterPath, s.clusterPath) {
			break
		}
		s.graph.plannedChanges.items[node.name] = itemChange{
			itemName:    node.name,
			newValue:    nil, // delete
			clusterPath: node.clusterPath,
		}
	}
}

// Get : Get summary info for all items in the cluster from DepGraph.
// This summary corresponds to the state after the last Sync().
func (s *clusterSelector) Get() (clusterSummary ClusterSummary, exists bool) {
	_, exists = s.graph.clusters[s.clusterPath]
	if !exists {
		return ClusterSummary{}, false
	}
	firstNode := s.graph.findNodeIndex("", s.clusterPath)
	clusterSummary, _ = s.getClusterSummary(s.clusterPath, firstNode)
	return clusterSummary, true
}

func (s *clusterSelector) getClusterSummary(clusterPath string, firstNode int) (
	clusterSummary ClusterSummary, nodeAfter int) {

	cluster, exists := s.graph.clusters[clusterPath]
	if !exists {
		panic(
			fmt.Sprintf("info for cluster %s should be available",
				s.clusterPath))
	}
	clusterSummary.Name = cluster.name
	clusterSummary.Description = cluster.description
	clusterSummary.ClusterPath = cluster.path
	i := firstNode
	for i < len(s.graph.nodes) {
		node := s.graph.sortedNodes[i]
		if !strings.HasPrefix(node.clusterPath, clusterPath) {
			break
		}
		if node.clusterPath == clusterPath {
			clusterSummary.Items = append(clusterSummary.Items,
				nodeToItemSummary(node))
			i++
			continue
		}
		// item is from a sub-cluster
		suffix := node.clusterPath[len(clusterPath):]
		nextSep := strings.Index(suffix, clusterPathSep)
		if nextSep == -1 {
			panic(fmt.Sprintf("invalid cluster path: %s", node.clusterPath))
		}
		subClusterPath := node.clusterPath[:len(clusterPath)+nextSep+len(clusterPathSep)]
		var subCluster ClusterSummary
		subCluster, i = s.getClusterSummary(subClusterPath, i)
		clusterSummary.SubClusters = append(clusterSummary.SubClusters, subCluster)
	}
	return clusterSummary, i
}

// Put : Create/Modify item.
func (s *itemSelector) Put(item Item) {
	if s.itemName != item.Name() {
		panic("item name mismatch")
	}
	s.graph.plannedChanges.items[s.itemName] = itemChange{
		itemName:    s.itemName,
		newValue:    item,
		clusterPath: s.clusterPath,
	}
}

// Del : Delete item.
func (s *itemSelector) Del() {
	s.graph.plannedChanges.items[s.itemName] = itemChange{
		itemName:    s.itemName,
		newValue:    nil, // delete
		clusterPath: s.clusterPath,
	}
}

// Get : Get summary info for the item from DepGraph.
// This summary corresponds to the item state after the last Sync().
func (s *itemSelector) Get() (itemSummary ItemSummary, exists bool) {
	var node *node
	node, exists = s.graph.nodes[s.itemName]
	if !exists || !strings.HasPrefix(node.clusterPath, s.clusterPath) {
		return ItemSummary{}, false
	}
	return nodeToItemSummary(node), true
}

func nodeToItemSummary(node *node) ItemSummary {
	return ItemSummary{
		Item:        node.value,
		State:       node.state,
		LastOp:      node.lastOp,
		LastError:   node.lastErr,
		ClusterPath: node.clusterPath,
	}
}
