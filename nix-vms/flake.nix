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

      fakefsx = nixpkgs.lib.nixosSystem {
        inherit system;
        modules = [ ./fakefsx/vm.nix ];
      };
      fakefsxLauncher = import ./fakefsx/launcher.nix {
        inherit pkgs;
        vm = fakefsx.config.system.build.vm;
      };
    in
    {
      packages.${system} = launchers // {
        default = launchers.${firstVm};
        fakefsx = fakefsxLauncher;
      };

      apps.${system} = (lib.mapAttrs (name: l: {
        type = "app";
        program = "${l}/bin/microqemu-${name}";
      }) launchers) // {
        fakefsx = {
          type = "app";
          program = "${fakefsxLauncher}/bin/microqemu-fakefsx";
        };
      };

      nixosConfigurations.fakefsx = fakefsx;
      nixosModules.default = ./modules;
    };
}
