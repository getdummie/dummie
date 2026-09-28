ARG BUN_VERSION=latest

FROM docker.io/chromedp/headless-shell:stable AS chrome

FROM docker.io/oven/bun:${BUN_VERSION} AS bun

FROM ubuntu:26.04

ENV DEBIAN_FRONTEND=noninteractive

RUN set -ex \
  && apt update \
  && apt -y \
    -o Dpkg::Options::=--force-confold \
    -o Dpkg::Options::=--force-confdef \
    dist-upgrade \
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
    file \
    xsel \
    jq \
    unzip \
    bc \
    oathtool \
  && apt clean \
  && systemctl enable ssh \
  && systemctl enable systemd-resolved \
  && systemctl enable systemd-networkd \
  && systemctl enable systemd-udevd \
  && systemctl mask -- \
    snapd.service \
    snapd.socket \
    snapd.seeded.service \
    snapd.snap-repair.timer \
    snapd.snap-repair.service \
    unattended-upgrades.service \
    apt-daily.timer \
    apt-daily-upgrade.timer \
    motd-news.service \
    motd-news.timer \
    update-notifier-download.timer \
    update-notifier-motd.timer \
    fwupd.service \
    fwupd-refresh.service \
    fwupd-refresh.timer \
    plymouth.service \
    plymouth-start.service \
    plymouth-quit.service \
    plymouth-quit-wait.service \
    plymouth-read-write.service \
    man-db.timer \
    dpkg-db-backup.timer \
    e2scrub_all.timer \
    e2scrub_reap.service \
    ubuntu-advantage.service \
    ua-reboot-cmds.service \
    apport.service \
    apport-autoreport.timer \
    ufw.service \
    systemd-networkd-wait-online.service \
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

# ENV only applies to docker exec; the VM boots systemd, so login shells need these on disk.
RUN set -ex \
  && printf 'LANG=C.UTF-8\nLC_ALL=C.UTF-8\n' > /etc/default/locale \
  && printf 'LANG=C.UTF-8\nLC_ALL=C.UTF-8\n' > /etc/locale.conf \
  && printf 'LANG=C.UTF-8\nLC_ALL=C.UTF-8\n' >> /etc/environment \
  && sed -i 's/^#\?AcceptEnv LANG LC_\*/#AcceptEnv LANG LC_*/' /etc/ssh/sshd_config

USER root
COPY --from=bun /usr/local/bin/bun /usr/local/bin/bun
RUN set -ex \
  && apt-get install -y --no-install-recommends \
    ca-certificates \
    libatomic1 \
  && chmod 0755 /usr/local/bin/bun \
  && ln -s /usr/local/bin/bun /usr/local/bin/bunx \
  && ln -s /usr/local/bin/bun /usr/local/bin/node \
  && bun --version

COPY --from=chrome /headless-shell /headless-shell
ENV PATH=/headless-shell:$PATH

# headless-shell is dynamically linked; these are the libs it resolves at startup.
RUN set -e \
  && apt-get install -y --no-install-recommends \
    fonts-noto-color-emoji \
    fonts-symbola \
    fonts-noto-core \
    fonts-indic \
    fonts-liberation \
    libglib2.0-0 \
    libnss3 \
    libx11-6 \
    libxcomposite1 \
    libxdamage1 \
    libxext6 \
    libxi6 \
    libxrandr2 \
    libxkbcommon0 \
    libgbm1 \
    libasound2t64 \
    libpango-1.0-0 \
    libcairo2 \
  && fc-cache -f \
  && for n in chrome chromium chromium-browser google-chrome google-chrome-stable; do \
       ln -sf /headless-shell/headless-shell "/usr/local/bin/$n"; \
     done \
  && headless-shell --version

ENV CHROME_PATH=/headless-shell/headless-shell
ENV CHROME_BIN=/headless-shell/headless-shell
ENV PUPPETEER_EXECUTABLE_PATH=/headless-shell/headless-shell

COPY bun-as-npm /usr/local/bin/npm
ARG PI_VERSION=0.87.1
RUN set -e \
  && apt-get install -y --no-install-recommends \
    fd-find \
    ripgrep \
  && BUN_INSTALL=/usr/local bun install -g "@earendil-works/pi-coding-agent@${PI_VERSION}" \
  && chmod -R a+rX /usr/local/lib \
  && chmod 0755 /usr/local/bin/npm \
  && command -v pi \
  && pi --version

ARG CODEX_VERSION=0.158.0
RUN set -e \
  && BUN_INSTALL=/usr/local bun add --global "@openai/codex@${CODEX_VERSION}" \
  && command -v codex \
  && codex --version

