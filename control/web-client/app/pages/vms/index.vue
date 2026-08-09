<script setup lang="ts">
import { Copy, Plus, RefreshCw, Trash2 } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'

definePageMeta({ middleware: ['auth'] })
useHead({ title: 'dummie — vms' })

interface VM {
  id: string
  agent_id: string
  vm_id: string
  // Unique across the fleet, and the key its http route is published under.
  // Generated when the create request leaves it empty.
  name: string
  default_port: number
  public_ports: number[]
  status: 'pending' | 'running' | 'stopped' | 'failed' | 'gone'
  boot: string
  cpus: number
  memory_mib: number
  disk_mib: number
  ip: string
  last_error: string
  created_at: string
  // The spec as it was sent to the agent. Empty for a VM adopted from a host's
  // inventory report -- nobody here asked for it, so there is nothing to copy.
  spec: Record<string, unknown>
}

interface Quota {
  vcpu_limit: number
  memory_limit_mib: number
  disk_limit_mib: number
  vcpu_used: number
  memory_used_mib: number
  disk_used_mib: number
}

const { authFetch } = useAuth()

const items = ref<VM[]>([])
const quota = ref<Quota | null>(null)
// Only whether a key is on file. The server refuses a create without one, so the
// form needs to know before offering it -- the key itself is nothing this page
// shows.
const hasPublicKey = ref(true)
const loading = ref(true)
const error = ref<string | null>(null)

type BadgeVariant = 'default' | 'secondary' | 'outline' | 'destructive'
const statusVariant: Record<VM['status'], BadgeVariant> = {
  running: 'default',
  pending: 'secondary',
  stopped: 'secondary',
  gone: 'outline',
  failed: 'destructive',
}

async function readMessage(res: Response): Promise<string | null> {
  try {
    const b = await res.json()
    return typeof b?.message === 'string' ? b.message : null
  }
  catch {
    return null
  }
}

function fmtMiB(mib: number) {
  if (mib < 1024) return `${mib} MiB`
  const gib = mib / 1024
  return Number.isInteger(gib) ? `${gib} GiB` : `${gib.toFixed(1)} GiB`
}

// 11th–13th are the exception the mod-10 rule gets wrong.
function ordinal(n: number) {
  if (n % 100 >= 11 && n % 100 <= 13) return `${n}th`
  return `${n}${['th', 'st', 'nd', 'rd'][n % 10] ?? 'th'}`
}

function fmtDate(s: string) {
  if (!s) return '—'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  const date = `${ordinal(d.getDate())} ${d.toLocaleString(undefined, { month: 'long' })} ${d.getFullYear()}`
  return `${date}, ${d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })}`
}

async function load(quiet = false) {
  if (!quiet) loading.value = true
  error.value = null
  try {
    // Not on a quiet poll: the poll runs every few seconds to watch a pending
    // row, and a key does not change on that timescale.
    const [vmRes, qRes, meRes] = await Promise.all([
      authFetch('/vms?limit=100'),
      authFetch('/vms/quota'),
      quiet ? null : authFetch('/me'),
    ])
    if (!vmRes.ok) throw new Error(`HTTP ${vmRes.status}`)
    if (!qRes.ok) throw new Error(`HTTP ${qRes.status}`)
    items.value = (await vmRes.json()).items ?? []
    quota.value = await qRes.json()
    if (meRes?.ok) hasPublicKey.value = !!(await meRes.json()).public_key
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load your VMs'
  }
  finally {
    loading.value = false
  }
}
onMounted(() => load())

// A create takes minutes on the host — the agent downloads images and builds a
// filesystem — so the row sits at 'pending' and only a poll moves it. Polling
// stops as soon as nothing is in flight rather than running forever.
const anyPending = computed(() => items.value.some(v => v.status === 'pending'))

// A start or stop is answered with 202 and settles when the agent reports back,
// so the row keeps its old status for a moment. Tracking which rows are waiting
// keeps the poll running and the switch honest until they land.
const settleTimeoutMs = 60_000
const settling = ref<Record<string, { want: VM['status'], until: number }>>({})

const anySettling = computed(() => Object.keys(settling.value).length > 0)
let timer: ReturnType<typeof setInterval> | null = null

