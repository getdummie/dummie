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
    gnome-session \
    gnome-shell \
    gnome-terminal \
    gnome-remote-desktop \
    gnome-keyring \
    dbus-user-session \
    pipewire \
    dbus-x11 \
    libgl1-mesa-dri \
    openssl \
    neovim \
    firefox \
    chromium

RUN set -ex \
  && echo 'root:root' | chpasswd \
  && apt clean \
  && systemctl enable ssh \
  && systemctl enable systemd-resolved \
  && systemctl enable systemd-networkd \
  && systemctl set-default multi-user.target \
  && mkdir -p /root/.ssh \
  && curl -sL https://github.com/codingcoffee.keys > /root/.ssh/authorized_keys \
  && (id ubuntu || adduser --disabled-password --gecos "" ubuntu) \
  && echo 'ubuntu:ubuntu' | chpasswd \
  && adduser ubuntu sudo \
  && echo 'ubuntu ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/ubuntu \
  && chmod 0440 /etc/sudoers.d/ubuntu

# GNOME Remote Desktop over RDP (port 3389), user-session mode -- the difference
# that matters versus images/ubuntu-desktop.
#
# That image runs `grdctl --system` (remote login): GDM publishes its greeter over
# RDP, and once you authenticate the daemon hands the client to the real user
# session with a Server Redirection PDU. Only clients that advertise
# REDIRECTION_SUPPORTED in their GCC Cluster block can follow that, which rules out
# the in-browser client -- ironrdp-web never sends that block, so gnome-remote-desktop
# finishes licensing, finds it cannot hand the session over, and answers with
# Deactivate All plus an MCS Disconnect Provider Ultimatum.
#
# The user-session daemon shares one already-running session instead, so there is no greeter and
# no redirection, and both the browser and native clients land directly on the
# desktop. Nobody logs in locally on a VM with no display, so the session has to be
# manufactured: systemd lingering starts the user manager at boot, and gnome-shell
# runs headless against a virtual monitor.
RUN set -ex \
  && install -d -m 0755 /etc/gnome-remote-desktop-user \
  && openssl req -x509 -newkey rsa:4096 -nodes -days 3650 \
       -subj "/CN=ubuntu-desktop" \
       -keyout /etc/gnome-remote-desktop-user/tls.key \
       -out /etc/gnome-remote-desktop-user/tls.crt \
  && chown -R ubuntu:ubuntu /etc/gnome-remote-desktop-user \
  && chmod 0600 /etc/gnome-remote-desktop-user/tls.key \
  && chmod 0644 /etc/gnome-remote-desktop-user/tls.crt \
  && install -d -m 0755 /var/lib/systemd/linger \
  && touch /var/lib/systemd/linger/ubuntu

# The compositor. --headless gives mutter no seat to look for, and
# --virtual-monitor gives it a framebuffer to draw into, which is what
# gnome-remote-desktop then captures through PipeWire.
RUN cat > /etc/systemd/user/gnome-headless.service <<'EOF'
[Unit]
Description=Headless GNOME Shell on a virtual monitor
Before=gnome-remote-desktop.service

[Service]
Type=simple
Environment=XDG_SESSION_TYPE=wayland
ExecStart=/usr/bin/gnome-shell --headless --virtual-monitor 1920x1080
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
EOF

# grdctl talks to the session bus, so this cannot run at build time; it is a
# oneshot in the user manager instead, and is idempotent so a VM whose root is
# wiped every boot configures itself again.
RUN cat > /usr/local/sbin/grd-user-config.sh <<'EOF'
#!/bin/sh
set -eu

# User-mode credentials live in the login keyring rather than a file, and nothing
# ever types a password on this machine to unlock it. An empty-password keyring
# unlocks itself, which is the same bargain autologin makes.
install -d -m 0700 "$HOME/.local/share/keyrings"
if [ ! -f "$HOME/.local/share/keyrings/login.keyring" ]; then
  printf '\n' | gnome-keyring-daemon --unlock --components=secrets >/dev/null 2>&1 || true
fi

# The certificate goes in through gsettings rather than `grdctl rdp set-tls-cert`
# because grdctl validates the stored certificate on startup, whatever the
# subcommand: with no certificate configured yet it fails every command, the one
# that sets the certificate included.
gsettings set org.gnome.desktop.remote-desktop.rdp tls-cert '/etc/gnome-remote-desktop-user/tls.crt'
gsettings set org.gnome.desktop.remote-desktop.rdp tls-key '/etc/gnome-remote-desktop-user/tls.key'

# Bare grdctl is the user-session daemon; --system is the remote-login one this
# image exists to avoid, and there is no --user flag.
grdctl rdp set-credentials ubuntu ubuntu
grdctl rdp disable-view-only
grdctl rdp set-port 3389
# dproxy always dials :3389, so a daemon that quietly moved to another port would
# just look like a dead guest.
grdctl rdp disable-port-negotiation
grdctl rdp enable

# Restart rather than start: the daemon must bind 3389 after the settings above.
systemctl --user restart gnome-remote-desktop.service
EOF

RUN chmod 0755 /usr/local/sbin/grd-user-config.sh

RUN cat > /etc/systemd/user/grd-user-config.service <<'EOF'
[Unit]
Description=Configure gnome-remote-desktop RDP (user mode)
Requires=gnome-headless.service
After=gnome-headless.service

[Service]
Type=oneshot
# The compositor's wayland socket is the signal that the session is real; without
# it grdctl configures a session that cannot be captured.
ExecStartPre=/bin/sh -c 'for _ in $(seq 60); do [ -S "$XDG_RUNTIME_DIR/wayland-0" ] && exit 0; sleep 1; done; echo "no wayland socket after 60s" >&2; exit 1'
ExecStart=/usr/local/sbin/grd-user-config.sh
RemainAfterExit=yes

[Install]
WantedBy=default.target
EOF

RUN systemctl --global enable gnome-headless.service grd-user-config.service

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
