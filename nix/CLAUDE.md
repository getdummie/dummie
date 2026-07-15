# CLAUDE.md

Nix flake that runs fast-booting QEMU microVMs: the `microvm` machine type with
a custom kernel + a Debian rootfs (from a tarball), managed like lightweight
containers. Not `microvm.nix` — that's NixOS-guest only; this drives QEMU directly.

## Layout
- `vms.nix` — single source of truth: one attr per VM (data only).
- `lib/launcher.nix` — builds each VM's launcher script (start/stop/status/console/stats)
  and the offline rootfs-image builder.
- `modules/default.nix` — NixOS module: one systemd service + tap per VM (host lifecycle).
- `flake.nix` — wires it up: `packages`/`apps` per VM + `nixosModules.default`.

## Commands (user runs these — never run nix/qemu yourself)
- `nix run` / `nix run .#<vm>` — start (daemonized, no console attached)
- `nix run .#<vm> -- {stop|status|console|stats}`
- Managed: `systemctl {start,stop,status} microqemu-<vm>` via the module.

## Per-VM config (`vms.nix`)
cpu, mem, diskSize, ephemeral; tap/mac/ip/gateway/netmask/dns; kernel + rootfsTar
(absolute host paths, referenced at runtime, NOT pinned in the store); optional
`shares` (virtio-9p host dirs), `guestUser`, `bootCommand`/`bootWorkingDir`.

## Key behaviors / gotchas
- **Networking**: routed tap `vm-tap0` (host `10.68.0.1/16`, guest `10.68.0.2`),
  static via kernel `ip=`. Host must provide the tap + NAT (host NixOS config).
- **Tap queue mode must match vCPU count** (multi-queue iff cpu>1) or QEMU EINVALs.
- **Transport is virtio-pci** (`-machine microvm,acpi=on,pcie=on`); kernel needs
  `CONFIG_VIRTIO_PCI`. Nested virt needs `CONFIG_KVM_AMD` + host `-cpu host`.
- **Image is built once** from `rootfsTar` (fakeroot; bakes DNS, shares' fstab,
  guestUser, boot service). Config changes that affect the image need a rebuild:
  `nix run .#<vm> -- stop && rm <state>/rootfs.ext4 && nix run`.
- **State dir**: `./.microqemu/<vm>/` (manual) or `/var/lib/microqemu/<vm>/` (systemd).
- **ephemeral=true**: root writes go to a disposable qcow2 overlay (in `$TMPDIR`=state
  dir), discarded on stop; base image stays pristine. Persist data via a `share`.
- **Shutdown**: in-guest `poweroff` hangs (kernel ACPI); use `systemctl reboot`
  (clean, `-no-reboot` exits QEMU) or host `stop` (powerdown→quit→kill fallback).
- 9p `security_model=none` shows host uid/gid; `guestUser.uid` must match the
  share owner on the host.

## Constraints
- Per global instructions: do not run nix/qemu/etc. — provide commands for the user.
- shellcheck runs on the launcher (`writeShellApplication`); escape shell `${...}`
  as `''${...}` inside the Nix string.
