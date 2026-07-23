
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

