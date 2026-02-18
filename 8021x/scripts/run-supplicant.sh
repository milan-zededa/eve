#!/bin/sh

IFACE="veth1"
CONF_STD="/etc/wpa_supplicant/wpa_supplicant.conf"
CONF_PKCS11="/etc/wpa_supplicant/wpa_supplicant-pkcs11.conf"

# ----------------------------------------------------
# Select configuration based on TPM presence
# ----------------------------------------------------
if [ -c /dev/tpmrm0 ] || [ -c /dev/tpm0 ]; then
    echo "[INFO] TPM detected, using PKCS#11 configuration"
    CONF="$CONF_PKCS11"
else
    echo "[INFO] No TPM detected, using standard configuration"
    CONF="$CONF_STD"
fi

# ----------------------------------------------------
# Start wpa_supplicant
# ----------------------------------------------------
wpa_supplicant -i "$IFACE" -Dwired -c "$CONF" -d &

sleep 2

# ----------------------------------------------------
# Start wpa_cli event handler
# ----------------------------------------------------
wpa_cli -i "$IFACE" -a /usr/local/bin/wpa-event.sh &
