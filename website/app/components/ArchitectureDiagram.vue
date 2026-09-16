<script setup lang="ts">
import {
  ArrowDown,
  ArrowRight,
  ArrowUp,
  Box,
  Container,
  Cpu,
  Database,
  Globe,
  HardDrive,
  Network,
  Server,
  Shield,
  Terminal,
} from '@lucide/vue'

type Kind = 'client' | 'binary' | 'container' | 'db' | 'store' | 'kernel' | 'net' | 'host' | 'vm' | 'guest'

interface Node {
  id: string
  name: string
  kind: Kind
  sub: string
  where: string
  summary: string
  points: string[]
}

const kindIcon: Record<Kind, unknown> = {
  client: Globe,
  binary: Terminal,
  container: Container,
  db: Database,
  store: HardDrive,
  kernel: Shield,
  net: Network,
  host: Server,
  vm: Cpu,
  guest: Box,
}

const kindLabel: Record<Kind, string> = {
  client: 'client',
  binary: 'Go binary',
  container: 'container',
  db: 'database',
  store: 'object store',
  kernel: 'kernel ruleset',
  net: 'network device',
  host: 'physical host',
  vm: 'microVM',
  guest: 'your code',
}

const nodes: Node[] = [
  {
    id: 'clients',
    name: 'browser / SDK',
    kind: 'client',
    sub: 'the only thing users address',
    where: 'anywhere',
    summary: 'Everything a person or a program talks to is the control plane. No APIs ever addresses a host directly.',
    points: [
      'The web console, the SDKs and the CLI all speak the same public API.',
      'Guest traffic is the one exception: ssh and https to a VM go straight to the host that runs it.',
    ],
  },
  {
    id: 'control',
    name: 'control',
    kind: 'binary',
    sub: 'API · console · spec · migrations',
    where: 'control plane · Go binary',
    summary: 'One binary that is the main brain: HTTP API, web console, OpenAPI document and both sets of database migrations are embedded in it.',
    points: [
      'Serves the API at /api/v1 and the console at console.<your-domain>.',
      'Hosts reconcile toward the records it keeps.',
    ],
  },
  {
    id: 'postgres',
    name: 'PostgreSQL',
    kind: 'db',
    sub: 'users · tokens · VM records',
    where: 'control plane · database',
    summary: 'The system of record. Everything the control plane must not lose lives here.',
    points: [
      'Users, API tokens, and the VM records that hosts reconcile toward.',
      'Its migrations are embedded in the control binary, so there is no separate migration step to ship.',
    ],
  },
  {
    id: 'clickhouse',
    name: 'ClickHouse',
    kind: 'db',
    sub: 'events · telemetry',
    where: 'control plane · database',
    summary: 'The append-heavy half of the state: event and telemetry history, kept separately from the records in PostgreSQL.',
    points: [
      'Sized for writes and for scanning long time ranges, which is the wrong shape for the relational store.',
      'It has its own set of migrations, also embedded in the control binary.',
    ],
  },
  {
    id: 's3',
    name: 'S3 store',
    kind: 'store',
    sub: 'user artifacts',
    where: 'control plane · object storage',
    summary: 'Artifacts users produce, held in ordinary object storage rather than in a database.',
    points: ['Any S3-compatible endpoint works — there is no dependency on one vendor.'],
  },
  {
    id: 'host',
    name: 'QEMU host',
    kind: 'host',
    sub: 'one of many · reconciles toward control',
    where: 'the fleet',
    summary: 'A machine that actually boots sandboxes. Every host runs the same set of services',
    points: [
    ],
  },
  {
    id: 'dclient',
    name: 'dclient',
    kind: 'binary',
    sub: 'the host agent — registers with control, owns everything above',
    where: 'QEMU host · agent',
    summary: 'The host\'s agent. It registers with the control plane, receives the work assigned to it, and owns every local service that makes a sandbox work.',
    points: [
      'Builds a rootfs from an ordinary OCI image and boots a QEMU microVM against the in-tree kernel',
      'Installs the nftables ruleset, runs CoreDNS and suricata, and writes the config for dproxy, dpipe and intproxy.',
      'Brokers integration tokens on behalf of intproxy, so the host\'s bearer token stays inside dclient.',
    ],
  },
  {
    id: 'dproxy',
    name: 'dproxy',
    kind: 'binary',
    sub: 'accepts · routes · hands off the fd',
    where: 'QEMU host · ingress',
    summary: 'Owns the public listeners and decides where each connection goes, then hands the connection to dpipe. It holds no long-lived connection state, so it can be redeployed at any time.',
    points: [
      'HTTP — sniff the first request\'s Host, dial the backend, replay the sniffed prefix, hand off.',
      'TCP — routed by ingress listener, dial, hand off.',
      'HTTPS and SSH — hand the raw socket to dpipe before any encrypted byte, then answer dpipe\'s resolve query from the host map or the pubkey policy.',
      'Binds 0.0.0.0:443 with SO_REUSEADDR and SO_REUSEPORT, which is what lets intproxy sit alongside it.',
    ],
  },
  {
    id: 'dpipe',
    name: 'dpipe',
    kind: 'binary',
    sub: 'holds connections open across restarts',
    where: 'QEMU host · data plane',
    summary: 'A long-lived process that owns the file descriptors and moves their bytes, taking commands from dproxy over a unix socket.',
    points: [
      'Byte-opaque for TCP and HTTP; terminates SSH and TLS, the two protocols that cannot be fd-passed once encrypted.',
      'Because it owns the descriptor rather than dproxy, a dproxy restart or crash does not drop a live session.',
      'dpipe restarts are also gracefully handled by creating a new dpipe process and reaping the old dpipe process only after all existing connections have been terminated',
    ],
  },
  {
    id: 'intproxy',
    name: 'intproxy',
    kind: 'binary',
    sub: 'integrations, without a credential',
    where: 'QEMU host · integrations',
    summary: 'A non-transparent proxy for integrations, served under *.int.<tld>. A guest clones from github.int.<tld> and never sees a credential.',
    points: [
      'CoreDNS answers *.int.<tld> with 10.64.255.254, an address dclient owns, and intproxy binds that concrete address on :443 beside dproxy\'s wildcard.',
      'Identifies the calling VM by the source address of the connection.',
      'Asks dclient over a unix socket for a short-lived GitHub App token scoped to that (vm, repo) pair, then reverse-proxies to github.com with the credential injected.',
      'The control server, not intproxy, decides whether a request is allowed.',
    ],
  },
  {
    id: 'coredns',
    name: 'CoreDNS',
    kind: 'container',
    sub: 'which names resolve',
    where: 'QEMU host · container',
    summary: 'Resolves names for guests according to the same per-run policy that governs egress.',
    points: [
      'Runs as a container that dclient manages, like suricata.',
      'Answers *.int.<tld> with the address intproxy listens on.',
      'A name that policy does not allow simply does not resolve.',
    ],
  },
  {
    id: 'suricata',
    name: 'suricata',
    kind: 'container',
    sub: 'what may leave',
    where: 'QEMU host · container',
    summary: 'Watches guest network traffic. Egress policy is enforced per run, not per fleet.',
    points: ['Runs as a container that dclient manages, alongside CoreDNS.'],
  },
  {
    id: 'nftables',
    name: 'nftables',
    kind: 'kernel',
    sub: 'who a packet is from',
    where: 'QEMU host · kernel',
    summary: 'The ruleset dclient installs drops any packet from a tap whose source address is not the exact (tap, address) pair it allocated.',
    points: [
      'This is what makes a source address trustworthy as an identity.',
      'intproxy\'s whole identity model rests on it, so nothing may weaken this rule.',
    ],
  },
  {
    id: 'vm',
    name: 'microVM',
    kind: 'vm',
    sub: 'its own kernel',
    where: 'QEMU host · guest',
    summary: 'A real microVM with its own kernel, not a container. A guest gets a full machine, so code you did not write and cannot trust has nothing to escape into.',
    points: [
      'The kernel is built in-tree and stripped to what a short-lived guest needs, which is what makes cold boot fast enough to sit in a request path.',
      'Root filesystems come from ordinary OCI images, exported to a rootfs at build time.',
      'A host runs many of them at once, each with its own tap device and pinned address.',
    ],
  },
  {
    id: 'tap',
    name: 'tap device',
    kind: 'net',
    sub: 'one per VM · pinned address',
    where: 'QEMU host · kernel network device',
    summary: 'The virtual NIC that attaches a guest to the host network. dclient allocates one per VM and pins a single address to it.',
    points: [
      'nftables drops any packet from a tap whose source address is not the exact (tap, address) pair dclient allocated.',
      'That pinning is what lets intproxy trust a source address as the caller\'s identity.',
    ],
  },
  {
    id: 'dinit',
    name: 'dinit',
    kind: 'binary',
    sub: 'pid 1',
    where: 'microVM · pid 1',
    summary: 'The guest init. dclient copies it into every rootfs and boots it as pid 1, so an image needs no init, no dhcp client and no sshd of its own.',
    points: [
      'Configured entirely by the kernel command line (dclient.ip, dclient.gw, dclient.dns) and /etc/dclient/image.json.',
      'That file carries the user, entrypoint, cmd and env recorded from the container image at build time.',
      'dinit never calls home. A rootfs.tar exported from debian:stable or alpine boots, gets its address and answers ssh.',
    ],
  },
  {
    id: 'workload',
    name: 'your workload',
    kind: 'guest',
    sub: 'from an OCI image',
    where: 'microVM · your code',
    summary: 'Whatever the image says to run. dinit starts it as the user, entrypoint and command the image declared.',
    points: ['No agent, no sidecar and no special base image — a stock debian or alpine image works unchanged.'],
  },
]

