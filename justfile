GORELEASER := "goreleaser/goreleaser:v2.17.1"
CONTROL_IMAGE := "codingcoffee/dummie-control"
WEBSITE_IMAGE := "codingcoffee/dummie-website"
# arm64 can be added here; the Nuxt stages then build a second time under emulation.
IMAGE_PLATFORMS := "linux/amd64"

# print the version everything is built from.
version:
  @cat VERSION

# cut a release: bump VERSION, commit, tag, publish the binaries to github, push
# the control image. bump is patch | minor | major. GITHUB_TOKEN must be set.
#
# VERSION is the source of truth. The tag is derived from it, and goreleaser is
# then pointed back at that tag -- so the file, the tag, the binaries and the
# image tag are one number by construction.
release bump:
  #!/usr/bin/env bash
  set -euo pipefail
  : "${GITHUB_TOKEN:?GITHUB_TOKEN is not set}"

  case "{{bump}}" in patch|minor|major) ;; *) echo "bump must be patch, minor or major" >&2; exit 1 ;; esac
  # --untracked-files=all explicitly: goreleaser runs in a container that has none
  # of your git config, so it counts untracked files as dirty whatever
  # status.showUntrackedFiles says here. Better to fail before the tag is pushed.
  [ -z "$(git status --porcelain --untracked-files=all)" ] || {
    echo "working tree is dirty (goreleaser counts untracked files too):" >&2
    git status --porcelain --untracked-files=all >&2
    exit 1
  }
  branch=$(git rev-parse --abbrev-ref HEAD)
  [ "$branch" = "main" ] || { echo "not on main (on $branch)" >&2; exit 1; }

  # Prove both images build before anything is published. They are the likeliest
  # step to fail -- lockfile drift, a tarball that will not extract, a base image
  # tag that moved -- and until this ran last, a failure there left the tag and
  # the github release already public with no images to go with them.
  #
  # Not free: it is a full build. But buildx caches the layers, so the pushing
  # builds at the end of this recipe mostly reuse this work rather than repeat it.
  echo "preflight: building images before tagging"
  just _preflight-images

  prev=$(tr -d '[:space:]' < VERSION)
  if git rev-parse -q --verify "refs/tags/v${prev}" >/dev/null; then
    IFS=. read -r major minor patch <<< "$prev"
    case "{{bump}}" in
      major) major=$((major + 1)); minor=0; patch=0 ;;
      minor) minor=$((minor + 1)); patch=0 ;;
      patch) patch=$((patch + 1)) ;;
    esac
    next="${major}.${minor}.${patch}"
    echo "releasing ${prev} -> ${next}"
  else
    # The version in the file has never shipped, so it is what ships -- ignoring
    # the requested bump. This is how the first release comes out as 0.0.1 rather
    # than as a bump of it.
    next="$prev"
    echo "v${next} is not tagged yet; releasing it as-is (ignoring {{bump}})"
  fi
  tag="v${next}"

  if [ "$next" != "$prev" ]; then
    echo "$next" > VERSION
    git add VERSION
    git commit -m "chore: release ${tag}"
  fi
  git tag -a "$tag" -m "$tag"
  git push origin main "$tag"

  just _goreleaser release --clean
  just release-image "$next"
  just release-website-image "$next"

# build both images without tagging or pushing, to prove they build. Used as the
# release preflight; VERSION is a placeholder because nothing here is published.
_preflight-images:
  #!/usr/bin/env bash
  set -euo pipefail
  docker buildx build \
    --platform "{{IMAGE_PLATFORMS}}" \
    -f control/Dockerfile \
    --build-arg VERSION="preflight" \
    --build-arg COMMIT="$(git rev-parse --short HEAD)" \
    --build-arg DATE="$(git log -1 --format=%cI)" \
    -t "{{CONTROL_IMAGE}}:preflight" \
    control/
  docker buildx build \
    --platform "{{IMAGE_PLATFORMS}}" \
    -f website/Dockerfile \
    -t "{{WEBSITE_IMAGE}}:preflight" \
    website/

# build the binaries and the image without tagging, pushing or publishing anything.
release-snapshot:
  #!/usr/bin/env bash
  set -euo pipefail
  just _goreleaser release --clean --snapshot --skip=publish
  docker buildx build \
    --platform "{{IMAGE_PLATFORMS}}" \
    -f control/Dockerfile \
    --build-arg VERSION="$(tr -d '[:space:]' < VERSION)-snapshot" \
    --build-arg COMMIT="$(git rev-parse --short HEAD)" \
    --build-arg DATE="$(git log -1 --format=%cI)" \
    -t "{{CONTROL_IMAGE}}:snapshot" \
    control/
  docker buildx build \
    --platform "{{IMAGE_PLATFORMS}}" \
    -f website/Dockerfile \
    -t "{{WEBSITE_IMAGE}}:snapshot" \
    website/

