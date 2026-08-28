
{
  qemu-host = {
    cpu = 4;
    mem = 4096; # MiB
    diskSize = "20G"; # rootfs image size (sparse; guest images need room to build)

    ephemeral = true;

    tap = "vm-tap0";
    mac = "02:00:00:00:00:01";
    ip = "10.68.0.2";
    gateway = "10.68.0.1";
    netmask = "255.255.0.0";
    dns = "1.1.1.1";

    kernel = "/home/cc/Projects/.personal/dummie-v2/kernel/linux-v7.1.4/arch/x86/boot/bzImage";
    rootfsTar = "/home/cc/Projects/.personal/dummie-v2/images/debian-vm-host/rootfs.tar";

    shares = [
      {
        tag = "app";
        source = "/home/cc/Projects/.personal/dummie-v2/control";
        mountPoint = "/home/ubuntu/app";
      }
    ];

    guestUser = {
      name = "ubuntu";
      uid = 1000;
      gid = 100; # 100 = users, matching host `cc`'s primary group
      home = "/home/ubuntu";
    };

    bootCommand = "sudo ./dclient install";
    bootWorkingDir = "/home/ubuntu/app/tmp";

    bootEnv.PATH = "/home/ubuntu/.bun/bin:/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin";
  };
}