const byId = Object.fromEntries(nodes.map(n => [n.id, n])) as Record<string, Node>

const active = ref<string | null>(null)
const detail = computed(() => (active.value ? byId[active.value] : null))
const card = useTemplateRef<HTMLElement>('card')
const pos = ref({ left: 0, top: 0 })

// placed beside the node in viewport coordinates, then clamped, so the card is
// always on screen rather than running off an edge or below the fold.
async function show(id: string, ev: Event) {
  const el = ev.currentTarget as HTMLElement | null
  active.value = id
  if (!el) return
  const r = el.getBoundingClientRect()
  await nextTick()
  const gap = 12
  const vw = window.innerWidth
  const vh = window.innerHeight
  const w = card.value?.offsetWidth ?? 320
  const h = card.value?.offsetHeight ?? 240

  const roomRight = vw - r.right - gap * 2
  const roomLeft = r.left - gap * 2

  let left: number
  let top = r.top
  if (roomRight >= w || roomLeft >= w) {
    left = roomRight >= w ? r.right + gap : r.left - gap - w
  }
  else {
    // no room either side (a full-width box): centre it over the node and put
    // it wherever there is more vertical space.
    left = r.left + r.width / 2 - w / 2
    top = vh - r.bottom > r.top ? r.bottom + gap : r.top - gap - h
  }

  pos.value = {
    left: Math.min(Math.max(left, gap), Math.max(gap, vw - w - gap)),
    top: Math.min(Math.max(top, gap), Math.max(gap, vh - h - gap)),
  }
}

