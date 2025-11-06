#!/bin/sh
IFACE=$1
EVENT=$2

update_route() {
    # Wait until dhclient creates the lease file (indicating success)
    LEASE_FILE="/var/lib/dhcp/dhclient.leases"

    echo "[$IFACE] Waiting for DHCP lease..."

    # Wait up to 10 seconds for the lease file to be updated
    for i in $(seq 1 20); do
        if grep -q "interface \"$IFACE\"" "$LEASE_FILE" 2>/dev/null; then
            echo "[$IFACE] DHCP lease acquired — updating route"
            ip route del default 2>/dev/null
            ip route add default via 192.168.50.1 dev veth1
            return
        fi
        sleep 0.5
    done

    echo "[$IFACE] DHCP lease NOT acquired — route not updated"
}

case "$EVENT" in
    CONNECTED)
        echo "[$IFACE] Connected — start DHCP"
        dhclient -d -v "$IFACE" &
        update_route &
        ;;
    DISCONNECTED)
        echo "[$IFACE] Disconnected — stop DHCP"
        pkill -f "dhclient $IFACE"
        ip addr flush veth1
        ;;
esac