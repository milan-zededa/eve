#!/bin/sh

rm -rf /run/*
mkdir -p /run/sshd
mkdir -p /run/nginx

nginx

/usr/sbin/sshd -h /root/.ssh/id_rsa

echo "Started evefd"

# Running shell as the entrypoint allows to enter the container using
# `eve attach-app-console <console-id>/cons` and have interactive session.
# This is useful when a deployed container cannot be accessed via ssh over the network.
while true; do /bin/sh; done
