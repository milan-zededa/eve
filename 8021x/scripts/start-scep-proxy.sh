#!/bin/bash
set -e

IP="192.168.60.2"

echo "Waiting for IP ${IP} to be assigned to an interface..."
while ! ip addr show | grep -qw "${IP}"; do
  sleep 1
done
echo "IP ${IP} detected."

# Start SCEP proxy
echo "Starting SCEP proxy on https://${IP}/proxy/scep"

/usr/local/bin/scep-proxy \
  --listen ${IP}:443 \
  --tls-cert /etc/certs/proxy-tls-server.pem \
  --tls-key /etc/certs/proxy-tls-server.key \
  --tls-ca-cert=/etc/certs/proxy-tls-ca.pem \
  --client-cert /etc/certs/proxy-client.pem \
  --proxy-cert /etc/certs/proxy-server.pem \
  --proxy-key /etc/certs/proxy-server.key &


tail -f /dev/null