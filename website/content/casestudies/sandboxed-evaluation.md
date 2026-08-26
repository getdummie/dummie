---
number: "001"
title: Sandboxed evaluation
description: Run model-written Python inside a throwaway microVM across several turns, read each result back, then delete the VM.
date: 2026-08-19
readingTime: 8 min
topics:
  - evals
  - agents
  - ssh
  - python sdk
---

Run model-written Python inside a throwaway microVM over several turns, read each
result back, then delete the VM. This is the shape an eval harness or a coding
agent wants: state carries across turns because it is the same guest, and nothing
the guest does reaches the machine driving it.

The VM is created with a 10 minute TTL. The script deletes it in a `finally`, so
the TTL is only the backstop for the run that dies before it gets there — a killed
process, a lost network, a traceback in the harness itself.

Teardown is `DELETE /vms/{id}` rather than `POST /vms/{id}/destroy`. Both destroy
the guest and free the quota; the destroy leaves the record behind as `gone`, which
is what an operator wants and what a harness creating a VM per run does not.

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
- `DUMMIE_SSH_HOST` — the **qemu host**, where `dproxy` listens on port 22 and
  routes the session to a guest by the key it authenticated with. `10.68.0.2` for
  the local `nix-vms` host. `GET /vms/hosts` will not tell you this: `hostname` is
  what the host reported about itself, not necessarily anything you can dial.

## The script

```python [evaluate.py]
#!/usr/bin/env python3
"""Run python in a throwaway VM across several turns, then delete it."""

import os
import sys
import textwrap
import time
from http import HTTPStatus
from typing import NamedTuple
from uuid import UUID

import paramiko

from dummie import AuthenticatedClient
from dummie.api.vms import (
    delete_vms_id,
    get_vms_hosts,
    get_vms_id,
    get_vms_kernels,
    get_vms_osimages,
    post_vms,
)
from dummie.models import ArtifactList, CreateVMReq, HostList, VmDTO

BASE_URL = os.environ.get("DUMMIE_BASE_URL", "http://control:1323/api/v1")
SSH_HOST = os.environ.get("DUMMIE_SSH_HOST", "10.68.0.2")
SSH_PORT = int(os.environ.get("DUMMIE_SSH_PORT", "22"))
SSH_KEY = os.environ.get("DUMMIE_SSH_KEY", os.path.expanduser("~/.ssh/id_ed25519"))
SSH_USER = "ubuntu"

TTL_SECONDS = 600
BOOT_TIMEOUT = 180
SSH_TIMEOUT = 120
TURN_TIMEOUT = 60
DELETE_TIMEOUT = 60
WORKDIR = "/home/ubuntu/eval"

TURNS = [
    """
    from pathlib import Path
    Path("primes.txt").write_text("\\n".join(
        str(n) for n in range(2, 200)
        if all(n % d for d in range(2, int(n**0.5) + 1))
    ))
    print("wrote", Path("primes.txt").stat().st_size, "bytes")
    """,
    """
    from pathlib import Path
    primes = [int(line) for line in Path("primes.txt").read_text().split()]
    print("count:", len(primes))
    print("sum:", sum(primes))
    """,
    """
    import platform
    print("kernel:", platform.release())
    print("python:", platform.python_version())
    """,
]


class Result(NamedTuple):
    exit_status: int
    stdout: str
    stderr: str


def expect(result, want):
    if not isinstance(result, want):
        raise RuntimeError(f"api call failed: {result!r}")
    return result


def create_vm(client: AuthenticatedClient) -> VmDTO:
    hosts = expect(get_vms_hosts.sync(client=client), HostList)
    kernels = expect(get_vms_kernels.sync(client=client), ArtifactList)
    osimages = expect(get_vms_osimages.sync(client=client), ArtifactList)
    if not (hosts.items and kernels.items and osimages.items):
        raise RuntimeError("need a connected host, a kernel and an os image")

    req = CreateVMReq(
        name="amazing-jepsen",
        client_id=hosts.items[0].id,
        kernel_id=kernels.items[0].id,
        osimage_id=osimages.items[0].id,
        cpus=1,
        memory_mib=512,
        disk_size="2G",
        ttl_seconds=TTL_SECONDS,
    )
    return expect(post_vms.sync(client=client, body=req), VmDTO)


def wait_running(client: AuthenticatedClient, vm_id: str) -> VmDTO:
    deadline = time.monotonic() + BOOT_TIMEOUT
    status = "unknown"
    while time.monotonic() < deadline:
        vm = expect(get_vms_id.sync(UUID(vm_id), client=client), VmDTO)
        status = vm.status
        if status == "running":
            return vm
        if status == "failed":
            raise RuntimeError(f"vm failed to boot: {vm.last_error}")
        time.sleep(2)
    raise TimeoutError(f"vm was still {status!r} after {BOOT_TIMEOUT}s")


def delete_vm(client: AuthenticatedClient, vm_id: str) -> None:
    """Destroy the guest, drop the record, and wait for it to actually be gone.

    The 202 means the destroy reached the host, not that it finished. What the
    next run needs -- the quota, and the host's one dproxy entry per key -- is only
    released once the host confirms, so the wait is the useful part. A 204 is a VM
    that had nothing on a host, and is already gone by the time it returns.
    """
    res = delete_vms_id.sync_detailed(UUID(vm_id), client=client)
    if res.status_code not in (HTTPStatus.ACCEPTED, HTTPStatus.NO_CONTENT):
        raise RuntimeError(f"could not delete the vm: {res.parsed!r}")
    if res.status_code == HTTPStatus.NO_CONTENT:
        return

    deadline = time.monotonic() + DELETE_TIMEOUT
    while time.monotonic() < deadline:
        got = get_vms_id.sync_detailed(UUID(vm_id), client=client)
        if got.status_code == HTTPStatus.NOT_FOUND:
            return
        time.sleep(2)
    raise TimeoutError(f"the vm record was still there after {DELETE_TIMEOUT}s")


def connect_ssh() -> paramiko.SSHClient:
    """Connect once a command actually runs, not once the handshake succeeds.

    Every step here is transient while a guest comes up. dproxy routes by
    public key, so a session opened before its regenerated config lands
    authenticates and then dies at the first channel -- which is why the probe
    is part of the attempt rather than the first turn's problem.
    """
    key = paramiko.PKey.from_path(SSH_KEY)
    deadline = time.monotonic() + SSH_TIMEOUT
    last: Exception | None = None
    while time.monotonic() < deadline:
        client = paramiko.SSHClient()
        # The guest's host key is new for every VM, so there is nothing to
        # remember; no known_hosts is loaded, so AutoAdd stays in this process.
        client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        try:
            client.connect(
                hostname=SSH_HOST,
                port=SSH_PORT,
                username=SSH_USER,
                pkey=key,
                look_for_keys=False,
                allow_agent=False,
                timeout=10,
                banner_timeout=10,
                auth_timeout=10,
            )
            _, stdout, _ = client.exec_command("true", timeout=10)
            if stdout.channel.recv_exit_status() != 0:
                raise paramiko.SSHException("probe command did not run")
            return client
        except (paramiko.SSHException, EOFError, OSError) as err:
            last = err
            client.close()
            time.sleep(2)
    raise TimeoutError(f"no usable ssh to the guest after {SSH_TIMEOUT}s: {last!r}")


def run_turn(ssh: paramiko.SSHClient, n: int, code: str) -> Result:
    stdin, stdout, stderr = ssh.exec_command(
        f"mkdir -p {WORKDIR} && cd {WORKDIR} && python3 -", timeout=TURN_TIMEOUT
    )
    stdin.write(textwrap.dedent(code))
    stdin.channel.shutdown_write()

    out = stdout.read().decode()
    err = stderr.read().decode()
    result = Result(stdout.channel.recv_exit_status(), out, err)

    print(f"--- turn {n} (exit {result.exit_status})")
    if out:
        print(out, end="")
    if err:
        print(err, end="", file=sys.stderr)
    return result


def main() -> int:
    client = AuthenticatedClient(
        base_url=BASE_URL,
        token=os.environ["DUMMIE_TOKEN"],
        raise_on_unexpected_status=True,
    )

    started = time.monotonic()
    vm = create_vm(client)
    print(f"created {vm.name} ({vm.id}), ttl {TTL_SECONDS}s")
    try:
        vm = wait_running(client, vm.id)
        booted = time.monotonic()
        print(f"running at {vm.ip} after {booted - started:.1f}s")
        # One connection for every turn, so a turn's cost is a channel rather
        # than a handshake, and the guest sees a single session throughout.
        ssh = connect_ssh()
        print(
            f"ssh ready after a further {time.monotonic() - booted:.1f}s"
            f" ({time.monotonic() - started:.1f}s since create)"
        )
        try:
            for n, code in enumerate(TURNS, start=1):
                if run_turn(ssh, n, code).exit_status != 0:
                    print("turn failed; stopping", file=sys.stderr)
                    break
        finally:
            ssh.close()
    finally:
        delete_vm(client, vm.id)
        print(f"deleted {vm.name}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
```

