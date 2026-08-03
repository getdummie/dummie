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
    file \
    e2fsprogs \
    qemu-system-x86 \
    qemu-utils \
    neovim \
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

RUN set -ex \
  && apt update \
  && apt install -y \
    ca-certificates \
    curl \
  && install -m 0755 -d /etc/apt/keyrings \
  && curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc \
  && chmod a+r /etc/apt/keyrings/docker.asc \
  && printf 'Types: deb\nURIs: https://download.docker.com/linux/debian\nSuites: %s\nComponents: stable\nArchitectures: %s\nSigned-By: /etc/apt/keyrings/docker.asc\n' \
    "$(. /etc/os-release && echo "${VERSION_CODENAME}")" \
    "$(dpkg --print-architecture)" \
    > /etc/apt/sources.list.d/docker.sources \
  && apt update \
  && apt install -y \
    docker-ce \
    docker-ce-cli \
    containerd.io \
    docker-buildx-plugin \
    docker-compose-plugin \
  && apt clean \
  && systemctl enable docker \
  && adduser ubuntu docker

USER ubuntu
RUN set -ex \
  && mkdir -p /home/ubuntu/.ssh/ \
  && curl -sL https://github.com/codingcoffee.keys > /home/ubuntu/.ssh/authorized_keys \
  && echo 'alias d=docker' >> /home/ubuntu/.bashrc \
  && echo 'alias dco="docker compose"' >> /home/ubuntu/.bashrc \
  && echo 'alias v=neovim' >> /home/ubuntu/.bashrc
