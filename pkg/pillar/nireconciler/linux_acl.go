// Copyright (c) 2023 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package nireconciler

import (
	"fmt"
	"net"
	"strconv"

	dg "github.com/lf-edge/eve-libs/depgraph"
	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/iptables"
	"github.com/lf-edge/eve/pkg/pillar/types"
	"github.com/lf-edge/eve/pkg/pillar/utils/generics"
	"github.com/lf-edge/eve/pkg/pillar/utils/netutils"
	uuid "github.com/satori/go.uuid"
)

const (
	// dropCounterChain : chain with no rules, used merely to count dropped packets.
	dropCounterChain = "DROP-COUNTER"
)

// Describes protocol that is allowed implicitly because it provides some essential
// function for applications.
type essentialProto struct {
	label         string
	ingressMatch  []string
	egressMatch   []string
	mark          uint32
	markChainName string
}

// User-configured ACL rule.
type userACLRule struct {
	// iptables arguments to match egress traffic (from app)
	egressMatch []string
	// iptables arguments to match ingress traffic (to app) before DNAT stage
	preDNATIngressMatch []string
	// iptables arguments to match ingress traffic (to app) after DNAT stage
	postDNATIngressMatch []string
	// arguments for "limit" action (nil if isLimitRule==false)
	limitArgs []string
	// true if the action is "LIMIT"
	isLimitRule bool
	// true if the action is "DROP"
	drop bool
	// arguments provided if the action is "PORTMAP"
	portMap *portMap
	// ALLOW | DROP | LIMIT | PORTMAP
	actionLabel string
}

// Port-forwarding ACL rule.
type portMap struct {
	protocol     string
	externalPort string
	targetPort   int
	// interface names of network adapters to which the port-map rule should be limited
	adapters []string
}

// These variables are used as constants.
var (
	usedIptablesChains = map[string][]string{ // table -> chains
		"raw":    {"PREROUTING"},
		"filter": {"INPUT", "FORWARD"},
		"mangle": {"PREROUTING", "POSTROUTING"},
		"nat":    {"PREROUTING", "POSTROUTING"},
	}
)

func appChain(chain string) string {
	return chain + iptables.AppChainSuffix
}

func vifChain(chain string, vif vifInfo) string {
	return chain + "-" + vif.hostIfName
}

// Ingress = traffic entering application.
// Note that chain name is limited to 28 characters.
func ingressVifChain(chain string, vif vifInfo) string {
	return chain + "-" + vif.hostIfName + "-IN"
}

// Egress = traffic exiting application.
// Note that chain name is limited to 28 characters.
func egressVifChain(chain string, vif vifInfo) string {
	return chain + "-" + vif.hostIfName + "-OUT"
}

func matchVifIfName(vif vifInfo) string {
	// Match any suffix - qemu may append "-emu" to the interface name.
	return vif.hostIfName + "+"
}

func getEssentialIPv4Protos(niType types.NetworkInstanceType,
	bridgeIP net.IP) (protos []essentialProto) {
	switch niType {
	case types.NetworkInstanceTypeSwitch:
		protos = append(protos, essentialProto{
			label:         "BOOTP and DHCPv4",
			egressMatch:   []string{"-p", "udp", "--dport", "bootps"},
			ingressMatch:  []string{"-p", "udp", "--sport", "bootps"},
			mark:          iptables.ControlProtocolMarkingIDMap["app_dhcp"],
			markChainName: "dhcpv4",
		})
		protos = append(protos, essentialProto{
			label:         "DNS over UDP",
			egressMatch:   []string{"-p", "udp", "--dport", "domain"},
			ingressMatch:  []string{"-p", "udp", "--sport", "domain"},
			mark:          iptables.ControlProtocolMarkingIDMap["app_dns"],
			markChainName: "dns",
		})
		protos = append(protos, essentialProto{
			label:         "DNS over TCP",
			egressMatch:   []string{"-p", "tcp", "--dport", "domain"},
			ingressMatch:  []string{"-p", "tcp", "--sport", "domain"},
			mark:          iptables.ControlProtocolMarkingIDMap["app_dns"],
			markChainName: "dns",
		})
	case types.NetworkInstanceTypeLocal:
		// Nil ingressMatch - - NAT disallows accessing applications from outside
		// (without explicit port mapping ACLs).
		protos = append(protos, essentialProto{
			label: "BOOTP and DHCPv4 with local dst IP",
			egressMatch: []string{"-m", "set", "--match-set", localIPv4Ipset, "dst",
				"-p", "udp", "--dport", "bootps"},
			mark:          iptables.ControlProtocolMarkingIDMap["app_dhcp"],
			markChainName: "dhcpv4",
		})
		if bridgeIP != nil {
			protos = append(protos, essentialProto{
				label: "BOOTP and DHCPv4 with bridge dst IP",
				egressMatch: []string{"-d", bridgeIP.String(),
					"-p", "udp", "--dport", "bootps"},
				mark:          iptables.ControlProtocolMarkingIDMap["app_dhcp"],
				markChainName: "dhcpv4",
			})
			protos = append(protos, essentialProto{
				label: "DNS over UDP",
				egressMatch: []string{"-d", bridgeIP.String(),
					"-p", "udp", "--dport", "domain"},
				mark:          iptables.ControlProtocolMarkingIDMap["app_dns"],
				markChainName: "dns",
			})
			protos = append(protos, essentialProto{
				label: "DNS over TCP",
				egressMatch: []string{"-d", bridgeIP.String(),
					"-p", "tcp", "--dport", "domain"},
				mark:          iptables.ControlProtocolMarkingIDMap["app_dns"],
				markChainName: "dns",
			})
		}
	}
	return
}

