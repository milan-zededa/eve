// Copyright (c) 2023 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package zedrouter

import (
	"fmt"
	"strings"
	"time"

	"github.com/lf-edge/eve/pkg/pillar/types"
	uuid "github.com/satori/go.uuid"
)

func (z *zedrouter) doNetworkInstanceSanityCheck(
	status *types.NetworkInstanceStatus) error {
	z.log.Functionf("Sanity Checking NetworkInstance(%s-%s): type:%d, IpType:%d",
		status.DisplayName, status.UUID, status.Type, status.IpType)

	//  Check NetworkInstanceType
	switch status.Type {
	case types.NetworkInstanceTypeLocal:
		// Do nothing
	case types.NetworkInstanceTypeSwitch:
		// Do nothing
	default:
		return fmt.Errorf("network instance type %d is not supported", status.Type)
	}

	// IpType - Check for valid types
	switch status.IpType {
	case types.AddressTypeNone:
		// Do nothing
	case types.AddressTypeIPV4, types.AddressTypeIPV6,
		types.AddressTypeCryptoIPV4, types.AddressTypeCryptoIPV6:
		err := z.doNetworkInstanceSubnetSanityCheck(status)
		if err != nil {
			return err
		}
		err = z.doNetworkInstanceDhcpRangeSanityCheck(status)
		if err != nil {
			return err
		}
		err = z.doNetworkInstanceGatewaySanityCheck(status)
		if err != nil {
			return err
		}

	default:
		return fmt.Errorf("IpType %d not supported", status.IpType)
	}
	return nil
}

func (z *zedrouter) doNetworkInstanceSubnetSanityCheck(
	status *types.NetworkInstanceStatus) error {
	if status.Subnet.IP == nil || status.Subnet.IP.IsUnspecified() {
		return fmt.Errorf("subnet unspecified for %s-%s: %+v",
			status.Key(), status.DisplayName, status.Subnet)
	}

	items := z.pubNetworkInstanceStatus.GetAll()
	for key2, status2 := range items {
		niStatus2 := status2.(types.NetworkInstanceStatus)
		if status.Key() == key2 {
			continue
		}

		// We check for overlapping subnets by checking the
		// SubnetAddr ( first address ) is not contained in the subnet of
		// any other NI and vice-versa ( Other NI Subnet addrs are not
		// contained in the current NI subnet)

		// Check if status.Subnet is contained in iterStatusEntry.Subnet
		if niStatus2.Subnet.Contains(status.Subnet.IP) {
			return fmt.Errorf("subnet(%s, IP:%s) overlaps with another "+
				"network instance(%s-%s) Subnet(%s)",
				status.Subnet.String(), status.Subnet.IP.String(),
				niStatus2.DisplayName, niStatus2.UUID,
				niStatus2.Subnet.String())
		}

		// Reverse check: check if iterStatusEntry.Subnet is contained in status.subnet
		if status.Subnet.Contains(niStatus2.Subnet.IP) {
			return fmt.Errorf("another network instance(%s-%s) Subnet(%s) "+
				"overlaps with Subnet(%s)",
				niStatus2.DisplayName, niStatus2.UUID,
				niStatus2.Subnet.String(),
				status.Subnet.String())
		}
	}
	return nil
}

func (z *zedrouter) doNetworkInstanceDhcpRangeSanityCheck(
	status *types.NetworkInstanceStatus) error {
	if status.DhcpRange.Start == nil || status.DhcpRange.Start.IsUnspecified() {
		return fmt.Errorf("DhcpRange Start Unspecified: %+v",
			status.DhcpRange.Start)
	}
	if !status.Subnet.Contains(status.DhcpRange.Start) {
		return fmt.Errorf("DhcpRange Start(%s) not within Subnet(%s)",
			status.DhcpRange.Start.String(), status.Subnet.String())
	}
	if status.DhcpRange.End == nil || status.DhcpRange.End.IsUnspecified() {
		return fmt.Errorf("DhcpRange End Unspecified: %+v",
			status.DhcpRange.Start)
	}
	if !status.Subnet.Contains(status.DhcpRange.End) {
		return fmt.Errorf("DhcpRange End(%s) not within Subnet(%s)",
			status.DhcpRange.End.String(), status.Subnet.String())
	}
	return nil
}

func (z *zedrouter) doNetworkInstanceGatewaySanityCheck(
	status *types.NetworkInstanceStatus) error {
	if status.Gateway == nil || status.Gateway.IsUnspecified() {
		return fmt.Errorf("gateway is not specified: %+v",
			status.Gateway)
	}
	if !status.Subnet.Contains(status.Gateway) {
		return fmt.Errorf("gateway(%s) not within Subnet(%s)",
			status.Gateway.String(), status.Subnet.String())
	}
	if status.DhcpRange.Contains(status.Gateway) {
		return fmt.Errorf("gateway(%s) is in DHCP Range(%v,%v)",
			status.Gateway, status.DhcpRange.Start,
			status.DhcpRange.End)
	}
	return nil
}

