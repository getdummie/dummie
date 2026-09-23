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
    gdm3 \
    gnome-session \
    gnome-shell \
    gnome-terminal \
    gnome-remote-desktop \
    pipewire \
    libgl1-mesa-dri \
    openssl \
    neovim \
    gnupg \
    ca-certificates \
  && apt clean \
  && rm -rf /var/lib/apt/lists/*

# Ubuntu's firefox deb is a stub that shells out to snap, so firefox comes from
# Mozilla's own apt repo instead, pinned above the stub.
RUN set -ex \
  && install -d -m 0755 /etc/apt/keyrings \
  && curl -fsSL https://packages.mozilla.org/apt/repo-signing-key.gpg \
       -o /etc/apt/keyrings/packages.mozilla.org.asc \
  && gpg -n -q --homedir "$(mktemp -d)" --import --import-options import-show \
       /etc/apt/keyrings/packages.mozilla.org.asc \
     | grep -q 35BAA0B33E9EB396F59CA838C0BA5CE6DC6315A3 \
  && echo "deb [signed-by=/etc/apt/keyrings/packages.mozilla.org.asc] https://packages.mozilla.org/apt mozilla main" \
       > /etc/apt/sources.list.d/mozilla.list \
  && printf 'Package: firefox*\nPin: origin packages.mozilla.org\nPin-Priority: 1000\n' \
       > /etc/apt/preferences.d/mozilla \
  && apt update \
  && apt install -y firefox \
  && apt policy firefox | grep -q packages.mozilla.org \
  && apt clean \
  && rm -rf /var/lib/apt/lists/*

RUN set -e \
  && apt-get install -y --no-install-recommends \
    fonts-noto-color-emoji \
    fonts-noto-core

RUN set -ex \
  && systemctl enable ssh \
  && systemctl enable systemd-resolved \
  && systemctl enable systemd-networkd \
  && systemctl set-default graphical.target \
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
  && (id ubuntu || adduser --disabled-password --gecos "" ubuntu) \
  && echo 'ubuntu:ubuntu' | chpasswd \
  && adduser ubuntu sudo \
  && echo 'ubuntu ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/ubuntu \
  && chmod 0440 /etc/sudoers.d/ubuntu

RUN set -ex \
  && printf 'LANG=C.UTF-8\nLC_ALL=C.UTF-8\n' > /etc/default/locale \
  && printf 'LANG=C.UTF-8\nLC_ALL=C.UTF-8\n' > /etc/locale.conf \
  && printf 'LANG=C.UTF-8\nLC_ALL=C.UTF-8\n' >> /etc/environment \
  && sed -i 's/^#\?AcceptEnv LANG LC_\*/#AcceptEnv LANG LC_*/' /etc/ssh/sshd_config

RUN set -ex \
  && install -d -m 0755 /etc/gnome-remote-desktop \
  && openssl req -x509 -newkey rsa:4096 -nodes -days 3650 \
       -subj "/CN=ubuntu-desktop" \
       -keyout /etc/gnome-remote-desktop/tls.key \
       -out /etc/gnome-remote-desktop/tls.crt

RUN cat > /usr/local/sbin/grd-config.sh <<'EOF'
#!/bin/sh
set -eu

chown gnome-remote-desktop:gnome-remote-desktop /etc/gnome-remote-desktop/tls.crt /etc/gnome-remote-desktop/tls.key
grdctl --system rdp set-tls-cert /etc/gnome-remote-desktop/tls.crt
grdctl --system rdp set-tls-key /etc/gnome-remote-desktop/tls.key
grdctl --system rdp set-credentials ubuntu ubuntu
grdctl --system rdp disable-view-only
grdctl --system rdp set-port 3389
# dproxy always dials :3389, so a daemon that quietly moved to another port would
# just look like a dead guest.
grdctl --system rdp disable-port-negotiation
grdctl --system rdp enable
systemctl enable gnome-remote-desktop.service

# Restart rather than start: the daemon must bind 3389 after the settings above.
systemctl restart gnome-remote-desktop.service
EOF

RUN chmod 0755 /usr/local/sbin/grd-config.sh

RUN cat > /etc/systemd/system/grd-config.service <<'EOF'
[Unit]
Description=Configure gnome-remote-desktop RDP (system mode)
After=gnome-remote-desktop.service systemd-user-sessions.service
Wants=gnome-remote-desktop.service

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/grd-config.sh
RemainAfterExit=yes

[Install]
WantedBy=graphical.target
EOF

RUN systemctl enable grd-config.service

USER ubuntu
