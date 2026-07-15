{ pkgs ? import <nixpkgs> { } }:

pkgs.mkShell {
  name = "linux-kernel-build";

  # Tools needed to configure and compile a Linux kernel (vmlinux/bzImage).
  nativeBuildInputs = with pkgs; [
    gnumake
    gcc
    bc            # kernel version arithmetic scripts
    flex          # Kconfig / build-system lexer
    bison         # Kconfig / build-system parser
    openssl       # module signing
    ncurses       # `make menuconfig` TUI
    pkg-config
    elfutils      # ELF handling (objtool, etc.)
    perl
    gmp
    libmpc
    mpfr
    xz
    zlib
    cpio          # needed if you build an initramfs
    fakeroot
    e2fsprogs
    lz4
  ];

  shellHook = ''
    echo "kernel build shell ready: $(gcc --version | head -1)"
  '';
}
