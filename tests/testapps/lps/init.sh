#!/bin/bash

/usr/sbin/sshd
/usr/local/bin/lps &
exec /bin/bash
