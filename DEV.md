
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

## Local VMs

Four VMs share the host bridge `vm-br0` (`10.68.0.1/16`). The host's NixOS
config provides the bridge, the taps and NAT.

| VM            | IP         | tap         | role                           |
| ------------- | ---------- | ----------- | ------------------------------ |
| `qemu-host-1` | 10.68.0.2  | `vm-tap0`   | qemu host running `dclient`    |
| `qemu-host-2` | 10.68.0.3  | `vm-tap1`   | qemu host running `dclient`    |
| `fakefsx`     | 10.68.0.10 | `vm-tapfsx` | ZFS + NFS + fake FSx API :8080 |
| `fakeedge`    | 10.68.0.11 | `vm-tapedge` | edge: `dclient` with `role: edge` |

Run these from `nix-vms/`: state lives in `.microqemu/<vm>/` under the directory
you launch from, which is also where the `just vm-*` recipes look.

```sh
cd nix-vms

nix run .#qemu-host-1                # start (also: `just vm-start`)
nix run .#qemu-host-2
nix run .#fakefsx
nix run .#fakeedge

nix run .#<vm> -- status
nix run .#<vm> -- stop
nix run .#<vm> -- console            # serial console, Ctrl-] to detach
nix run .#<vm> -- stats              # qemu hosts only
```

The qemu hosts boot from `images/debian-vm-host/rootfs.tar`, built with
`just image-build debian-vm-host`. Their root disk is ephemeral; to rebuild it
from the tar after changing the image, delete it while stopped:

```sh
nix run .#qemu-host-1 -- stop
rm -f .microqemu/qemu-host-1/rootfs.ext4     # or `just vm-clean` for host 1
```

Deleting `fakefsx` wipes the pool and every volume on it. Remove the matching
FSx images and disks in the admin panel afterwards, since control still
points at them:

```sh
nix run .#fakefsx -- stop
rm -rf .microqemu/fakefsx
ssh-keygen -R 10.68.0.10
```

### SSH

```sh
just vm-ssh-1                        # ubuntu@10.68.0.2, port 2222
just vm-ssh-2                        # ubuntu@10.68.0.3, port 2222
ssh root@10.68.0.10                  # fakefsx, keys from github.com/codingcoffee.keys
ssh -p 2222 ubuntu@10.68.0.11        # fakeedge
```

### Edge (fakeedge)

`fakeedge` routes a domain's names to the hosts behind it: http by Host, https
by SNI (passed through untouched) and ssh by login name (`ssh <vm>@<tld>`). It
runs no VMs, only `dproxy` and `dpipe`. Its boot writes `control_url`,
`role: edge` and `insecure: true` to `/etc/dclient/config.yaml`, then runs
`dclient install`; it uses one vCPU, so `vm-tapedge` must not be multi-queue.

```sh
cd nix-vms
nix run .#fakeedge                   # start
nix run .#fakeedge -- status
nix run .#fakeedge -- console        # Ctrl-] to detach; `stty sane onlcr` if lines stair-step
nix run .#fakeedge -- stop

nix run .#fakeedge -- stop && rm -f .microqemu/fakeedge/rootfs.ext4   # rebuild after changing its boot
```

Once it enrolls, Admin → Domains → Edge column: pick it for the domain, then
point the domain at it in `/etc/hosts` (`10.68.0.11 <tld> <vm>.<tld>`). Pick
"none" and point back at a host to go direct again; hosts stay reachable either
way.

Inside it:

```sh
journalctl -u dclient -f                         # enrolment, configs pushed by control
journalctl -u dproxy -f                          # routing: "edge route", "ssh authorize"
journalctl -u dpipe -f                           # ssh relay to the host: "ssh backend …"
sudo cat /etc/dclient/dproxy.yaml                # edge.hosts / edge.fallback / ssh.users
sudo cat /etc/dclient/config.yaml
```

On a host behind it, `ssh.trusted_upstreams` in `/etc/dclient/dproxy.yaml`
names the edge's key and `10.68.0.11`. Bridged VM-to-VM traffic must not be
masqueraded on the laptop (`net.bridge.bridge-nf-call-iptables=1` routes it
through iptables), or the host sees the edge as `10.68.0.1` and logs
`ssh deny: upstream key from an untrusted source`. Check with:

```sh
sudo iptables -t nat -S POSTROUTING   # only `-s 10.68.0.0/16 ! -o vm-br0 -j MASQUERADE` for the VMs
```

### FSx disks locally

Point control at the fake in `control/.env`, then recreate the container with
`docker compose up -d --force-recreate control`:

```sh
AWS_ENDPOINT_URL_FSX=http://10.68.0.10:8080
AWS_REGION=ap-south-1
AWS_ACCESS_KEY_ID=test
AWS_SECRET_ACCESS_KEY=test
```

In Admin → Settings: turn on FSx for OpenZFS VM disks, set the file system ID
to `fs-0fa4ef5a00000001` and the NFS clients to `10.68.0.0/16`. Leave the
parent volume empty; the root volume's ID changes whenever the pool is recreated.

Inside `fakefsx` (`ssh root@10.68.0.10`), each FSx volume is a ZFS dataset
under the pool `fsx`, golden images are `img-*` with an `@golden` snapshot,
and VM disks are `vm-<uuid>` clones of one:

```sh
zfs list -o name,used,refer,quota,mountpoint         # volumes
zfs list -t snapshot                                 # golden snapshots
zfs list -o name,origin                              # which image a disk is cloned from
zfs get -r -o name,value fakefsx:id fsx              # FSx ids (fsvol-…, fsvolsnap-…)
zpool list fsx                                       # pool usage
exportfs -v                                          # NFS exports and their clients
journalctl -u fakefsx -f                             # API calls control makes
systemctl status fakefsx-pool fakefsx
```

Delete volumes from Admin → FSx images / FSx disks rather than with
`zfs destroy`, so control's records stay in step.

From a qemu host, check the API and NFS:

```sh
curl -s -X POST \
  -H 'X-Amz-Target: AWSSimbaAPIService_v20180301.DescribeFileSystems' \
  -H 'Content-Type: application/x-amz-json-1.1' \
  -d '{}' http://10.68.0.10:8080 | jq
showmount -e 10.68.0.10
mount | grep nfs                     # FSx disks of the VMs on this host
sudo dclient doctor                  # "fsx volumes can be mounted" should pass
```

## Starting a VM inside a qemu host

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

Ubuntu Setup

```sh
sudo apt install -y ncdu neovim sudo btop htop qemu-utils qemu-system-x86
wget https://github.com/getdummie/dummie/releases/download/v0.0.26/dclient_0.0.26_linux_amd64.tar.gz
tar -xvf dclient_0.0.26_linux_amd64.tar.gz
sudo mv dclient /usr/local/bin/.

# change default ssh port
sudo tee /etc/systemd/system/ssh.socket.d/override.conf > /dev/null <<'EOF'
[Socket]
ListenStream=
ListenStream=0.0.0.0:2202
ListenStream=[::]:2202
EOF

sudo systemctl daemon-reload
sudo systemctl restart ssh.socket

# check doctor
dclient doctor
```
