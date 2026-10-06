FROM ubuntu:24.04

RUN apt-get update \
    && apt-get install -y openssh-server \
    && mkdir -p /run/sshd \
    && useradd -m -s /bin/bash test \
    && echo 'test:test' | chpasswd \
    && rm -rf /var/lib/apt/lists/*

RUN printf '\nPasswordAuthentication yes\nPermitRootLogin no\n' >> /etc/ssh/sshd_config

EXPOSE 22

CMD ["/usr/sbin/sshd", "-D", "-e"]