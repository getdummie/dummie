
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

dclient connect --control-url https://dummie-ww.zerodha.io --key 3bDZ-6kdPPDsStpnGQlOoikJ6LdVxVF_WJ1gVuTEjtw
