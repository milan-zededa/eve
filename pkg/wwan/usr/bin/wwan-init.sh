#!/bin/sh
# set -x

# Currently our message bus is files in /run
# When we replace it, we should pay attention
# to the notion of the current context which
# is currently expressed as an FS path rooted
# under /run. This makes all rendezvous points
# be relative path names (either ./ for the ones
# that are local to this service or ../ for the
# global context).
#
# Some inspiration (but not the code!) taken from:
#    https://github.com/openwrt-mirror/openwrt/blob/master/package/network/utils/uqmi/files/lib/netifd/proto/qmi.sh
BBS=/run/wwan
CONFIG_PATH="${BBS}/config.json"
STATUS_PATH="${BBS}/status.json"
METRICS_PATH="${BBS}/metrics.json"

LTESTAT_TIMEOUT=120
PROBE_INTERVAL=300  # how often to probe the connectivity status (in seconds)
METRICS_INTERVAL=60 # how often to obtain and publish metrics (in seconds)
UNAVAIL_SIGNAL_METRIC=$(printf "%d" 0x7FFFFFFF) # max int32

DEFAULT_PROBE_ADDR="8.8.8.8"
DEFAULT_APN="internet"

IPV4_REGEXP='[0-9]\+\.[0-9]\+\.[0-9]\+\.[0-9]\+'

SRC=$(cd $(dirname "$0"); pwd)
. "${SRC}/wwan-qmi.sh"
. "${SRC}/wwan-mbim.sh"

json_attr() {
  printf '"%s":%s' "$1" "$2"
}

json_str_attr() {
  printf '"%s":"%s"' "$1" "$2"
}

json_struct() {
  local ITEMS="$(for ARG in "${@}"; do printf ",%s" "$ARG"; done | cut -c2-)"
  printf "{%s}" "$ITEMS"
}

json_array() {
  local ITEMS="$(while read LINE; do [ ! -z "${LINE}" ] && printf ",%s" "${LINE}"; done | cut -c2-)"
  printf "[%s]" "$ITEMS"
}

parse_json_attr() {
  local JSON="$1"
  local JSON_PATH="$2"
  echo $JSON | jq -rc ".$JSON_PATH | select (.!=null)"
}

mod_reload() {
  local RLIST
  for mod in $* ; do
    RLIST="$mod $RLIST"
    rmmod -f $mod
  done
  for mod in $RLIST ; do
    RLIST="$mod $RLIST"
    modprobe $mod
  done
}

wait_for() {
  local EXPECT="$1"
  shift
  for i in `seq 1 10`; do
     eval RES='"$('"$*"')"'
     [ "$RES" = "$EXPECT" ] && return 0
     sleep 6
  done
  return 1
}

mbus_publish() {
  [ -d "$BBS" ] || mkdir -p $BBS || exit 1
  cat > "$BBS/${1}.json"
}

# parse value of an attribute returned by mbimcli or qmicli
parse_modem_attr() {
  local STDOUT="$1"
  local ATTR="$2"
  echo "$STDOUT" | sed -n "s/\s*$ATTR: \(.*\)/\1/p" | tr -d "'"
}

intended_modem_op_mode() {
  if [ "$AIRPLANE_MODE" = "true" ]; then
    echo "radio-off"
  else
    # default if airplane mode is not specified
    echo "online"
  fi
}

config_checksum() {
  local CONFIG="$1"
  printf "%s" "$CONFIG" | md5sum | cut -d " " -f1
}

sys_get_modem_protocol() {
  local SYS_DEV="$1"
  local MODULE="$(basename "$(readlink "${SYS_DEV}/device/driver/module")")"
  case "$MODULE" in
    "cdc_mbim") echo "mbim"
    ;;
    "qmi_wwan") echo "qmi"
    ;;
    *) return 1
    ;;
  esac
}

sys_get_modem_interface() {
  local SYS_DEV="$1"
  ls "${SYS_DEV}/device/net"
}

sys_get_modem_usbaddr() {
  local SYS_DEV="$1"
  local DEV_PATH="$(readlink -f "${SYS_DEV}/device")"
  while [ -e "$DEV_PATH/subsystem" ]; do
    if [ "$(basename "$(readlink "$DEV_PATH/subsystem")")" != "usb" ]; then
      DEV_PATH="$(dirname "$DEV_PATH")"
      continue
    fi
    echo "$(basename $DEV_PATH | cut -d ":" -f 1 | tr '-' ':')"
    return
  done
}

