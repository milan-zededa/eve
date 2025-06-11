// Copyright (c) 2023 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package netutils

import (
	"bytes"
	"net"
	"syscall"
)

// IsEmptyIP returns true if the IP address is not defined.
func IsEmptyIP(ip net.IP) bool {
	return ip == nil || ip.Equal(net.IP{})
}

// EqualIPs compares two IP addresses.
func EqualIPs(ip1 net.IP, ip2 net.IP) bool {
	if ip1 == nil {
		return ip2 == nil
	}
	if ip2 == nil {
		return ip1 == nil
	}
	return ip1.Equal(ip2)
}

// EqualIPNets compares two IP addresses with masks.
func EqualIPNets(ipNet1, ipNet2 *net.IPNet) bool {
	if ipNet1 == nil || ipNet2 == nil {
		return ipNet1 == ipNet2
	}
	return ipNet1.IP.Equal(ipNet2.IP) &&
		bytes.Equal(ipNet1.Mask, ipNet2.Mask)
}

// SameIPVersions returns true if both IP addresses are of the same version
func SameIPVersions(ip1, ip2 net.IP) bool {
	firstIsV4 := ip1.To4() != nil
	secondIsV4 := ip2.To4() != nil
	return firstIsV4 == secondIsV4
}

// AddToIP increments IP address by the given integer number.
func AddToIP(ip net.IP, addition int) net.IP {
	// TODO: support IPv4 and IPv6
	return net.IP{}
}

// GetIPNetwork returns the first IP Address of the subnet(Network Address)
func GetIPNetwork(subnet *net.IPNet) net.IP {
	if subnet == nil {
		return nil
	}
	return subnet.IP.Mask(subnet.Mask)
}

// HostFamily returns the address family for the given IP address
func HostFamily(ip net.IP) int {
	if ip.To4() != nil {
		return syscall.AF_INET
	}
	return syscall.AF_INET6
}

// HostSubnet returns the subnet mask for the given IP address
func HostSubnet(ip net.IP) *net.IPNet {
	if ip.To4() != nil {
		return &net.IPNet{IP: ip, Mask: net.CIDRMask(32, 32)}
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)}
}

// OverlappingSubnets returns true if the given subnets share at least one IP address.
func OverlappingSubnets(subnet1, subnet2 *net.IPNet) bool {
	if subnet1 == nil || len(subnet1.IP) == 0 ||
		subnet2 == nil || len(subnet2.IP) == 0 {
		// One of the subnets or both are undefined.
		return false
	}
	return subnet1.Contains(subnet2.IP) || subnet2.Contains(subnet1.IP)
}