func (z *zedrouter) validateAppNetworkConfig(appNetConfig types.AppNetworkConfig) error {
	z.log.Functionf("AppNetwork(%s), check for duplicate port map acls",
		appNetConfig.DisplayName)
	// For App Networks, check for common port map rules
	ulCfgList1 := appNetConfig.UnderlayNetworkList
	if len(ulCfgList1) == 0 {
		return nil
	}
	if z.containsHangingACLPortMapRule(ulCfgList1) {
		return fmt.Errorf("network with no uplink, has portmap")
	}
	sub := z.subAppNetworkConfig
	items := sub.GetAll()
	for _, c := range items {
		appNetConfig2 := c.(types.AppNetworkConfig)
		ulCfgList2 := appNetConfig2.UnderlayNetworkList
		if len(ulCfgList2) == 0 {
			continue
		}
		if appNetConfig.DisplayName == appNetConfig2.DisplayName {
			continue
		}
		appNetStatus2 := z.lookupAppNetworkStatus(appNetConfig2.Key())
		if appNetStatus2 == nil {
			continue
		}
		if appNetStatus2.HasError() || !appNetStatus2.Activated {
			continue
		}
		if z.checkForPortMapOverlap(ulCfgList1, ulCfgList2) {
			return fmt.Errorf("app %s and %s have duplicate portmaps",
				appNetConfig.DisplayName, appNetStatus2.DisplayName)
		}
	}
	return nil
}

func (z *zedrouter) validateAppNetworkConfigForModify(
	newConfig types.AppNetworkConfig, oldConfig types.AppNetworkConfig) error {
	// XXX What about changing the number of interfaces as part of an inactive/active
	// transition?
	// XXX We could allow the addition of interfaces if the domU would find out through
	// some hotplug event.
	// But deletion is hard.
	// For now don't allow any adds or deletes.
	if len(newConfig.UnderlayNetworkList) != len(oldConfig.UnderlayNetworkList) {
		return fmt.Errorf("changing number of underlays (for %s) is unsupported",
			newConfig.UUIDandVersion)
	}
	return z.validateAppNetworkConfig(newConfig)
}

func (z *zedrouter) checkNetworkReferencesFromApp(config types.AppNetworkConfig) (
	netInErrState bool, err error) {
	// Check networks for Underlay
	// XXX - Should we also check for Network(instance)Status objects here itself?
	for _, ulConfig := range config.UnderlayNetworkList {
		netInstStatus := z.lookupNetworkInstanceStatus(ulConfig.Network.String())
		if netInstStatus == nil {
			err := fmt.Errorf("missing underlay network %s for app %s/%s",
				ulConfig.Network.String(), config.UUIDandVersion, config.DisplayName)
			z.log.Error(err)
			// App network configuration that has underlays pointing to non-existent
			// network instances is invalid. Such configuration should never come to
			// device from cloud.
			// But, on the device sometimes, zedrouter sees the app network configuration
			// before seeing the required network instance configuration. This is transient
			// and zedrouter re-creates the app network when the corresponding network
			// instance configuration finally arrives.
			// In such cases it is less confusing to put the app network in network wait
			// state rather than in error state.
			// We use the AwaitNetworkInstance in AppNetworkStatus that is already present.
			return false, err
		}
		if !netInstStatus.Activated {
			err := fmt.Errorf("underlay network %s needed by app %s/%s is not activated",
				ulConfig.Network.String(), config.UUIDandVersion, config.DisplayName)
			z.log.Error(err)
			return false, err
		}
		if netInstStatus.HasError() {
			err := fmt.Errorf(
				"underlay network %s needed by app %s/%s is in error state: %s",
				ulConfig.Network.String(), config.UUIDandVersion, config.DisplayName,
				netInstStatus.Error)
			z.log.Error(err)
			return true, err
		}
	}
	return false, nil
}

// Check if there is a portmap rule for a network instance with no uplink interface.
func (z *zedrouter) containsHangingACLPortMapRule(
	ulCfgList []types.UnderlayNetworkConfig) bool {
	for _, ulCfg := range ulCfgList {
		network := ulCfg.Network.String()
		netInstStatus := z.lookupNetworkInstanceStatus(network)
		if netInstStatus == nil || netInstStatus.PortLogicalLabel != "" {
			continue
		}
		for _, ace := range ulCfg.ACLs {
			for _, action := range ace.Actions {
				if action.PortMap {
					return true
				}
			}
		}
	}
	return false
}

