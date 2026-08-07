# Single source of truth for all VMs. Add a VM = add an attribute here.
# Paths to kernel/rootfs are referenced at RUNTIME (not pinned into the Nix
# store) because they're external artifacts you gave as absolute paths.

# min 4G disk always req
# 2G RAM, 8G disk for running container
{
  # edge = {
  #   cpu = 1;
  #   mem = 512;
  #   diskSize = "2G";
  #
  #   ephemeral = true;
  #
  #   # host tap device + addressing (host side gets <gateway>/16)
  #   tap = "vm-tap1";
  #   mac = "02:00:00:00:00:01";
  #   ip = "10.68.0.3";
  #   gateway = "10.68.0.1";
  #   netmask = "255.255.0.0";
  #   dns = "1.1.1.1";
  #
  #   kernel = "/home/cc/Projects/.personal/dummie-v2/kernel/linux-v7.1.4/arch/x86/boot/bzImage";
  #   rootfsTar = "/home/cc/Projects/.personal/dummie-v2/images/debian-vm-host/rootfs.tar";
  #
  #   shares = [
  #     {
  #       tag = "app";
  #       source = "/home/cc/Projects/.personal/dummie-v2/backstage";
  #       mountPoint = "/home/ubuntu/backstage";
  #     }
  #   ];
  #
  #   guestUser = {
  #     name = "ubuntu";
  #     uid = 1000;
  #     gid = 100; # 100 = users, matching host `cc`'s primary group
  #     home = "/home/ubuntu";
  #   };
  #
  #   # Command run at every boot, as guestUser, from bootWorkingDir.
  #   bootCommand = "sudo ./dagent install";
  #   bootWorkingDir = "/home/ubuntu/app/tmp";
  #
  #   bootEnv.PATH = "/home/ubuntu/.bun/bin:/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin";
  # };
  qemu-host = {
    cpu = 4;
    mem = 1024; # MiB
    diskSize = "6G"; # rootfs image size

    # true  = pristine root every boot, changes discarded on shutdown
    # false = persistent root (writes survive reboots)
    ephemeral = true;

    # host tap device + addressing (host side gets <gateway>/16)
    tap = "vm-tap0";
    mac = "02:00:00:00:00:01";
    ip = "10.68.0.2";
    gateway = "10.68.0.1";
    netmask = "255.255.0.0";
    dns = "1.1.1.1";

    kernel = "/home/cc/Projects/.personal/dummie-v2/kernel/linux-v7.1.4/arch/x86/boot/bzImage";
    rootfsTar = "/home/cc/Projects/.personal/dummie-v2/images/debian-vm-host/rootfs.tar";

    # Host directories shared into the guest over virtiofs. Each gets an
    # fstab entry (nofail) + mountpoint baked into the image, so it mounts
    # automatically at boot.
    shares = [
      {
        tag = "app";
        source = "/home/cc/Projects/.personal/dummie-v2/control";
        mountPoint = "/home/ubuntu/app";
      }
      {
        tag = "pipe";
        source = "/home/cc/Projects/.personal/dummie-v2/backstage";
        mountPoint = "/home/ubuntu/backstage";
      }
    ];

    # Guest user identity. uid AND gid MUST match the host owner of the shared
    # dir: virtiofsd runs unprivileged as that host user and does guest file ops
    # under the caller's uid/gid, so it can only assume ids that host user has.
    # Host `cc` is uid 1000, gid 100 (users); a gid mismatch ⇒ creates fail with
    # EPERM even though reads work.
    #   NB: the launcher only *creates* this user when it's absent from the
    #   rootfsTar. This image already ships `ubuntu`, so its uid/gid come from
    #   the tarball's /etc/passwd (fix them there too); here gid only drives the
    #   home-dir chown, which must stay in sync with the tarball.
    guestUser = {
      name = "ubuntu";
      uid = 1000;
      gid = 100; # 100 = users, matching host `cc`'s primary group
      home = "/home/ubuntu";
    };

    # Command run at every boot, as guestUser, from bootWorkingDir.
    bootCommand = "sudo ./dagent install";
    bootWorkingDir = "/home/ubuntu/app/tmp";

    # Extra env for the boot service. systemd's PATH is bare, so tools installed
    # by the go-bun-dev image (air/sqlc/swag in /go/bin, go in /usr/local/go/bin,
    # bun in ~/.bun/bin) — only on PATH via the interactive shell's .bashrc — are
    # invisible to the boot command unless we set PATH here explicitly.
    bootEnv.PATH = "/home/ubuntu/.bun/bin:/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin";
  };
}
