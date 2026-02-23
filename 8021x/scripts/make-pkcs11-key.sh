#!/bin/bash
set -e

mkdir -p /etc/tpm2_pkcs11

# tpm2_ptool source code is at: https://github.com/tpm2-software/tpm2-pkcs11/tree/master/tools/tpm2_pkcs11
tpm2_ptool init
tpm2_ptool addtoken --pid=1 --sopin=1234 --userpin=1234 --label=pnac
tpm2_ptool addkey --label=pnac --userpin=1234 --algorithm=rsa2048 --key-label=pnac_client

# Show DB content with:
# sqlite3 /etc/tpm2_pkcs11/tpm2_pkcs11.sqlite3 .dump

yaml_rsa0=$(tpm2_ptool export --label pnac --key-label pnac_client --userpin 1234)
auth_rsa0=$(echo "$yaml_rsa0" | grep "object-auth" | cut -d' ' -f2-)

SUBJ="/C=US/L=Somewhere/O=Example Inc./CN=testing/emailAddress=testing@test.com"

openssl req \
    -new \
    -provider tpm2 \
    -provider base \
    -key pnac_client.pem \
    -passin "pass:$auth_rsa0" \
    -subj "${SUBJ}" \
    -out pnac_client.csr

openssl x509 -req \
    -in pnac_client.csr \
    -CA /etc/certs/pnac-ca.pem \
    -CAkey /etc/certs/pnac-ca.key \
    -CAcreateserial \
    -out /etc/certs/pnac_client.crt \
    -days 365 \
    -sha256