# build and push the control image for a version that is already tagged. Takes
# the bare number (0.0.3), the way VERSION and the release notes write it; the
# git tag it reads the commit and date from is v-prefixed.
release-image version:
  #!/usr/bin/env bash
  set -euo pipefail
  docker buildx build \
    --platform "{{IMAGE_PLATFORMS}}" \
    -f control/Dockerfile \
    --build-arg VERSION="{{version}}" \
    --build-arg COMMIT="$(git rev-list -n1 --abbrev-commit "v{{version}}")" \
    --build-arg DATE="$(git log -1 --format=%cI "v{{version}}")" \
    -t "{{CONTROL_IMAGE}}:{{version}}" \
    -t "{{CONTROL_IMAGE}}:latest" \
    --push \
    control/

# build and push the website image for a version that is already tagged. Takes
# the bare number, like release-image. The site carries no build-time reference
# to any control plane -- CONSOLE_URL is read at container start -- so there is
# nothing version-shaped to stamp into it beyond the tag.
release-website-image version:
  #!/usr/bin/env bash
  set -euo pipefail
  docker buildx build \
    --platform "{{IMAGE_PLATFORMS}}" \
    -f website/Dockerfile \
    -t "{{WEBSITE_IMAGE}}:{{version}}" \
    -t "{{WEBSITE_IMAGE}}:latest" \
    --push \
    website/

# goreleaser in a container, so the binaries never depend on the host toolchain.
# GOTOOLCHAIN=auto lets it fetch the Go the go.mod files ask for.
_goreleaser *args:
  #!/usr/bin/env bash
  set -euo pipefail
  docker run --rm \
    -v "$PWD:/src" -w /src \
    -v "${HOME}/go/pkg/mod:/go/pkg/mod" \
    -e GITHUB_TOKEN -e GOTOOLCHAIN=auto \
    --entrypoint sh "{{GORELEASER}}" -c \
    'git config --global --add safe.directory /src && exec goreleaser "$@"' -- {{args}}

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

# Local host VM: user networking, packaged kernels, and a persistent disk.
vm-local-start:
  #!/usr/bin/env bash
  cd nix-vms/
  nix run ".#qemu-local"

vm-local-stop:
  #!/usr/bin/env bash
  cd nix-vms/
  nix run ".#qemu-local" -- stop

vm-local-ssh:
  ssh -o StrictHostKeyChecking=no \
      -o UserKnownHostsFile=/dev/null \
      -o LogLevel=ERROR \
      -p 2223 \
      ubuntu@127.0.0.1

vm-local-console:
  #!/usr/bin/env bash
  cd nix-vms/
  nix run ".#qemu-local" -- console

vm-local-stats:
  #!/usr/bin/env bash
  cd nix-vms/
  nix run ".#qemu-local" -- stats

# Forward the enrolled host's dproxy HTTP and SSH listeners to unprivileged
# loopback ports on the development machine.
vm-local-proxy-tunnel:
  ssh -fNT -o ExitOnForwardFailure=yes \
      -o StrictHostKeyChecking=no \
      -o UserKnownHostsFile=/dev/null \
      -o LogLevel=ERROR \
      -L 127.0.0.1:8080:127.0.0.1:80 \
      -L 127.0.0.1:2224:127.0.0.1:22 \
      -p 2223 ubuntu@127.0.0.1

# attach to the VM's serial console via QEMU (no ssh/network needed). Ctrl-] to detach.
vm-console:
  #!/usr/bin/env bash
  set -euo pipefail
  cd nix-vms/
  nix run ".#qemu-host" -- console