// Drop a row from `settling` once the server agrees, or once waiting stops
// being reasonable — otherwise a job that never lands leaves the switch stuck.
watch(items, (rows) => {
  const now = Date.now()
  const next: typeof settling.value = {}
  for (const [id, s] of Object.entries(settling.value)) {
    const row = rows.find(r => r.id === id)
    if (row && row.status !== s.want && now < s.until) next[id] = s
  }
  settling.value = next
})

watch([anyPending, anySettling], ([pending, waiting]) => {
  if ((pending || waiting) && !timer) {
    timer = setInterval(() => load(true), 5000)
  }
  else if (!pending && !waiting && timer) {
    clearInterval(timer)
    timer = null
  }
}, { immediate: true })

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})

// --- allowance ---
const cpuRemaining = computed(() => {
  const q = quota.value
  return q ? Math.max(0, q.vcpu_limit - q.vcpu_used) : 0
})
const memRemaining = computed(() => {
  const q = quota.value
  return q ? Math.max(0, q.memory_limit_mib - q.memory_used_mib) : 0
})
const diskRemaining = computed(() => {
  const q = quota.value
  return q ? Math.max(0, q.disk_limit_mib - q.disk_used_mib) : 0
})
const atCapacity = computed(() =>
  cpuRemaining.value < 1 || memRemaining.value < 64 || diskRemaining.value < 1)

// Two independent reasons a create cannot happen. The button needs one answer,
// but the label has to say which -- "disabled" with no reason is a dead end.
const blockedReason = computed(() => {
  if (!hasPublicKey.value) return 'Cannot create a VM: add an SSH public key in Settings first'
  if (atCapacity.value) return 'Cannot create a VM: your allowance is fully used'
  return null
})

// --- create ---
interface Host {
  id: string
  hostname: string
}

const hosts = ref<Host[]>([])
const hostsError = ref<string | null>(null)

async function loadHosts() {
  hostsError.value = null
  try {
    const res = await authFetch('/vms/hosts')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    hosts.value = (await res.json()).items ?? []
  }
  catch (e) {
    hostsError.value = e instanceof Error ? e.message : 'Could not load hosts'
  }
}

function hostLabel(h: Host) {
  return h.hostname || `${h.id.slice(0, 8)}…`
}

const createOpen = ref(false)
const creating = ref(false)
const createError = ref<string | null>(null)

const blankForm = {
  agent_id: '',
  name: '',
  cpus: '1',
  memory_mib: '512',
  default_port: '8000',
  public_ports: '',
  // Sizes the per-VM overlay, not the shared base image built from the tar.
  disk_size: '2G',
  kernel: '',
  kernel_sha256: '',
  rootfs_tar: '',
  rootfs_tar_sha256: '',
}
const form = reactive({ ...blankForm })

function resetForm() {
  Object.assign(form, blankForm)
  createError.value = null
  copiedFrom.value = null
}

// Set while the dialog was opened by copying, so it can say what it copied.
const copiedFrom = ref<string | null>(null)

async function openCreate() {
  resetForm()
  copiedFrom.value = null
  createOpen.value = true
  // A host that came online since the page loaded should be pickable now.
  await loadHosts()
  // Preselect when there is no choice to make; with several, the pick is real.
  if (hosts.value.length === 1) form.agent_id = hosts.value[0]!.id
}

// Only a VM this server created has a spec to copy. One adopted from a host's
// inventory report has an empty one, and a form prefilled from nothing is worse
// than no button.
function copyable(v: VM) {
  return !!(v.spec?.kernel || v.spec?.rootfs_tar || v.spec?.rootfs)
}

/** Reads a spec field as a string, since the spec is whatever was sent. */
function specStr(spec: Record<string, unknown>, key: string) {
  const v = spec?.[key]
  return typeof v === 'string' ? v : ''
}

