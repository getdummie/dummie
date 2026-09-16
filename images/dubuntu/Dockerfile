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
    tmux \
    git \
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
ENV LANG=C.UTF-8
ENV TERM=xterm-256color

USER root
ARG NODE_VERSION=26.8.1
RUN set -e \
  && apt update \
  && apt-get install -y --no-install-recommends \
      ca-certificates \
      libatomic1 \
      curl \
      xz-utils \
  && curl -fsSLO "https://nodejs.org/dist/v$NODE_VERSION/node-v$NODE_VERSION-linux-x64.tar.xz" \
  && tar -xJf "node-v$NODE_VERSION-linux-x64.tar.xz" -C /usr/local --strip-components=1 --no-same-owner \
  && rm "node-v$NODE_VERSION-linux-x64.tar.xz"

ENV PLAYWRIGHT_BROWSERS_PATH=/opt/playwright-browsers
RUN set -e \
  && npm install -g playwright \
  && playwright install --with-deps chromium \
  && chmod -R a+rX /opt/playwright-browsers

RUN set -e \
  && apt-get install -y --no-install-recommends \
    fd-find \
    ripgrep \
  && npm install -g --ignore-scripts \
    "@earendil-works/pi-coding-agent"

USER ubuntu
RUN set -ex \
  mkdir -p /home/ubuntu/app /home/ubuntu/.pi/agent
COPY <<'EOF' /home/ubuntu/.pi/agent/APPEND_SYSTEM.md
Never add comments unless asked. Code is the source of truth.
Network access is restricted. If you need a domain or IP whitelisted, ask the user with a short reason.
EOF
WORKDIR /home/ubuntu/app