function on(id: string) {
  const handler = (ev: Event) => show(id, ev)
  return { mouseenter: handler, focus: handler, click: handler }
}

function iconFor(id: string) {
  const node = byId[id]
  return node ? kindIcon[node.kind] : null
}

function nodeBind(id: string) {
  return {
    'type': 'button',
    'aria-pressed': active.value === id,
    'class': [{ 'arch-on': active.value === id }, `arch-k-${byId[id]?.kind}`],
  }
}

const stores = ['postgres', 'clickhouse', 's3']
const policy = ['intproxy', 'coredns', 'suricata', 'nftables']
const guest = ['tap', 'dinit', 'workload']
const legend: Kind[] = ['host', 'vm', 'binary', 'container', 'db', 'net', 'kernel']
</script>

<template>
  <div class="relative">
    <ul class="mb-8 flex flex-wrap items-center justify-center gap-x-6 gap-y-2">
      <li
        v-for="kind in legend"
        :key="kind"
        class="flex items-center gap-1.5 font-mono text-[0.7rem] text-muted-foreground"
      >
        <component :is="kindIcon[kind]" class="size-3.5 text-primary-text" aria-hidden="true" />
        {{ kindLabel[kind] }}
      </li>
    </ul>

    <div ref="board" class="relative" @mouseleave="active = null">
      <div aria-hidden="true" class="pointer-events-none absolute inset-0 bg-grid opacity-40" />

      <p class="sr-only">
        A diagram of the dummie architecture: a control plane on the left, reached by clients, and
        a QEMU host on the right running the ingress proxies, the per-run policy services and the
        microVMs. Each component is a button; activating one shows a description beside it.
      </p>

      <div class="relative grid gap-4 lg:grid-cols-[minmax(0,0.9fr)_auto_minmax(0,2.1fr)] lg:items-start lg:gap-2">
        <div class="flex flex-col lg:col-start-1">
          <div class="flex justify-center">
            <button class="arch-node w-full max-w-xs" v-bind="nodeBind('clients')" v-on="on('clients')">
              <span class="arch-head">
                <component :is="iconFor('clients')" class="arch-icon" aria-hidden="true" />
                <span class="arch-name">{{ byId.clients?.name }}</span>
              </span>
              <span class="arch-sub">{{ byId.clients?.sub }}</span>
            </button>
          </div>

          <div class="arch-link">
            <span class="arch-rule" />
            <ArrowDown class="size-3.5 text-muted-foreground" aria-hidden="true" />
            <span class="arch-edge">https · /api/v1</span>
          </div>

          <section class="arch-region">
            <p class="arch-region-head">
              <Server class="size-3.5" aria-hidden="true" />
              control plane
              <span class="arch-region-note">run once</span>
            </p>

            <button class="arch-node arch-node-lg w-full" v-bind="nodeBind('control')" v-on="on('control')">
              <span class="arch-head">
                <component :is="iconFor('control')" class="arch-icon" aria-hidden="true" />
                <span class="arch-name">{{ byId.control?.name }}</span>
              </span>
              <span class="arch-sub">{{ byId.control?.sub }}</span>
            </button>

            <div class="arch-link arch-link-tight">
              <span class="arch-rule" />
              <ArrowDown class="size-3.5 text-muted-foreground" aria-hidden="true" />
              <span class="arch-edge">reads and writes</span>
            </div>

            <div class="grid gap-2 sm:grid-cols-3">
              <button
                v-for="id in stores"
                :key="id"
                class="arch-node arch-node-sm arch-cyl"
                v-bind="nodeBind(id)"
                v-on="on(id)"
              >
                <span class="arch-head">
                  <component :is="iconFor(id)" class="arch-icon" aria-hidden="true" />
                  <span class="arch-name arch-name-tight">{{ byId[id]?.name }}</span>
                </span>
                <span class="arch-sub">{{ byId[id]?.sub }}</span>
              </button>
            </div>
          </section>
        </div>

        <div class="arch-span arch-span-main">
          <span class="arch-span-line">
            <span class="arch-span-rule" />
            <ArrowRight class="size-3.5 shrink-0 text-muted-foreground max-lg:rotate-90" aria-hidden="true" />
          </span>
          <span class="arch-edge arch-span-label">control assigns work to dclient</span>
        </div>

        <section class="arch-region arch-region-host" :class="{ 'arch-region-on': active === 'host' }">
          <button type="button" class="arch-region-head w-full" :aria-pressed="active === 'host'" v-on="on('host')">
            <component :is="iconFor('host')" class="size-3.5" aria-hidden="true" />
            {{ byId.host?.name }}
            <span class="arch-region-note">{{ byId.host?.sub }}</span>
          </button>

          <div class="arch-group">
            <p class="arch-group-label">public ingress</p>
            <div class="grid items-center gap-2 sm:grid-cols-[1fr_auto_1fr]">
              <button class="arch-node arch-node-sm" v-bind="nodeBind('dproxy')" v-on="on('dproxy')">
                <span class="arch-head">
                  <component :is="iconFor('dproxy')" class="arch-icon" aria-hidden="true" />
                  <span class="arch-name">{{ byId.dproxy?.name }}</span>
                </span>
                <span class="arch-sub">{{ byId.dproxy?.sub }}</span>
              </button>

              <span class="arch-span arch-span-inline">
                <span class="arch-span-line">
                  <span class="arch-span-rule" />
                  <ArrowRight class="size-3.5 shrink-0 text-muted-foreground max-sm:rotate-90" aria-hidden="true" />
                </span>
                <span class="arch-edge arch-span-label">unix socket · fd passing</span>
              </span>

              <button class="arch-node arch-node-sm" v-bind="nodeBind('dpipe')" v-on="on('dpipe')">
                <span class="arch-head">
                  <component :is="iconFor('dpipe')" class="arch-icon" aria-hidden="true" />
                  <span class="arch-name">{{ byId.dpipe?.name }}</span>
                </span>
                <span class="arch-sub">{{ byId.dpipe?.sub }}</span>
              </button>
            </div>
          </div>

          <div class="arch-link arch-link-tight">
            <span class="arch-rule" />
            <ArrowDown class="size-3.5 text-muted-foreground" aria-hidden="true" />
            <span class="arch-edge">bytes to the guest</span>
          </div>

          <div class="grid gap-2.5 sm:grid-cols-3">
            <section
              v-for="n in 3"
              :key="n"
              class="arch-region arch-region-vm"
              :class="{ 'arch-region-on': active === 'vm' }"
            >
              <button type="button" class="arch-region-head w-full" :aria-pressed="active === 'vm'" v-on="on('vm')">
                <component :is="iconFor('vm')" class="size-3.5" aria-hidden="true" />
                {{ byId.vm?.name }} {{ n - 1 }}
              </button>
              <div class="grid gap-1.5">
                <button
                  v-for="id in guest"
                  :key="id"
                  class="arch-node arch-node-xs"
                  v-bind="nodeBind(id)"
                  v-on="on(id)"
                >
                  <span class="arch-head">
                    <component :is="iconFor(id)" class="arch-icon" aria-hidden="true" />
                    <span class="arch-name">{{ byId[id]?.name }}</span>
                  </span>
                  <span class="arch-sub">{{ byId[id]?.sub }}</span>
                </button>
              </div>
            </section>
          </div>

          <div class="arch-link arch-link-tight">
            <span class="arch-rule" />
            <ArrowDown class="size-3.5 text-muted-foreground" aria-hidden="true" />
            <span class="arch-edge">every packet and every name meets policy</span>
          </div>

          <div class="arch-group">
            <p class="arch-group-label">per-run policy</p>
            <div class="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
              <button
                v-for="id in policy"
                :key="id"
                class="arch-node arch-node-sm"
                v-bind="nodeBind(id)"
                v-on="on(id)"
              >
                <span class="arch-head">
                  <component :is="iconFor(id)" class="arch-icon" aria-hidden="true" />
                  <span class="arch-name arch-name-sm">{{ byId[id]?.name }}</span>
                </span>
                <span class="arch-sub">{{ byId[id]?.sub }}</span>
              </button>
            </div>
          </div>

          <div class="arch-link arch-link-tight">
            <span class="arch-edge">starts and configures all of the above</span>
            <ArrowUp class="size-3.5 text-muted-foreground" aria-hidden="true" />
            <span class="arch-rule" />
          </div>

          <button class="arch-node w-full" v-bind="nodeBind('dclient')" v-on="on('dclient')">
            <span class="arch-head">
              <component :is="iconFor('dclient')" class="arch-icon" aria-hidden="true" />
              <span class="arch-name">{{ byId.dclient?.name }}</span>
            </span>
            <span class="arch-sub">{{ byId.dclient?.sub }}</span>
          </button>
        </section>
      </div>

      <!-- teleported: an ancestor transform would make position:fixed resolve
           against that ancestor instead of the viewport. -->
      <Teleport to="body">
        <Transition name="arch-pop">
          <div
            v-if="detail"
            ref="card"
            class="arch-pop"
            :style="{ left: `${pos.left}px`, top: `${pos.top}px` }"
            role="status"
          >
          <p class="flex items-center gap-2">
            <component :is="kindIcon[detail.kind]" class="size-4 shrink-0 text-primary-text" aria-hidden="true" />
            <span class="font-mono text-[0.7rem] uppercase tracking-[0.14em] text-primary-text">
              {{ kindLabel[detail.kind] }}
            </span>
          </p>
          <h3 class="mt-1.5 font-mono text-sm font-semibold">{{ detail.name }}</h3>
          <p class="mt-0.5 font-mono text-[0.7rem] text-muted-foreground">{{ detail.where }}</p>
          <p class="mt-2 text-[0.8rem] leading-relaxed text-muted-foreground">{{ detail.summary }}</p>
          <ul class="mt-2 space-y-1.5">
            <li
              v-for="point in detail.points"
              :key="point"
              class="flex gap-2 text-[0.8rem] leading-relaxed text-muted-foreground"
            >
              <span aria-hidden="true" class="mt-2 h-px w-2 shrink-0 bg-primary" />
              <span>{{ point }}</span>
            </li>
            </ul>
          </div>
        </Transition>
      </Teleport>
    </div>

    <p class="mt-6 text-center font-mono text-[0.7rem] text-muted-foreground">
      Point at any box — or tap it — to read what that piece does.
    </p>
  </div>
