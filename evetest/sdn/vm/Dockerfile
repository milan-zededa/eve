# syntax=docker/dockerfile:1.6.0

# Copyright (c) 2026 Zededa, Inc.
# SPDX-License-Identifier: Apache-2.0

# Input arguments
ARG ALPINE_VERSION=3.21
ARG GOLANG_VERSION=1.25

###########################
# Build stage
###########################
FROM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS builder

# Set environment
ENV GOBIN=/out/usr/local/bin
ENV GO111MODULE=on
WORKDIR /build

# Install build dependencies
# hadolint ignore=DL3018
RUN apk add --no-cache \
    make \
    gcc \
    musl-dev \
    pkgconfig \
    git

# Download dependencies first for better layer caching.
COPY go.mod go.sum ./
RUN go mod download

# Copy Go source code
COPY . .

# Run vet and fmt checks
RUN echo "Running go vet" && go vet ./...
RUN echo "Running go fmt" && \
    ERR=$(find . -name '*.go' -exec gofmt -e -l -s {} +) && \
    if [ -n "$ERR" ]; then echo "go fmt failed - ERR: $ERR"; exit 1; fi

# Install all main binaries directly into $GOBIN
RUN go install ./cmd/sdnagent && \
    go install ./cmd/httpsrv && \
    go install ./cmd/goproxy && \
    go install ./cmd/netbootsrv && \
    go install ./cmd/conntrack

ARG SCEP_SERVER_VERSION=2.3.0
RUN git clone --branch v2.3.0 --depth 1 https://github.com/micromdm/scep.git && \
    go install -C scep/cmd/scepserver .

###########################
# Runtime stage
###########################
FROM alpine:${ALPINE_VERSION}

# Install runtime dependencies and some tools useful for troubleshooting purposes
# hadolint ignore=DL3018
RUN apk add --no-cache \
    bash \
    hostapd \
    socat \
    openssl \
    dnsmasq \
    iptables \
    ip6tables \
    iproute2 \
    tcpdump \
    vim \
    ca-certificates \
    openssh-client \
    openssh-server \
    dhcpcd \
    ipset \
    radvd \
    ethtool \
    jq \
    strace \
    curl \
    vim

# Copy built Go binaries
COPY --from=builder /out/ /

# Configure SSH server
RUN mkdir -p /etc/ssh /root/.ssh && chmod 0700 /root/.ssh
COPY cert/ssh/sdn_rsa /root/.ssh/
COPY cert/ssh/sdn_rsa.pub /root/.ssh/authorized_keys
RUN chmod 600 /root/.ssh/sdn_rsa && chmod 644 /root/.ssh/authorized_keys
RUN sed -i 's/#*PubkeyAuthentication yes/PubkeyAuthentication yes/' /etc/ssh/sshd_config && \
    sed -i 's/#*PasswordAuthentication yes/PasswordAuthentication no/' /etc/ssh/sshd_config && \
    sed -i 's/AllowTcpForwarding no/AllowTcpForwarding yes/' /etc/ssh/sshd_config && \
    chown -R root:root /root/.ssh/

# Copy shell scripts
COPY scripts/*sh /bin/

WORKDIR /
SHELL ["/bin/sh", "-c"]

# Default command
CMD ["init.sh"]
