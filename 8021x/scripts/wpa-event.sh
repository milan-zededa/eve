#!/bin/sh
IFACE=$1
EVENT=$2

case "$EVENT" in
    CONNECTED)
        echo "[$IFACE] WPA EVENT: Connected"
        ;;
    DISCONNECTED)
        echo "[$IFACE] WPA EVENT: Disconnected"
        ;;
esac