# Local VMs on NixOS

The local control server sends jobs to `dclient` in a Debian QEMU host VM.
That host starts sandbox guests. This setup uses packaged kernels and QEMU's
user network, so it does not need a local kernel compile, tap interface, NAT
rule, or host sudo access.

## Build the host and guest artifacts

From the repository root:

```sh
just image-build debian-vm-host
just vm-prebuilt-kernels
just vm-binaries
```

The Debian image installs its prebuilt cloud kernel and initramfs. The kernel
recipe copies those files to `vm-artifacts/` and downloads a LinuxKit guest
kernel with virtio, ext4, squashfs, and overlay support built in. This pinned
LinuxKit kernel is the local development guest artifact. `just vm-binaries`
makes static Linux binaries for `dclient` and its services.

The host image includes the public key from `~/.ssh/id_ed25519.pub`. Set
`DUMMIE_VM_SSH_PUBLIC_KEY_FILE` to another absolute path when building if you
use a different SSH key. Keep the matching private key in your SSH agent or
select it with `ssh -i` when connecting.

## Start the services and host VM

Follow [DEV.md](DEV.md) for PostgreSQL, ClickHouse, RustFS, and the control
server. Keep the API and RustFS ports bound to host loopback. Add these values
to the ignored `control/.env` and restart the control server:

```dotenv
S3_HOST_ENDPOINT=http://10.68.0.1:9000
LOCAL_VM_PROXY_PORT=8080
```

`S3_PUBLIC_ENDPOINT` stays at `http://127.0.0.1:9000` from [DEV.md](DEV.md),
so browser downloads use a reachable address. `S3_HOST_ENDPOINT` signs VM
artifact downloads for the QEMU host at its guest-facing address.

If you used an earlier draft of this guide that stored a persistent local VM
under `.microqemu/qemu-host`, stop that VM and move its state before switching
to the opt-in `qemu-local` launcher:

```sh
just vm-stop
mv nix-vms/.microqemu/qemu-host nix-vms/.microqemu/qemu-local
```

Skip this step if you used `qemu-host` with its original tap network setup.

Start the artifact server in one terminal and leave it running:

```sh
just vm-artifacts
```

In another terminal, boot and enter the host VM:

```sh
just vm-local-start
just vm-local-ssh
```

The artifact server binds to `127.0.0.1:8081` on the host and serves only
`vm-artifacts/`. Inside the host VM, `10.68.0.1` reaches host loopback through
QEMU's user network. The Debian host has `10.68.0.2`; SSH is forwarded to
`127.0.0.1:2223` on your machine. The console remains at
`http://localhost:1323`.

## Enroll the host

In the console, set these **Admin → Settings** values:

| Setting | Value |
| --- | --- |
| dclient download URL | `http://10.68.0.1:8081/dclient` |
| dpipe download URL | `http://10.68.0.1:8081/dpipe` |
| dproxy download URL | `http://10.68.0.1:8081/dproxy` |
| dinit download URL | `http://10.68.0.1:8081/dinit` |
| intproxy download URL | `http://10.68.0.1:8081/intproxy` |

In **Admin → Client keys**, create a one-use enrollment key. Inside the host
VM, write `/etc/dclient/config.yaml` using `sudo`:

```yaml
control_url: http://10.68.0.1:1323
enrollment_key: <one-use-key-from-console>
insecure: true
features:
  ip_forward: true
  kvm_access: true
  nftables: true
  dhcp: true
  metadata: true
network:
  uplink: enp0s2
```

Then run in the host VM:

```sh
sudo systemctl restart dclient
sudo journalctl -u dclient -n 40 --no-pager
```

The host should appear online in **Admin → Clients**. Its disk is persistent,
so enrollment survives `just vm-local-stop` and `just vm-local-start`. The
local disk and any private enrollment seed live under ignored
`nix-vms/.microqemu/`; keep that directory private. If you delete
`rootfs.ext4`, you need a new enrollment key unless you saved an
`enrollment-seed.tar` there.

## Reach guest SSH and browser consoles

