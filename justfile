
vm-start:
  #!/usr/bin/env bash
  cd nix-vms/
  nix run

vm-stop:
  #!/usr/bin/env bash
  cd nix-vms/
  nix run ".#qemu-host" -- stop

vm-clean:
  #!/usr/bin/env bash
  set -euo pipefail
  cd nix-vms/
  nix run "#qemu-host" -- stop
  rm -rf .microqemu/qemu-host/console.log .microqemu/qemu-host/rootfs.ext4
  ssh-keygen -R 10.68.0.2

vm-stats:
  #!/usr/bin/env bash
  set -euo pipefail
  cd nix-vms/
  nix run "#qemu-host" -- stats

vm-ssh:
  #!/usr/bin/env bash
  set -euo pipefail
  cd nix-vms/
  # ephemeral VM: host key changes every boot, so skip known_hosts entirely
  # (no prompt, no "IDENTIFICATION HAS CHANGED" on rebuild).
  ssh -o StrictHostKeyChecking=no \
      -o UserKnownHostsFile=/dev/null \
      -o LogLevel=ERROR \
      -p 2222 \
      ubuntu@10.68.0.2

# attach to the VM's serial console via QEMU (no ssh/network needed). Ctrl-] to detach.
vm-console:
  #!/usr/bin/env bash
  set -euo pipefail
  cd nix-vms/
  nix run ".#qemu-host" -- console

image-build target:
  #!/usr/bin/env bash
  set -euo pipefail
  cd images/{{target}}/
  docker build -t {{target}} .
  cid=$(docker create {{target}})
  rm -f rootfs.tar
  docker export "$cid" -o rootfs.tar
  docker rm "$cid"

kernel-setup version:
  #!/usr/bin/env bash
  cd kernel
  git clone --depth 1 --branch "{{version}}" https://git.kernel.org/pub/scm/linux/kernel/git/stable/linux.git "{{version}}"

kernel-build version:
  #!/usr/bin/env bash
  cd kernel
  nix-shell --run '
    set -euo pipefail
    cd "linux-{{version}}"
    make kernelversion
    make defconfig
    ../optimize.sh
    make olddefconfig
    make -j"$(nproc)" bzImage
  '
