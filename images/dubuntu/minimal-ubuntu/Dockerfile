FROM ubuntu:26.04

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
    rsync \
    lsof \
  && apt clean \
  && systemctl enable ssh \
  && systemctl enable systemd-resolved \
  && systemctl enable systemd-networkd \
  && systemctl enable systemd-udevd \
  && adduser ubuntu sudo \
  && echo 'ubuntu ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/ubuntu \
  && chmod 0440 /etc/sudoers.d/ubuntu

ARG GO_VERSION=1.27.1
RUN set -ex \
  && ARCH="$(dpkg --print-architecture)" \
  && curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz" -o /tmp/go.tar.gz \
  && tar -C /usr/local -xzf /tmp/go.tar.gz \
  && rm /tmp/go.tar.gz

ENV PATH=/usr/local/go/bin:$PATH

USER ubuntu
RUN set -ex \
  mkdir -p /home/ubuntu/app /home/ubuntu/.pi/agent
COPY <<'EOF' /home/ubuntu/app/.pi/agent/APPEND_SYSTEM.md
Never add comments unless asked. Code is the source of truth.
Network access is restricted. If you need a domain or IP whitelisted, ask the user with a short reason.
EOF
WORKDIR /home/ubuntu/app