## Running it

Save the block above as `evaluate.py` somewhere the sdk container can see it —
the repo keeps no copy of its own, this page is the source — then:

```sh
docker compose exec control-sdk python evaluate.py
```

Mount your private key into the container, or run it on the host with
`DUMMIE_BASE_URL=http://localhost:1323/api/v1`.

## Things that will bite you

**One VM per user per host.** `dproxy` routes SSH by public key alone, and the
control plane writes one entry per VM keyed by its owner's key
(`control/proxy_config.go:246-251`). Two live VMs owned by you on the same host
produce two entries with the same key, and `dproxy` rejects the whole config as a
duplicate rather than picking one (`backstage/dproxy/internal/proxy/resolver.go:48-50`).
So this script deletes its VM, and waits for the record to go, before another run
creates one. Parallel evaluation needs either a host each or a routing key that is
not just the owner.

**The guest has no egress.** A VM is created with an empty allowlist, so `pip
install` and any other fetch is refused. Add what a turn genuinely needs with
`POST /vms/{id}/targets`, which takes its own `ttl_seconds` — pass `targets` in the
create call to have it in force before the guest is up, rather than as a follow-up
the boot can race. `GET /vms/{id}/denied` shows what a turn tried to reach.

**`202` is not "booted".** The create returns `pending`; the host reports the real
outcome over its own socket afterwards, which is what `wait_running` is polling
for. And `running` is the host's claim as of `reported_at`, not a live check: it
says qemu started, not that sshd is listening or that the host's regenerated dproxy
config has landed. Both of those accept a connection before they can serve one, so
`connect_ssh` runs a throwaway command and only counts the session as ready once
that succeeds. Retrying the handshake alone is not enough — a session opened too
early authenticates fine and then dies with `EOFError` at the first channel.

**Only a destroy or a delete frees quota.** Stopping a VM keeps its disk and its
allowance, and the TTL keeps running while it is stopped.

**A delete is `202` too.** `DELETE /vms/{id}` sends the same destroy job to the
host, so the record survives until the host confirms — and if the destroy fails, so
does the VM, carrying the reason in `last_error`. That is why `delete_vm` polls for
the `404` rather than treating the `202` as the end of the run.
