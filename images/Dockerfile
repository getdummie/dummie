FROM debian:stable

ENV DEBIAN_FRONTEND=noninteractive

RUN set -ex \
  && apt update \
  && apt install -y \
    systemd \
    systemd-sysv \
    systemd-resolved \
    sudo \
    curl \
    dnsutils \
    tcpdump \
    iputils-ping \
    telnet \
    mtr \
    iproute2 \
    openssh-server \
    ethtool \
    python3 \
    htop \
    btop \
  && echo 'root:root' | chpasswd \
  && apt clean \
  && systemctl enable ssh \
  && systemctl enable systemd-resolved \
  && mkdir -p /root/.ssh \
  && curl -sL https://github.com/codingcoffee.keys > /root/.ssh/authorized_keys \
  && adduser --disabled-password --gecos "" ubuntu \
  && adduser ubuntu sudo \
  && echo 'ubuntu ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/ubuntu \
  && chmod 0440 /etc/sudoers.d/ubuntu

USER ubuntu
RUN set -ex \
  && mkdir -p /home/ubuntu/.ssh/ \
  && curl -sL https://github.com/codingcoffee.keys > /home/ubuntu/.ssh/authorized_keys
