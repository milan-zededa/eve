#!/bin/sh

MAC=$(hostapd_cli -p /var/run/hostapd -i veth0 all_sta | head -n 1)
echo "De-authenticating port with MAC ${MAC}"
hostapd_cli -p /var/run/hostapd -i veth0 deauthenticate "$MAC"