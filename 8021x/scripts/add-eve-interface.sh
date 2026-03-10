#!/bin/bash
set -e

IFNAME=$1

ip link set $IFNAME master br0
ip link set $IFNAME up
bridge vlan add vid 100 dev $IFNAME pvid untagged

echo "Added interface $IFNAME connecting EVE device to the unauthenticated VLAN"