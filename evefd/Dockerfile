FROM golang:1.23-alpine AS build

WORKDIR /go/src/evefd
COPY pkg .

RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/root/evefd
COPY pkg/static /out/root/static
COPY pkg/templates /out/root/templates

COPY cert/id_rsa* /out/root/.ssh/
COPY cert/id_rsa.pub /out/root/.ssh/authorized_keys

FROM alpine:3.16.2

# hadolint ignore=DL3018
RUN apk add --no-cache lshw \
    curl \
    nginx \
    iproute2 \
    netcat-openbsd \
    net-tools \
    openssh \
    jq \
    lsblk \
    tcpdump \
    iputils

COPY --from=build /out /
COPY entrypoint.sh /entrypoint.sh

SHELL ["/bin/ash", "-eo", "pipefail", "-c"]
RUN chown root:root /root/.ssh/ && \
    chmod go-w /root && \
    chmod 600 /root/.ssh/id_rsa* && \
    mkdir /var/run/sshd && \
    mkdir -p /var/www/html && \
    sed -i 's/#*PermitRootLogin prohibit-password/PermitRootLogin yes/g' /etc/ssh/sshd_config && \
    sed -i 's/#*PubkeyAuthentication yes/PubkeyAuthentication yes/g' /etc/ssh/sshd_config && \
    sed -i 's/#*PasswordAuthentication yes/PasswordAuthentication yes/g' /etc/ssh/sshd_config

EXPOSE 22
EXPOSE 80
CMD ["/entrypoint.sh"]