async function openCopy(v: VM) {
  resetForm()
  copiedFrom.value = v.name || v.vm_id || 'that VM'

  // The name is deliberately not copied: it is unique across the fleet, so
  // reusing it would be rejected. Left empty, the copy gets a generated one.
  form.cpus = String(v.cpus || 1)
  form.memory_mib = String(v.memory_mib || 512)
  form.default_port = String(v.default_port || 8000)
  form.public_ports = (v.public_ports ?? []).join(', ')
  // From the spec, not from disk_mib: the spec holds what was typed ("2G"),
  // which is what belongs back in the field. disk_mib is the parsed number.
  form.disk_size = specStr(v.spec, 'disk_size') || '2G'
  form.kernel = specStr(v.spec, 'kernel')
  form.kernel_sha256 = specStr(v.spec, 'kernel_sha256')
  form.rootfs_tar = specStr(v.spec, 'rootfs_tar')
  form.rootfs_tar_sha256 = specStr(v.spec, 'rootfs_tar_sha256')

  createOpen.value = true
  await loadHosts()
  // The original host only if it is still connected — otherwise the create
  // would be rejected, and preselecting an unusable host hides why.
  if (hosts.value.some(h => h.id === v.agent_id)) form.agent_id = v.agent_id
  else if (hosts.value.length === 1) form.agent_id = hosts.value[0]!.id
}

// Mirrors the server's parseSizeMiB: same units, same rounding up, so the form
// and the server agree on whether a size fits.
function sizeToMiB(s: string): number | null {
  const v = s.trim()
  if (!v) return 0
  const m = /^(\d+)([KkMmGgTt]?)$/.exec(v)
  if (!m) return null
  const mult: Record<string, number> = { '': 1, k: 1 << 10, m: 1 << 20, g: 1 << 30, t: 2 ** 40 }
  const bytes = Number(m[1]) * (mult[m[2]!.toLowerCase()] ?? 1)
  return Math.ceil(bytes / (1 << 20))
}

// Checked client-side purely so the form can say no before a round trip; the
// server does the same check and is the one that decides.
const wouldExceed = computed(() => {
  const q = quota.value
  if (!q) return false
  const cpus = Number(form.cpus)
  const mem = Number(form.memory_mib)
  const disk = sizeToMiB(form.disk_size)
  if (!Number.isFinite(cpus) || !Number.isFinite(mem) || disk === null) return false
  return q.vcpu_used + cpus > q.vcpu_limit
    || q.memory_used_mib + mem > q.memory_limit_mib
    || q.disk_used_mib + disk > q.disk_limit_mib
})

// Mirrors the server's checks so a mistake is caught before the round trip.
// The server repeats all of them and is the one that decides.
// The server is the authority on both of these -- the name has a unique
// constraint behind it, and the ports are re-validated there. These checks only
// save a round trip on a typo.
const namePattern = /^[a-z0-9]+(-[a-z0-9]+)*$/

/** "8000, 9090" -> [8000, 9090]. Blank entries are dropped, not zeroed. */
function parsePorts(s: string): number[] {
  return s.split(',').map(p => p.trim()).filter(Boolean).map(Number)
}

function validate(): string | null {
  if (!form.agent_id) return 'Choose a host to run this VM on.'
  const name = form.name.trim()
  if (name && (name.length < 3 || name.length > 52)) {
    return 'A name must be between 3 and 52 characters.'
  }
  if (name && !namePattern.test(name)) {
    return 'A name must be lowercase letters, digits and single hyphens — e.g. hello-kitty.'
  }
  const defaultPort = Number(form.default_port)
  if (!Number.isInteger(defaultPort) || defaultPort < 1 || defaultPort > 65535) {
    return 'The default port must be a whole number between 1 and 65535.'
  }
  if (parsePorts(form.public_ports).some(p => !Number.isInteger(p) || p < 1 || p > 65535)) {
    return 'Public ports must be whole numbers between 1 and 65535, separated by commas.'
  }
  const cpus = Number(form.cpus)
  const memory = Number(form.memory_mib)
  if (!Number.isInteger(cpus) || cpus < 1) return 'vCPU must be a whole number of at least 1.'
  if (!Number.isInteger(memory) || memory < 64) return 'Memory must be at least 64 MiB.'
  if (!form.kernel.trim()) return 'A kernel URL is required.'
  if (!form.rootfs_tar.trim()) return 'A root filesystem tar URL is required.'
  if (form.disk_size.trim() && !/^\d+[KkMmGgTt]?$/.test(form.disk_size.trim())) {
    return 'Disk size must be a number, optionally with a K, M, G or T suffix — e.g. 2G.'
  }
  for (const [label, v] of [['kernel', form.kernel_sha256], ['root filesystem tar', form.rootfs_tar_sha256]] as const) {
    if (v.trim() && !/^[0-9a-f]{64}$/i.test(v.trim())) {
      return `The ${label} SHA256 must be 64 hex characters.`
    }
  }
  return null
}