func getEssentialIPv6Protos(niType types.NetworkInstanceType,
	bridgeIP net.IP) (protos []essentialProto) {
	switch niType {
	case types.NetworkInstanceTypeSwitch:
		protos = append(protos, essentialProto{
			label:         "ICMPv6",
			egressMatch:   []string{"-p", "ipv6-icmp"},
			ingressMatch:  []string{"-p", "ipv6-icmp"},
			mark:          iptables.ControlProtocolMarkingIDMap["app_icmpv6"],
			markChainName: "icmpv6",
		})
		protos = append(protos, essentialProto{
			label:         "DHCPv6",
			egressMatch:   []string{"-p", "udp", "--dport", "dhcpv6-server"},
			ingressMatch:  []string{"-p", "udp", "--sport", "dhcpv6-server"},
			mark:          iptables.ControlProtocolMarkingIDMap["app_dhcp"],
			markChainName: "dhcpv6",
		})
		protos = append(protos, essentialProto{
			label:         "DNS over UDP",
			egressMatch:   []string{"-p", "udp", "--dport", "domain"},
			ingressMatch:  []string{"-p", "udp", "--sport", "domain"},
			mark:          iptables.ControlProtocolMarkingIDMap["app_dns"],
			markChainName: "dns",
		})
		protos = append(protos, essentialProto{
			label:         "DNS over TCP",
			egressMatch:   []string{"-p", "tcp", "--dport", "domain"},
			ingressMatch:  []string{"-p", "udp", "--sport", "domain"},
			mark:          iptables.ControlProtocolMarkingIDMap["app_dns"],
			markChainName: "dns",
		})
	case types.NetworkInstanceTypeLocal:
		// Nil ingressMatch - - NAT disallows accessing applications from outside
		// (without explicit port mapping ACLs).
		protos = append(protos, essentialProto{
			label: "ICMPv6 with local dst IP",
			egressMatch: []string{"-m", "set", "--match-set", localIPv6Ipset, "dst",
				"-p", "ipv6-icmp"},
			mark:          iptables.ControlProtocolMarkingIDMap["app_icmpv6"],
			markChainName: "icmpv6",
		})
		protos = append(protos, essentialProto{
			label: "DHCPv6 with local dst IP",
			egressMatch: []string{"-m", "set", "--match-set", localIPv6Ipset, "dst",
				"-p", "udp", "--dport", "dhcpv6-server"},
			mark:          iptables.ControlProtocolMarkingIDMap["app_dhcp"],
			markChainName: "dhcpv6",
		})
		if bridgeIP != nil {
			protos = append(protos, essentialProto{
				label:         "ICMPv6 with bridge dst IP",
				egressMatch:   []string{"-d", bridgeIP.String(), "-p", "ipv6-icmp"},
				mark:          iptables.ControlProtocolMarkingIDMap["app_icmpv6"],
				markChainName: "icmpv6",
			})
			protos = append(protos, essentialProto{
				label: "DHCPv6 with bridge dst IP",
				egressMatch: []string{"-d", bridgeIP.String(),
					"-p", "udp", "--dport", "dhcpv6-server"},
				mark:          iptables.ControlProtocolMarkingIDMap["app_dhcp"],
				markChainName: "dhcpv6",
			})
			protos = append(protos, essentialProto{
				label: "DNS over UDP",
				egressMatch: []string{"-d", bridgeIP.String(),
					"-p", "udp", "--dport", "domain"},
				mark:          iptables.ControlProtocolMarkingIDMap["app_dns"],
				markChainName: "dns",
			})
			protos = append(protos, essentialProto{
				label: "DNS over TCP",
				egressMatch: []string{"-d", bridgeIP.String(),
					"-p", "udp", "--dport", "domain"},
				mark:          iptables.ControlProtocolMarkingIDMap["app_dns"],
				markChainName: "dns",
			})
		}
	}
	return
}

