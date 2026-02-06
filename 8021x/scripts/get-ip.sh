#!/bin/bash
set -e

IFACE="$1"
DHCP_TIMEOUT=20  # seconds to wait for lease

if [ -z "$IFACE" ]; then
    echo "Usage: $0 <interface>"
    exit 1
fi

log() {
    echo "[$IFACE] $*"
}

# Kill any existing dhclient for this interface
LEASE_FILE="/var/lib/dhcp/dhclient.leases"
if pidof dhclient >/dev/null 2>&1; then
    log "Stopping existing dhclient..."
    pkill -f "dhclient $IFACE" || true
    rm -f "$LEASE_FILE"
fi

# Flush IP addresses
log "Flushing existing IP addresses..."
ip addr flush dev "$IFACE"

# Start dhclient
log "Starting dhclient..."
dhclient -v "$IFACE" &
DHCLIENT_PID=$!

# Wait for lease and set default route
log "Waiting for DHCP lease..."
for i in $(seq 1 $DHCP_TIMEOUT); do
    # Look for a default route via this interface
    GW=$(grep "option routers" "$LEASE_FILE" | tail -n1 | awk '{print $3}' | tr -d ';')
    if [ -n "$GW" ]; then
        log "DHCP lease acquired — gateway is $GW"
        # Remove any existing default route and set the new one
        ip route del default 2>/dev/null || true
        ip route add default via "$GW" dev "$IFACE"
        log "Default route set via $GW"
        exit 0
    fi
    sleep 1
done

log "DHCP lease NOT acquired after $DHCP_TIMEOUT seconds"
exit 1
