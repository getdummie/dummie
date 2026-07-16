# Single source of truth for all VMs. Add a VM = add an attribute here.
# Paths to kernel/rootfs are referenced at RUNTIME (not pinned into the Nix
# store) because they're external artifacts you gave as absolute paths.
{
  web = {
    cpu = 2;
    mem = 1024; # MiB
    diskSize = "4G"; # rootfs image size

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

    kernel = "/home/cc/Projects/.personal/dummie-v2/kernel/linux-v7.1.3/arch/x86/boot/bzImage";
    rootfsTar = "/home/cc/Projects/.personal/dummie-v2/images/debian-systemd/debian-systemd-rootfs.tar";

    # Host directories shared into the guest over virtio-9p. Each gets an
    # fstab entry (nofail) + mountpoint baked into the image, so it mounts
    # automatically at boot.
    shares = [
      {
        tag = "app";
        source = "/home/cc/Projects/.personal/dummie-v2/sample";
        mountPoint = "/home/ubuntu/app";
      }
    ];

    # User created in the guest image. uid/gid MUST match the owner of the
    # shared dir on the host (9p security_model=none surfaces the host's
    # numeric uid/gid), so the mount shows up as owned by this user.
    guestUser = {
      name = "ubuntu";
      uid = 1000;
      gid = 1000;
      home = "/home/ubuntu";
    };

    # Command run at every boot, as guestUser, from bootWorkingDir.
    bootCommand = "python3 main.py";
    bootWorkingDir = "/home/ubuntu/app";
  };
}