// Return errors without LogAndErrPrefix - it is prepended inside callers.
func parseUserACLRule(log *base.LogObject, aclRule types.ACE,
	ni *niInfo, vif vifInfo,
	forIPv6 bool) (parsedRule userACLRule, skip bool, err error) {
	if len(aclRule.Actions) > 1 {
		return parsedRule, true, fmt.Errorf(
			"ACL rule (%v) with multiple actions is not supported", aclRule)
	}
	// Parse action.
	// Default action (if not specified) is to allow traffic to continue.
	parsedRule.actionLabel = "ALLOW"
	if len(aclRule.Actions) > 0 {
		action := aclRule.Actions[0]
		var actionCount int
		if action.Drop {
			actionCount++
			parsedRule.drop = true
			parsedRule.actionLabel = "DROP"
		}
		if action.PortMap {
			actionCount++
			parsedRule.actionLabel = "PORTMAP"
			if action.TargetPort == 0 {
				err = fmt.Errorf("portmap ACL rule (%+v) with zero target port",
					aclRule)
				return parsedRule, true, err
			}
			parsedRule.portMap = &portMap{
				targetPort: action.TargetPort,
			}
		}
		if action.Limit {
			actionCount++
			parsedRule.isLimitRule = true
			parsedRule.actionLabel = "LIMIT"
			// For example: -m limit --limit 4/s --limit-burst 4
			parsedRule.limitArgs = []string{"-m", "limit"}
			if action.LimitRate != 0 {
				limit := strconv.Itoa(action.LimitRate) + "/" + action.LimitUnit
				parsedRule.limitArgs = append(parsedRule.limitArgs, "--limit", limit)
			}
			if action.LimitBurst != 0 {
				burst := strconv.Itoa(action.LimitBurst)
				parsedRule.limitArgs = append(parsedRule.limitArgs, "--limit-burst", burst)
			}
		}
		if actionCount > 1 {
			return parsedRule, true, fmt.Errorf(
				"ACL rule (%+v) combines DROP/PORTMAP/LIMIT actions", aclRule)
		}
	}
	// Parse match arguments.
	var (
		ipWithPrefix *net.IPNet
		ipsetName    string
		protocol     string
		lport        string
		fport        string
	)
	anyAdapter := true
	for _, match := range aclRule.Matches {
		switch match.Type {
		case "ip":
			if ip := net.ParseIP(match.Value); ip != nil {
				ipWithPrefix = netutils.HostSubnet(ip)
			} else if _, subnet, err := net.ParseCIDR(match.Value); err == nil {
				ipWithPrefix = subnet
			} else {
				err = fmt.Errorf("ACL rule with invalid IP/subnet (%+v) ", aclRule)
				return parsedRule, true, err
			}
		case "protocol":
			protocol = match.Value
		case "fport":
			// Need a protocol as well. Checked below.
			fport = match.Value
		case "lport":
			// Need a protocol as well. Checked below.
			lport = match.Value
		case "host":
			// Check if this should really be an "ip" ACL
			if ip := net.ParseIP(match.Value); ip != nil {
				ipWithPrefix = netutils.HostSubnet(ip)
				log.Warnf("%s: found host ACL rule with IP %s; treating as ip ACL",
					LogAndErrPrefix, match.Value)
				break
			} else if _, subnet, err := net.ParseCIDR(match.Value); err == nil {
				ipWithPrefix = subnet
				log.Warnf("%s: found host ACL rule with CIDR %s; treating as ip ACL",
					LogAndErrPrefix, match.Value)
				break
			}
			if ni.config.Type == types.NetworkInstanceTypeSwitch {
				err := fmt.Errorf("ACL rule with host is not supported "+
					"on switch network instance (%+v)", aclRule)
				return parsedRule, true, err
			}
			if ipsetName != "" {
				err := fmt.Errorf("ACL rule with both host and eidset "+
					"is not supported (%+v)", aclRule)
				return parsedRule, true, err
			}
			ipsetBasename := HostIPSetBasename(match.Value)
			if forIPv6 {
				ipsetName = ipsetNamePrefixV4 + ipsetBasename
			} else {
				ipsetName = ipsetNamePrefixV4 + ipsetBasename
			}
		case "eidset":
			if ni.config.Type == types.NetworkInstanceTypeSwitch {
				err := fmt.Errorf("ACL rule with eidset is not supported "+
					"on switch network instance (%+v)", aclRule)
				return parsedRule, true, err
			}
			if ipsetName != "" {
				err := fmt.Errorf("ACL rule with both host and eidset "+
					"is not supported (%+v)", aclRule)
				return parsedRule, true, err
			}
			ipsetName = eidsIpsetName(vif, forIPv6)
		case "adapter":
			if parsedRule.portMap == nil {
				err := fmt.Errorf("ACL rule with (%+v) 'adapter' match (%s) "+
					"is not supported for egress traffic (only for ingress port-map rules)",
					aclRule, match.Value)
				return parsedRule, true, err
			}
			anyAdapter = false
			for _, port := range ni.bridge.Ports {
				if generics.ContainsItem(port.SharedLabels, match.Value) {
					parsedRule.portMap.adapters = append(parsedRule.portMap.adapters,
						port.IfName)
				}
			}
		default:
			err := fmt.Errorf("ACL rule (%+v) with unsupported match type: %s",
				aclRule, match)
			return parsedRule, true, err
		}
	}
	if fport != "" && protocol == "" {
		err := fmt.Errorf("ACL rule (%+v) with fport %s and no protocol match",
			aclRule, fport)
		return parsedRule, true, err
	}
	if lport != "" && protocol == "" {
		err := fmt.Errorf("ACL rule (%+v) with lport %s and no protocol match",
			aclRule, fport)
		return parsedRule, true, err
	}
	if parsedRule.portMap != nil && lport == "" {
		err := fmt.Errorf("portmap ACL rule (%+v) without lport", aclRule)
		return parsedRule, true, err
	}
	if ipWithPrefix != nil {
		ipv6 := ipWithPrefix.IP.To4() == nil
		if ipv6 != forIPv6 {
			// Skip this rule, it is for the other IP version.
			return parsedRule, true, nil
		}
		parsedRule.egressMatch = append(parsedRule.egressMatch,
			"-d", ipWithPrefix.String())
		// ip refers to the remote endpoint IP address, not the local IP address
		// which is being DNATed.
		ingressMatch := []string{"-s", ipWithPrefix.String()}
		parsedRule.preDNATIngressMatch = append(parsedRule.preDNATIngressMatch,
			ingressMatch...)
		parsedRule.postDNATIngressMatch = append(parsedRule.postDNATIngressMatch,
			ingressMatch...)
	}
	// Make sure we put the protocol before any port numbers
	if protocol != "" {
		match := []string{"-p", protocol}
		parsedRule.egressMatch = append(parsedRule.egressMatch, match...)
		parsedRule.preDNATIngressMatch = append(parsedRule.preDNATIngressMatch, match...)
		parsedRule.postDNATIngressMatch = append(parsedRule.postDNATIngressMatch, match...)
	}
	if fport != "" {
		parsedRule.egressMatch = append(parsedRule.egressMatch, "--dport", fport)
		ingressMatch := []string{"--sport", fport}
		parsedRule.preDNATIngressMatch = append(parsedRule.preDNATIngressMatch,
			ingressMatch...)
		parsedRule.postDNATIngressMatch = append(parsedRule.postDNATIngressMatch,
			ingressMatch...)
	}
	if lport != "" {
		if parsedRule.portMap != nil {
			parsedRule.portMap.externalPort = lport
			parsedRule.portMap.protocol = protocol // verified above that it is not empty
			// egressMatch is before SNAT from app port to lport
			parsedRule.egressMatch = append(parsedRule.egressMatch,
				"--sport", strconv.Itoa(parsedRule.portMap.targetPort))
			parsedRule.preDNATIngressMatch = append(parsedRule.preDNATIngressMatch,
				"--dport", lport)
			parsedRule.postDNATIngressMatch = append(parsedRule.postDNATIngressMatch,
				"--dport", strconv.Itoa(parsedRule.portMap.targetPort))
		} else {
			parsedRule.egressMatch = append(parsedRule.egressMatch, "--sport", lport)
			ingressMatch := []string{"--dport", lport}
			parsedRule.preDNATIngressMatch = append(parsedRule.preDNATIngressMatch,
				ingressMatch...)
			parsedRule.postDNATIngressMatch = append(parsedRule.postDNATIngressMatch,
				ingressMatch...)
		}
	}
	if ipsetName != "" {
		parsedRule.egressMatch = append(parsedRule.egressMatch,
			"-m", "set", "--match-set", ipsetName, "dst")
		ingressMatch := []string{"-m", "set", "--match-set", ipsetName, "src"}
		parsedRule.preDNATIngressMatch = append(parsedRule.preDNATIngressMatch,
			ingressMatch...)
		parsedRule.postDNATIngressMatch = append(parsedRule.postDNATIngressMatch,
			ingressMatch...)
	}
	if parsedRule.portMap != nil && anyAdapter {
		for _, port := range ni.bridge.Ports {
			parsedRule.portMap.adapters = append(parsedRule.portMap.adapters, port.IfName)
		}
	}
	return parsedRule, false, nil
}