In **Admin → Domains**, create `dummie.localhost` and assign it to the host in
**Admin → Clients**. Add your public key from `~/.ssh/id_ed25519.pub` to your
account profile. Set the host's dproxy download URL to
`http://10.68.0.1:8081/dproxy` in its client service settings. Then run:

```sh
just vm-local-proxy-tunnel
ssh -p 2224 <vm-name>@127.0.0.1
```

The tunnel exposes the host's proxy only on local ports 8080 and 2224.
`LOCAL_VM_PROXY_PORT=8080` makes the VM page's browser console use that
HTTP/WebSocket port. Names under `dummie.localhost` resolve to loopback, so the
console link works without editing `/etc/hosts`.

## Boot a small guest from the CLI

You can verify nested VM boot before using the console catalogue. On the
machine running Docker, export a small Alpine rootfs into the artifact
directory:

```sh
docker pull alpine:3.21
cid=$(docker create alpine:3.21)
docker export "$cid" -o vm-artifacts/alpine-rootfs.tar
docker rm "$cid"
```

With `just vm-artifacts` still running, execute inside the Debian host VM:

```sh
sudo dclient vm create \
  --name alpine-smoke \
  --kernel http://10.68.0.1:8081/guest-vmlinuz \
  --rootfs-tar http://10.68.0.1:8081/alpine-rootfs.tar \
  --memory 256 --cpus 1 --no-network
sudo dclient vm list
sudo dclient vm console <id-from-list>
```

Press `Ctrl-]` to leave the guest console. For the normal product flow,
upload `vm-artifacts/guest-vmlinuz` in **Admin → Kernels**, create an OS image
from `docker.io/library/alpine:3.21` in **Admin → OS images**, then use
**VMs → Create VM** with the connected host.

The existing `just kernel-setup` and `just kernel-build` recipes remain
available when you need a custom kernel. `just kernel-setup-archive` downloads
a stable source archive instead of cloning Git history. Neither is needed for
this local setup.

## Codex in dubuntu

The local `dubuntu` image installs pi 0.87.1 and Codex CLI 0.158.0. Both start
with `chatgpt/gpt-6-sol` through the internal LLM proxy, which uses the ChatGPT
integration connected to your account under **Integrations** in the control
console. No ChatGPT credential is stored in the guest. Pi's bundled Dummie
extension loads available models from the proxy and selects the default. In
the guest, run `pi` or `codex`. To pick another model, use pi's `/model` menu
or run `codex -m chatgpt/gpt-6-astra`. Run `pi --list-models dummie` to see
pi's current choices. To list the proxy's models directly:

```sh
curl -fsS -H 'Host: llm.int.dummie.localhost' \
  http://10.64.255.254/v1/models | jq -r '.data[].id'
```

The local proxy listens on `10.64.255.254:80`. Codex uses that address and
sends `llm.int.dummie.localhost` as its HTTP Host header because `.localhost`
names resolve to the guest's own loopback in some clients. Pi's extension uses
the same address and header. The image sets
`features.plugins = false` in `/home/ubuntu/.codex/config.toml`; plugin
startup can hang on this restricted guest network. To opt into plugins in a
guest, change that value to `true`.

Build a fresh rootfs for new VMs with `just image-build-local-dubuntu`, then
upload `images/dubuntu/rootfs.tar` under **Admin → OS images**. Set its image user to
`ubuntu` in the image configuration before creating a VM. The current rootfs
is about 2.15 GiB, so set `KERNEL_MAX_UPLOAD_MIB=3072` in the local
`control/.env` and restart the control server before uploading. A plain
`just image-build dubuntu` leaves the Dummie provider unconfigured for other
developers and fleets. To configure another fleet, set both build variables:

```sh
DUMMIE_LLM_TLD=example.com \
  DUMMIE_LLM_PROXY_URL=https://llm.int.example.com/v1 \
  just image-build dubuntu
```

Use that fleet's real domain and proxy address. The internal proxy must serve
the `llm.int.<domain>` host and accept the configured scheme.
