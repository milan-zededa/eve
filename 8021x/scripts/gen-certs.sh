#!/bin/sh
set -e

CERT_DIR="certs"
ECDSA_CURVE="prime256v1"

rm -rf "${CERT_DIR}"
mkdir -p "${CERT_DIR}"
cd "${CERT_DIR}"

###############################################################################
# Key generation helpers
###############################################################################

gen_key() {
    alg="$1"
    keyfile="$2"

    case "${alg}" in
        RSA)
            openssl genrsa -traditional -out "${keyfile}" 2048
            ;;
        ECDSA)
            openssl ecparam -name "${ECDSA_CURVE}" -genkey -noout -out "${keyfile}"
            ;;
        *)
            echo "Unsupported key algorithm: ${alg}"
            exit 1
            ;;
    esac
}

###############################################################################
# Certificate generators
###############################################################################

gen_ca() {
    prefix="$1"
    alg="$2"

    echo "🔧 Generating ${prefix} Root CA (${alg})..."

    gen_key "${alg}" "${prefix}-ca.key"

    openssl req -x509 -new \
        -key "${prefix}-ca.key" \
        -days 3650 \
        -out "${prefix}-ca.pem" \
        -subj "/CN=${prefix}-Test CA/OU=Lab/O=Example/C=US"
}

gen_client_cert() {
    prefix="$1"
    alg="$2"
    cn="$3"

    echo "🔧 Generating ${prefix} client certificate (${alg})..."

    gen_key "${alg}" "${prefix}-client.key"

    openssl req -new \
        -key "${prefix}-client.key" \
        -out "${prefix}-client.csr" \
        -subj "/CN=${cn}/OU=Lab/O=Example/C=US"

    openssl x509 -req \
        -in "${prefix}-client.csr" \
        -CA "${prefix}-ca.pem" \
        -CAkey "${prefix}-ca.key" \
        -CAcreateserial \
        -out "${prefix}-client.pem" \
        -days 365
}

gen_server_cert() {
    prefix="$1"
    alg="$2"
    cn="$3"
    san="$4"

    echo "🔧 Generating ${prefix} server certificate (${alg})..."

    echo "subjectAltName=${san}" > "${prefix}-san.cnf"

    gen_key "${alg}" "${prefix}-server.key"

    openssl req -new \
        -key "${prefix}-server.key" \
        -out "${prefix}-server.csr" \
        -subj "/CN=${cn}/OU=Lab/O=Example/C=US"

    openssl x509 -req \
        -in "${prefix}-server.csr" \
        -CA "${prefix}-ca.pem" \
        -CAkey "${prefix}-ca.key" \
        -CAcreateserial \
        -out "${prefix}-server.pem" \
        -days 365 \
        -extfile "${prefix}-san.cnf"

    rm -f "${prefix}-san.cnf"
}

cleanup() {
    rm -f *.csr *.srl
}

###############################################################################
# PNAC (802.1X) — RSA
###############################################################################

PNAC_PREFIX="pnac"
PNAC_ALG="RSA"

gen_ca "${PNAC_PREFIX}" "${PNAC_ALG}"

# TODO: Temporary bootstrap cert
gen_client_cert "${PNAC_PREFIX}" "${PNAC_ALG}" "supplicant.lab.local"

gen_server_cert "${PNAC_PREFIX}" "${PNAC_ALG}" \
    "authenticator.lab.local" \
    "DNS:authenticator.lab.local,IP:192.168.50.1"

###############################################################################
# SCEP Proxy (client + server) — ECDSA
###############################################################################

PROXY_PREFIX="proxy"
PROXY_ALG="ECDSA"

gen_ca "${PROXY_PREFIX}" "${PROXY_ALG}"

gen_client_cert "${PROXY_PREFIX}" "${PROXY_ALG}" "proxy-client.lab.local"

gen_server_cert "${PROXY_PREFIX}" "${PROXY_ALG}" \
    "proxy-server.lab.local" \
    "DNS:proxy-server.lab.local,IP:192.168.60.2"

###############################################################################
# HTTPS endpoint for SCEP proxy — RSA
###############################################################################

TLS_PREFIX="proxy-tls"
TLS_ALG="RSA"

gen_ca "${TLS_PREFIX}" "${TLS_ALG}"

gen_server_cert "${TLS_PREFIX}" "${TLS_ALG}" \
    "proxy-server-tls.lab.local" \
    "DNS:proxy-server-tls.lab.local,IP:192.168.60.2"

###############################################################################

cleanup

echo "✅ Certificates generated in ./${CERT_DIR}"
echo "   - RSA keys: PKCS#1 (BEGIN RSA PRIVATE KEY)"
echo "   - ECDSA keys: SEC1 (BEGIN EC PRIVATE KEY)"
