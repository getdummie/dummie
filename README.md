
# Setup

## Compile a kernel

```sh
cd kernel
nix-shell
git clone --depth 1 --branch v7.1.3 https://git.kernel.org/pub/scm/linux/kernel/git/stable/linux.git linux-v7.1.3
cd linux-v7.1.3
make kernelversion
make defconfig
../optimize.sh
make olddefconfig
make -j"$(nproc)" bzImage
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
journalctl -u microqemu-boot --no-pager
```

## Control Server

For rustfs

```sh
mkdir -p rustfs-data rustfs-logs
sudo chown -R 10001:10001 ./rustfs-data ./rustfs-logs
```

