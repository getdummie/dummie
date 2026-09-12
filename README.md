<p align="center">
  <img src="website/public/logo.svg" alt="dummie" width="52" height="52">
</p>

<h2 align="center">dummie</h2>

<p align="center">
  Secure computers for everyone.<br>
  VM booted in seconds, sealed off from everything else,<br>
  and yours for as long as you need it.
</p>

<p align="center">
  <a href="#architecture">Architecture</a> ·
  <a href="#repository-layout">Layout</a> ·
  <a href="#license">License</a>
</p>

---

## What this is

dummie hands out sandboxes that are virtual machines. Every guest gets its own
kernel, its own filesystem and its own network stack, so code you did not write
and cannot trust has nothing to escape into. The kernel is built in-tree and
stripped to what a short-lived guest needs, which is what makes cold boot fast
enough to sit in a request path.

It is self-hostable end to end: a control plane you run once, and as many hosts
as you want behind it.

- **Hardware isolation, not namespaces.** A sandbox is a microVM, not a container.
- **Per-run network policy.** Pick the domains and ports you want to whitelist
  for your VM
- **One binary to deploy.** Single binary deploy.

## Architecture

```
                        ┌──────────────────────────┐
  browser / SDK ──────► │  control  (Go binary)    │
                        │  API + console + spec    │
                        └────────────┬─────────────┘
                                     │  assigns work
                         ┌───────────┴───────────┐
                         ▼                       ▼
                 ┌───────────────┐       ┌───────────────┐
                 │ QEMU host     │       │ QEMU host     │
                 │  dclient      │       │  dclient      │
                 │  dproxy/dpipe │       │  dproxy/dpipe │
                 │  suricata     │       │  suricata     │
                 │  ┌─────────┐  │       │  ┌─────────┐  │
                 │  │ microVM │  │       │  │ microVM │  │
                 │  └─────────┘  │       │  └─────────┘  │
                 └───────────────┘       └───────────────┘
```

The control plane never runs a guest. It records what should exist and hosts
reconcile toward it; a host that goes silent stops being given work, and the VMs
it was running are reaped by their TTL.

## Repository layout

Each directory is a project in its own right.

| Directory | What lives there |
| --- | --- |
| `control/` | The control plane: API, console, migrations, OpenAPI spec, generated SDK. `cmd/dclient/` builds the host agent. |
| `backstage/` | `dproxy` and `dpipe`, the connection services `dclient` runs on each host, plus `dinit`, the guest init copied into every rootfs and booted as pid 1. |
| `website/` | Landing page, case studies and docs. Prerendered Nuxt with SSR on, shipped as its own container — deliberately separate from the console SPA in `control/web-client`. |

## License

[GNU Affero General Public License v3.0](LICENSE).
