// Copyright (c) 2023 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package genericitems

import (
	"fmt"
	"net"

	dg "github.com/lf-edge/eve/libs/depgraph"
	"github.com/lf-edge/eve/pkg/pillar/utils"
)

// IPAddress : IP address assigned to a network interface.
// This is a generic item but with network stack specific configurators.
type IPAddress struct {
	// AddrWithMask : IP address including mask of the subnet to which it belongs.
	AddrWithMask *net.IPNet
	// NetIf : network interface to which the IP address is assigned.
	NetIf NetworkIf
	// AssignedByNIM : true if this IP address is assigned to the target network
	// interface by NIM and therefore from the zedrouter point of view it is an external
	// item.
	AssignedByNIM bool
}

// Name returns the IP address in the string format.
// Mask is intentionally excluded because two instances of the same IP address
// cannot be assigned at the same time (in the same network namespace) even if masks
// or target interfaces are different (hence we should treat them as the same item
// that can only exist in one instance).
func (ip IPAddress) Name() string {
	return ip.AddrWithMask.IP.String()
}

// Label returns the IP address including the mask in the string format.
func (ip IPAddress) Label() string {
	return ip.AddrWithMask.String()
}

// Type of the item.
func (ip IPAddress) Type() string {
	return IPAddressTypename
}

// Equal compares two IP addresses.
func (ip IPAddress) Equal(other dg.Item) bool {
	ip2, isIPAddress := other.(IPAddress)
	if !isIPAddress {
		return false
	}
	return ip.NetIf == ip2.NetIf &&
		utils.EqualIPNets(ip.AddrWithMask, ip2.AddrWithMask)
}

// External returns true if the address is assigned to the interface by NIM.
func (ip IPAddress) External() bool {
	return ip.AssignedByNIM
}

// String describes IP address.
func (ip IPAddress) String() string {
	return fmt.Sprintf("IPAddress: {ifName: %s, address: %s}",
		ip.NetIf.IfName, ip.AddrWithMask.String())
}

// Dependencies returns the target network interface as the only dependency.
func (ip IPAddress) Dependencies() (deps []dg.Dependency) {
	deps = append(deps, dg.Dependency{
		RequiredItem: ip.NetIf.ItemRef,
		Description:  "target network interface must exist",
	})
	return deps
}