</template>

<style scoped>
.arch-node {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 0.25rem;
  padding: 0.75rem 0.875rem;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background-color: var(--background);
  text-align: left;
  transition: border-color 150ms ease, background-color 150ms ease, transform 150ms ease;
}
button.arch-node:hover,
.arch-on {
  border-color: var(--primary);
  background-color: var(--accent);
  transform: translateY(-1px);
}
.arch-node-lg {
  padding: 1rem 1.125rem;
}
.arch-node-sm {
  padding: 0.5rem 0.625rem;
}
.arch-node-xs {
  gap: 0.1rem;
  padding: 0.4rem 0.5rem;
}

.arch-head {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  min-width: 0;
}
.arch-icon {
  width: 0.875rem;
  height: 0.875rem;
  flex-shrink: 0;
  color: var(--primary-text);
}
.arch-name {
  min-width: 0;
  font-family: var(--font-mono);
  font-size: 0.875rem;
  font-weight: 600;
  overflow-wrap: anywhere;
}
/* sized so the longest name still sits on one line in a narrow box */
.arch-name-tight {
  font-size: 0.68rem;
  letter-spacing: -0.01em;
}
.arch-name-sm,
.arch-node-xs .arch-name {
  font-size: 0.78rem;
}
.arch-node-lg .arch-name {
  font-size: 1rem;
}
.arch-sub {
  font-size: 0.7rem;
  line-height: 1.35;
  color: var(--muted-foreground);
  overflow-wrap: anywhere;
}

