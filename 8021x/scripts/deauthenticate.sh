#!/bin/sh

HOSTAPD_SOCK="/var/run/hostapd"
IFACE="br0"

# Get all lines from all_sta
hostapd_cli -p "$HOSTAPD_SOCK" -i "$IFACE" all_sta | \
while read -r LINE; do
    # Only match lines that look like a MAC address
    if echo "$LINE" | grep -Eq '^([0-9a-f]{2}:){5}[0-9a-f]{2}$'; then
        MAC="$LINE"
        echo "De-authenticating station with MAC $MAC"
        hostapd_cli -p "$HOSTAPD_SOCK" -i "$IFACE" deauthenticate "$MAC"
    fi
done