# The internal proxy has a fixed guest-facing address. Its virtual host uses
# the fleet domain; the local development catalogue uses dummie.localhost.
ARG DUMMIE_LLM_TLD=dummie.localhost
ARG DUMMIE_LLM_PROXY_URL=http://10.64.255.254/v1
RUN set -e \
  && mkdir -p /home/ubuntu/.codex \
  && printf 'model = "chatgpt/gpt-6-sol"\nmodel_provider = "dummie"\n\n[features]\nplugins = false\n\n[model_providers.dummie]\nname = "Dummie LLM proxy"\nbase_url = "%s"\nwire_api = "responses"\nhttp_headers = { Host = "llm.int.%s" }\n' "$DUMMIE_LLM_PROXY_URL" "$DUMMIE_LLM_TLD" \
    > /home/ubuntu/.codex/config.toml \
  && chmod 0644 /home/ubuntu/.codex/config.toml

ARG DOCKER_SUITE=
RUN set -ex \
  && apt-get install -y --no-install-recommends ca-certificates curl \
  && install -m 0755 -d /etc/apt/keyrings \
  && curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc \
  && chmod a+r /etc/apt/keyrings/docker.asc \
  && SUITE="${DOCKER_SUITE:-$(. /etc/os-release && echo "$VERSION_CODENAME")}" \
  && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu ${SUITE} stable" \
    > /etc/apt/sources.list.d/docker.list \
  && apt-get update \
  && apt-get install -y --no-install-recommends \
    docker-ce \
    docker-ce-cli \
    containerd.io \
    docker-compose-plugin \
    iptables \
  && apt-get clean \
  && rm -rf /var/lib/apt/lists/* \
  && systemctl enable docker.service \
  && adduser ubuntu docker \
  && dockerd --version \
  && docker compose version

# ENV only reaches docker exec; booted as a VM, each of these is read by a different consumer.
ARG VM_PATH_PREFIX=/usr/local/go/bin:/headless-shell
RUN set -ex \
  && printf 'PATH=%s:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\n' "$VM_PATH_PREFIX" >> /etc/environment \
  && printf 'PATH=%s:$PATH\nexport PATH\n' "$VM_PATH_PREFIX" > /etc/profile.d/10-vm-path.sh \
  && chmod 0644 /etc/profile.d/10-vm-path.sh \
  && printf 'Defaults secure_path="%s:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"\n' "$VM_PATH_PREFIX" > /etc/sudoers.d/secure_path \
  && chmod 0440 /etc/sudoers.d/secure_path \
  && visudo -c \
  && mkdir -p /etc/systemd/system.conf.d \
  && printf '[Manager]\nDefaultEnvironment=PATH=%s:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\n' "$VM_PATH_PREFIX" \
    > /etc/systemd/system.conf.d/10-vm-path.conf

COPY pi-extension/index.ts /home/ubuntu/.pi/agent/extensions/dummie/index.ts
COPY pi-extension/settings.json /home/ubuntu/.pi/agent/settings.json
RUN set -e \
  && jq -n --arg baseUrl "$DUMMIE_LLM_PROXY_URL" --arg host "llm.int.$DUMMIE_LLM_TLD" \
    '{baseUrl: $baseUrl, host: $host}' \
    > /home/ubuntu/.pi/agent/extensions/dummie/config.json \
  && ln -s /usr/local/install/global/node_modules /home/ubuntu/.pi/agent/extensions/dummie/node_modules

RUN set -ex \
  && mkdir -p /home/ubuntu/app /home/ubuntu/.pi/agent
COPY <<'EOF' /home/ubuntu/.pi/agent/APPEND_SYSTEM.md
- Never add comments unless asked.
- Network access is restricted. If you need a domain or IP whitelisted, ask the user with a reason in 10 words.
- For accessing a browser you can access chromium over CDP
EOF
COPY <<'EOF' /home/ubuntu/.pi/agent/skills/web-development/SKILL.md
---
name: web-development
description: "A base skill for developing web apps. Use this skill when you are starting with a fresh web development project for guidance on the architecture."
---
- golang echo server for backed (which serves APIs namespaced with /api/v1/ ) listening on port 8000 and host 0.0.0.0
- vuejs for frontend
- golang backend initializes the vuejs server on port 3000 and anything not coming on /api/v1/ gets internally routed to vuejs and served
EOF
RUN chown -R ubuntu:ubuntu /home/ubuntu

USER ubuntu
WORKDIR /home/ubuntu/app
