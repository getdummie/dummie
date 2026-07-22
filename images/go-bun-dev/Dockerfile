FROM golang:1.26.5-trixie

RUN set -ex \
  && apt update \
  && apt install -y \
    sudo \
    openssh-server \
  && echo 'root:root' | chpasswd \
  && apt clean \
  && systemctl enable ssh \
  && mkdir -p /root/.ssh \
  && curl -sL https://github.com/codingcoffee.keys > /root/.ssh/authorized_keys \
  && adduser --disabled-password --gecos "" ubuntu \
  && adduser ubuntu sudo \
  && echo 'ubuntu ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/ubuntu \
  && chmod 0440 /etc/sudoers.d/ubuntu

WORKDIR /home/ubuntu

USER ubuntu
RUN set -ex \
  && mkdir -p /home/ubuntu/.ssh/ \
  && curl -sL https://github.com/codingcoffee.keys > /home/ubuntu/.ssh/authorized_keys \
  && sudo apt update \
  && sudo apt install -y \
    git \
    curl \
    bash \
    unzip \
    neovim \
  && go install "github.com/air-verse/air@v1.67.1" \
  && go install "github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1" \
  && go install "github.com/swaggo/swag/cmd/swag@latest" \
  && curl -fsSL https://bun.com/install | bash -s "bun-v1.3.14" \
  && echo 'export PATH="/go/bin:/usr/local/go/bin:$PATH"' >> /home/ubuntu/.bashrc
