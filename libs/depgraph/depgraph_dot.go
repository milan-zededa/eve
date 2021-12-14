// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"fmt"
	"io"
	"sort"
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
	_, err = sb.WriteString("digraph G {\n")
	if err != nil {
		return "", err
	}
	// Render clusters starting with the implicit top-level one.
	_, err = r.renderCluster(&sb, clusterPathSep, 0, indentChar, r.genHueMap())
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
			_, err = sb.WriteString(fmt.Sprintf("%s%s -> %s [color = %s, tooltip = \"%s\"];\n",
				indentChar, escapeName(edge.fromNode), escapeName(edge.toNode), color,
				escapeTooltip(edge.dependency.String())))
			if err != nil {
				return "", err
			}
		}
	}
	for nodeName := range missingNodes {
		_, err = sb.WriteString(fmt.Sprintf("%s%s [color = grey, style = dashed, "+
			"shape = ellipse, tooltip = \"<missing>\", label = \"%s\"];\n",
			indentChar, escapeName(nodeName), nodeName))
		if err != nil {
			return "", err
		}
	}
	_, err = sb.WriteString("}\n")
	if err != nil {
		return "", err
	}
	return sb.String(), nil
}

func (r *dotRenderer) renderCluster(w io.StringWriter, clusterPath string,
	firstNode int, indent string, hueMap map[string]float32) (nodeAfter int, err error) {
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
		_, err = w.WriteString(fmt.Sprintf("%ssubgraph cluster_%s {\n",
			indent, escapeName(clusterName)))
		if err != nil {
			return 0, err
		}
		_, err = w.WriteString(fmt.Sprintf("%scolor = black;\n",
			nestedIndent))
		if err != nil {
			return 0, err
		}
		_, err = w.WriteString(fmt.Sprintf("%slabel = %s;\n",
			nestedIndent, escapeName(cluster.name)))
		if err != nil {
			return 0, err
		}
		_, err = w.WriteString(fmt.Sprintf("%stooltip = \"%s\";\n",
			nestedIndent, escapeTooltip(cluster.description)))
		if err != nil {
			return 0, err
		}
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
			var saturation float32
			switch node.state {
			case ItemStatePending:
				color = "grey"
				saturation = 0.25
			case ItemStateFailure:
				color = "red"
				saturation = 0.60
			default:
				color = "black"
				saturation = 0.60
			}
			hue := hueMap[node.value.Type()]
			fillColor := fmt.Sprintf("%.3f %.3f 0.800",
				hue, saturation)
			label := node.value.Label()
			if label == "" {
				label = node.name
			}
			shape := "ellipse"
			if node.value.External() {
				shape = "doubleoctagon"
			}
			_, err = w.WriteString(fmt.Sprintf("%s%s [color = %s, fillcolor = \"%s\", "+
				"shape = %s, style = filled, tooltip = \"%s\", label = \"%s\"];\n",
				nestedIndent, escapeName(node.name), color, fillColor, shape,
				escapeTooltip(node.value.String()), label))
			if err != nil {
				return 0, err
			}
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
		i, err = r.renderCluster(w, subClusterPath, i, nestedIndent, hueMap)
		if err != nil {
			return 0, err
		}
	}

	// closing cluster bracket
	if clusterName != "" {
		_, err = w.WriteString(indent + "}\n")
		if err != nil {
			return 0, err
		}
	}
	return i, err
}

// Generate Hue part of the HSV color for different types of items.
// Returns map: <item-type> -> <hue>
func (r *dotRenderer) genHueMap() map[string]float32 {
	hueMap := make(map[string]float32)
	gradeCount := len(r.graph.configurators)
	gradeInc := (float32(1) / 3) / float32(gradeCount + 1)
	// Order item types to get deterministic outcome.
	var itemTypes []string
	for itemType := range r.graph.configurators {
		itemTypes = append(itemTypes, itemType)
	}
	sort.Strings(itemTypes)
	for i, itemType := range itemTypes {
		// chose color from between green and blue (avoid red)
		const green = float32(1) / 3
		hue := green + gradeInc * float32(i+1)
		hueMap[itemType] = hue
	}
	return hueMap
}

func escapeName(name string) string {
	name = strings.Replace(name, "-", "_", -1)
	name = strings.Replace(name, "/", "_", -1)
	return name
}

func escapeTooltip(tooltip string) string {
	tooltip = strings.Replace(tooltip, "\n", "\\n", -1)
	tooltip = strings.Replace(tooltip, "\"", "\\\"", -1)
	return tooltip
}
