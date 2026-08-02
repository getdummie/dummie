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
    neovim \
    udev \
  && echo 'root:root' | chpasswd \
  && apt clean \
  && systemctl enable ssh \
  && systemctl enable systemd-resolved \
  && systemctl enable systemd-networkd \
  && systemctl enable systemd-udevd \
  && mkdir -p /root/.ssh \
  && curl -sL https://github.com/codingcoffee.keys > /root/.ssh/authorized_keys \
  && adduser --disabled-password --gecos "" ubuntu \
  && adduser ubuntu sudo \
  && echo 'ubuntu ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/ubuntu \
  && chmod 0440 /etc/sudoers.d/ubuntu

RUN cat > /etc/systemd/network/10-eth0.network <<'EOF'
[Match]
Name=eth0

[Network]
DHCP=ipv4
EOF

USER ubuntu
RUN set -ex \
  && mkdir -p /home/ubuntu/.ssh/ \
  && curl -sL https://github.com/codingcoffee.keys > /home/ubuntu/.ssh/authorized_keys
