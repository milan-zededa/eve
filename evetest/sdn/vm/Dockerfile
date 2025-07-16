# syntax=docker/dockerfile:1.6.0

# Input arguments
ARG ALPINE_VERSION=3.21
ARG GOLANG_VERSION=1.24

###########################
# Build stage
###########################
FROM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS builder

# Set environment
ENV GOBIN=/out/usr/local/bin
ENV GO111MODULE=on
WORKDIR /build

# Install build dependencies
RUN apk add --no-cache \
    make \
    gcc \
    musl-dev \
    pkgconfig \
    git

# Copy Go source code and vendor directory
# I decided to vendor everything, at least until development is completed.
COPY . .

# Run vet and fmt checks
RUN echo "Running go vet" && go vet ./...
RUN echo "Running go fmt" && \
    ERR=$(gofmt -e -l -s $(find . -name '*.go' | grep -v /vendor/)) && \
    if [ -n "$ERR" ]; then echo "go fmt failed - ERR: $ERR"; exit 1; fi

# Install all main binaries directly into $GOBIN
ENV GOFLAGS="-mod=vendor"
RUN go install ./cmd/sdnagent && \
    go install ./cmd/httpsrv && \
    go install ./cmd/goproxy && \
    go install ./cmd/netbootsrv && \
    go install ./cmd/conntrack

ARG SCEP_SERVER_VERSION=2.3.0
ENV GOFLAGS=""
RUN git clone --branch v2.3.0 --depth 1 https://github.com/micromdm/scep.git && \
    cd scep/cmd/scepserver && \
    go install

###########################
# Runtime stage
###########################
FROM alpine:${ALPINE_VERSION}

# Install runtime dependencies and some tools usefull for troubleshooting purposes
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