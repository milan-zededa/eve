FROM ubuntu:24.04

RUN apt-get update && \
    apt-get install -y openssh-server dnsutils tcpdump curl iproute2 net-tools iputils-ping && \
    rm -rf /var/lib/apt/lists/* && \
    mkdir /run/sshd

RUN echo 'root:testpassword' | chpasswd
RUN sed -i 's/#PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config && \
    sed -i 's/#PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config
EXPOSE 22

COPY init.sh /
CMD ["/bin/bash", "/init.sh"]
