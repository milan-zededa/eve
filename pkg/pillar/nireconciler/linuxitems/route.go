// Copyright (c) 2023 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package linuxitems

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"

	dg "github.com/lf-edge/eve/libs/depgraph"
	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/netmonitor"
	"github.com/lf-edge/eve/pkg/pillar/nireconciler/genericitems"
	"github.com/vishvananda/netlink"
)

var (
	_, ipv4Any, _ = net.ParseCIDR("0.0.0.0/0")
	_, ipv6Any, _ = net.ParseCIDR("::/0")
)

// Route : Network route.
type Route struct {
	// Non-zero Route.LinkIndex should match OutputIf.
	// If Route.LinkIndex is zero, RouteConfigurator will find the output interface
	// index.
	netlink.Route
	// OutputIf : output interface for the routed traffic.
	// Leave undefined if the destination is unreachable.
	OutputIf RouteOutIf
}

// RouteOutIf : output interface for the route.
// Only one of these should be defined (this is like union).
type RouteOutIf struct {
	// UplinkIfName : uplink interface used as the output device for the route.
	UplinkIfName string
	// BridgeIfName : bridge interface used as the output device for the route.
	BridgeIfName string
	// DummyIfName : dummy interface used as the output device for the route.
	DummyIfName string
}

// Name combines the IP version, output interface name, route table ID and the destination
// address to construct a unique route identifier.
func (r Route) Name() string {
	var dst string
	if r.hasDefaultDst() {
		dst = "default"
	} else {
		dst = r.Route.Dst.String()
	}
	if r.outputIfName() == "" {
		return fmt.Sprintf("%s/%d/%s", r.ipVersionStr(), r.Table, dst)
	}
	return fmt.Sprintf("%s/%d/%s/%s", r.ipVersionStr(), r.Table, dst, r.outputIfName())
}

// Label is more human-readable than name.
func (r Route) Label() string {
	var dst string
	if r.hasDefaultDst() {
		dst = "<default>"
	} else {
		dst = r.Route.Dst.String()
	}
	if r.outputIfName() == "" {
		return fmt.Sprintf("%s route table %d dst %s is unreachable",
			r.ipVersionStr(), r.Table, dst)
	}
	return fmt.Sprintf("%s route table %d dst %s dev %s via %v",
		r.ipVersionStr(), r.Table, dst, r.outputIfName(), r.Gw)
}

func (r Route) ipVersionStr() string {
	switch r.Family {
	case netlink.FAMILY_V4:
		return "IPv4"
	case netlink.FAMILY_V6:
		return "IPv6"
	default:
		return "IPv?"
	}
}

func (r Route) hasDefaultDst() bool {
	if r.Route.Dst == nil {
		return true
	}
	ones, _ := r.Route.Dst.Mask.Size()
	return ones == 0 && r.Route.Dst.IP.IsUnspecified()
}

func (r Route) outputIfName() string {
	if r.OutputIf.UplinkIfName != "" {
		return r.OutputIf.UplinkIfName
	}
	if r.OutputIf.BridgeIfName != "" {
		return r.OutputIf.BridgeIfName
	}
	if r.OutputIf.DummyIfName != "" {
		return r.OutputIf.DummyIfName
	}
	return ""
}

// Type of the item.
func (r Route) Type() string {
	if r.Family == netlink.FAMILY_V4 {
		return genericitems.IPv4RouteTypename
	}
	return genericitems.IPv6RouteTypename
}

// Any destination IP and nil destination IP are treated as the same.
// However, netlink RouteAdd and RouteDel require a non-nil destination IP.
func (r Route) normalizedNetlinkRoute() netlink.Route {
	route := r.Route
	if route.Dst == nil {
		if route.Family == netlink.FAMILY_V4 {
			route.Dst = ipv4Any
		} else {
			route.Dst = ipv6Any
		}
	}
	// Also clear flags like RTNH_F_LINKDOWN - in the scope of state reconciliation
	// we do not care about them.
	route.Flags = 0
	return route
}

// Equal compares two Route instances.
func (r Route) Equal(other dg.Item) bool {
	r2, isRoute := other.(Route)
	if !isRoute {
		return false
	}
	return r.normalizedNetlinkRoute().Equal(r2.normalizedNetlinkRoute()) &&
		r.OutputIf == r2.OutputIf
}

// External returns false.
func (r Route) External() bool {
	return false
}

// String describes Route in detail.
func (r Route) String() string {
	return fmt.Sprintf("Network route for output interface '%s' with priority %d: %s",
		r.outputIfName(), r.Route.Priority, r.Route.String())
}