func (r *LinuxNIReconciler) getIntendedACLRootChains() dg.Graph {
	graphArgs := dg.InitArgs{
		Name: ACLRootChainsSG,
		Description: "iptables chains pre-created by NIM, " +
			"used to traverse application and device-wide ACLs",
	}
	intendedACLChains := dg.New(graphArgs)
	ipv4Chains := dg.New(dg.InitArgs{
		Name:        IPv4ChainsSG,
		Description: "iptables chains for traversal of IPv4 ACLs",
	})
	intendedACLChains.PutSubGraph(ipv4Chains)
	ipv6Chains := dg.New(dg.InitArgs{
		Name:        IPv6ChainsSG,
		Description: "iptables chains for traversal of IPv6 ACLs",
	})
	intendedACLChains.PutSubGraph(ipv6Chains)
	for table, chains := range usedIptablesChains {
		for _, forIPv6 := range []bool{false, true} {
			sg := ipv4Chains
			if forIPv6 {
				sg = ipv6Chains
			}
			for _, chain := range chains {
				sg.PutItem(iptables.Chain{
					ChainName:  appChain(chain),
					Table:      table,
					ForIPv6:    forIPv6,
					PreCreated: true,
				}, nil)
			}
		}
	}
	return intendedACLChains
}

func (r *LinuxNIReconciler) getIntendedAppConnACLs(niID uuid.UUID,
	vif vifInfo, ul types.AppNetAdapterConfig) dg.Graph {
	graphArgs := dg.InitArgs{
		Name:        AppConnACLsSG,
		Description: "ACLs configured for application VIF",
	}
	ni := r.nis[vif.NI]
	portIPs := make(map[string][]*net.IPNet) // key: interface name
	for _, port := range ni.bridge.Ports {
		ifIndex, found, err := r.netMonitor.GetInterfaceIndex(port.IfName)
		if err != nil {
			r.log.Errorf("%s: getIntendedAppConnACLs: failed to get ifIndex "+
				"for port %s: %v", LogAndErrPrefix, port.IfName, err)
		} else if found {
			ips, _, err := r.netMonitor.GetInterfaceAddrs(ifIndex)
			if err != nil {
				r.log.Errorf(
					"%s: getIntendedAppConnACLs: failed to get port %s addresses: %v",
					LogAndErrPrefix, port.IfName, err)
			}
			ips = generics.FilterList(ips, func(ipNet *net.IPNet) bool {
				return ipNet.IP.IsGlobalUnicast()
			})
			portIPs[port.IfName] = ips
		}
	}
	intendedAppConnACLs := dg.New(graphArgs)
	for _, ipv6 := range []bool{true, false} {
		// TODO: use IPv4RulesSG and IPv6RulesSG ?
		if ni.config.Type == types.NetworkInstanceTypeLocal {
			if ni.config.IsIPv6() != ipv6 {
				continue
			}
		}
		for _, item := range r.getIntendedAppConnRawIptables(vif, ul, ipv6) {
			intendedAppConnACLs.PutItem(item, nil)
		}
		for _, item := range r.getIntendedAppConnFilterIptables(vif, ul, ipv6) {
			intendedAppConnACLs.PutItem(item, nil)
		}
		for _, item := range r.getIntendedAppConnNATIptables(vif, ul, ipv6, portIPs) {
			intendedAppConnACLs.PutItem(item, nil)
		}
		for _, item := range r.getIntendedAppConnMangleIptables(vif, ul, ipv6, portIPs) {
			intendedAppConnACLs.PutItem(item, nil)
		}
	}
	return intendedAppConnACLs
}

// Table RAW, chain PREROUTING is used to:
//   - LOG to-be-dropped traffic *coming out* from local NIs (dropped during routing phase)
//   - Apply rate-limit ACL rules (DROP extra egress packets)
//   - LOG + fully apply (incl. DROP) ACL rules on traffic *coming out* from switch NIs
func (r *LinuxNIReconciler) getIntendedAppConnRawIptables(vif vifInfo,
	ul types.AppNetAdapterConfig, ipv6 bool) (items []dg.Item) {
	ni := r.nis[vif.NI]
	var bridgeIP net.IP
	if ni.bridge.IPAddress != nil {
		bridgeIP = ni.bridge.IPAddress.IP
	}
	// Put raw/PREROUTING rules for this VIF into a separate table.
	items = append(items, iptables.Chain{
		Table:     "raw",
		ChainName: vifChain("PREROUTING", vif),
		ForIPv6:   ipv6,
	})
	items = append(items, iptables.Rule{
		RuleLabel: fmt.Sprintf("Traverse VIF %s egress ACLs", vif.hostIfName),
		Table:     "raw",
		ChainName: appChain("PREROUTING"),
		ForIPv6:   ipv6,
		MatchOpts: []string{"-i", ni.brIfName,
			"-m", "physdev", "--physdev-in", matchVifIfName(vif)},
		Target: vifChain("PREROUTING", vif),
	})
	// Put ACL rules into the VIF-specific chain.
	// We have already applied physdev filter and get only traffic coming from the VIF.
	var aclRules []iptables.Rule
	// 1. Essential protocols allowed implicitly.
	var essentialProtos []essentialProto
	if ipv6 {
		essentialProtos = getEssentialIPv6Protos(ni.config.Type, bridgeIP)
	} else {
		essentialProtos = getEssentialIPv4Protos(ni.config.Type, bridgeIP)
	}
	for _, proto := range essentialProtos {
		aclRules = append(aclRules, iptables.Rule{
			RuleLabel: "Allow " + proto.label,
			MatchOpts: proto.egressMatch,
			Target:    "ACCEPT",
		})
	}
	// 2. Enable access to the metadata server
	if !ipv6 && bridgeIP != nil {
		aclRules = append(aclRules, iptables.Rule{
			RuleLabel: "Allow access to Metadata server",
			MatchOpts: []string{"-d", metadataSrvIP, "-p", "tcp", "--dport", "80"},
			Target:    "ACCEPT",
		})
	}
	// 3. User-configured ACL rules
	for _, aclRule := range ul.ACLs {
		parsedRule, skip, err := parseUserACLRule(r.log, aclRule, ni, vif, ipv6)
		if err != nil {
			r.log.Errorf("%s: parseUserACLRule failed: %v", LogAndErrPrefix, err)
			continue
		}
		if skip {
			continue
		}
		iptablesRule := iptables.Rule{
			RuleLabel: fmt.Sprintf("User-configured %s ACL rule %d",
				parsedRule.actionLabel, aclRule.RuleID),
			MatchOpts: parsedRule.egressMatch,
		}
		if parsedRule.isLimitRule {
			iptablesRule.MatchOpts = append(iptablesRule.MatchOpts,
				parsedRule.limitArgs...)
		}
		if parsedRule.drop {
			if ni.config.Type == types.NetworkInstanceTypeSwitch {
				iptablesRule.Target = "DROP"
			} else {
				// Add rule to only count the dropped packet, without actually dropping it.
				// Flow is instead dropped during the routing phase (using the blackhole
				// interface).
				iptablesRule.Target = dropCounterChain
			}
		} else {
			iptablesRule.Target = "ACCEPT"
		}
		aclRules = append(aclRules, iptablesRule)
		if parsedRule.isLimitRule {
			// Drop packets exceeding the limit.
			iptablesRule2 := iptables.Rule{
				RuleLabel: fmt.Sprintf("Drop packets exceeding limit "+
					"of user-configured ACL rule %d", aclRule.RuleID),
				MatchOpts: parsedRule.egressMatch,
				Target:    "DROP",
			}
			aclRules = append(aclRules, iptablesRule2)
		}
	}
	// 4. Packet counting rule for the default drop.
	aclRules = append(aclRules, iptables.Rule{
		RuleLabel: "Count packets matched by the Default DROP",
		Target:    dropCounterChain,
	})
	if ni.config.Type == types.NetworkInstanceTypeSwitch {
		// Switched traffic cannot be dropped using the blackhole - it requires routing.
		// Therefore, we drop traffic for switch NIs already in the raw table, without
		// recording flows.
		aclRules = append(aclRules, iptables.Rule{
			RuleLabel: "Default DROP",
			Target:    "DROP",
		})
	}
	// Finally, put all rules together.
	for i, rule := range aclRules {
		rule.ChainName = vifChain("PREROUTING", vif)
		rule.Table = "raw"
		// Keep exact order.
		if i < len(aclRules)-1 {
			rule.AppliedBefore = []string{aclRules[i+1].RuleLabel}
		}
		rule.ForIPv6 = ipv6
		items = append(items, rule)
	}
	return items
}

