
# Setup

## Compile a kernel

```sh
just kernel-setup v7.1.4
just kernel-build v7.1.4
```

## Build a base debian image

```sh
just build images
```

## Run a VM

```sh
just run
```

## SSH into the VM

```sh
just ssh
```

## Check process running in VM

From inside the VM run

```sh
systemctl status microqemu-boot
sudo systemctl restart microqemu-boot
sudo systemctl stop microqemu-boot
journalctl -u microqemu-boot -f
```

## Control Server

For rustfs

```sh
mkdir -p rustfs-data rustfs-logs
sudo chown -R 10001:10001 ./rustfs-data ./rustfs-logs
```

Running the control server

```sh
docker compose up -d
docker compose logs -f
```

## QEMU Host Configuration

Starting the host VM

```sh
just vm-start
```

SSH into the host VM

```sh
just vm-ssh
```

Starting a VM inside the host VM

```sh
sudo tee /etc/dclient/config.yaml > /dev/null <<'EOF'
control_url: http://10.68.0.1:1323
# enrollment_key: paste-once-then-it-is-ignored
insecure: true

data_dir: /var/lib/dclient
socket: /run/dclient/dclient.sock
group: dclient

features:
  ip_forward: true
  kvm_access: true
  nftables: true
  docker_compat: true
  suricata: true
  dhcp: true
  metadata: true

network:
  pool: 10.64.0.0/16
  gateway: 10.64.0.1
  uplink: enp0s2
  dns: 1.1.1.1
  queues: 4
EOF

sudo systemctl restart dclient
```

`dpipe`, `dproxy` and `vector` are not configured here. Which build of each one a
host runs is set in the control server, per host, on that client's page under
**Managed binaries** — so upgrading a host is an edit rather than an ssh session.

Saving records the intent and installs whatever the host is missing. It never
replaces a binary that is already running: that takes the **Upgrade now** button
on the same card, per host. Nothing else moves them — not a reconnect, not a
control server deploy.

Where a host fetches each binary from is resolved most specific first:

1. the download URL on that host's own page — one machine, one build
2. the version on that host's own page — one machine moved onto a release
3. the fleet-wide download URL in **Settings** — an installation serving its own
   builds from somewhere the control plane cannot name
4. the published release for the control server's own version, which is what a
   normal installation runs and never configures:
   `https://github.com/getdummie/dummie/releases/download/v0.0.15/dpipe_0.0.15_linux_amd64.tar.gz`

For local development that means setting the three URLs once in **Settings**
rather than per host — the artifact server serves the binaries raw, and a
`.tar.gz` works just as well:

```
dpipe    → http://10.68.0.1:8081/backstage/dpipe/dpipe
dproxy   → http://10.68.0.1:8081/backstage/dproxy/dproxy
```

The dev VM gets dclient by running `./dclient install` from a copied binary, so it
needs no dclient URL. Set one only to exercise self-upgrade, pointing at wherever
your build is served from.

That matters in dev because a control server built from source reports its
version as `dev`, so step 4 resolves to nothing and a host with no URL anywhere
installs nothing at all.

Upgrading with the dclient version moved replaces dclient itself: it downloads the
build, checks it runs, swaps the binary and restarts its own unit, coming back on
the new version a few seconds later.

For generating python sdk

```sh
just sdk-python
just sdk-install
```

Archlinux Setup

```sh
pacman -Syyu ncdu neovim sudo btop htop qemu-base qemu-img docker
sudo systemctl enable --now docker
```
