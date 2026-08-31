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

The image's own configuration is too large and too quote-hostile for a command
line, so dclient writes it into the rootfs instead, at
`/etc/dclient/image.json`, mode `0600`:

```json
{
  "user": "appuser",
  "entrypoint": [],
  "cmd": ["sh", "-c", "exec marimo edit --no-token -p $PORT --host $HOST"],
  "env": ["PATH=/usr/local/bin:/usr/bin:/bin", "PORT=8080", "HOST=0.0.0.0"]
}
```

That is the whole configuration: the command line and that file. dinit never
calls home. The file is optional — without it a guest behaves exactly as it did
before it existed: root, and no workload.

It comes from the os image row in the control server, which records what the
container image declared at build time (`docker export` keeps none of it) and
lets an admin correct it on `/admin/model/osimages/{id}`. It is copied into a
VM's spec when the VM is created, and it is part of the rootfs cache key, so an
edit rebuilds the rootfs for VMs created after it — a VM that already exists
keeps the configuration it was built with.

## What it does

1. Mounts `/proc`, `/sys` and `/dev`.
2. Brings up `lo`. The kernel leaves loopback down and nothing in a bare image
   raises it, so without this `127.0.0.1` belongs to no interface and anything
   binding to it fails with `EADDRNOTAVAIL` — most databases, and python's
   `multiprocessing`. It happens even for a VM with no network at all.
3. Configures `eth0`: the address as a `/32`, an on-link route to the gateway,
   then the default route via it. Writes `/etc/hostname`, `/etc/hosts` and
   `/etc/resolv.conf`, leaving symlinked files to whatever manages them.
4. **Handoff mode** — if the image ships systemd (`/lib/systemd/systemd` or
   `/usr/lib/systemd/systemd`), dinit `exec`s it and is gone. Those images bring
   up their own sshd, xrdp and the rest exactly as they did before dinit existed;
   all they gain is a network that is already up.
5. **Agent mode** — otherwise dinit stays pid 1: it reaps orphans, respawns a
   login shell on `/dev/console` (so `dclient vm console` works on any image),
   serves ssh on `:22`, and runs the image's workload.

Only systemd counts as a real init. busybox's `/sbin/init` brings up nothing
without an `/etc/inittab`, so alpine and friends are better served by the agent.

Handoff mode ignores `image.json` entirely: those images run their own services
under their own init, and dinit is gone before any of it would apply.

## The image's user, environment and workload — agent mode

- **The user** is `user` from the config, in any form docker accepts: a name, a
  numeric uid, or either with a group after a colon (`appuser`, `1000`,
  `appuser:appgroup`, `1000:1000`). A numeric id needs no `/etc/passwd` entry,
  so a scratch image works. An image that declares no user, or one that does not
  resolve, stays on root as before. It applies to the console shell and to the
  workload; an ssh session is still whoever the client asked to be, since dproxy
  and dpipe connect as `root`.
- **The environment** is applied everywhere a process can start: the workload,
  the console shell and every ssh session. `PATH` from the image wins over the
  built-in one — it is the one its binaries were installed against — and a
  client-pushed ssh `env` request still wins over the image. For logins and
  binaries dinit does not spawn itself, the variables are also written to
  `/etc/environment` and `/etc/profile.d/dinit-image.sh`.
- **The workload** is the entrypoint with the command as its arguments, which is
  what docker runs when both are set. It runs as the image's user, from that
  user's home, and it is **restarted when it exits** with a 1s→30s exponential
  backoff that resets once it has stayed up a minute. Its stdout and stderr go
  to `/var/log/dinit-workload.log`, not the console: the console is a shell an
  operator is using, and interleaving an app's output into it makes both
  unusable. Nothing is reported back to the host, so a crash loop is visible
  only in that log and in the console log.

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