/* a database is a cylinder: rounded barrel plus an elliptical cap */
.arch-cyl {
  position: relative;
  padding-top: 1rem;
  border-radius: 0.9rem / 0.55rem;
}
.arch-cyl::before {
  content: "";
  position: absolute;
  top: 0.15rem;
  left: 0.35rem;
  right: 0.35rem;
  height: 0.5rem;
  border: 1px solid var(--border);
  border-radius: 50%;
}

.arch-k-kernel {
  border-style: dotted;
  border-width: 1.5px;
}
.arch-k-net {
  border-style: solid;
  border-color: color-mix(in oklch, var(--primary) 40%, var(--border));
}
.arch-k-guest {
  border-style: dashed;
}

.arch-link {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.25rem;
  padding-block: 0.6rem;
}
.arch-link-tight {
  padding-block: 0.45rem;
}
.arch-rule {
  width: 1px;
  height: 1rem;
  background-color: var(--border);
}
.arch-edge {
  font-family: var(--font-mono);
  font-size: 0.65rem;
  color: var(--muted-foreground);
}

.arch-span {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.4rem;
  padding-block: 0.25rem;
}
.arch-span-line {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.25rem;
}
.arch-span-rule {
  width: 1px;
  height: 1rem;
  background-color: var(--border);
}
.arch-span-label {
  max-width: 11rem;
  text-align: center;
  line-height: 1.35;
}
@media (min-width: 640px) {
  .arch-span-inline .arch-span-line {
    flex-direction: row;
  }
  .arch-span-inline .arch-span-rule {
    width: 0.75rem;
    height: 1px;
  }
  .arch-span-inline .arch-span-label {
    max-width: 5.5rem;
  }
}
/* scoped to the main connector: the inline one has its own rules above */
@media (min-width: 1024px) {
  .arch-span-main {
    align-self: center;
    padding-block: 0;
    padding-inline: 0.25rem;
  }
  .arch-span-main .arch-span-line {
    flex-direction: row;
  }
  .arch-span-main .arch-span-rule {
    width: 1.25rem;
    height: 1px;
  }
  .arch-span-main .arch-span-label {
    max-width: 7rem;
  }
}

