# 001 — sandboxed evaluation

Run model-written Python inside a throwaway microVM over several turns, read each
result back, then destroy the VM. This is the shape an eval harness or a coding
agent wants: state carries across turns because it is the same guest, and nothing
the guest does reaches the machine driving it.

The VM is created with a 10 minute TTL. The script destroys it in a `finally`, so
the TTL is only the backstop for the run that dies before it gets there — a killed
process, a lost network, a traceback in the harness itself.

## What it needs

- A personal access token (Settings, or `POST /me/tokens`), as `DUMMIE_TOKEN`.
- An SSH public key on your account. It is baked into the image at boot, so it
  cannot be added to a VM afterwards — set it before creating anything.
- The matching private key readable by the script.
- The generated SDK installed: `just sdk-python && just sdk-install`.
- `paramiko`, 3.2 or newer for `PKey.from_path`. `Dockerfile.sdk.dev` pins 5.0.0.
- An unencrypted private key. Nothing here prompts for a passphrase, and the
  keyword that loads an encrypted one was renamed in paramiko 5.0 (`passphrase` →
  `password`), so hardcoding it would break across the versions this runs on.

Two addresses are involved and only one of them comes from the API:

- `DUMMIE_BASE_URL` — the control API, `http://control:1323/api/v1` from the sdk
  container.
- `DUMMIE_SSH_HOST` — the **qemu host**, where `proxy` listens on port 22 and
  routes the session to a guest by the key it authenticated with. `10.68.0.2` for
  the local `nix-vms` host. `GET /vms/hosts` will not tell you this: `hostname` is
  what the host reported about itself, not necessarily anything you can dial.

## Running it

```sh
docker compose exec control-sdk python casestudies/001-sandboxed-evaluation/evaluate.py
```

Mount your private key into the container, or run it on the host with
`DUMMIE_BASE_URL=http://localhost:1323/api/v1`.


## Things that will bite you

**One VM per user per host.** `proxy` routes SSH by public key alone, and the
control plane writes one entry per VM keyed by its owner's key
(`control/proxy_config.go:246-251`). Two live VMs owned by you on the same host
produce two entries with the same key, and `proxy` rejects the whole config as a
duplicate rather than picking one (`backstage/proxy/internal/proxy/resolver.go:48-50`).
So this script destroys its VM before another run creates one. Parallel evaluation
needs either a host each or a routing key that is not just the owner.

**The guest has no egress.** A VM is created with an empty allowlist, so `pip
install` and any other fetch is refused. Add what a turn genuinely needs with
`POST /vms/{id}/targets`, which takes its own `ttl_seconds` — pass `targets` in the
create call to have it in force before the guest is up, rather than as a follow-up
the boot can race. `GET /vms/{id}/denied` shows what a turn tried to reach.

**`202` is not "booted".** The create returns `pending`; the host reports the real
outcome over its own socket afterwards, which is what `wait_running` is polling
for. And `running` is the host's claim as of `reported_at`, not a live check: it
says qemu started, not that sshd is listening or that the host's regenerated proxy
config has landed. Both of those accept a connection before they can serve one, so
`connect_ssh` runs a throwaway command and only counts the session as ready once
that succeeds. Retrying the handshake alone is not enough — a session opened too
early authenticates fine and then dies with `EOFError` at the first channel.

**A destroy is the only thing that frees quota.** Stopping a VM keeps its disk and
its allowance, and the TTL keeps running while it is stopped.
