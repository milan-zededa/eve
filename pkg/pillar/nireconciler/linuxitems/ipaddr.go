// Copyright (c) 2023 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package linuxitems

import (
	"context"
	"errors"
	"fmt"

	dg "github.com/lf-edge/eve/libs/depgraph"
	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/nireconciler/genericitems"
	"github.com/vishvananda/netlink"
)

// IPAddressConfigurator implements Configurator interface (libs/reconciler)
// for an IP address assigned to network interface.
// Note that the item type is defined in genericitems while this configurator
// is specific to the Linux network stack.
type IPAddressConfigurator struct {
	Log *base.LogObject
}

// Create assigns IP address to the target network interface.
func (c *IPAddressConfigurator) Create(ctx context.Context, item dg.Item) error {
	ipAddress, isIPAddress := item.(genericitems.IPAddress)
	if !isIPAddress {
		return fmt.Errorf("invalid item type %T, expected IPAddress", item)
	}
	ifName := ipAddress.NetIf.IfName
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		err = fmt.Errorf("failed to get link for network interface %s: %w",
			ifName, err)
		c.Log.Error(err)
		return err
	}
	addr := &netlink.Addr{IPNet: ipAddress.AddrWithMask}
	err = netlink.AddrAdd(link, addr)
	if err != nil {
		err = fmt.Errorf("failed to assign IP address %s to network interface %s: %w",
			ipAddress.AddrWithMask.String(), ifName, err)
		c.Log.Error(err)
		return err
	}
	return nil
}

// Modify is not implemented.
func (c *IPAddressConfigurator) Modify(ctx context.Context, oldItem, newItem dg.Item) (err error) {
	return errors.New("not implemented")
}

// Delete un-assigns the IP address.
func (c *IPAddressConfigurator) Delete(ctx context.Context, item dg.Item) error {
	ipAddress, isIPAddress := item.(genericitems.IPAddress)
	if !isIPAddress {
		return fmt.Errorf("invalid item type %T, expected IPAddress", item)
	}
	ifName := ipAddress.NetIf.IfName
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		err = fmt.Errorf("failed to get link for network interface %s: %w",
			ifName, err)
		c.Log.Error(err)
		return err
	}
	addr := &netlink.Addr{IPNet: ipAddress.AddrWithMask}
	err = netlink.AddrDel(link, addr)
	if err != nil {
		err = fmt.Errorf("failed to un-assign IP address %s from network interface %s: %w",
			ipAddress.AddrWithMask.String(), ifName, err)
		c.Log.Error(err)
		return err
	}
	return nil
}

// NeedsRecreate always returns true - Modify is not implemented.
func (c *IPAddressConfigurator) NeedsRecreate(oldItem, newItem dg.Item) (recreate bool) {
	return true
}