// Table FILTER, chain FORWARD is used to:
//   - Count packets of to-be-dropped traffic *coming into* local NIs
//     (dropped during the routing phase)
//     XXX Isn't routing performed before filter/forward ?!
//   - Apply rate-limit ACL rules (DROP extra ingress packets)
//   - Count packets + fully apply (incl. DROP) ACLs on traffic *coming into* switch NIs
func (r *LinuxNIReconciler) getIntendedAppConnFilterIptables(vif vifInfo,
	ul types.AppNetAdapterConfig, ipv6 bool) (items []dg.Item) {
	ni := r.nis[vif.NI]
	var bridgeIP net.IP
	if ni.bridge.IPAddress != nil {
		bridgeIP = ni.bridge.IPAddress.IP
	}
	// Put filter/FORWARD rules for this VIF into a separate table.
	// Chain is configured also for air-gapped NIs even if not actually used.
	// This is to prevent LinuxCollector.fetchIptablesCounters from failing to find
	// the chain and logging many errors.
	items = append(items, iptables.Chain{
		Table:     "filter",
		ChainName: vifChain("FORWARD", vif),
		ForIPv6:   ipv6,
	})
	if len(ni.bridge.Ports) == 0 {
		// Air-gapped - not possible to reach applications from outside.
		return
	}
	switch ni.config.Type {
	case types.NetworkInstanceTypeSwitch:
		items = append(items, iptables.Rule{
			RuleLabel: fmt.Sprintf("Traverse VIF %s ingress ACLs", vif.hostIfName),
			Table:     "filter",
			ChainName: appChain("FORWARD"),
			ForIPv6:   ipv6,
			MatchOpts: []string{"-o", ni.brIfName,
				"-m", "physdev", "--physdev-out", matchVifIfName(vif)},
			Target: vifChain("FORWARD", vif),
		})
	case types.NetworkInstanceTypeLocal:
		if vif.GuestIP == nil {
			break
		}
		items = append(items, iptables.Rule{
			RuleLabel: fmt.Sprintf("Traverse VIF %s ingress ACLs", vif.hostIfName),
			Table:     "filter",
			ChainName: appChain("FORWARD"),
			ForIPv6:   ipv6,
			MatchOpts: []string{"-o", ni.brIfName, "-d", vif.GuestIP.String()},
			Target:    vifChain("FORWARD", vif),
		})
	}
	// Put ACL rules into the VIF-specific chain.
	// We have already applied physdev filter or destination IP match and get only traffic
	// going into the VIF.
	var aclRules []iptables.Rule
	// 1. Essential protocols allowed implicitly.
	var essentialProtos []essentialProto
	if ipv6 {
		essentialProtos = getEssentialIPv6Protos(ni.config.Type, bridgeIP)
	} else {
		essentialProtos = getEssentialIPv4Protos(ni.config.Type, bridgeIP)
	}
	for _, proto := range essentialProtos {
		if proto.ingressMatch == nil {
			continue
		}
		aclRules = append(aclRules, iptables.Rule{
			RuleLabel: "Allow " + proto.label,
			MatchOpts: proto.ingressMatch,
			Target:    "ACCEPT",
		})
	}
	// 2. User-configured ACL rules
	for _, aclRule := range ul.ACLs {
		parsedRule, skip, err := parseUserACLRule(r.log, aclRule, ni, vif, ipv6)
		if err != nil {
			r.log.Errorf("%s: parseUserACLRule failed: %v", LogAndErrPrefix, err)
			continue
		}
		if skip {
			continue
		}
		iptablesRule := iptables.Rule{
			RuleLabel: fmt.Sprintf("User-configured %s ACL rule %d",
				parsedRule.actionLabel, aclRule.RuleID),
			MatchOpts: parsedRule.postDNATIngressMatch,
		}
		if parsedRule.isLimitRule {
			iptablesRule.MatchOpts = append(iptablesRule.MatchOpts,
				parsedRule.limitArgs...)
		}
		if parsedRule.drop {
			if ni.config.Type == types.NetworkInstanceTypeSwitch {
				iptablesRule.Target = "DROP"
			} else {
				// Add rule to only count the dropped packet, without actually dropping it.
				// Flow is instead dropped during the routing phase (using the blackhole
				// interface).
				iptablesRule.Target = dropCounterChain
			}
		} else {
			iptablesRule.Target = "ACCEPT"
		}
		aclRules = append(aclRules, iptablesRule)
		if parsedRule.isLimitRule {
			// Drop packets exceeding the limit.
			iptablesRule2 := iptables.Rule{
				RuleLabel: fmt.Sprintf("Drop packets exceeding limit "+
					"of user-configured ACL rule %d", aclRule.RuleID),
				MatchOpts: parsedRule.postDNATIngressMatch,
				Target:    "DROP",
			}
			aclRules = append(aclRules, iptablesRule2)
		}
	}
	// 3. Packet counting rule for the default drop.
	aclRules = append(aclRules, iptables.Rule{
		RuleLabel: "Count packets matched by the Default DROP",
		Target:    dropCounterChain,
	})
	if ni.config.Type == types.NetworkInstanceTypeSwitch {
		// Switched traffic cannot be dropped using the blackhole - it requires routing.
		// Therefore, we drop ingress traffic for switch NIs inside the filter table.
		aclRules = append(aclRules, iptables.Rule{
			RuleLabel: "Default DROP",
			Target:    "DROP",
		})
	}
	// Finally, put all rules together.
	for i, rule := range aclRules {
		rule.ChainName = vifChain("FORWARD", vif)
		rule.Table = "filter"
		// Keep exact order.
		if i < len(aclRules)-1 {
			rule.AppliedBefore = []string{aclRules[i+1].RuleLabel}
		}
		rule.ForIPv6 = ipv6
		items = append(items, rule)
	}
	return items
}

