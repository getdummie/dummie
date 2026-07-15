
run:
  #!/usr/bin/env bash
  cd nix/
  nix run

stop:
  #!/usr/bin/env bash
  cd nix/
  nix run ".#web" -- stop

clean:
  #!/usr/bin/env bash
  set -euo pipefail
  cd nix/
  nix run "#web" -- stop
  rm -rf .microqemu/web/console.log .microqemu/web/rootfs.ext4
  ssh-keygen -R 10.68.0.2

ssh:
  #!/usr/bin/env bash
  set -euo pipefail
  cd nix/
  ssh ubuntu@10.68.0.2

build target:
  #!/usr/bin/env bash
  set -euo pipefail
  cd {{target}}/
  docker build -t debian-systemd .
  cid=$(docker create debian-systemd)
  rm -f debian-systemd-rootfs.tar
  docker export "$cid" -o debian-systemd-rootfs.tar
  docker rm "$cid"

