// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package iptables

// Connection mark is used to remember for a given flow to which application it belongs
// and which ACE was applied.
// The 32 bits of a connmark are used as follows:
//
//	+----------------+-------------+---------------+--------------+------------------+
//	| App ID (8bits) | App (1 bit) | Action (1bit) | User (1 bit) | ACE ID (21 bits) |
//	+----------------+-------------+---------------+--------------+------------------+
//
// where:
//   - Drop action = 1; Allow action = 0
//   - User is 1 for user-configured ACL rules (0 for implicit rules)
//   - App is 1 for application traffic (EVE mgmt flows will get 0)
const (
	// AppIDMask : bits of the connection mark allocated to store application ID.
	AppIDMask = 0xff << 24
	// AceActionMask : bit of the connection mark used to store the action.
	AceActionMask = 0x1 << 22
	// AceDropAction : bit representation of the Drop action.
	AceDropAction = AceActionMask
	// AceUser : bit representation of the user flag.
	AceUser = 0x1 << 21
	// AceApp : bit representation of the application flag.
	AceApp = 0x1 << 23
	// AceIDMask : bits of the connection mark allocated to store ACE ID.
	AceIDMask = 0x1fffff
	// DefaultDropAceID : by default, traffic not matched by any ACE is dropped.
	// For this default rule we use the maximum integer value available for ACE ID.
	DefaultDropAceID = AceIDMask
)

// ControlProtocolMarkingIDMap : Map describing the control flow marking values
// (for implicit, i.e. not user defined, ACL rules) that we intend to use.
// XXX It is only read from, never written to, hence no concurrency.
// But LockedStringMap would be better.
var ControlProtocolMarkingIDMap = map[string]uint32{
	// INPUT flows for HTTP, SSH & GUACAMOLE
	"in_http_ssh_guacamole": 1,
	// INPUT flows for VNC
	"in_vnc": 2,
	// There was some feature here that used marking values "3" & "4".
	// Marking values "3" & "4" are unused as of now.

	// OUTPUT flows for all types
	"out_all": 5,
	// App initiated UDP flows towards dom0 for DHCP
	"app_dhcp": 6,
	// App initiated TCP/UDP flows towards dom0 for DNS
	"app_dns": 7,
	// 8 : deprecated (previously: VPN control packets)
	// ICMP and ICMPv6
	"in_icmp": 9,
	// DHCP packets originating from outside
	// (e.g. DHCP multicast requests from other devices on the same network)
	"in_dhcp": 10,
	// App initiated HTTP requests towards the metadata server running in dom0
	"app_http": 11,
	// ICMPv6 traffic to and from an application
	"app_icmpv6": 12,
	// for Kubernetes DNS, allowing coreDNS to talk to external DNS servers
	"in_dns": 13,
}

// GetConnmark : create connection mark corresponding to the given attributes.
// Expects zero appID for non-application flow.
func GetConnmark(appID uint8, aceID uint32, user, drop bool) uint32 {
	mark := uint32(appID)<<24 | aceID
	if appID > 0 {
		mark |= AceApp
	}
	if user {
		mark |= AceUser
	}
	if drop {
		mark |= AceDropAction
	}
	return mark
}

// ParseConnmark : parse attributes stored inside a connection mark.
// Returns zero appID for non-application flow.
func ParseConnmark(mark uint32) (appID uint8, aceID uint32, user, drop bool) {
	appID = uint8(mark >> 24)
	aceID = mark & AceIDMask
	drop = (mark & AceActionMask) == AceDropAction
	user = (mark & AceUser) != 0
	return
}
