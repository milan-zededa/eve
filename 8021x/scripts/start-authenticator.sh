#!/bin/bash
set -e

# ---------------------------
# Create bridge br0
# ---------------------------
echo "Setting up bridge br0..."
ip link add br0 type bridge 2>/dev/null || true
ip link set br0 up
ip link set br0 type bridge vlan_filtering 1
bridge vlan add dev br0 vid 100 self
bridge vlan add dev br0 vid 200 self

# ---------------------------
# Create VLAN sub-interfaces
# ---------------------------
# VLAN 100
ip link add link br0 name br0.100 type vlan id 100
ip addr add 192.168.100.1/24 dev br0.100
ip link set br0.100 up

# VLAN 200
ip link add link br0 name br0.200 type vlan id 200
ip addr add 192.168.200.1/24 dev br0.200
ip link set br0.200 up

# --------------------------------
# Configure NAT for outside access
# --------------------------------
iptables -t nat -A POSTROUTING -s 192.168.100.0/24 -o eth0 -j MASQUERADE
iptables -t nat -A POSTROUTING -s 192.168.200.0/24 -o eth0 -j MASQUERADE

# ------------------------------------------------------
# Allow VLAN 100 to only access Zedcloud and SCEP server
# ------------------------------------------------------
# allow return traffic
iptables -A FORWARD -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
# allow DNS
iptables -A FORWARD -s 192.168.100.0/24 -p udp --dport 53 -j ACCEPT
iptables -A FORWARD -s 192.168.100.0/24 -p tcp --dport 53 -j ACCEPT
# allow Zedcloud
iptables -A FORWARD -s 192.168.100.0/24 -d 104.18.2.204/32 -j ACCEPT
iptables -A FORWARD -s 192.168.100.0/24 -d 104.18.3.204/32 -j ACCEPT
# allow SCEP server
iptables -A FORWARD -s 192.168.100.0/24 -d 13.60.188.251/32 -j ACCEPT
# allow accessing my laptop (e.g., to download new EVE image)
iptables -A FORWARD -s 192.168.100.0/24 -d 10.10.10.102/32 -j ACCEPT
# drop rest of internet
iptables -A FORWARD -s 192.168.100.0/24 -o eth0 -j DROP

echo "Enabling EAPOL forwarding on br0..."
echo 0x8 > /sys/class/net/br0/bridge/group_fwd_mask ||\
  echo "Could not set group_fwd_mask!"

# Start dnsmasq for each VLAN to provide DHCP service
echo "Starting dnsmasq DHCP server for VLAN 100..."
dnsmasq --no-daemon --interface=br0.100 --bind-interfaces --port=0 \
    --dhcp-range=192.168.100.2,192.168.100.255,12h \
    --dhcp-option=3,192.168.100.1 \
    --dhcp-option=6,1.1.1.1,8.8.8.8 &
sleep 1
echo "Starting dnsmasq DHCP server for VLAN 200..."
dnsmasq --no-daemon --interface=br0.200 --bind-interfaces --port=0 \
    --dhcp-range=192.168.200.2,192.168.200.255,12h \
    --dhcp-option=3,192.168.200.1 \
    --dhcp-option=6,1.1.1.1,8.8.8.8 &
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
        if [ -S /var/run/hostapd/br0 ]; then
            echo "Control socket ready, starting event listener"
            break
        fi
        sleep 1
    done
    hostapd_cli -p /var/run/hostapd -a /usr/local/bin/on-auth-event.sh
) &

echo "✅ Running: DHCP servers and 802.1X authenticator (with blocking)"
tail -f /dev/null