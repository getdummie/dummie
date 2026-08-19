#!/usr/bin/env python3
"""Run python in a throwaway VM across several turns, then destroy it."""

import os
import sys
import textwrap
import time
from typing import NamedTuple
from uuid import UUID

import paramiko

from dummie import AuthenticatedClient
from dummie.api.vms import (
    get_vms_hosts,
    get_vms_id,
    get_vms_kernels,
    get_vms_osimages,
    post_vms,
    post_vms_id_destroy,
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


def connect_ssh() -> paramiko.SSHClient:
    """Connect once a command actually runs, not once the handshake succeeds.

    Every step here is transient while a guest comes up. The proxy routes by
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
        expect(post_vms_id_destroy.sync(UUID(vm.id), client=client), VmDTO)
        print(f"destroyed {vm.name}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