.arch-region {
  padding: 0.875rem;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background-color: color-mix(in oklch, var(--card) 70%, transparent);
  transition: border-color 150ms ease;
}
.arch-region-on {
  border-color: color-mix(in oklch, var(--primary) 60%, var(--border));
}
.arch-region-host {
  border-width: 2px;
}
.arch-region-vm {
  padding: 0.6rem;
  border-style: dashed;
  border-color: color-mix(in oklch, var(--primary) 45%, var(--border));
  background-color: color-mix(in oklch, var(--accent) 45%, transparent);
}
.arch-region-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.3rem 0.5rem;
  margin-bottom: 0.7rem;
  font-family: var(--font-mono);
  font-size: 0.7rem;
  letter-spacing: 0.14em;
  text-transform: uppercase;
  color: var(--primary-text);
  text-align: left;
}
.arch-region-note {
  letter-spacing: normal;
  text-transform: none;
  color: var(--muted-foreground);
}

.arch-group {
  padding: 0.7rem;
  border: 1px dashed var(--border);
  border-radius: var(--radius-md);
}
.arch-group-label {
  display: block;
  margin-bottom: 0.55rem;
  font-family: var(--font-mono);
  font-size: 0.65rem;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  color: var(--muted-foreground);
}

.arch-pop {
  position: fixed;
  z-index: 50;
  width: min(20rem, calc(100vw - 1.5rem));
  padding: 0.875rem 1rem;
  border: 1px solid var(--primary);
  border-radius: var(--radius-md);
  background-color: var(--popover);
  box-shadow: 0 12px 32px -12px rgb(0 0 0 / 0.35);
  pointer-events: none;
}
.arch-pop-enter-active,
.arch-pop-leave-active {
  transition: opacity 120ms ease;
}
.arch-pop-enter-from,
.arch-pop-leave-to {
  opacity: 0;
}
</style>
