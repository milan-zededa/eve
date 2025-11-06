#!/bin/bash
set -e

echo "Setting up bridge br0..."
ip link add br0 type bridge 2>/dev/null
ip addr add 192.168.50.1/24 dev br0 2>/dev/null
ip link set br0 up
iptables -t nat -A POSTROUTING -s 192.168.50.0/24 -o eth0 -j MASQUERADE

# TODO: is this needed?
#echo "Enabling EAPOL forwarding on br0..."
#echo 0x8 > /sys/class/net/br0/bridge/group_fwd_mask ||\
# echo "Could not set group_fwd_mask!"

# Wait for veth0 to appear
echo "Waiting for veth0 to appear..."
for i in $(seq 1 30); do
    if ip link show veth0 >/dev/null 2>&1; then
        echo "veth0 found (after ${i}s)"
        ip link set veth0 master br0
        ip link set veth0 up
        break
    fi
    sleep 1
done

if ! ip link show veth0 >/dev/null 2>&1; then
    echo "ERROR: veth0 did not appear within timeout."
    exit 1
fi

# Block all non-EAPOL (0x888e) traffic initially
echo "Blocking non-EAPOL on veth0..."
ebtables -A FORWARD -i veth0 -p ! 0x888e -j DROP
ebtables -A INPUT -i veth0 -p ! 0x888e -j DROP

# Start dnsmasq for bridge DHCP
echo "Starting dnsmasq DHCP server..."
dnsmasq --no-daemon --interface=br0 --bind-interfaces \
    --dhcp-range=192.168.50.100,192.168.50.200,12h &
sleep 1

# Start hostapd
echo "Starting hostapd..."
hostapd -dd /etc/hostapd/hostapd.conf &
sleep 1

# Launch hostapd_cli monitor
(
    # Wait for the control socket
    echo "Waiting for hostapd control socket..."
    for i in $(seq 1 20); do
        if [ -S /var/run/hostapd/veth0 ]; then
            echo "Control socket ready, starting event listener"
            break
        fi
        sleep 1
    done
    hostapd_cli -p /var/run/hostapd -a /usr/local/bin/on-auth-event.sh
) &

echo "✅ Running: DHCP server and 802.1X authenticator (with blocking)"
tail -f /dev/null