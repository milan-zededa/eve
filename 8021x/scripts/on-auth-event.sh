#!/bin/bash
set -e

# Called automatically by hostapd_cli -a
EVENT="$2"
MAC="$3"
BRIDGE="br0"
VLAN_BLOCK=100
VLAN_ALLOW=200

echo "802.1x Event: $EVENT for $MAC"

# Helper: move port into a VLAN
set_port_vlan() {
    local mac="$1"
    local vlan="$2"

    # Find interface(s) corresponding to this MAC on the bridge
    # Only considers ports currently in the bridge
    PORTS=$(bridge fdb show br "$BRIDGE" | awk -v m="$mac" '$1==m {print $3}')

    if [ -z "$PORTS" ]; then
        echo "WARNING: MAC $mac not found in bridge $BRIDGE"
        return
    fi

    for p in $PORTS; do
        echo "Setting port $p for MAC $mac into VLAN $vlan"
        bridge vlan del dev $p vid $VLAN_BLOCK 2>/dev/null || true
        bridge vlan del dev $p vid $VLAN_ALLOW 2>/dev/null || true
        bridge vlan add dev $p vid $vlan pvid untagged
    done
}

case "$EVENT" in
    CTRL-EVENT-EAP-SUCCESS | CTRL-EVENT-EAP-SUCCESS2)
        echo "802.1x Event: $MAC authorized — assigning VLAN $VLAN_ALLOW"
        set_port_vlan "$MAC" "$VLAN_ALLOW"
        ;;
    AP-STA-DISCONNECTED | CTRL-EVENT-EAP-FAILURE)
        echo "802.1x Event: $MAC unauthorized — assigning VLAN $VLAN_BLOCK"
        set_port_vlan "$MAC" "$VLAN_BLOCK"
        ;;
    *)
        # ignore other events
        ;;
esac
