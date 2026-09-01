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
    chromium

# Ubuntu's firefox deb is a stub that shells out to snap, so firefox comes from
# Mozilla's own apt repo instead, pinned above the stub.
RUN set -ex \
  && apt install -y gnupg ca-certificates \
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
  && apt policy firefox | grep -q packages.mozilla.org

RUN set -ex \
  && apt clean \
  && systemctl enable ssh \
  && systemctl enable systemd-resolved \
  && systemctl enable systemd-networkd \
  && systemctl set-default multi-user.target \
  && (id ubuntu || adduser --disabled-password --gecos "" ubuntu) \
  && adduser ubuntu sudo \
  && echo 'ubuntu ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/ubuntu \
  && chmod 0440 /etc/sudoers.d/ubuntu

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

USER ubuntu