async function create() {
  const problem = validate()
  if (problem) {
    createError.value = problem
    return
  }
  creating.value = true
  createError.value = null
  try {
    const res = await authFetch('/vms', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        agent_id: form.agent_id,
        name: form.name,
        cpus: Number(form.cpus),
        memory_mib: Number(form.memory_mib),
        default_port: Number(form.default_port),
        public_ports: parsePorts(form.public_ports),
        disk_size: form.disk_size,
        kernel: form.kernel,
        kernel_sha256: form.kernel_sha256,
        rootfs_tar: form.rootfs_tar,
        rootfs_tar_sha256: form.rootfs_tar_sha256,
      }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    createOpen.value = false
    resetForm()
    await load(true)
  }
  catch (e) {
    createError.value = e instanceof Error ? e.message : 'Could not create the VM'
  }
  finally {
    creating.value = false
  }
}

// --- start / stop ---
//
// The switch shows where the VM is being asked to go while a job is in flight,
// not where it currently is: a switch that snaps back for the minute a boot
// takes reads as "that didn't work".
function isRunning(v: VM) {
  const want = settling.value[v.id]?.want
  return want ? want === 'running' : v.status === 'running'
}

// Only a VM the host has actually built can be started or stopped. 'pending'
// has no id yet, 'gone' no longer exists, 'failed' never got that far.
function switchable(v: VM) {
  return !!v.vm_id && (v.status === 'running' || v.status === 'stopped')
}

