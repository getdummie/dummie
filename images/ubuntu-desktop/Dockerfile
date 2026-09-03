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
    dbus-x11 \
    libgl1-mesa-dri \
    openssl \
    neovim

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
  && systemctl enable systemd-resolved \
  && systemctl set-default graphical.target \
  && (id ubuntu || adduser --disabled-password --gecos "" ubuntu) \
  && echo 'ubuntu:ubuntu' | chpasswd \
  && adduser ubuntu sudo \
  && echo 'ubuntu ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/ubuntu \
  && chmod 0440 /etc/sudoers.d/ubuntu

# GNOME Remote Desktop over RDP (port 3389), headless system/remote-login mode.
# GDM + the gnome-remote-desktop system daemon expose the greeter remotely; you log in
# over RDP and get a full Wayland GNOME session rendered headlessly by mutter (software GL
# via mesa-dri, captured through PipeWire). RDP requires a TLS cert, generated here.
#
# The actual `grdctl --system` configuration is deferred to a boot-time oneshot
# (grd-config.service) instead of running at build: grdctl talks to the live system D-Bus,
# which isn't available during `docker build`. The service is idempotent, so it also works
# for ephemeral VMs whose root is wiped every boot.
RUN set -ex \
  && install -d -m 0755 /etc/gnome-remote-desktop \
  && openssl req -x509 -newkey rsa:4096 -nodes -days 3650 \
       -subj "/CN=ubuntu-desktop" \
       -keyout /etc/gnome-remote-desktop/tls.key \
       -out /etc/gnome-remote-desktop/tls.crt \
  && printf '#!/bin/sh\nset -eu\n# TLS material must be readable by the gnome-remote-desktop daemon user\nchown gnome-remote-desktop:gnome-remote-desktop /etc/gnome-remote-desktop/tls.crt /etc/gnome-remote-desktop/tls.key || true\ngrdctl --system rdp set-tls-cert /etc/gnome-remote-desktop/tls.crt\ngrdctl --system rdp set-tls-key /etc/gnome-remote-desktop/tls.key\ngrdctl --system rdp set-credentials ubuntu ubuntu\ngrdctl --system rdp disable-view-only\ngrdctl --system rdp enable\nsystemctl enable gnome-remote-desktop.service || true\n# restart (not just start) so the daemon binds 3389 AFTER the config above is applied\nsystemctl restart gnome-remote-desktop.service || true\n' \
     > /usr/local/sbin/grd-config.sh \
  && chmod 0755 /usr/local/sbin/grd-config.sh \
  && printf '[Unit]\nDescription=Configure gnome-remote-desktop RDP\nAfter=gnome-remote-desktop.service systemd-user-sessions.service\nWants=gnome-remote-desktop.service\n\n[Service]\nType=oneshot\nExecStart=/usr/local/sbin/grd-config.sh\nRemainAfterExit=yes\n\n[Install]\nWantedBy=graphical.target\n' \
     > /etc/systemd/system/grd-config.service \
  && systemctl enable grd-config.service

