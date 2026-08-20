GORELEASER := "goreleaser/goreleaser:v2.17.1"
CONTROL_IMAGE := "codingcoffee/dummie-control"
# arm64 can be added here; the Nuxt stage then builds a second time under emulation.
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
  just release-image "$tag"

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

# build and push the control image for a tag that already exists.
release-image version:
  #!/usr/bin/env bash
  set -euo pipefail
  docker buildx build \
    --platform "{{IMAGE_PLATFORMS}}" \
    -f control/Dockerfile \
    --build-arg VERSION="{{version}}" \
    --build-arg COMMIT="$(git rev-list -n1 --abbrev-commit "{{version}}")" \
    --build-arg DATE="$(git log -1 --format=%cI "{{version}}")" \
    -t "{{CONTROL_IMAGE}}:{{version}}" \
    -t "{{CONTROL_IMAGE}}:latest" \
    --push \
    control/

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

# regenerate the python sdk from the control api spec. The recipe lives in
# control/justfile so it also runs inside the sdk container, where /app is control/.
sdk-python:
  #!/usr/bin/env bash
  set -euo pipefail
  cd control/
  just sdk-python