// Table NAT, chain PREROUTING is used to apply port-map ACL rules both for:
//   - traffic coming from outside via device port
//   - traffic coming from another app on the same network instance
//
// Table NAT, chain POSTROUTING is used to:
//   - for every port-map ACL rule, make sure that traffic going via NI bridge
//     and towards the application is SNATed to bridge IP
func (r *LinuxNIReconciler) getIntendedAppConnNATIptables(vif vifInfo,
	ul types.AppNetAdapterConfig, ipv6 bool, portIPs map[string][]*net.IPNet) (items []dg.Item) {
	ni := r.nis[vif.NI]
	if ni.config.Type != types.NetworkInstanceTypeLocal {
		// Only local network instance uses port-mapping ACL rules.
		return items
	}
	if vif.GuestIP == nil || ni.bridge.IPAddress == nil {
		// Missing one or more IPs needed for port forwarding.
		return items
	}
	// Put NAT/PREROUTING and POSTROUTING rules for this VIF into separate tables.
	items = append(items, iptables.Chain{
		Table:     "nat",
		ChainName: vifChain("PREROUTING", vif),
		ForIPv6:   ipv6,
	})
	items = append(items, iptables.Chain{
		Table:     "nat",
		ChainName: vifChain("POSTROUTING", vif),
		ForIPv6:   ipv6,
	})
	items = append(items, iptables.Rule{
		RuleLabel: fmt.Sprintf("Traverse VIF %s port maps", vif.hostIfName),
		Table:     "nat",
		ChainName: appChain("PREROUTING"),
		ForIPv6:   ipv6,
		Target:    vifChain("PREROUTING", vif),
	})
	items = append(items, iptables.Rule{
		RuleLabel: fmt.Sprintf("Traverse VIF %s port maps", vif.hostIfName),
		Table:     "nat",
		ChainName: appChain("POSTROUTING"),
		ForIPv6:   ipv6,
		Target:    vifChain("POSTROUTING", vif),
	})
	for _, aclRule := range ul.ACLs {
		parsedRule, skip, err := parseUserACLRule(r.log, aclRule, ni, vif, ipv6)
		if err != nil {
			r.log.Errorf("%s: parseUserACLRule failed: %v", LogAndErrPrefix, err)
			continue
		}
		if skip {
			continue
		}
		portMap := parsedRule.portMap
		if portMap == nil {
			continue
		}
		// Add DNAT rules for port-map ACL.
		for _, portIfname := range portMap.adapters {
			for _, portIP := range portIPs[portIfname] {
				target := fmt.Sprintf("%s:%d", vif.GuestIP, portMap.targetPort)
				items = append(items, iptables.Rule{
					RuleLabel: fmt.Sprintf("User-configured PORTMAP ACL rule %d "+
						"for port %s IP %s from outside", aclRule.RuleID, portIfname,
						portIP.IP.String()),
					Table:     "nat",
					ChainName: vifChain("PREROUTING", vif),
					ForIPv6:   ipv6,
					MatchOpts: []string{"-i", portIfname,
						"-p", portMap.protocol, "-d", portIP.IP.String(),
						"--dport", portMap.externalPort},
					Target:     "DNAT",
					TargetOpts: []string{"--to-destination", target},
				})
				items = append(items, iptables.Rule{
					RuleLabel: fmt.Sprintf("User-configured PORTMAP ACL rule %d "+
						"for port %s IP %s from inside", aclRule.RuleID, portIfname,
						portIP.IP.String()),
					Table:     "nat",
					ChainName: vifChain("PREROUTING", vif),
					ForIPv6:   ipv6,
					MatchOpts: []string{"-i", ni.brIfName,
						"-p", portMap.protocol, "-d", portIP.IP.String(),
						"--dport", portMap.externalPort},
					Target:     "DNAT",
					TargetOpts: []string{"--to-destination", target},
				})
			}
		}
		// Add SNAT rule for port-map ACL.
		items = append(items, iptables.Rule{
			RuleLabel: fmt.Sprintf("User-configured PORTMAP ACL rule %d",
				aclRule.RuleID),
			Table:     "nat",
			ChainName: vifChain("POSTROUTING", vif),
			ForIPv6:   ipv6,
			MatchOpts: []string{"-o", ni.brIfName,
				"-p", portMap.protocol, "-d", vif.GuestIP.String(),
				"--dport", strconv.Itoa(portMap.targetPort)},
			Target:     "SNAT",
			TargetOpts: []string{"--to", ni.bridge.IPAddress.IP.String()},
		})
	}
	return items
}

