# dinit

The guest init. `dclient` copies this binary into every rootfs it builds from a
container tar and boots it as pid 1, so an image needs no init, no dhcp client
and no sshd of its own: a `rootfs.tar` exported from `debian:stable` or `alpine`
boots, gets its address and answers ssh.

## The contract with dclient

dclient installs the binary at `/sbin/dinit` and adds this to the kernel command
line:

```
init=/sbin/dinit dclient.ip=<addr> dclient.gw=<gateway> dclient.dns=<resolver>
```

The hostname is read from `systemd.hostname=`, which dclient already sets.
Nothing else is passed in, and dinit never calls home — the command line is the
whole configuration.

## What it does

1. Mounts `/proc`, `/sys` and `/dev`.
2. Configures `eth0`: the address as a `/32`, an on-link route to the gateway,
   then the default route via it. Writes `/etc/hostname`, `/etc/hosts` and
   `/etc/resolv.conf`, leaving symlinked files to whatever manages them.
3. **Handoff mode** — if the image ships systemd (`/lib/systemd/systemd` or
   `/usr/lib/systemd/systemd`), dinit `exec`s it and is gone. Those images bring
   up their own sshd, xrdp and the rest exactly as they did before dinit existed;
   all they gain is a network that is already up.
4. **Agent mode** — otherwise dinit stays pid 1: it reaps orphans, respawns a
   login shell on `/dev/console` (so `dclient vm console` works on any image),
   and serves ssh on `:22`.

Only systemd counts as a real init. busybox's `/sbin/init` brings up nothing
without an `/etc/inittab`, so alpine and friends are better served by the agent.

## The ssh server

- Authorizes the keys dclient injected: `/etc/dclient/authorized_keys` and
  root's `authorized_keys`. dproxy and dpipe connect as `root`.
- ed25519 host key, generated on first boot and kept at
  `/etc/dclient/ssh_host_ed25519_key`, so it survives a reboot of the same VM.
- Supports sessions (pty, shell, exec) and `direct-tcpip` forwarding.
- Does **not** implement the sftp subsystem, so `scp` and `sftp` do not work in
  agent mode. A bare image has no `scp` binary either, so file transfer needs an
  image that ships openssh — which is handoff mode anyway.

## How it reaches a host

Exactly the way dproxy and dpipe do. The control server names a release — a
version, or a download URL — and dclient installs it at `/usr/local/bin/dinit`
on its next connect. In precedence order:

1. `dinit_version` / `dinit_download_url` on the host's own page in
   `/admin/model/clients`
2. `dinit_download_url` fleet-wide in `/admin/model/settings`
3. otherwise the published release matching the control server's own version

It is not a service, so there is no unit and nothing is started: the binary only
has to be on disk when dclient builds a rootfs. Its digest is part of the rootfs
cache key, so moving dinit rebuilds those images as VMs are created from them.

## Building

```
just build   # CGO_ENABLED=0, static: it runs inside a guest with an unknown libc
just test
```

For local testing, point `dinit_download_url` at `bin/dinit` on an artifact
server, or drop the binary at `/usr/local/bin/dinit` on the qemu host by hand.