# Build binaries that run inside the Debian VM. Static linking avoids a NixOS
# dynamic loader path that does not exist in the guest.
vm-binaries:
  #!/usr/bin/env bash
  set -euo pipefail
  (cd control && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o tmp/dclient ./cmd/dclient)
  for service in dproxy dpipe dinit intproxy; do
    (cd "backstage/$service" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$service" "./cmd/$service")
  done

# Serve only VM artifacts on host loopback; QEMU's user-network gateway
# forwards the guest's 10.68.0.1 requests to this listener.
vm-artifacts:
  #!/usr/bin/env bash
  set -euo pipefail
  mkdir -p vm-artifacts
  install -m 0755 control/tmp/dclient vm-artifacts/dclient
  for service in dproxy dpipe dinit intproxy; do
    install -m 0755 "backstage/$service/$service" "vm-artifacts/$service"
  done
  exec python3 -m http.server 8081 --bind 127.0.0.1 --directory vm-artifacts

image-build target:
  #!/usr/bin/env bash
  set -euo pipefail
  cd images/{{target}}/
  build_args=()
  if [ "{{target}}" = "debian-vm-host" ]; then
    pubkey_file="${DUMMIE_VM_SSH_PUBLIC_KEY_FILE:-$HOME/.ssh/id_ed25519.pub}"
    [ -s "$pubkey_file" ] || {
      echo "SSH public key missing: $pubkey_file" >&2
      exit 1
    }
    build_args=(--build-arg "VM_SSH_PUBLIC_KEY=$(cat "$pubkey_file")")
  fi
  if [ "{{target}}" = "dubuntu" ] && { [ -n "${DUMMIE_LLM_TLD:-}" ] || [ -n "${DUMMIE_LLM_PROXY_URL:-}" ]; }; then
    [ -n "${DUMMIE_LLM_TLD:-}" ] && [ -n "${DUMMIE_LLM_PROXY_URL:-}" ] || {
      echo "Set both DUMMIE_LLM_TLD and DUMMIE_LLM_PROXY_URL" >&2
      exit 1
    }
    build_args+=(--build-arg "DUMMIE_LLM_TLD=$DUMMIE_LLM_TLD" --build-arg "DUMMIE_LLM_PROXY_URL=$DUMMIE_LLM_PROXY_URL")
  fi
  docker build "${build_args[@]}" -t {{target}} .
  cid=$(docker create {{target}})
  rm -f rootfs.tar
  docker export "$cid" -o rootfs.tar
  docker rm "$cid"

# Opt in to the Dummie provider for the local VM catalogue.
image-build-local-dubuntu:
  DUMMIE_LLM_TLD=dummie.localhost DUMMIE_LLM_PROXY_URL=http://10.64.255.254/v1 just image-build dubuntu

# Reuse binary kernels: Debian's cloud kernel boots the host VM with its
# initramfs; LinuxKit's built-in virtio/squashfs drivers boot sandbox guests.
vm-prebuilt-kernels:
  #!/usr/bin/env bash
  set -euo pipefail
  mkdir -p vm-artifacts
  scratch=$(mktemp -d)
  host_cid=$(docker create debian-vm-host:latest)
  guest_cid=
  cleanup() {
    docker rm -f "$host_cid" >/dev/null 2>&1 || true
    if [ -n "$guest_cid" ]; then docker rm -f "$guest_cid" >/dev/null 2>&1 || true; fi
    rm -rf "$scratch"
  }
  trap cleanup EXIT
  mkdir "$scratch/boot"
  docker cp "$host_cid:/boot/." "$scratch/boot"
  shopt -s nullglob
  kernels=("$scratch"/boot/vmlinuz-*)
  initrds=("$scratch"/boot/initrd.img-*)
  [ "${#kernels[@]}" -eq 1 ] && [ "${#initrds[@]}" -eq 1 ] || {
    echo "expected one Debian kernel and initramfs in /boot" >&2
    exit 1
  }
  install -m 0644 "${kernels[0]}" vm-artifacts/host-vmlinuz
  install -m 0644 "${initrds[0]}" vm-artifacts/host-initrd.img
  docker pull linuxkit/kernel:6.6.71
  guest_cid=$(docker create --entrypoint /kernel linuxkit/kernel:6.6.71)
  docker cp "$guest_cid:/kernel" vm-artifacts/guest-vmlinuz
  chmod 0644 vm-artifacts/guest-vmlinuz
  file vm-artifacts/host-vmlinuz vm-artifacts/guest-vmlinuz

# Find kernel versions here: https://git.kernel.org/pub/scm/linux/kernel/git/stable/linux.git

kernel-setup variant version:
  #!/usr/bin/env bash
  case "{{ variant }}" in
    host|guest) ;;
    *) echo "error: variant must be 'host' or 'guest', got '{{ variant }}'" >&2; exit 1 ;;
  esac
  cd kernel
  git clone --depth 1 --branch "{{version}}" https://git.kernel.org/pub/scm/linux/kernel/git/stable/linux.git "linux-{{variant}}-{{version}}"

# Download a stable kernel source archive without the Git object database.
kernel-setup-archive variant version:
  #!/usr/bin/env bash
  set -euo pipefail
  case "{{ variant }}" in
    host|guest) ;;
    *) echo "error: variant must be 'host' or 'guest', got '{{ variant }}'" >&2; exit 1 ;;
  esac
  cd kernel
  source_dir="linux-{{variant}}-{{version}}"
  if [ -f "$source_dir/Makefile" ]; then
    echo "$source_dir already exists"
    exit 0
  fi
  release="{{version}}"
  release="${release#v}"
  major="${release%%.*}"
  archive="linux-${release}.tar.xz"
  if [ ! -f "$archive" ]; then
    curl -fL --retry 3 "https://cdn.kernel.org/pub/linux/kernel/v${major}.x/$archive" -o "$archive.part"
    mv "$archive.part" "$archive"
  fi
  mkdir -p "$source_dir"
  tar -xJf "$archive" --strip-components=1 -C "$source_dir"

kernel-build variant version:
  #!/usr/bin/env bash
  case "{{ variant }}" in
    host|guest) ;;
    *) echo "error: variant must be 'host' or 'guest', got '{{ variant }}'" >&2; exit 1 ;;
  esac
  cd kernel
  nix-shell --run '
    set -euo pipefail
    cd "linux-{{variant}}-{{version}}"
    make kernelversion
    make defconfig
    ../{{variant}}/optimize.sh
    make olddefconfig
    make -j"$(nproc)" bzImage
  '

# regenerate the python sdk from the control api spec. The recipe lives in
# control/justfile so it also runs inside the sdk container, where /app is control/.
sdk-python:
  #!/usr/bin/env bash
  set -euo pipefail
  cd control/
  just sdk-python
