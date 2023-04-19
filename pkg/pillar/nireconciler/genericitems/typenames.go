// Copyright (c) 2023 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package genericitems

const (
	// RouteTypename : typename for network route.
	// Not implemented in genericitems (implementation specific to network stack).
	RouteTypename = "Route"
	// VIFTypename : typename for VIF.
	VIFTypename = "VIF"
	// UplinkTypename : typename for uplink interface.
	UplinkTypename = "Uplink"
	// HTTPServerTypename : typename for HTTP server.
	HTTPServerTypename = "HTTPServer"
	// DnsmasqTypename : typename for dnsmasq program (DNS and DHCP server).
	DnsmasqTypename = "Dnsmasq"
	// RadvdTypename : typename for radvd program (router advertisement daemon).
	RadvdTypename = "Radvd"
	// IPSetTypename : typename for Linux IP set (from netfilter).
	// Implemented in linuxitems.
	// Type definition is here because it is referenced by dnsmasq
	// (when used with Linux ipsets).
	IPSetTypename = "IPSet"
)
