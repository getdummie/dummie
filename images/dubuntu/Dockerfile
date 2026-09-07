FROM codingcoffee/dummie-ubuntu-minimal

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

RUN set -e \
  && apt update \
  && apt-get install -y --no-install-recommends \
    git\
    fd-find \
    ripgrep \
  && npm install -g --ignore-scripts \
    "@earendil-works/pi-coding-agent"

USER ubuntu
WORKDIR /home/ubuntu
