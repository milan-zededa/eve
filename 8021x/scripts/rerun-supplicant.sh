#!/bin/sh

wpa_cli -i veth1 disconnect
wpa_cli -i veth1 terminate
sleep 5
pkill -f wpa_cli

/usr/local/bin/run-supplicant.sh

