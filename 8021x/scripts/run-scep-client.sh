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
  -private-key /etc/certs/pnac-client.key
  -certificate /etc/certs/pnac-client.pem
  -challenge=secret
"

if [ "$WITH_PROXY" -eq 1 ]; then
    echo "🔀 Using SCEP proxy"

    # TODO: specify trusted TLS cert also
    SCEP_OPTS="$SCEP_OPTS
      --proxy-addr https://192.168.60.2/proxy/scep
      --proxy-client-key /etc/certs/proxy-client.key
      --proxy-server-certificate /etc/certs/proxy-server.pem
      --proxy-tls-ca-certificate /etc/certs/tls-ca.pem
    "
else
    echo "➡️  Using direct SCEP server"

    SCEP_OPTS="$SCEP_OPTS
      -server-url http://192.168.70.2:2016/scep
    "
fi

# Run SCEP client
exec scep-client $SCEP_OPTS