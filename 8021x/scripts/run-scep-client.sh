#!/bin/sh
set -e

WITH_PROXY=0

# Parse arguments
for arg in "$@"; do
    case "$arg" in
        --with-proxy)
            WITH_PROXY=1
            ;;
        *)
            echo "Unknown argument: $arg" >&2
            exit 1
            ;;
    esac
done

# Common options
SCEP_OPTS="
  -debug
  -server-url http://192.168.70.2:2016/scep
  -private-key /etc/certs/pnac-client.key
  -certificate /etc/certs/pnac-client.pem
  -challenge=secret
"

if [ "$WITH_PROXY" -eq 1 ]; then
    echo "🔀 Using SCEP proxy"

    SCEP_OPTS="$SCEP_OPTS
      --proxy-url https://192.168.60.2/proxy/scep
      --proxy-client-cert /etc/certs/proxy-client.pem
      --proxy-client-key /etc/certs/proxy-client.key
      --proxy-server-certificate /etc/certs/proxy-server.pem
      --proxy-tls-ca-certificate /etc/certs/proxy-tls-ca.pem
    "
else
    echo "➡️  Using direct SCEP server"
fi

# Run SCEP client
exec scep-client $SCEP_OPTS