// Table MANGLE, chain PREROUTING is used to:
//   - mark connections with the ID of the applied ACL rule
func (r *LinuxNIReconciler) getIntendedAppConnMangleIptables(vif vifInfo,
	ul types.AppNetAdapterConfig, ipv6 bool, portIPs map[string][]*net.IPNet) (items []dg.Item) {
	ni := r.nis[vif.NI]
	app := r.apps[vif.App]
	var bridgeIP net.IP
	if ni.bridge.IPAddress != nil {
		bridgeIP = ni.bridge.IPAddress.IP
	}
	markChainPrefix := fmt.Sprintf("%s-%s-", ni.brIfName, vif.hostIfName)
	addedMarkChains := make(map[string]struct{})
	var essentialProtos []essentialProto
	if ipv6 {
		essentialProtos = getEssentialIPv6Protos(ni.config.Type, bridgeIP)
	} else {
		essentialProtos = getEssentialIPv4Protos(ni.config.Type, bridgeIP)
	}

	// Put mangle/PREROUTING rules for this VIF into a separate table.
	items = append(items, iptables.Chain{
		Table:     "mangle",
		ChainName: vifChain("PREROUTING", vif),
		ForIPv6:   ipv6,
	})
	items = append(items, iptables.Rule{
		RuleLabel: fmt.Sprintf("Traverse VIF %s ACLs", vif.hostIfName),
		Table:     "mangle",
		ChainName: appChain("PREROUTING"),
		ForIPv6:   ipv6,
		Target:    vifChain("PREROUTING", vif),
	})
	// This is further split into ingress and egress rules.
	items = append(items, iptables.Chain{
		Table:     "mangle",
		ChainName: ingressVifChain("PREROUTING", vif),
		ForIPv6:   ipv6,
	})
	items = append(items, iptables.Chain{
		Table:     "mangle",
		ChainName: egressVifChain("PREROUTING", vif),
		ForIPv6:   ipv6,
	})
	ingressTraversal := iptables.Rule{
		RuleLabel: fmt.Sprintf("Traverse VIF %s ingress ACLs", vif.hostIfName),
		Table:     "mangle",
		ChainName: vifChain("PREROUTING", vif),
		ForIPv6:   ipv6,
		Target:    ingressVifChain("PREROUTING", vif),
	}
	egressTraversal := iptables.Rule{
		RuleLabel: fmt.Sprintf("Traverse VIF %s egress ACLs", vif.hostIfName),
		Table:     "mangle",
		ChainName: vifChain("PREROUTING", vif),
		ForIPv6:   ipv6,
		MatchOpts: []string{"-i", ni.brIfName,
			"-m", "physdev", "--physdev-in", matchVifIfName(vif)},
		Target:        egressVifChain("PREROUTING", vif),
		AppliedBefore: []string{ingressTraversal.RuleLabel},
	}
	items = append(items, ingressTraversal, egressTraversal)

	// 1. Add ingress ACL rules
	// Matched by input interface and possibly also by dst IP address.
	var ingressRules []iptables.Rule
	if len(ni.bridge.Ports) == 0 {
		// Air-gapped - not possible to reach applications from outside.
		goto mangleEgress
	}
	// 1.1. Mark essential protocols allowed implicitly.
	for _, proto := range essentialProtos {
		if proto.ingressMatch == nil {
			continue
		}
		markChain := markChainPrefix + proto.markChainName
		mark := iptables.GetConnmark(
			uint8(app.appNum), proto.mark, false, false)
		if _, alreadyAdded := addedMarkChains[markChain]; !alreadyAdded {
			items = append(items, getMarkingChainCfg(markChain, ipv6, markToString(mark))...)
			addedMarkChains[markChain] = struct{}{}
		}
		ingressRules = append(ingressRules, iptables.Rule{
			RuleLabel: "Mark " + proto.label,
			// Ingress marking of essential protocols is used only for switch network
			// instance, hence "-i <brIfName>".
			MatchOpts: append([]string{"-i", ni.brIfName}, proto.ingressMatch...),
			Target:    markChain,
		})
	}
	// 1.2. User-configured ACL rules
	for _, aclRule := range ul.ACLs {
		parsedRule, skip, err := parseUserACLRule(r.log, aclRule, ni, vif, ipv6)
		if err != nil {
			r.log.Errorf("%s: parseUserACLRule failed: %v", LogAndErrPrefix, err)
			continue
		}
		if skip {
			continue
		}
		// Add marking rules only for traffic that can originate from outside.
		// Inside-originating traffic is marked by egress rules.
		if ni.config.Type != types.NetworkInstanceTypeSwitch && parsedRule.portMap == nil {
			continue
		}
		markChain := markChainPrefix + strconv.Itoa(int(aclRule.RuleID))
		mark := iptables.GetConnmark(
			uint8(app.appNum), uint32(aclRule.RuleID), true, parsedRule.drop)
		if _, alreadyAdded := addedMarkChains[markChain]; !alreadyAdded {
			items = append(items,
				getMarkingChainCfg(markChain, ipv6, markToString(mark))...)
			addedMarkChains[markChain] = struct{}{}
		}
		if parsedRule.portMap != nil {
			for _, portIfname := range parsedRule.portMap.adapters {
				for _, portIP := range portIPs[portIfname] {
					iptablesRule := iptables.Rule{
						RuleLabel: fmt.Sprintf("User-configured PORTMAP ACL rule %d "+
							"for port %s IP %s from outside", aclRule.RuleID, portIfname,
							portIP.IP.String()),
						MatchOpts: append([]string{
							"-i", portIfname,
							"-d", portIP.IP.String()},
							parsedRule.preDNATIngressMatch...),
						Target: markChain,
					}
					ingressRules = append(ingressRules, iptablesRule)
					// Also mark port-mapped traffic coming from other application
					// on the same network instance.
					iptablesRule2 := iptables.Rule{
						RuleLabel: fmt.Sprintf("User-configured PORTMAP ACL rule %d "+
							"for port %s IP %s from inside", aclRule.RuleID, portIP,
							portIP.IP.String()),
						MatchOpts: append([]string{
							"-i", ni.brIfName,
							"-d", portIP.IP.String()},
							parsedRule.preDNATIngressMatch...),
						Target: markChain,
					}
					ingressRules = append(ingressRules, iptablesRule2)
				}
			}
			continue
		}
		for _, port := range ni.bridge.Ports {
			matchOpts := []string{"-i", port.IfName}
			iptablesRule := iptables.Rule{
				RuleLabel: fmt.Sprintf("User-configured %s ACL rule %d for ingress "+
					"port %s", parsedRule.actionLabel, aclRule.RuleID, port.IfName),
				MatchOpts: append(matchOpts, parsedRule.preDNATIngressMatch...),
				Target:    markChain,
			}
			if parsedRule.isLimitRule {
				iptablesRule.MatchOpts = append(iptablesRule.MatchOpts,
					parsedRule.limitArgs...)
			}
			ingressRules = append(ingressRules, iptablesRule)
		}
	}

mangleEgress:
	// 2. Add egress ACL rules.
	var egressRules []iptables.Rule
	// 2.1. Mark essential protocols allowed implicitly.
	for _, proto := range essentialProtos {
		markChain := markChainPrefix + proto.markChainName
		mark := iptables.GetConnmark(
			uint8(app.appNum), proto.mark, false, false)
		if _, alreadyAdded := addedMarkChains[markChain]; !alreadyAdded {
			items = append(items, getMarkingChainCfg(markChain, ipv6, markToString(mark))...)
			addedMarkChains[markChain] = struct{}{}
		}
		egressRules = append(egressRules, iptables.Rule{
			RuleLabel: "Mark " + proto.label,
			MatchOpts: proto.egressMatch,
			Target:    markChain,
		})
	}
	// 2.2. Mark request from app to the metadata server
	if !ipv6 && bridgeIP != nil {
		httpMark := iptables.GetConnmark(uint8(app.appNum),
			iptables.ControlProtocolMarkingIDMap["app_http"], false, false)
		markMetadataChain := markChainPrefix + "metadata"
		if _, alreadyAdded := addedMarkChains[markMetadataChain]; !alreadyAdded {
			items = append(items,
				getMarkingChainCfg(markMetadataChain, ipv6, markToString(httpMark))...)
			addedMarkChains[markMetadataChain] = struct{}{}
		}
		egressRules = append(egressRules, iptables.Rule{
			RuleLabel: "Allow access to Metadata server",
			MatchOpts: []string{"-d", metadataSrvIP, "-p", "tcp", "--dport", "80"},
			Target:    markMetadataChain,
		})
	}
	// 2.3. User-configured ACL rules
	for _, aclRule := range ul.ACLs {
		parsedRule, skip, err := parseUserACLRule(r.log, aclRule, ni, vif, ipv6)
		if err != nil {
			r.log.Errorf("%s: parseUserACLRule failed: %v", LogAndErrPrefix, err)
			continue
		}
		if skip {
			continue
		}
		if parsedRule.portMap != nil {
			// Initiated from outside hence marked by the ingress rule.
			continue
		}
		markChain := markChainPrefix + strconv.Itoa(int(aclRule.RuleID))
		mark := iptables.GetConnmark(
			uint8(app.appNum), uint32(aclRule.RuleID), true, parsedRule.drop)
		if _, alreadyAdded := addedMarkChains[markChain]; !alreadyAdded {
			items = append(items,
				getMarkingChainCfg(markChain, ipv6, markToString(mark))...)
			addedMarkChains[markChain] = struct{}{}
		}
		iptablesRule := iptables.Rule{
			RuleLabel: fmt.Sprintf("User-configured %s ACL rule %d",
				parsedRule.actionLabel, aclRule.RuleID),
			MatchOpts: parsedRule.egressMatch,
			Target:    markChain,
		}
		if parsedRule.isLimitRule {
			iptablesRule.MatchOpts = append(iptablesRule.MatchOpts,
				parsedRule.limitArgs...)
		}
		egressRules = append(egressRules, iptablesRule)
	}
	// 2.3. By default, everything not matched is marked with the drop action.
	// Note that DROP for switch NI is already applied in the raw table.
	if ni.config.Type == types.NetworkInstanceTypeLocal {
		dropAllChain := markChainPrefix + "drop-all"
		mark := iptables.GetConnmark(
			uint8(app.appNum), iptables.DefaultDropAceID, false, true)
		items = append(items,
			getMarkingChainCfg(dropAllChain, ipv6, markToString(mark))...)
		defaultDropMark := iptables.Rule{
			RuleLabel: "Default DROP mark",
			Target:    dropAllChain,
		}
		egressRules = append(egressRules, defaultDropMark)
	}

	// Finally, put all rules together.
	for i, rule := range ingressRules {
		rule.ChainName = ingressVifChain("PREROUTING", vif)
		rule.Table = "mangle"
		// Keep exact order.
		if i < len(ingressRules)-1 {
			rule.AppliedBefore = []string{ingressRules[i+1].RuleLabel}
		}
		rule.ForIPv6 = ipv6
		items = append(items, rule)
	}
	for i, rule := range egressRules {
		rule.ChainName = egressVifChain("PREROUTING", vif)
		rule.Table = "mangle"
		// Keep exact order.
		if i < len(egressRules)-1 {
			rule.AppliedBefore = []string{egressRules[i+1].RuleLabel}
		}
		rule.ForIPv6 = ipv6
		items = append(items, rule)
	}
	return items
}

