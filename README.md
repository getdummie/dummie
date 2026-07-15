
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

## Build a base debian image

```sh
just run
cd nix/
rm -rf .microqemu/web/console.log .microqemu/web/rootfs.ext4
nix run
ssh-keygen -R 10.68.0.2
ssh ubuntu@10.68.0.2
```

## Check process running in VM

```sh
systemctl status microqemu-boot
journalctl -u microqemu-boot --no-pager
```
