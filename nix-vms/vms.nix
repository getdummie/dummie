let
  qemuHost = {
    cpu = 4;
    mem = 4096; # MiB
    diskSize = "20G"; # rootfs image size (sparse; guest images need room to build)

    ephemeral = true;

    gateway = "10.68.0.1";
    netmask = "255.255.0.0";
    dns = "1.1.1.1";

    kernel = "/home/cc/Projects/.personal/dummie-v2/kernel/linux-host-v7.2.2/arch/x86/boot/bzImage";
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
in
{
  qemu-host-1 = qemuHost // {
    tap = "vm-tap0";
    mac = "02:00:00:00:00:01";
    ip = "10.68.0.2";
  };

  qemu-host-2 = qemuHost // {
    tap = "vm-tap1";
    mac = "02:00:00:00:00:02";
    ip = "10.68.0.3";
  };

  # Runs dclient as an edge (only dproxy + dpipe), routing a domain to the hosts.
  fakeedge = qemuHost // {
    cpu = 1;
    mem = 2048;
    tap = "vm-tapedge";
    mac = "02:00:00:00:00:0b";
    ip = "10.68.0.11";
    bootCommand = "sudo sh -c 'mkdir -p /etc/dclient && (grep -qs ^control_url: /etc/dclient/config.yaml || { echo control_url: http://10.68.0.1:1323; echo role: edge; echo insecure: true; } > /etc/dclient/config.yaml)' && sudo ./dclient install";
  };
}
