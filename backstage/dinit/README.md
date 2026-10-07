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

## The coding agent — `dinit agent`

The console's agent tab (`wss://<vm>.shell.<tld>/agent`) is dpipe running
`/sbin/dinit agent attach --tld <tld>` over ssh as the VM's session user. It is
not pid 1 work, and it works the same in handoff and agent mode.

- `attach` starts `dinit agent serve` if it is not running (setsid, logging to
  `~/.dummie/agent.log`), then relays frames between its stdio and the daemon's
  unix socket at `~/.dummie/agent-<version>-<inode>.sock` (mode 0600). A frame is one
  opcode byte (1 text, 2 binary), a big-endian uint32 length, then the payload.
- Before that it points pi at the fleet's llm proxy: the `dummie` and
  `dummie-chatgpt` providers in `~/.pi/agent/models.json`, from
  `llm.int.<tld>/v1/models`. Nothing else in that file is touched. It also
  records the proxy in `~/.dummie/llm.json`, and removes that file when the
  proxy cannot be reached, so the other harnesses fall back to their own logins.
- `serve` drives five harnesses, each in its own protocol, and forwards their
  events to the browser as they are; the browser turns them into a chat.

  | harness  | process                                | sessions listed from                       | on the proxy                                      |
  | -------- | -------------------------------------- | ------------------------------------------ | ------------------------------------------------- |
  | pi       | `pi --mode rpc` per session            | `~/.pi/agent/sessions`                     | models.json, above                                |
  | claude   | `claude -p` in stream-json per session | `~/.claude/projects`                       | `ANTHROPIC_BASE_URL`, chat models                 |
  | codex    | `codex app-server` per session         | `~/.codex/sessions`                        | `-c model_providers.dummie`, chatgpt models only  |
  | opencode | one `opencode serve` on loopback       | its server's api                           | `OPENCODE_CONFIG_CONTENT`, both providers         |
  | gemini   | `gemini --experimental-acp` per session| `~/.gemini/tmp`, by hashing known dirs     | never; the proxy has no gemini api                |

  The proxy settings go into the child's environment or flags only; no
  harness's own config file is written. Every harness runs without asking for
  permission (the vm is the sandbox), and any question it still asks is
  answered yes. Only pi steers a running turn; a prompt sent to the others
  mid-turn waits for the turn to end.
  ssh runs attach without a login shell, so the daemon puts `~/.bun/bin`,
  `~/.local/bin`, `~/.opencode/bin` and `~/.npm-global/bin` ahead of the
  image's `PATH`; a harness installed there as the user is found.
- It polls `git` for the diff pane of every directory a tab is watching, and
  stores uploads under `~/.dummie/uploads`. It stops an agent left unused for
  15 minutes and exits after 30 minutes with no tab and no agent at work, so
  closing the tab does not stop a running agent.

The socket is named after the binary, its version and its inode, since a
local build always says `dev` and an upgrade renames a new file into place.
After an upgrade the next tab starts a new daemon and tells the old one to
retire: it exits as soon as none of its agents is mid-turn.

## Upgrading in place — `dinit upgrade`

A VM's rootfs keeps the dinit it was built with. `sudo /sbin/dinit upgrade`
replaces `/sbin/dinit` inside the guest; the write lands in the VM's own
overlay, so it survives restarts.

```
sudo /sbin/dinit upgrade                       # latest github.com/getdummie/dummie release
sudo /sbin/dinit upgrade --version 0.0.37      # a given release
sudo /sbin/dinit upgrade --source https://example.com/dinit.tar.gz --sha256 <hex>
sudo /sbin/dinit upgrade --source /tmp/dinit   # a binary already copied in
```

Release downloads are checked against the release's `checksums.txt`. A
`--source` is checked only when `--sha256` is given. Either way the binary must
be a static ELF for the VM's architecture, and it is renamed into place so a
boot never sees half a file. The agent picks it up on the next tab; pid 1 on the
next boot.

## Building

```
just build   # CGO_ENABLED=0, static: it runs inside a guest with an unknown libc
just test
```

For local testing, point `dinit_download_url` at `bin/dinit` on an artifact
server, or drop the binary at `/usr/local/bin/dinit` on the qemu host by hand.
