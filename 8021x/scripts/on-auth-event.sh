#!/bin/bash

# Called automatically by hostapd_cli -a
EVENT="$2"
MAC="$3"
echo "802.1x Event: $EVENT for $MAC"

case "$EVENT" in
    AP-STA-CONNECTED | CTRL-EVENT-EAP-SUCCESS | CTRL-EVENT-EAP-SUCCESS2)
        echo "802.1x Event: $MAC authorized — unblocking traffic"
        ebtables -D FORWARD -i veth0 -p ! 0x888e -j DROP 2>/dev/null || true
        ebtables -D INPUT -i veth0 -p ! 0x888e -j DROP 2>/dev/null || true
        ;;
    AP-STA-DISCONNECTED | CTRL-EVENT-EAP-FAILURE)
        echo "802.1x Event: $MAC unauthorized — blocking traffic"
        ebtables -C FORWARD -i veth0 -p ! 0x888e -j DROP 2>/dev/null || \
          ebtables -A FORWARD -i veth0 -p ! 0x888e -j DROP
        ebtables -C INPUT -i veth0 -p ! 0x888e -j DROP 2>/dev/null || \
          ebtables -A INPUT -i veth0 -p ! 0x888e -j DROP
        ;;
    *)
        # ignore other events
        ;;
esac