#!/bin/sh

wpa_supplicant -i veth1 -Dwired -c /etc/wpa_supplicant/wpa_supplicant.conf -d &
sleep 2
wpa_cli -i veth1 -a /usr/local/bin/wpa-event.sh &