// Copyright (c) 2021 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"fmt"
	"io"
	"strings"
)

type dotRenderer struct {
	graph *depGraph
}

const (
	indentChar = "\t"
)

// Return DOT description of the graph content. This can be visualized
// with Graphviz and used for troubleshooting purposes.
func (r *dotRenderer) render() (dot string, err error) {
	sb := strings.Builder{}
	sb.WriteString("digraph G {\n")
	// Render clusters starting with the implicit top-level one.
	_, err = r.renderCluster(&sb, clusterPathSep, 0, indentChar)
	if err != nil {
		return "", err
	}
	// Output all edges.
	missingNodes := make(map[string]struct{}) // not in the graph but with edges pointing to them
	for _, edges := range r.graph.outgoingEdges {
		for _, edge := range edges {
			if edge.toNode == "" {
				continue
			}
			if _, exists := r.graph.nodes[edge.toNode]; !exists {
				missingNodes[edge.toNode] = struct{}{}
			}
			var color string
			if r.graph.isSatisfiedDep(edge) {
				color = "black"
			} else {
				color = "red"
			}
			sb.WriteString(fmt.Sprintf("%s%s -> %s [color = %s, tooltip = \"%s\"];\n",
				indentChar, escapeName(edge.fromNode), escapeName(edge.toNode), color,
				escapeTooltip(edge.dependency.String())))
		}
	}
	for node := range missingNodes {
		sb.WriteString(fmt.Sprintf("%s%s [color = grey, style = dashed, "+
			"tooltip = \"<missing>\"];\n", indentChar, escapeName(node)))
	}
	sb.WriteString("}\n")
	return sb.String(), nil
}

func (r *dotRenderer) renderCluster(w io.StringWriter, clusterPath string,
	firstNode int, indent string) (nodeAfter int, err error) {
	nestedIndent := indent

	// output cluster header
	clusterName := clusterNameFromPath(clusterPath)
	if clusterName != "" {
		nestedIndent += indentChar
		cluster, ok := r.graph.clusters[clusterPath]
		if !ok {
			return 0, fmt.Errorf("failed to get info for cluster %s",
				clusterPath)
		}
		w.WriteString(fmt.Sprintf("%ssubgraph cluster_%s {\n",
			indent, escapeName(clusterName)))
		w.WriteString(fmt.Sprintf("%scolor = blue;\n", nestedIndent))
		w.WriteString(fmt.Sprintf("%slabel = %s;\n",
			nestedIndent, escapeName(cluster.name)))
		w.WriteString(fmt.Sprintf("%stooltip = \"%s\";\n",
			nestedIndent, escapeTooltip(cluster.description)))
	}

	// output nodes and sub-clusters
	i := firstNode
	for i < len(r.graph.nodes) {
		node := r.graph.sortedNodes[i]
		if !strings.HasPrefix(node.clusterPath, clusterPath) {
			break
		}
		if node.clusterPath == clusterPath {
			var color string
			switch node.state {
			case ItemStatePending:
				color = "grey"
			case ItemStateFailure:
				color = "red"
			default:
				color = "black"
			}
			w.WriteString(fmt.Sprintf("%s%s [color = %s, tooltip = \"%s\"];\n",
				nestedIndent, escapeName(node.name), color,
				escapeTooltip(node.value.String())))
			i++
			continue
		}

		// node is from a sub-cluster
		suffix := node.clusterPath[len(clusterPath):]
		nextSep := strings.Index(suffix, clusterPathSep)
		if nextSep == -1 {
			panic(fmt.Sprintf("invalid cluster path: %s", node.clusterPath))
		}
		subClusterPath := node.clusterPath[:len(clusterPath)+nextSep+len(clusterPathSep)]
		i, err = r.renderCluster(w, subClusterPath, i, nestedIndent)
		if err != nil {
			return 0, err
		}
	}

	// closing cluster bracket
	if clusterName != "" {
		w.WriteString(indent + "}\n")
	}
	return i, err
}

func escapeName(name string) string {
	return strings.Replace(name, "-", "_", -1)
}

func escapeTooltip(tooltip string) string {
	return strings.Replace(tooltip, "\n", "\\n", -1)
}