async function toggleRunning(v: VM, run: boolean) {
  actionError.value = null
  settling.value = {
    ...settling.value,
    [v.id]: { want: run ? 'running' : 'stopped', until: Date.now() + settleTimeoutMs },
  }
  try {
    const res = await authFetch(`/vms/${v.id}/${run ? 'start' : 'stop'}`, { method: 'POST' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    await load(true)
  }
  catch (e) {
    const next = { ...settling.value }
    delete next[v.id]
    settling.value = next
    actionError.value = e instanceof Error ? e.message : `Could not ${run ? 'start' : 'stop'} the VM`
  }
}

// --- destroy ---
const toDestroy = ref<VM | null>(null)
const working = ref(false)
const actionError = ref<string | null>(null)

function destroyable(v: VM) {
  return !!v.vm_id && v.status !== 'gone'
}

async function confirmDestroy() {
  if (!toDestroy.value) return
  working.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/vms/${toDestroy.value.id}/destroy`, { method: 'POST' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toDestroy.value = null
    await load(true)
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not destroy the VM'
  }
  finally {
    working.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-12 sm:px-6">
    <div class="flex flex-wrap items-end justify-between gap-4">
      <div>
        <p class="eyebrow mb-2 text-primary-text">// vms</p>
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">Your VMs</h1>
      </div>
      <div class="flex items-center gap-2">
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="loading" aria-label="Refresh your VMs" @click="load()">
          <RefreshCw class="size-4" aria-hidden="true" />
          Refresh
        </Button>
        <Dialog v-model:open="createOpen" @update:open="(v: boolean) => !v && resetForm()">
          <Button
            class="font-mono text-xs"
            :disabled="!!blockedReason"
            :aria-label="blockedReason ?? 'Create a VM'"
            @click="openCreate"
          >
            <Plus class="size-4" aria-hidden="true" />
            New VM
          </Button>
          <!-- Scrolling content: the form is taller than a short viewport, and
               the footer must stay reachable. -->
          <DialogContent class="max-h-[85svh] overflow-y-auto sm:max-w-lg">
            <DialogHeader>
              <DialogTitle>{{ copiedFrom ? 'New VM from a copy' : 'New VM' }}</DialogTitle>
              <DialogDescription>
                <template v-if="copiedFrom">
                  Prefilled from <span class="font-mono">{{ copiedFrom }}</span>. Nothing is created
                  until you submit, so change whatever you need first.
                </template>
                <template v-else>
                  Pushed to the host you pick, which downloads the images and boots it. Building takes
                  a few minutes — the row stays <span class="font-mono">pending</span> until it reports back.
                </template>
              </DialogDescription>
            </DialogHeader>

            <form class="space-y-4" :aria-busy="creating" @submit.prevent="create">
              <div class="space-y-2">
                <Label for="vm-host">Host</Label>
                <Select v-model="form.agent_id">
                  <SelectTrigger id="vm-host" class="w-full">
                    <SelectValue placeholder="Choose a host…" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem v-for="h in hosts" :key="h.id" :value="h.id">
                      {{ hostLabel(h) }}
                    </SelectItem>
                  </SelectContent>
                </Select>
                <p v-if="hostsError" class="text-xs text-destructive">{{ hostsError }}</p>
                <p v-else-if="!hosts.length" class="text-xs text-muted-foreground">
                  No host is connected right now.
                </p>
              </div>

              <div class="space-y-2">
                <Label for="vm-name">Name</Label>
                <Input
                  id="vm-name"
                  v-model="form.name"
                  placeholder="leave empty for a generated name"
                  aria-describedby="vm-name-hint"
                />
                <p id="vm-name-hint" class="text-xs text-muted-foreground">
                  Lowercase letters, digits and single hyphens, 3–52 characters. Unique across every
                  VM, because requests are routed to it by this name.
                </p>
              </div>

              <div class="grid gap-4 sm:grid-cols-2">
                <div class="space-y-2">
                  <Label for="vm-default-port">Default port</Label>
                  <Input
                    id="vm-default-port"
                    v-model="form.default_port"
                    type="number"
                    min="1"
                    max="65535"
                    inputmode="numeric"
                    aria-describedby="vm-default-port-hint"
                  />
                  <p id="vm-default-port-hint" class="text-xs text-muted-foreground">
                    Where a request goes when it does not pick a port.
                  </p>
                </div>
                <div class="space-y-2">
                  <Label for="vm-public-ports">Public ports</Label>
                  <Input
                    id="vm-public-ports"
                    v-model="form.public_ports"
                    placeholder="8000, 9090"
                    inputmode="numeric"
                    aria-describedby="vm-public-ports-hint"
                  />
                  <p id="vm-public-ports-hint" class="text-xs text-muted-foreground">
                    Comma separated. Empty publishes nothing.
                  </p>
                </div>
              </div>

              <div class="grid gap-4 sm:grid-cols-2">
                <div class="space-y-2">
                  <Label for="vm-cpus">vCPU</Label>
                  <Input id="vm-cpus" v-model="form.cpus" type="number" min="1" step="1" inputmode="numeric" />
                  <p class="font-mono text-xs text-muted-foreground">{{ cpuRemaining }} left</p>
                </div>
                <div class="space-y-2">
                  <Label for="vm-mem">Memory (MiB)</Label>
                  <Input id="vm-mem" v-model="form.memory_mib" type="number" min="64" step="64" inputmode="numeric" />
                  <p class="font-mono text-xs text-muted-foreground">{{ fmtMiB(memRemaining) }} left</p>
                </div>
              </div>

              <div class="space-y-2">
                <Label for="vm-disk">Disk size</Label>
                <Input id="vm-disk" v-model="form.disk_size" placeholder="2G" aria-describedby="vm-disk-hint" />
                <!-- Says what the number does: it grows the block device, not
                     the filesystem inside it, which is the surprise otherwise. -->
                <p id="vm-disk-hint" class="text-xs text-muted-foreground">
                  Size of this VM's disk. The guest still has to grow its own filesystem to use the space.
                </p>
                <p class="font-mono text-xs text-muted-foreground">{{ fmtMiB(diskRemaining) }} left</p>
              </div>

              <div class="space-y-2">
                <Label for="vm-kernel">Kernel URL</Label>
                <Input id="vm-kernel" v-model="form.kernel" required placeholder="https://…/vmlinuz" />
                <Input
                  v-model="form.kernel_sha256"
                  class="font-mono text-xs"
                  placeholder="sha256 (optional)"
                  aria-label="Kernel SHA256, optional"
                />
              </div>

              <div class="space-y-2">
                <Label for="vm-rootfs-tar">Root filesystem tar URL</Label>
                <Input id="vm-rootfs-tar" v-model="form.rootfs_tar" required placeholder="https://…/rootfs.tar" />
                <Input
                  v-model="form.rootfs_tar_sha256"
                  class="font-mono text-xs"
                  placeholder="sha256 (optional)"
                  aria-label="Root filesystem tar SHA256, optional"
                />
              </div>

              <p v-if="wouldExceed" class="text-sm text-destructive">
                This is more than your remaining allowance.
              </p>
              <FormError id="create-vm-error" :message="createError" />

              <DialogFooter>
                <DialogClose as-child>
                  <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
                </DialogClose>
                <Button type="submit" class="font-mono text-xs" :disabled="creating || wouldExceed">
                  {{ creating ? 'Creating…' : 'Create' }}
                </Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </Dialog>
      </div>
    </div>

    <!-- allowance -->
    <div class="mt-6 grid gap-4 sm:grid-cols-3">
      <div class="rounded-lg border border-border p-4">
        <p class="eyebrow text-muted-foreground">vCPU</p>
        <p v-if="quota" class="mt-1 font-mono text-lg">
          {{ quota.vcpu_used }} <span class="text-muted-foreground">/ {{ quota.vcpu_limit }}</span>
        </p>
        <Skeleton v-else class="mt-1 h-7 w-20" aria-hidden="true" />
      </div>
      <div class="rounded-lg border border-border p-4">
        <p class="eyebrow text-muted-foreground">Memory</p>
        <p v-if="quota" class="mt-1 font-mono text-lg">
          {{ fmtMiB(quota.memory_used_mib) }} <span class="text-muted-foreground">/ {{ fmtMiB(quota.memory_limit_mib) }}</span>
        </p>
        <Skeleton v-else class="mt-1 h-7 w-28" aria-hidden="true" />
      </div>
      <div class="rounded-lg border border-border p-4">
        <p class="eyebrow text-muted-foreground">Disk</p>
        <p v-if="quota" class="mt-1 font-mono text-lg">
          {{ fmtMiB(quota.disk_used_mib) }} <span class="text-muted-foreground">/ {{ fmtMiB(quota.disk_limit_mib) }}</span>
        </p>
        <Skeleton v-else class="mt-1 h-7 w-28" aria-hidden="true" />
      </div>
    </div>

    <!-- Before the allowance notice: a missing key blocks every create regardless
         of how much room is left, so it is the thing to fix first. -->
    <Alert v-if="!hasPublicKey && !loading" class="mt-4">
      <AlertTitle>No SSH public key on your profile</AlertTitle>
      <AlertDescription>
        A VM is built to accept your key before it boots, so one has to be on file first.
        <NuxtLink
          to="/settings"
          class="text-primary-text underline decoration-primary-text/40 underline-offset-4 transition-colors hover:decoration-primary-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
        >
          Add it in Settings
        </NuxtLink>.
      </AlertDescription>
    </Alert>

    <Alert v-if="atCapacity && !loading" class="mt-4">
      <AlertTitle>Allowance fully used</AlertTitle>
      <AlertDescription>
        Destroy a VM to free capacity, or ask an admin to raise your limit.
      </AlertDescription>
    </Alert>

    <Alert v-if="error" variant="destructive" class="mt-4">
      <AlertTitle>Could not load your VMs</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <Alert v-if="actionError" variant="destructive" class="mt-4">
      <AlertTitle>Action failed</AlertTitle>
      <AlertDescription>{{ actionError }}</AlertDescription>
    </Alert>

    <div class="mt-6 rounded-lg border border-border" aria-live="polite" :aria-busy="loading">
      <p v-if="loading" class="sr-only">Loading your VMs…</p>
      <Table label="Your VMs">
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Size</TableHead>
            <TableHead>Disk</TableHead>
            <TableHead>Address</TableHead>
            <TableHead>Created</TableHead>
            <TableHead>Power</TableHead>
            <TableHead class="text-right">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <template v-if="loading">
            <TableRow v-for="n in 3" :key="n" aria-hidden="true">
              <TableCell v-for="c in 8" :key="c"><Skeleton class="h-4 w-full" /></TableCell>
            </TableRow>
          </template>
          <TableEmpty v-else-if="!items.length" :colspan="8">
            You have no VMs yet.
          </TableEmpty>
          <TableRow v-for="v in items" v-else :key="v.id">
            <TableCell>
              <NuxtLink
                :to="`/vms/${v.id}`"
                class="font-mono text-primary-text underline decoration-primary-text/40 underline-offset-4 transition-colors hover:decoration-primary-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                :aria-label="`View details for ${v.name || v.vm_id || 'this VM'}`"
              >
                {{ v.name || v.vm_id || '—' }}
              </NuxtLink>
              <!-- The failure reason is the whole point of a failed row, so it
                   is shown inline rather than hidden behind a detail view. -->
              <span v-if="v.status === 'failed' && v.last_error" class="mt-1 block text-xs text-destructive">
                {{ v.last_error }}
              </span>
            </TableCell>
            <TableCell>
              <Badge :variant="statusVariant[v.status]" class="font-mono">{{ v.status }}</Badge>
            </TableCell>
            <TableCell class="font-mono text-muted-foreground">
              {{ v.cpus }} vCPU · {{ fmtMiB(v.memory_mib) }}
            </TableCell>
            <!-- An em dash, not '0': the size was never recorded for VMs made
                 before the column existed or adopted from a host, and 0 would
                 read as a diskless VM. -->
            <TableCell class="font-mono text-muted-foreground">
              {{ v.disk_mib ? fmtMiB(v.disk_mib) : '—' }}
            </TableCell>
            <TableCell class="font-mono text-muted-foreground">{{ v.ip || '—' }}</TableCell>
            <TableCell class="text-muted-foreground">{{ fmtDate(v.created_at) }}</TableCell>
            <TableCell>
              <!-- One switch per row, so the name has to be in the label or
                   they all read alike to a screen reader. (WCAG 2.4.6) -->
              <Switch
                :model-value="isRunning(v)"
                :disabled="!switchable(v) || !!settling[v.id]"
                :aria-label="switchable(v)
                  ? `${isRunning(v) ? 'Stop' : 'Start'} VM ${v.name || v.vm_id}`
                  : `Cannot start or stop ${v.name || v.vm_id}: it is ${v.status}`"
                @update:model-value="(run: boolean) => toggleRunning(v, run)"
              />
            </TableCell>
            <TableCell class="text-right">
              <div class="flex justify-end gap-1">
                <!-- Icon-only, one per row: the name has to be in the label or
                     every button reads the same to a screen reader. (WCAG 2.4.6) -->
                <Button
                  variant="ghost"
                  size="icon"
                  :disabled="!copyable(v) || !!blockedReason"
                  :aria-label="!copyable(v)
                    ? `Cannot copy ${v.name || v.vm_id}: it was created on its host, not here`
                    : blockedReason ?? `Copy ${v.name || v.vm_id} as a template for a new VM`"
                  @click="openCopy(v)"
                >
                  <Copy class="size-4" aria-hidden="true" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  class="text-destructive hover:text-destructive"
                  :disabled="!destroyable(v)"
                  :aria-label="destroyable(v)
                    ? `Destroy VM ${v.name || v.vm_id}`
                    : `Cannot destroy ${v.name || v.vm_id}: it does not exist on a host`"
                  @click="toDestroy = v"
                >
                  <Trash2 class="size-4" aria-hidden="true" />
                </Button>
              </div>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>

    <!-- destroy confirm -->
    <Dialog :open="!!toDestroy" @update:open="(v: boolean) => { if (!v) toDestroy = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Destroy VM</DialogTitle>
          <DialogDescription>
            <span class="font-mono text-foreground">{{ toDestroy?.name || toDestroy?.vm_id }}</span>
            is shut down and its disk is deleted on the host. This cannot be undone.
            The capacity it holds is returned to your allowance.
          </DialogDescription>
        </DialogHeader>
        <FormError id="destroy-vm-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="working" @click="confirmDestroy">
            {{ working ? 'Destroying…' : 'Destroy' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
