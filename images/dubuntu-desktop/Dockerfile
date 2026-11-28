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
    dbus-user-session \
    gnome-session \
    gnome-shell \
    gnome-terminal \
    gnome-remote-desktop \
    pipewire \
    pipewire-pulse \
    wireplumber \
    fuse3 \
    libgl1-mesa-dri \
    openssl \
    neovim \
    gnupg \
    ca-certificates \
    fonts-noto-color-emoji \
    fonts-noto-core \
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

RUN set -ex \
  && systemctl enable ssh \
  && systemctl enable systemd-resolved \
  && systemctl enable systemd-networkd \
  && systemctl set-default multi-user.target \
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
    gnome-remote-desktop.service \
  && (id ubuntu || adduser --disabled-password --gecos "" ubuntu) \
  && echo 'ubuntu:ubuntu' | chpasswd \
  && adduser ubuntu sudo \
  && echo 'ubuntu ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/ubuntu \
  && chmod 0440 /etc/sudoers.d/ubuntu \
  && install -d -m 0755 /var/lib/systemd/linger \
  && touch /var/lib/systemd/linger/ubuntu

RUN set -ex \
  && printf 'LANG=C.UTF-8\nLC_ALL=C.UTF-8\n' > /etc/default/locale \
  && printf 'LANG=C.UTF-8\nLC_ALL=C.UTF-8\n' > /etc/locale.conf \
  && printf 'LANG=C.UTF-8\nLC_ALL=C.UTF-8\n' >> /etc/environment \
  && sed -i 's/^#\?AcceptEnv LANG LC_\*/#AcceptEnv LANG LC_*/' /etc/ssh/sshd_config

# gnome-remote-desktop runs in user mode, headless, under the lingering ubuntu
# user's systemd: no login screen and no handover redirect, which the in-browser
# client cannot follow.
COPY virtual-sink.conf /etc/pipewire/pipewire.conf.d/virtual-sink.conf

RUN cat > /usr/local/bin/grd-setup.sh <<'EOF'
#!/bin/sh
set -eu

dir="$HOME/.local/share/gnome-remote-desktop"
mkdir -p "$dir"
if [ ! -f "$dir/tls.crt" ]; then
  openssl req -x509 -newkey rsa:4096 -nodes -days 3650 -subj "/CN=$(hostname)" \
    -keyout "$dir/tls.key" -out "$dir/tls.crt" 2>/dev/null
  chmod 0600 "$dir/tls.key"
fi

g="grdctl --headless"
$g rdp set-tls-cert "$dir/tls.crt"
$g rdp set-tls-key "$dir/tls.key"
$g rdp set-credentials ubuntu ubuntu
$g rdp disable-view-only
$g rdp set-port 3389
# dpipe always dials :3389, so a daemon that quietly moved to another port would
# just look like a dead guest.
$g rdp disable-port-negotiation
$g rdp enable
EOF

RUN chmod 0755 /usr/local/bin/grd-setup.sh

RUN cat > /etc/systemd/user/dummie-gnome-shell.service <<'EOF'
[Unit]
Description=Headless GNOME Shell
Requires=pipewire.service wireplumber.service
After=pipewire.service wireplumber.service

[Service]
Type=dbus
BusName=org.gnome.Mutter.RemoteDesktop
ExecStart=/usr/bin/gnome-shell --headless
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
EOF

RUN cat > /etc/systemd/user/dummie-remote-desktop.service <<'EOF'
[Unit]
Description=GNOME Remote Desktop (headless, user mode)
Requires=dummie-gnome-shell.service
After=dummie-gnome-shell.service
PartOf=dummie-gnome-shell.service

[Service]
ExecStartPre=/usr/local/bin/grd-setup.sh
ExecStart=/usr/libexec/gnome-remote-desktop-daemon --headless
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
EOF

RUN systemctl --global enable \
    pipewire.socket \
    pipewire-pulse.socket \
    wireplumber.service \
    dummie-gnome-shell.service \
    dummie-remote-desktop.service

USER ubuntu