// Dependencies lists the output interface as the only dependency.
// Note that we do not check if the route gateway (if any) matches IP addresses
// assigned to the output interface. This is because zedrouter mostly just mirrors
// routes from the main routing table to per-NI tables and the presence of these
// routes essentially proves that this requirement is satisfied.
// The only exception is the route for the "blackhole" dummy interface, but that one
// is defined without gateway.
func (r Route) Dependencies() (deps []dg.Dependency) {
	if r.OutputIf.UplinkIfName != "" {
		deps = append(deps, dg.Dependency{
			RequiredItem: dg.ItemRef{
				ItemType: genericitems.UplinkTypename,
				ItemName: r.OutputIf.UplinkIfName,
			},
			Attributes: dg.DependencyAttributes{
				// Linux automatically removes the route when the interface disappears.
				AutoDeletedByExternal: true,
			},
			Description: "Uplink interface must exist",
		})
	} else if r.OutputIf.BridgeIfName != "" {
		deps = append(deps, dg.Dependency{
			RequiredItem: dg.ItemRef{
				ItemType: BridgeTypename,
				ItemName: r.OutputIf.BridgeIfName,
			},
			Attributes: dg.DependencyAttributes{
				// Linux automatically removes the route when the interface disappears.
				AutoDeletedByExternal: true,
			},
			Description: "Bridge interface must exist",
		})
	} else if r.OutputIf.DummyIfName != "" {
		deps = append(deps, dg.Dependency{
			RequiredItem: dg.ItemRef{
				ItemType: DummyIfTypename,
				ItemName: r.OutputIf.DummyIfName,
			},
			Attributes: dg.DependencyAttributes{
				// Linux automatically removes the route when the interface disappears.
				AutoDeletedByExternal: true,
			},
			Description: "Dummy interface must exist",
		})
	}
	return deps
}

// RouteConfigurator implements Configurator interface (libs/reconciler) for network routes.
type RouteConfigurator struct {
	Log            *base.LogObject
	NetworkMonitor netmonitor.NetworkMonitor
}

// Create adds network route.
func (c *RouteConfigurator) Create(ctx context.Context, item dg.Item) error {
	route, isRoute := item.(Route)
	if !isRoute {
		return fmt.Errorf("invalid item type %T, expected Route", item)
	}
	netlinkRoute, err := c.makeNetlinkRoute(route)
	if err != nil {
		c.Log.Error(err)
		return err
	}
	err = netlink.RouteAdd(netlinkRoute)
	if err != nil {
		if errors.Is(err, syscall.EEXIST) {
			// Ignore duplicate route.
			return nil
		}
		err = fmt.Errorf("failed to add route %+v: %w", route, err)
		c.Log.Error(err)
		return err
	}
	return nil
}

func (c *RouteConfigurator) makeNetlinkRoute(route Route) (*netlink.Route, error) {
	// Copy, do not change the original.
	netlinkRoute := route.normalizedNetlinkRoute()
	if netlinkRoute.LinkIndex == 0 && route.outputIfName() != "" {
		// Caller has left it to RouteConfigurator to find the interface index.
		ifIdx, exists, err := c.NetworkMonitor.GetInterfaceIndex(route.outputIfName())
		if !exists {
			// Dependencies should prevent this.
			err = fmt.Errorf("missing route output interface %s", route.outputIfName())
			c.Log.Error(err)
			return nil, err
		}
		if err != nil {
			err = fmt.Errorf("failed to get index of route output interface %s: %w",
				route.outputIfName(), err)
			c.Log.Error(err)
			return nil, err
		}
		netlinkRoute.LinkIndex = ifIdx
	}
	return &netlinkRoute, nil
}

// Modify is not implemented.
func (c *RouteConfigurator) Modify(ctx context.Context, oldItem, newItem dg.Item) (err error) {
	return errors.New("not implemented")
}

// Delete removes network route.
func (c *RouteConfigurator) Delete(ctx context.Context, item dg.Item) error {
	route, isRoute := item.(Route)
	if !isRoute {
		return fmt.Errorf("invalid item type %T, expected Route", item)
	}
	netlinkRoute, err := c.makeNetlinkRoute(route)
	if err != nil {
		c.Log.Error(err)
		return err
	}
	err = netlink.RouteDel(netlinkRoute)
	if err != nil {
		if errors.Is(err, syscall.ESRCH) {
			// Route already removed by kernel, ignore the error.
			return nil
		}
		err = fmt.Errorf("failed to delete route %+v: %w", route, err)
		c.Log.Error(err)
		return err
	}
	return nil
}

// NeedsRecreate returns true - Modify is not implemented.
func (c *RouteConfigurator) NeedsRecreate(oldItem, newItem dg.Item) (recreate bool) {
	return true
}
