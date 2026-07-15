
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

