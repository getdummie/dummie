let
  # Resolved by the launcher at runtime, so generated kernel/rootfs files and
  # the live source checkout do not need to be copied into the Nix store.
  root = "$DUMMIE_REPO_ROOT";
in
{
  qemu-host = {
    cpu = 4;
    mem = 4096; # MiB
    diskSize = "20G"; # rootfs image size (sparse; guest images need room to build)

    ephemeral = false;
    seedTar = "${root}/nix-vms/.microqemu/qemu-host/enrollment-seed.tar";

    tap = "vm-tap0";
    networkMode = "user";
    sshForwardPort = 2223;
    mac = "02:00:00:00:00:01";
    ip = "10.68.0.2";
    gateway = "10.68.0.1";
    netmask = "255.255.0.0";
    dns = "1.1.1.1";

    kernel = "${root}/vm-artifacts/host-vmlinuz";
    initrd = "${root}/vm-artifacts/host-initrd.img";
    rootfsTar = "${root}/images/debian-vm-host/rootfs.tar";

    shares = [
      {
        tag = "app";
        source = "${root}/control";
        mountPoint = "/home/ubuntu/app";
      }
    ];

    guestUser = {
      name = "ubuntu";
      uid = 1000;
      gid = 1000;
      home = "/home/ubuntu";
    };

    bootCommand = "sudo ./dclient install";
    bootWorkingDir = "/home/ubuntu/app/tmp";

    bootEnv.PATH = "/home/ubuntu/.bun/bin:/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin";
  };
}
