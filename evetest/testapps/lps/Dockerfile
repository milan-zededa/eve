FROM golang:1.24 AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /lps ./cmd/lps

FROM ubuntu:24.04

RUN apt-get update && \
    apt-get install -y openssh-server curl && \
    rm -rf /var/lib/apt/lists/* && \
    mkdir /run/sshd

RUN echo 'root:testpassword' | chpasswd
RUN sed -i 's/#PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config && \
    sed -i 's/#PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config
EXPOSE 22 8888

COPY --from=build /lps /usr/local/bin/lps
COPY init.sh /

CMD ["/bin/bash", "/init.sh"]
