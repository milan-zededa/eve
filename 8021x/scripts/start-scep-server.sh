#!/bin/bash
set -e

# Start SCEP server
echo "Starting SCEP server on port 2016"
scepserver -depot /etc/certs -port 2016 -challenge=secret -allowrenew 0 &
tail -f /dev/null