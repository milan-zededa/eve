qmi() {
  local JSON
  JSON=`timeout -s KILL "$LTESTAT_TIMEOUT" uqmi -d "/dev/$CDC_DEV" "$@"`
  if [ $? -eq 0 ] && (echo "$JSON" | jq -ea . > /dev/null 2>&1) ; then
    echo "$JSON"
    return 0
  fi
  return 1
}

# For Device Management Service (DMS) we use qmicli instead of uqmi.
# This is mostly because uqmi doesn't provide any getter methods for DMS.
qmi_dms() {
  timeout -s KILL "$LTESTAT_TIMEOUT" qmicli -d "/dev/$CDC_DEV" --dms-"$@"
}

# Apart from DMS we use qmicli (over uqmi) also with Wireless Data Service (WDS)
# to obtain packet/byte statistics (not available with uqmi).
qmi_get_packet_stats() {
  local CMD="--wds-get-packet-statistics"
  local STATS="$(timeout -s KILL "$LTESTAT_TIMEOUT" qmicli -d "/dev/$CDC_DEV" "$CMD")"
  local TXP=$(parse_modem_attr "$STATS" "TX packets OK")
  local TXB=$(parse_modem_attr "$STATS" "TX bytes OK")
  local TXD=$(parse_modem_attr "$STATS" "TX packets dropped")
  local RXP=$(parse_modem_attr "$STATS" "RX packets OK")
  local RXB=$(parse_modem_attr "$STATS" "RX bytes OK")
  local RXD=$(parse_modem_attr "$STATS" "RX packets dropped")
  json_struct \
    "$(json_attr tx-bytes ${TXB:-0})" "$(json_attr tx-packets ${TXP:-0})" "$(json_attr tx-drops ${TXD:-0})" \
    "$(json_attr rx-bytes ${RXB:-0})" "$(json_attr rx-packets ${RXP:-0})" "$(json_attr rx-drops ${RXD:-0})"
}

qmi_get_signal_info() {
  local INFO
  INFO="$(qmi --get-signal-info)" || INFO="{}"
  FILTER="{rssi: (if .rssi == null then $UNAVAIL_SIGNAL_METRIC else .rssi end),
           rsrq: (if .rsrq == null then $UNAVAIL_SIGNAL_METRIC else .rsrq end),
           rsrp: (if .rsrp == null then $UNAVAIL_SIGNAL_METRIC else .rsrp end),
           snr:  (if .snr  == null then $UNAVAIL_SIGNAL_METRIC else .snr end)}"
  echo "$INFO" | jq -c "$FILTER"
}

# qmi_get_op_mode returns one of: "" (aka unspecified), "online", "online-and-connected", "radio-off", "offline", "unrecognized"
qmi_get_op_mode() {
  local OP_MODE="$(qmi_dms get-operating-mode | sed -n "s/\s*Mode: '\(.*\)'/\1/p")"
  case "$OP_MODE" in
    "online")
      if [ "$(qmi --get-data-status)" = '"connected"' ]; then
        echo "online-and-connected"
      else
        echo "online"
      fi
    ;;
    "offline") echo "$OP_MODE"
    ;;
    "low-power" | "persistent-low-power" | "mode-only-low-power") echo "radio-off"
    ;;
    *) echo "unrecognized"
    ;;
  esac
}

qmi_get_imei() {
  qmi --get-imei | tr -d '"'
}

qmi_get_modem_model() {
  qmi_dms get-model | sed -n "s/\s*Model: '\(.*\)'/\1/p"
}

qmi_get_modem_revision() {
  qmi_dms get-revision | sed -n "s/\s*Revision: '\(.*\)'/\1/p"
}

qmi_get_providers() {
  local PROVIDERS
  PROVIDERS="$(qmi --network-scan)"
  if [ $? -ne 0 ]; then
    echo "[]"
    return 1
  fi
  FILTER='[.network_info[] | { "plmn": [if .mcc == null then "000" else .mcc end, if .mnc == null then "000" else .mnc end] | join("-"),
                               "description": .description,
                               "current-serving": .status | contains(["current_serving"]),
                               "roaming":  .status | contains(["roaming"])}
          ] | unique'
  echo "$PROVIDERS" | jq -c "$FILTER"
}

qmi_start_network() {
  echo "[$CDC_DEV] Starting network for APN ${APN}"
  ip link set $IFACE down
  echo Y > /sys/class/net/$IFACE/qmi/raw_ip
  ip link set $IFACE up

  qmi --sync
  qmi --start-network --apn "${APN}" --keep-client-id wds |\
      mbus_publish pdh_$IFACE
}

qmi_wait_for_sim() {
  # FIXME XXX this is only for MBIM for now
  :
}

qmi_wait_for_wds() {
  echo "[$CDC_DEV] Waiting for DATA services to connect"
  local CMD="qmi --get-data-status | jq -r ."

  if ! wait_for connected "$CMD"; then
    echo "Timeout waiting for DATA services to connect" >&2
    return 1
  fi
}

qmi_wait_for_register() {
  echo "[$CDC_DEV] Waiting for the device to register on the network"
  local CMD="qmi --get-serving-system | jq -r .registration"

  if ! wait_for registered "$CMD"; then
    echo "Timeout waiting for the device to register on the network" >&2
    return 1
  fi
}

qmi_wait_for_settings() {
  echo "[$CDC_DEV] Waiting for IP configuration for the $IFACE interface"
  local CMD="qmi --get-current-settings"

  if ! wait_for connected "$CMD | jq -r .ipv4.ip | grep -q \"$IPV4_REGEXP\" && echo connected"; then
    echo "Timeout waiting for IP configuration for the $IFACE interface" >&2
    return 1
  fi
}

qmi_reset_modem() {
  # last ditch attempt to reset our modem -- not sure how effective :-(
  local PDH=`cat ${BBS}/pdh_${IFACE}.json 2>/dev/null`

  for i in $PDH 0xFFFFFFFF ; do
    qmi --stop-network $i --autoconnect || continue
  done

  qmi_dms reset

  for i in $PDH 0xFFFFFFFF ; do
    qmi --stop-network $i --autoconnect || continue
  done
}

qmi_toggle_rf() {
  if [ "$(intended_modem_op_mode)" = "radio-off" ]; then
    echo "[$CDC_DEV] Disabling RF"
    qmi --set-device-operating-mode "persistent_low_power"
  else
    echo "[$CDC_DEV] Enabling RF"
    qmi --set-device-operating-mode "online"
  fi
}
