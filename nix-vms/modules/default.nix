# NixOS module: turns the vms.nix attrset into one systemd service per VM,
# plus the host-side tap device for each. Import via the flake's
# nixosModules.default in your host configuration, then it's managed with:
#   systemctl {start,stop,status} microqemu-<name>
{ config, lib, pkgs, ... }:

let
  cfg = config.services.microqemu;
  mkLauncher = name: vm: import ../lib/launcher.nix { inherit pkgs name vm; };
in
{
  options.services.microqemu = {
    enable = lib.mkEnableOption "microqemu VMs";

    tapUser = lib.mkOption {
      type = lib.types.str;
      default = "cc";
      description = "User allowed to open the tap devices (QEMU runs as root here, but keep consistent).";
    };

    instances = lib.mkOption {
      type = lib.types.attrsOf (lib.types.attrsOf lib.types.anything);
      default = import ../vms.nix;
      description = "VM definitions (defaults to vms.nix).";
    };
  };

  config = lib.mkIf cfg.enable {
    # Host owns the taps now. Remove any manually-added vm-tap0 netdev/network
    # from your host config to avoid a duplicate definition.
    systemd.network.enable = true;

    systemd.network.netdevs = lib.mapAttrs' (
      name: vm:
      lib.nameValuePair "25-${vm.tap}" {
        netdevConfig = {
          Name = vm.tap;
          Kind = "tap";
        };
        tapConfig = {
          User = cfg.tapUser;
          Group = "users";
          MultiQueue = vm.cpu > 1;
        };
      }
    ) cfg.instances;

    systemd.network.networks = lib.mapAttrs' (
      name: vm:
      lib.nameValuePair "25-${vm.tap}" {
        matchConfig.Name = vm.tap;
        address = [ "${vm.gateway}/16" ];
      }
    ) cfg.instances;

    systemd.services = lib.mapAttrs' (
      name: vm:
      let
        launcher = mkLauncher name vm;
      in
      lib.nameValuePair "microqemu-${name}" {
        description = "microqemu VM: ${name}";
        after = [
          "network.target"
          "systemd-networkd.service"
        ];
        wantedBy = [ "multi-user.target" ];
        serviceConfig = {
          ExecStart = "${launcher}/bin/microqemu-${name} start";
          ExecStop = "${launcher}/bin/microqemu-${name} stop";
          StateDirectory = "microqemu/${name}";
          Environment = "MICROQEMU_STATE_DIR=%S/microqemu/${name}";
          TimeoutStopSec = 30;
          Restart = "no";
        };
      }
    ) cfg.instances;
  };
}
