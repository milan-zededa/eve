// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// DotExporter exports dependency graph into DOT [1].
// [1]: https://en.wikipedia.org/wiki/DOT_(graph_description_language)
type DotExporter struct {
	// CheckDeps : enable this option to have the dependencies checked
	// and edges colored accordingly (black vs. red).
	CheckDeps bool

	// Internal attributes used only during Export()
	g      GraphR
	hueMap map[string]float32 // item type -> fillcolor hue
}

const (
	indentChar = "\t"
)

// Export returns DOT description of the graph content. This can be visualized
// with Graphviz and used for troubleshooting/presentation purposes.
func (e *DotExporter) Export(g GraphR) (dot string, err error) {
	if g == nil {
		return "digraph G {}", nil
	}
	e.g = g
	e.hueMap = e.genHueMap()
	sb := strings.Builder{}
	_, err = sb.WriteString("digraph G {\n")
	if err != nil {
		return "", err
	}

	// Export subgraphs clusters starting with the implicit top-level one.
	err = e.exportSubgraph(&sb, g, indentChar)
	if err != nil {
		return "", err
	}

	// Output all edges.
	// missingNodes: not in the graph but with edges pointing to them
	missingNodes := make(map[NodeID]struct{})
	nodeIter := e.g.Nodes(true)
	for nodeIter.Next() {
		node := nodeIter.Node()
		edgeIter := e.g.OutgoingEdges(node.ID())
		for edgeIter.Next() {
			edge := edgeIter.Edge()
			if _, found := e.g.Node(edge.ToNode); !found {
				missingNodes[edge.ToNode] = struct{}{}
			}
			var color string
			if edge.IsDepSatisfied() {
				color = "black"
			} else {
				color = "red"
			}
			_, err = sb.WriteString(fmt.Sprintf("%s%s -> %s [color = %s, tooltip = \"%s\"];\n",
				indentChar, escapeName(edge.FromNode.String()),
				escapeName(edge.ToNode.String()), color,
				escapeTooltip(edge.Dependency.Description)))
			if err != nil {
				return "", err
			}
		}
	}

	// Output missing nodes (not present in the graph but with edges pointing to them).
	for nodeID := range missingNodes {
		_, err = sb.WriteString(fmt.Sprintf("%s%s [color = grey, style = dashed, "+
			"shape = ellipse, tooltip = \"<missing>\", label = \"%s\"];\n",
			indentChar, escapeName(nodeID.String()), nodeID.String()))
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

func (e *DotExporter) exportSubgraph(w io.StringWriter, subG GraphR, indent string) error {
	nestedIndent := indent

	// output cluster header
	if subG.ParentGraph() != nil {
		nestedIndent += indentChar
		_, err := w.WriteString(fmt.Sprintf("%ssubgraph cluster_%s {\n",
			indent, escapeName(subG.Name())))
		if err != nil {
			return err
		}
	}

	// output graph attributes
	_, err := w.WriteString(fmt.Sprintf("%scolor = black;\n",
		nestedIndent))
	if err != nil {
		return err
	}
	_, err = w.WriteString(fmt.Sprintf("%slabel = %s;\n",
		nestedIndent, escapeName(subG.Name())))
	if err != nil {
		return err
	}
	_, err = w.WriteString(fmt.Sprintf("%stooltip = \"%s\";\n",
		nestedIndent, escapeTooltip(subG.Description())))
	if err != nil {
		return err
	}

	// output nodes
	nodeIter := subG.Nodes(false)
	for nodeIter.Next() {
		node := nodeIter.Node()
		var (
			color      string
			saturation float32
			shape      string
		)
		if node.Item.External() {
			shape = "doubleoctagon"
		} else {
			shape = "ellipse"
		}
		switch node.State {
		case ItemStatePending:
			color = "grey"
			saturation = 0.25
		case ItemStateFailure:
			color = "red"
			saturation = 0.60
		case ItemStateCreating:
			fallthrough
		case ItemStateModifying:
			fallthrough
		case ItemStateDeleting:
			fallthrough
		case ItemStateRecreating:
			// in-progress operation
			color = "blue"
			saturation = 0.60
			shape = "cds"
		default:
			color = "black"
			saturation = 0.60
		}
		hue := e.hueMap[node.Item.Type()]
		fillColor := fmt.Sprintf("%.3f %.3f 0.800", hue, saturation)
		label := node.Item.Label()
		if label == "" {
			label = node.Item.Name()
		}
		_, err = w.WriteString(fmt.Sprintf("%s%s [color = %s, fillcolor = \"%s\", "+
			"shape = %s, style = filled, tooltip = \"%s\", label = \"%s\"];\n",
			nestedIndent, escapeName(node.ID().String()), color, fillColor, shape,
			escapeTooltip(node.Item.String()), label))
		if err != nil {
			return err
		}
	}

	// output subgraphs
	subGIter := subG.SubGraphs()
	for subGIter.Next() {
		nestedSubG := subGIter.SubGraph()
		err = e.exportSubgraph(w, nestedSubG, nestedIndent)
		if err != nil {
			return err
		}
	}

	// closing cluster bracket
	if subG.ParentGraph() != nil {
		_, err = w.WriteString(indent + "}\n")
		if err != nil {
			return err
		}
	}
	return err
}

// Generate Hue part of the HSV color for different types of items.
// Returns map: <item-type> -> <hue>
func (e *DotExporter) genHueMap() map[string]float32 {
	// Get and order item types to get deterministic outcome.
	itemTypesMap := make(map[string]struct{})
	iter := e.g.Nodes(true)
	for iter.Next() {
		node := iter.Node()
		itemType := node.Item.Type()
		itemTypesMap[itemType] = struct{}{}
	}
	var itemTypes []string
	for itemType := range itemTypesMap {
		itemTypes = append(itemTypes, itemType)
	}
	sort.Strings(itemTypes)
	// Assign a distinct color to each item type.
	hueMap := make(map[string]float32)
	gradeCount := len(itemTypes)
	gradeInc := (float32(1) / 3) / float32(gradeCount+1)
	for i, itemType := range itemTypes {
		// chose color from between green and blue (avoid red)
		const green = float32(1) / 3
		hue := green + gradeInc*float32(i+1)
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