func (z *zedrouter) checkForPortMapOverlap(ulCfgList1 []types.UnderlayNetworkConfig,
	ulCfgList2 []types.UnderlayNetworkConfig) bool {
	for _, ulCfg1 := range ulCfgList1 {
		network1 := ulCfg1.Network
		// Validate whether there are duplicate portmap rules within itself.
		if z.detectPortMapConflictWithinUL(ulCfg1.ACLs) {
			return true
		}
		for _, ulCfg2 := range ulCfgList2 {
			network2 := ulCfg2.Network
			if network1 == network2 || z.checkUplinkPortOverlap(network1, network2) {
				if z.detectPortMapConflictAcrossULs(ulCfg1.ACLs, ulCfg2.ACLs) {
					return true
				}
			}
		}
	}
	return false
}

// Check if network instances are sharing common uplink.
func (z *zedrouter) checkUplinkPortOverlap(network1, network2 uuid.UUID) bool {
	netInstStatus1 := z.lookupNetworkInstanceStatus(network1.String())
	netInstStatus2 := z.lookupNetworkInstanceStatus(network2.String())
	if netInstStatus1 == nil || netInstStatus2 == nil {
		return false
	}
	return netInstStatus1.SelectedUplinkIntfName == netInstStatus2.SelectedUplinkIntfName
}

// Caller should clear the appropriate status.Pending* if the caller will
// return after adding the error.
func (z *zedrouter) addAppNetworkError(status *types.AppNetworkStatus,
	tag string, err error) (changed bool) {
	z.log.Errorf("%s: %v", tag, err)
	// XXX The use of appendError() could be more normalized
	status.Error, changed = appendError(status.Error, tag, err.Error())
	status.ErrorTime = time.Now()
	if changed {
		z.publishAppNetworkStatus(status)
	}
	return changed
}

func appendError(allErrors string, prefix string, lasterr string) (
	newError string, changed bool) {
	if strings.Contains(allErrors, lasterr) {
		// Avoid duplicate errors.
		return allErrors, false
	}
	return fmt.Sprintf("%s%s: %s\n\n", allErrors, prefix, lasterr), true
}

func (z *zedrouter) detectPortMapConflictWithinUL(ACLs []types.ACE) bool {
	matchTypes1 := []string{"protocol"}
	matchTypes2 := []string{"protocol", "lport"}
	idx1 := 0
	ruleNum := len(ACLs)
	for idx1 < ruleNum-1 {
		ace1 := ACLs[idx1]
		for _, action := range ace1.Actions {
			if !action.PortMap {
				continue
			}
			idx2 := idx1 + 1
			for idx2 < ruleNum {
				ace2 := ACLs[idx2]
				for _, action1 := range ace2.Actions {
					if !action1.PortMap {
						continue
					}
					// check for protocol/TargetPort
					if action.TargetPort == action1.TargetPort &&
						z.matchACEs(ace1, ace2, matchTypes1) {
						z.log.Errorf("Port-map match found for %d %d: ace1 %v ace2 %v",
							idx1, idx2, ace1, ace2)
						return true
					}
					// check for protocol/lport
					if z.matchACEs(ace1, ace2, matchTypes2) {
						z.log.Errorf("Port-map match found for %d %d: ace1 %v ace2 %v",
							idx1, idx2, ace1, ace2)
						return true
					}
				}
				idx2++
			}
		}
		idx1++
	}
	return false
}

// Check for duplicate portmap rules in between two set of ACLs.
// For this, we will match the protocol/lport being same.
func (z *zedrouter) detectPortMapConflictAcrossULs(
	ACLs []types.ACE, ACLs1 []types.ACE) bool {
	matchTypes := []string{"protocol", "lport"}
	for _, ace1 := range ACLs {
		for _, action := range ace1.Actions {
			// not a portmap rule
			if !action.PortMap {
				continue
			}
			for _, ace2 := range ACLs1 {
				for _, action1 := range ace2.Actions {
					// not a portmap rule
					if !action1.PortMap {
						continue
					}
					// match for protocol/lport
					if z.matchACEs(ace1, ace2, matchTypes) {
						z.log.Errorf("Port-map match found for ace %v ace2 %v",
							ace1, ace2)
						return true
					}
				}
			}
		}
	}
	return false
}

// generic comparison routine for ACL match conditions
func (z *zedrouter) matchACEs(ace1 types.ACE, ace2 types.ACE,
	matchTypes []string) bool {
	valueList1 := make([]string, len(matchTypes))
	valueList2 := make([]string, len(matchTypes))

	for idx, matchType := range matchTypes {
		for _, match := range ace1.Matches {
			if matchType == match.Type {
				valueList1[idx] = match.Value
			}
		}
		for _, match := range ace2.Matches {
			if matchType == match.Type {
				valueList2[idx] = match.Value
			}
		}
	}
	for idx, value := range valueList1 {
		value1 := valueList2[idx]
		if value == "" || value1 == "" ||
			value != value1 {
			z.log.Functionf("difference for %d: value %s value1 %s",
				idx, value, value1)
			return false
		}
	}
	return true
}
