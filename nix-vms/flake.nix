{
  description = "Fast-booting QEMU microvm VMs (custom kernel + Debian rootfs)";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      lib = nixpkgs.lib;

      vms = import ./vms.nix;
      mkLauncher = name: vm: import ./lib/launcher.nix { inherit pkgs name vm; };
      launchers = lib.mapAttrs mkLauncher vms;
      firstVm = builtins.head (builtins.attrNames vms);
    in
    {
      # `nix run .#<name>`        -> start VM in the foreground (Ctrl-a x to quit)
      # `nix run .#<name> -- stop`   / `-- status`
      packages.${system} = launchers // {
        default = launchers.${firstVm};
      };

      apps.${system} = lib.mapAttrs (name: l: {
        type = "app";
        program = "${l}/bin/microqemu-${name}";
      }) launchers;

      # Import in your host config for systemd-managed lifecycle.
      nixosModules.default = ./modules;
    };
}
