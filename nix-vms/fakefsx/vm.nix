{ lib, modulesPath, pkgs, ... }:

let
  fakefsx = pkgs.buildGoModule {
    pname = "fakefsx";
    version = "0";
    src = ./.;
    vendorHash = null;
  };
  pool = "fsx";
  poolFile = "/var/lib/fakefsx/pool.img";
  ip = "10.68.0.10";
in
{
  imports = [ "${modulesPath}/virtualisation/qemu-vm.nix" ];

  system.stateVersion = "25.11";

  networking.hostName = "fakefsx";
  networking.hostId = "f5f5f5f5";
  boot.supportedFilesystems = [ "zfs" ];
  boot.zfs.forceImportRoot = false;

  virtualisation = {
    cores = 2;
    memorySize = 2048;
    # Sparse qcow2: only what the pool actually writes takes space on the host.
    diskSize = 24 * 1024;
    graphics = false;
    qemu.networkingOptions = lib.mkForce [
      "-netdev tap,id=net0,ifname=vm-tapfsx,script=no,downscript=no"
      "-device virtio-net-pci,netdev=net0,mac=02:00:00:00:00:0a"
    ];
  };

  networking.usePredictableInterfaceNames = false;
  networking.useDHCP = false;
  networking.interfaces.eth0.ipv4.addresses = [
    {
      address = ip;
      prefixLength = 16;
    }
  ];
  networking.defaultGateway = "10.68.0.1";
  networking.nameservers = [ "1.1.1.1" ];
  networking.firewall.allowedTCPPorts = [
    2049
    8080
  ];

  services.nfs.server.enable = true;

  users.users.root.password = "root";
  services.getty.autologinUser = "root";
  services.openssh = {
    enable = true;
    settings.PermitRootLogin = "prohibit-password";
    settings.PasswordAuthentication = false;
  };

  systemd.services.root-ssh-keys = {
    description = "fetch root's ssh keys from github";
    wantedBy = [ "multi-user.target" ];
    after = [ "network-online.target" ];
    wants = [ "network-online.target" ];
    path = [
      pkgs.curl
      pkgs.coreutils
    ];
    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      Restart = "on-failure";
      RestartSec = 5;
    };
    script = ''
      install -d -m 700 /root/.ssh
      curl -fsSL --retry 5 https://github.com/codingcoffee.keys -o /root/.ssh/authorized_keys.tmp
      chmod 600 /root/.ssh/authorized_keys.tmp
      mv /root/.ssh/authorized_keys.tmp /root/.ssh/authorized_keys
    '';
  };

  environment.systemPackages = [ pkgs.zfs ];

  systemd.services.fakefsx-pool = {
    description = "file-backed zpool for fakefsx";
    wantedBy = [ "multi-user.target" ];
    before = [ "fakefsx.service" ];
    after = [ "nfs-server.service" ];
    path = [
      pkgs.zfs
      pkgs.coreutils
    ];
    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
    };
    script = ''
      if zpool list ${pool} >/dev/null 2>&1; then exit 0; fi
      if [ -e ${poolFile} ]; then
        zpool import -d ${dirOf poolFile} ${pool}
      else
        mkdir -p ${dirOf poolFile}
        truncate -s 20G ${poolFile}
        zpool create -o ashift=12 -O mountpoint=/fsx -O compression=lz4 \
          -O xattr=sa -O acltype=posixacl ${pool} ${poolFile}
      fi
      zfs share -a
    '';
  };

  systemd.services.fakefsx = {
    description = "fake FSx for OpenZFS API";
    wantedBy = [ "multi-user.target" ];
    requires = [ "fakefsx-pool.service" ];
    after = [
      "fakefsx-pool.service"
      "network-online.target"
    ];
    wants = [ "network-online.target" ];
    path = [ pkgs.zfs ];
    serviceConfig = {
      ExecStart = "${fakefsx}/bin/fakefsx -listen ${ip}:8080 -pool ${pool} -dns ${ip}";
      Restart = "on-failure";
    };
  };
}
