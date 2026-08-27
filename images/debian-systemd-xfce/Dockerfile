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
    xfce4 \
    xfce4-terminal \
    dbus-x11 \
    tigervnc-standalone-server \
    tigervnc-common \
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

# VNC server (TigerVNC) serving an XFCE session for the ubuntu user on :1 (port 5901).
# This TigerVNC keeps its files under ~/.config/tigervnc (with ~/.vnc as a symlink);
# creating them there up front avoids the "could not migrate ~/.vnc" first-run failure.
RUN set -ex \
  && systemctl enable sshd \
  && install -d -m 0700 -o ubuntu -g ubuntu /home/ubuntu/.config/tigervnc \
  && ln -s .config/tigervnc /home/ubuntu/.vnc \
  && printf '#!/bin/sh\nunset SESSION_MANAGER\nunset DBUS_SESSION_BUS_ADDRESS\nexec startxfce4\n' \
     > /home/ubuntu/.config/tigervnc/xstartup \
  && chmod 0755 /home/ubuntu/.config/tigervnc/xstartup \
  && echo 'ubuntu' | vncpasswd -f > /home/ubuntu/.config/tigervnc/passwd \
  && chmod 0600 /home/ubuntu/.config/tigervnc/passwd \
  && chown -h ubuntu:ubuntu /home/ubuntu/.vnc \
  && chown -R ubuntu:ubuntu /home/ubuntu/.config \
  && printf '[Unit]\nDescription=TigerVNC server for display %%i\nAfter=network.target\n\n[Service]\nType=simple\nUser=ubuntu\nWorkingDirectory=/home/ubuntu\nExecStartPre=-/usr/bin/vncserver -kill :%%i\nExecStart=/usr/bin/vncserver :%%i -fg -geometry 1280x800 -depth 24 -localhost no\nExecStop=/usr/bin/vncserver -kill :%%i\nRestart=on-failure\nRestartSec=2\n\n[Install]\nWantedBy=multi-user.target\n' \
     > /etc/systemd/system/vncserver@.service \
  && systemctl enable vncserver@1.service

USER ubuntu
RUN set -ex \
  && mkdir -p /home/ubuntu/.ssh/ \
  && curl -sL https://github.com/codingcoffee.keys > /home/ubuntu/.ssh/authorized_keys