func markToString(mark uint32) string {
	return strconv.FormatUint(uint64(mark), 10)
}

func getMarkingChainCfg(chainName string, ipv6 bool, markStr string) (items []dg.Item) {
	items = append(items, iptables.Chain{
		Table:     "mangle",
		ChainName: chainName,
		ForIPv6:   ipv6,
	})
	rules := []iptables.Rule{
		{
			RuleLabel:  "Restore previous mark",
			Target:     "CONNMARK",
			TargetOpts: []string{"--restore-mark"},
		},
		{
			RuleLabel: "Accept marked connection",
			MatchOpts: []string{"-m", "mark", "!", "--mark", "0"},
			Target:    "ACCEPT",
		},
		{
			RuleLabel:  "Apply mark",
			Target:     "CONNMARK",
			TargetOpts: []string{"--set-mark", markStr},
		},
		{
			RuleLabel:  "Restore new mark",
			Target:     "CONNMARK",
			TargetOpts: []string{"--restore-mark"},
		},
		{
			RuleLabel: "Accept newly marked connection",
			Target:    "ACCEPT",
		},
	}
	for i, rule := range rules {
		rule.ChainName = chainName
		rule.Table = "mangle"
		// Keep exact order.
		if i < len(rules)-1 {
			rule.AppliedBefore = []string{rules[i+1].RuleLabel}
		}
		rule.ForIPv6 = ipv6
		items = append(items, rule)
	}
	return items
}