sys_get_modem_pciaddr() {
  local SYS_DEV="$1"
  local DEV_PATH="$(readlink -f "${DEV}/device")"
  while [ -e "$DEV_PATH/subsystem" ]; do
    if [ "$(basename "$(readlink "$DEV_PATH/subsystem")")" != "pci" ]; then
      DEV_PATH="$(dirname "$DEV_PATH")"
    continue
    fi
    echo "$(basename $DEV_PATH)"
    return
  done
}

# If successful, sets CDC_DEV, PROTOCOL, IFACE, USB_ADDR and PCI_ADDR variables.
lookup_modem() {
  local ARG_IF="$1"
  local ARG_USB="$2"
  local ARG_PCI="$3"

  for DEV in /sys/class/usbmisc/*; do
    DEV_PROT=$(sys_get_modem_protocol "$DEV") || continue

    # check interface name
    DEV_IF="$(sys_get_modem_interface "$DEV")"
    [ ! -z "$ARG_IF" ] && [ "$ARG_IF" != "$DEV_IF" ] && continue

    # check USB address
    DEV_USB="$(sys_get_modem_usbaddr "$DEV")"
    [ ! -z "$ARG_USB" ] && [ "$ARG_USB" != "$DEV_USB" ] && continue

    # check PCI address
    DEV_PCI="$(sys_get_modem_pciaddr "$DEV")"
    [ ! -z "$ARG_PCI" ] && [ "$ARG_PCI" != "$DEV_PCI" ] && continue

    PROTOCOL="$DEV_PROT"
    IFACE="$DEV_IF"
    USB_ADDR="$DEV_USB"
    PCI_ADDR="$DEV_PCI"
    CDC_DEV="$(basename "${DEV}")"
    return 0
  done

  echo "Failed to find modem for "\
    "interface=${ARG_IF:-<ANY>}, USB=${ARG_USB:-<ANY>}, PCI=${ARG_PCI:-<ANY>}" >&2
  return 1
}

bringup_iface() {
  if [ "$PROTOCOL" = mbim ]; then
     local JSON=$(mbim --query-ip-configuration)
     local DNS0="dns0"
     local DNS1="dns1"
  else
     local JSON=$(qmi --get-current-settings)
     local DNS0="dns1"
     local DNS1="dns2"
  fi
  ifconfig $IFACE `echo "$JSON" | jq -r .ipv4.ip` \
                   netmask `echo "$JSON" | jq -r .ipv4.subnet` \
                   pointopoint `echo "$JSON" | jq -r .ipv4.gateway`
  # NOTE we may want to disable /proc/sys/net/ipv4/conf/default/rp_filter instead
  #      Verify it by cat /proc/net/netstat | awk '{print $80}'
  ip route add default via `echo "$JSON" | jq -r .ipv4.gateway` dev $IFACE metric 65000
  mkdir $BBS/${NET}/resolv.conf || :
  cat > $BBS/${NET}/resolv.conf/${IFACE}.dhcp <<__EOT__
nameserver `echo "$JSON" | jq -r .ipv4.$DNS0`
nameserver `echo "$JSON" | jq -r .ipv4.$DNS1`
__EOT__
}

probe() {
  # ping is supposed to return 0 even if just a single packet out of 3 gets through
  local PROBE_OUTPUT
  PROBE_OUTPUT="$(ping -W 20 -w 20 -c 3 -I $IFACE $PROBE_ADDR 2>&1)"
  if [ $? -eq 0 ]; then
    unset PROBE_ERROR
    return 0
  else
    PROBE_ERROR="$(printf "%s" "$PROBE_OUTPUT" | grep "packet loss")"
    if [ -z "$PROBE_ERROR" ]; then
      PROBE_ERROR="$PROBE_OUTPUT"
    fi
    PROBE_ERROR="Failed to ping $PROBE_ADDR via $IFACE: $PROBE_ERROR"
    return 1
  fi
}

collect_modem_status() {
  local MODEM="$(json_struct \
    "$(json_str_attr model    "$(${PROTOCOL}_get_modem_model)")" \
    "$(json_str_attr revision "$(${PROTOCOL}_get_modem_revision)")")"
  local MODEM_STATUS="$(json_struct \
    "$(json_str_attr device-name      "$DEVICE_NAME")" \
    "$(json_attr     physical-addrs   "$ADDRS")" \
    "$(json_str_attr control-protocol "$PROTOCOL")" \
    "$(json_str_attr operating-mode   "$(${PROTOCOL}_get_op_mode)")" \
    "$(json_str_attr imei             "$(${PROTOCOL}_get_imei)")" \
    "$(json_attr     modem            "$MODEM")" \
    "$(json_str_attr config-error     "$CONFIG_ERROR")" \
    "$(json_str_attr probe-error      "$PROBE_ERROR")" \
    "$(json_attr     providers        "$(${PROTOCOL}_get_providers)")")"
  STATUS="${STATUS}${MODEM_STATUS}\n"
}

collect_modem_metrics() {
  local MODEM_METRICS="$(json_struct \
    "$(json_str_attr device-name    "$DEVICE_NAME")" \
    "$(json_attr     physical-addrs "$ADDRS")" \
    "$(json_attr     packet-stats   "$(${PROTOCOL}_get_packet_stats)")" \
    "$(json_attr     signal-info    "$(${PROTOCOL}_get_signal_info)")")"
  METRICS="${METRICS}${MODEM_METRICS}\n"
}

event_stream() {
  inotifywait -qm ${BBS} -e create -e modify -e delete &
  while true; do
    echo "PROBE"
    sleep $PROBE_INTERVAL
  done &
  while true; do
    echo "METRICS"
    sleep $METRICS_INTERVAL
  done
}

echo "Starting wwan manager"
mkdir -p ${BBS}
modprobe -a qcserial usb_wwan qmi_wwan cdc_wdm cdc_mbim cdc_acm

# Main event loop
event_stream | while read -r EVENT; do
  if ! echo "$EVENT" | grep -q "PROBE\|METRICS\|config.json"; then
    continue
  fi

  CONFIG_CHANGE=n
  if [ "$EVENT" != "PROBE" ] && [ "$EVENT" != "METRICS" ]; then
    CONFIG_CHANGE=y
  fi

  CONFIG="$(cat "${CONFIG_PATH}" 2>/dev/null)"
  if [ "$CONFIG_CHANGE" = "y" ]; then
    if [ "$LAST_CONFIG" = "$CONFIG" ]; then
      # spurious notification, ignore
      continue
    else
      LAST_CONFIG="$CONFIG"
    fi
  fi
  CHECKSUM="$(config_checksum "$CONFIG")"

  unset MODEMS
  unset STATUS
  unset METRICS
  AIRPLANE_MODE="$(parse_json_attr "$CONFIG" "\"airplane-mode\"")"

  # iterate over each configured cellular modem
  while read MODEM; do
    [ -z "$MODEM" ] && continue
    unset CONFIG_ERROR
    unset PROBE_ERROR

    # parse modem configuration
    DEVICE_NAME="$(parse_json_attr "$MODEM" "\"device-name\"")"
    ADDRS="$(parse_json_attr "$MODEM" "\"physical-addrs\"")"
    IFACE="$(parse_json_attr "$ADDRS" "interface")"
    USB_ADDR="$(parse_json_attr "$ADDRS" "usb")"
    PCI_ADDR="$(parse_json_attr "$ADDRS" "pci")"
    PROBE_ADDR="$(parse_json_attr "$MODEM" "\"probe-address\"")"
    PROBE_ADDR="${PROBE_ADDR:-$DEFAULT_PROBE_ADDR}"
    APN="$(parse_json_attr "$MODEM" "apns[0]")" # FIXME XXX limited to a single APN for now
    APN="${APN:-$DEFAULT_APN}"

    lookup_modem "${IFACE}" "${USB_ADDR}" "${PCI_ADDR}" 2>/tmp/wwan.stderr
    if [ $? -ne 0 ]; then
      CONFIG_ERROR="$(cat /tmp/wwan.stderr)"
      MODEM_STATUS="$(json_struct \
        "$(json_str_attr device-name    "$DEVICE_NAME")" \
        "$(json_attr     physical-addrs "$ADDRS")" \
        "$(json_str_attr config-error   "$CONFIG_ERROR")")"
      STATUS="${STATUS}${MODEM_STATUS}\n"
      continue
    fi
    MODEMS="${MODEMS}${CDC_DEV}\n"
    echo "Processing managed modem (event: $EVENT): $CDC_DEV"

    # in status.json and metrics.json print all modem addresses (as found by lookup_modem),
    # not just the ones used in config.json
    ADDRS="$(json_struct \
      "$(json_str_attr interface "$IFACE")" \
      "$(json_str_attr usb       "$USB_ADDR")" \
      "$(json_str_attr pci       "$PCI_ADDR")")"

    if [ "$EVENT" = "METRICS" ]; then
      collect_modem_metrics 2>/dev/null
      continue
    fi

    # reflect updated config or just probe the current status
    if [ "$(intended_modem_op_mode)" != "radio-off" ]; then
      if [ "$CONFIG_CHANGE" = "y" ] || ! probe; then
        echo "[$CDC_DEV] Restarting connection (APN=${APN}, interface=${IFACE})"
        {
          ${PROTOCOL}_reset_modem       &&\
          ${PROTOCOL}_toggle_rf         &&\
          ${PROTOCOL}_wait_for_sim      &&\
          ${PROTOCOL}_wait_for_register &&\
          ${PROTOCOL}_start_network     &&\
          ${PROTOCOL}_wait_for_wds      &&\
          ${PROTOCOL}_wait_for_settings &&\
          bringup_iface                 &&\
          echo "[$CDC_DEV] Connection successfully restarted"
        } 2>/tmp/wwan.stderr
        RV=$?
        if [ $RV -ne 0 ]; then
          CONFIG_ERROR="$(cat /tmp/wwan.stderr | sort -u)"
          CONFIG_ERROR="${CONFIG_ERROR:-(Re)Connection attempt failed with rv=$RV}"
        fi
        # retry probe to update PROBE_ERROR
        sleep 3
        probe
      fi
    else # Airplane mode is ON
      if [ "$(${PROTOCOL}_get_op_mode)" != "radio-off" ]; then
        echo "[$CDC_DEV] Trying to disable radio (APN=${APN}, interface=${IFACE})"
        ${PROTOCOL}_toggle_rf 2>/tmp/wwan.stderr
        if [ $? -ne 0 ]; then
          CONFIG_ERROR="$(cat /tmp/wwan.stderr)"
        fi
      fi
    fi

    collect_modem_status
  done <<__EOT__
  $(echo "$CONFIG" | jq -c '.modems[]')
__EOT__

  # manage RF state also for modems not configured by the controller
  for DEV in /sys/class/usbmisc/*; do
    unset CONFIG_ERROR
    unset PROBE_ERROR
    unset DEVICE_NAME # unmanaged modems do not have logical name

    PROTOCOL=$(sys_get_modem_protocol "$DEV") || continue
    CDC_DEV="$(basename "${DEV}")"
    if printf "%b" "$MODEMS" | grep -q "^$CDC_DEV$"; then
      # this modem has configuration and was already processed
      continue
    fi
    echo "Processing unmanaged modem (event: $EVENT): $CDC_DEV"
    IFACE=$(sys_get_modem_interface "$DEV")
    USB_ADDR=$(sys_get_modem_usbaddr "$DEV")
    PCI_ADDR=$(sys_get_modem_pciaddr "$DEV")
    ADDRS="$(json_struct \
        "$(json_str_attr interface "$IFACE")" \
        "$(json_str_attr usb       "$USB_ADDR")" \
        "$(json_str_attr pci       "$PCI_ADDR")")"

    if [ "$EVENT" = "METRICS" ]; then
      collect_modem_metrics 2>/dev/null
      continue
    fi

    if [ "$(intended_modem_op_mode)" != "$(${PROTOCOL}_get_op_mode)" ]; then
      action="enable"
      [ "$(intended_modem_op_mode)" = "radio-off" ] && action="disable"
      echo "[$CDC_DEV] Trying to $action radio (interface=${IFACE})"
      ${PROTOCOL}_toggle_rf 2>/tmp/wwan.stderr
      if [ $? -ne 0 ]; then
        CONFIG_ERROR="$(cat /tmp/wwan.stderr)"
      fi
    fi

    collect_modem_status
  done

  if [ "$EVENT" = "METRICS" ]; then
    json_struct \
      "$(json_attr modems "$(printf "%b" "$METRICS" | json_array)")" \
        | jq > "$METRICS_PATH"
  else
    json_struct \
      "$(json_attr     modems          "$(printf "%b" "$STATUS" | json_array)")" \
      "$(json_str_attr config-checksum "$CHECKSUM")" \
        | jq > "$STATUS_PATH"
  fi
done