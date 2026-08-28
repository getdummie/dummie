<script setup lang="ts">
import { Plus, RefreshCw, Trash2 } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogScrollContent,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { TableCell, TableRow } from '@/components/ui/table'
import type { DataTableColumn } from '@/lib/table'

const columns: DataTableColumn[] = [
  { key: 'vm', label: 'VM' },
  { key: 'host', label: 'Host' },
  { key: 'status', label: 'Status' },
  { key: 'boot', label: 'Boot' },
  { key: 'resources', label: 'Resources' },
  { key: 'address', label: 'Address' },
  { key: 'created', label: 'Created' },
  { key: 'actions', label: 'Actions', align: 'right' },
]

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · vms' })

interface VMRow {
  id: string
  client_id: string
  vm_id: string
  name: string
  default_port: number
  public_ports: number[]
  status: 'pending' | 'running' | 'stopped' | 'failed' | 'gone'
  boot: string
  cpus: number
  memory_mib: number
  ip: string
  last_error: string
  created_at: string
  started_at: string
  reported_at: string
}

const staleAfterMs = 2 * 60_000

const hostReported = new Set(['running', 'stopped'])

const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined

interface Settling {
  verb: string
  from: string
  running: boolean
  until: number
}
const settling = ref<Record<string, Settling>>({})
const settleTimeoutMs = 60_000
const settlePollMs = 1_500
let settlePoll: ReturnType<typeof setInterval> | undefined

function isSettling(v: VMRow) {
  return settling.value[v.id]
}

function displayStatus(v: VMRow) {
  const pending = settling.value[v.id]
  if (pending) return pending.verb
  if (!hostReported.has(v.status)) return v.status
  const at = v.reported_at ? new Date(v.reported_at).getTime() : Number.NaN
  if (Number.isNaN(at) || now.value - at > staleAfterMs) return 'stale'
  return v.status
}

function switchOn(v: VMRow) {
  return settling.value[v.id]?.running ?? v.status === 'running'
}

function watchSettle(v: VMRow, verb: string, running: boolean) {
  settling.value = {
    ...settling.value,
    [v.id]: { verb, from: v.status, running, until: Date.now() + settleTimeoutMs },
  }
  if (!settlePoll) {
    settlePoll = setInterval(() => {
      if (!Object.keys(settling.value).length) {
        clearInterval(settlePoll)
        settlePoll = undefined
        return
      }
      load(true)
    }, settlePollMs)
  }
}

function since(s: string) {
  if (!s) return 'never'
  const at = new Date(s).getTime()
  if (Number.isNaN(at)) return s
  const secs = Math.max(0, Math.round((now.value - at) / 1000))
  if (secs < 60) return `${secs}s ago`
  const mins = Math.round(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hours = Math.round(mins / 60)
  return hours < 24 ? `${hours}h ago` : `${Math.round(hours / 24)}d ago`
}

const { authFetch } = useAuth()

const items = ref<VMRow[]>([])
const total = ref(0)
const limit = ref(20)
const offset = ref(0)
const loading = ref(true)
const error = ref<string | null>(null)

interface ClientOption {
  id: string
  hostname: string
  machine_id: string
  connected: boolean
  status: string
}

const clients = ref<ClientOption[]>([])
const hostnames = computed<Record<string, string>>(() =>
  Object.fromEntries(clients.value.filter(a => a.hostname).map(a => [a.id, a.hostname])))

const targetable = computed(() => clients.value.filter(a => a.connected && a.status !== 'revoked'))

async function readMessage(res: Response): Promise<string | null> {
  try {
    const b = await res.json()
    return typeof b?.message === 'string' ? b.message : null
  }
  catch {
    return null
  }
}

function fmtDate(s: string) {
  if (!s) return '—'
  const d = new Date(s)
  return Number.isNaN(d.getTime()) ? s : d.toLocaleString()
}

function fmtMemory(mib: number) {
  if (!mib) return '—'
  return mib >= 1024 ? `${(mib / 1024).toFixed(mib % 1024 ? 1 : 0)} GiB` : `${mib} MiB`
}

function clientLabel(id: string) {
  return hostnames.value[id] || `${id.slice(0, 8)}…`
}

type BadgeVariant = 'default' | 'secondary' | 'outline' | 'destructive'

const statusVariant: Record<string, BadgeVariant> = {
  running: 'default',
  pending: 'secondary',
  stopped: 'secondary',
  gone: 'outline',
  failed: 'destructive',
  stale: 'destructive',
  starting: 'secondary',
  stopping: 'secondary',
  destroying: 'secondary',
}

async function load(silent = false) {
  if (!silent) loading.value = true
  error.value = null
  try {
    const res = await authFetch(`/admin/vms?limit=${limit.value}&offset=${offset.value}`)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = await res.json()
    items.value = data.items ?? []
    total.value = data.total ?? 0
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load vms'
  }
  finally {
    loading.value = false
  }
}

async function loadClients() {
  try {
    const res = await authFetch('/admin/clients?limit=100')
    if (!res.ok) return
    const data = await res.json()
    clients.value = (data.items ?? []).map((a: ClientOption) => ({
      id: a.id,
      hostname: a.hostname,
      machine_id: a.machine_id,
      connected: a.connected,
      status: a.status,
    }))
  }
  catch {
  }
}

watch(items, (rows) => {
  const entries = Object.entries(settling.value)
  if (!entries.length) return
  const at = Date.now()
  const next: Record<string, Settling> = {}
  for (const [id, s] of entries) {
    const row = rows.find(r => r.id === id)
    if (row && row.status === s.from && at < s.until) next[id] = s
  }
  if (Object.keys(next).length !== entries.length) settling.value = next
})

const pollInterval = 60_000

let poll: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  load()
  loadClients()
  poll = setInterval(() => load(true), pollInterval)
  clock = setInterval(() => (now.value = Date.now()), 10_000)
})
onUnmounted(() => {
  clearInterval(poll)
  clearInterval(clock)
  clearInterval(settlePoll)
})

const syncing = ref(false)

async function syncNow() {
  if (syncing.value) return
  syncing.value = true
  clearInterval(poll)
  try {
    await Promise.all([load(true), loadClients()])
  }
  finally {
    poll = setInterval(() => load(true), pollInterval)
    syncing.value = false
  }
}

const page = computed(() => Math.floor(offset.value / limit.value) + 1)
const pageCount = computed(() => Math.max(1, Math.ceil(total.value / limit.value)))
function next() {
  if (offset.value + limit.value < total.value) {
    offset.value += limit.value
    load()
  }
}
function prev() {
  if (offset.value > 0) {
    offset.value = Math.max(0, offset.value - limit.value)
    load()
  }
}

const createOpen = ref(false)
const creating = ref(false)
const createError = ref<string | null>(null)

const blankForm = {
  client_id: '',
  name: '',
  default_port: '8000',
  public_ports: '',
  boot: 'direct',
  source: 'image',
  kernel: '',
  kernel_sha256: '',
  initrd: '',
  initrd_sha256: '',
  rootfs: '',
  rootfs_sha256: '',
  rootfs_tar: '',
  rootfs_tar_sha256: '',
  rootfs_size: '',
  append: '',
  disk: '',
  disk_sha256: '',
  firmware: '',
  disk_size: '',
  cpus: '1',
  memory_mib: '1024',
  no_network: false,
  ip: '',
  egress: '',
  egress_any: false,
  rate_mbit: '',
  burst_kbit: '',
}
const form = reactive({ ...blankForm })

function openCreate() {
  Object.assign(form, blankForm)
  createError.value = null
  form.client_id = targetable.value.length === 1 ? targetable.value[0]!.id : ''
  createOpen.value = true
  loadClients()
}

function validate(): string | null {
  if (!form.client_id) return 'Choose a host to run this VM on.'
  const cpus = Number(form.cpus)
  const memory = Number(form.memory_mib)
  if (!Number.isInteger(cpus) || cpus < 1) return 'cpus must be a whole number of at least 1.'
  if (!Number.isInteger(memory) || memory < 64) return 'memory must be at least 64 MiB.'
  if (form.boot === 'direct') {
    if (!form.kernel.trim()) return 'Direct boot needs a kernel.'
    if (form.source === 'image' && !form.rootfs.trim()) return 'Direct boot needs a root filesystem image.'
    if (form.source === 'tar' && !form.rootfs_tar.trim()) return 'Direct boot needs a root filesystem tar.'
  }
  else if (!form.disk.trim()) {
    return 'Disk boot needs a bootable disk image.'
  }
  return null
}

function buildSpec() {
  const spec: Record<string, unknown> = { boot: form.boot }
  const put = (key: string, value: string) => {
    const v = value.trim()
    if (v) spec[key] = v
  }
  const putNum = (key: string, value: string) => {
    const v = Number(value)
    if (value.trim() && Number.isFinite(v) && v > 0) spec[key] = v
  }

  put('name', form.name)
  spec.default_port = Number(form.default_port)
  spec.public_ports = form.public_ports.split(',').map(p => p.trim()).filter(Boolean).map(Number)
  spec.cpus = Number(form.cpus)
  spec.memory_mib = Number(form.memory_mib)
  put('disk_size', form.disk_size)

  if (form.boot === 'direct') {
    put('kernel', form.kernel)
    put('kernel_sha256', form.kernel_sha256)
    put('initrd', form.initrd)
    put('initrd_sha256', form.initrd_sha256)
    put('append', form.append)
    if (form.source === 'image') {
      put('rootfs', form.rootfs)
      put('rootfs_sha256', form.rootfs_sha256)
    }
    else {
      put('rootfs_tar', form.rootfs_tar)
      put('rootfs_tar_sha256', form.rootfs_tar_sha256)
      put('rootfs_size', form.rootfs_size)
    }
  }
  else {
    put('disk', form.disk)
    put('disk_sha256', form.disk_sha256)
    put('firmware', form.firmware)
  }

  if (form.no_network) {
    spec.no_network = true
  }
  else {
    put('ip', form.ip)
    if (form.egress_any) spec.egress_any = true
    const egress = form.egress.split(/[\s,]+/).map(s => s.trim()).filter(Boolean)
    if (egress.length) spec.egress = egress
    putNum('rate_mbit', form.rate_mbit)
    putNum('burst_kbit', form.burst_kbit)
  }
  return spec
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
    const res = await authFetch(`/admin/clients/${form.client_id}/vms`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(buildSpec()),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    createOpen.value = false
    offset.value = 0
    await load()
  }
  catch (e) {
    createError.value = e instanceof Error ? e.message : 'Could not create VM'
  }
  finally {
    creating.value = false
  }
}

const actionError = ref<string | null>(null)
const working = ref(false)

function hasGuest(v: VMRow) {
  return !!v.vm_id && v.status !== 'gone'
}

const toStop = ref<VMRow | null>(null)
const toDestroy = ref<VMRow | null>(null)
const toForget = ref<VMRow | null>(null)

function askRemove(v: VMRow) {
  if (hasGuest(v)) toDestroy.value = v
  else toForget.value = v
}

async function act(v: VMRow, path: string, method: string, failure: string, settle: { verb: string, running: boolean } | null) {
  working.value = true
  actionError.value = null
  try {
    const res = await authFetch(path, { method })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    if (settle) watchSettle(v, settle.verb, settle.running)
    toStop.value = null
    toDestroy.value = null
    toForget.value = null
    await load(true)
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : failure
  }
  finally {
    working.value = false
  }
}

function start(v: VMRow) {
  act(v, `/admin/vms/${v.id}/start`, 'POST', 'Could not start this VM', { verb: 'starting', running: true })
}
function confirmStop() {
  const v = toStop.value
  if (v) act(v, `/admin/vms/${v.id}/stop`, 'POST', 'Could not stop this VM', { verb: 'stopping', running: false })
}
function confirmDestroy() {
  const v = toDestroy.value
  if (v) act(v, `/admin/vms/${v.id}/destroy`, 'POST', 'Could not destroy this VM', { verb: 'destroying', running: false })
}
function confirmForget() {
  const v = toForget.value
  if (v) act(v, `/admin/vms/${v.id}`, 'DELETE', 'Could not delete this VM record', null)
}

function togglePower(v: VMRow, on: boolean) {
  if (on) start(v)
  else toStop.value = v
}

function closeDialogs() {
  toStop.value = null
  toDestroy.value = null
  toForget.value = null
  actionError.value = null
}
</script>

<template>
  <AdminShell>

    <div class="mt-8 flex items-end justify-between gap-4">
      <div>
        <p class="eyebrow mb-2 text-primary-text">// admin · vms</p>
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">VMs</h1>
      </div>
      <div class="flex shrink-0 items-center gap-3">
        <span class="hidden font-mono text-xs text-muted-foreground sm:inline">refreshes every 1m</span>
        <Button
          variant="outline"
          size="sm"
          class="font-mono text-xs"
          :disabled="syncing || loading"
          @click="syncNow"
        >
          <RefreshCw class="size-3.5" :class="syncing && 'animate-spin'" aria-hidden="true" />
          {{ syncing ? 'Syncing…' : 'Sync now' }}
        </Button>

        <Dialog v-model:open="createOpen">
          <Button class="font-mono text-xs" @click="openCreate">
            <Plus class="size-4" aria-hidden="true" />
            New VM
          </Button>
          <DialogScrollContent class="sm:max-w-xl">
            <DialogHeader>
              <DialogTitle>New VM</DialogTitle>
              <DialogDescription>
                Pushed to a connected client, which resolves the images and boots it.
                That takes minutes — the row appears as <span class="font-mono">pending</span>
                and settles when the host reports back.
              </DialogDescription>
            </DialogHeader>

            <form class="space-y-4" :aria-busy="creating" @submit.prevent="create">
              <div class="space-y-2">
                <Label for="vm-client">Host</Label>
                <div class="*:w-full">
                  <NativeSelect id="vm-client" v-model="form.client_id">
                    <NativeSelectOption value="" disabled>Choose a host…</NativeSelectOption>
                    <NativeSelectOption v-for="a in targetable" :key="a.id" :value="a.id">
                      {{ a.hostname || a.machine_id }}
                    </NativeSelectOption>
                  </NativeSelect>
                </div>
                <p v-if="!targetable.length" class="text-xs text-muted-foreground">
                  No client is connected. A VM can only be pushed to a live host.
                </p>
              </div>

              <div class="grid gap-4 sm:grid-cols-2">
                <div class="space-y-2">
                  <Label for="vm-name">Name</Label>
                  <Input
                    id="vm-name"
                    v-model="form.name"
                    placeholder="leave empty for a generated name"
                    aria-describedby="vm-name-hint"
                  />
                  <p id="vm-name-hint" class="text-xs text-muted-foreground">
                    Lowercase, digits and single hyphens, 3–52 characters. Unique across every VM.
                  </p>
                </div>
                <div class="space-y-2">
                  <Label for="vm-boot">Boot mode</Label>
                  <div class="*:w-full">
                    <NativeSelect id="vm-boot" v-model="form.boot">
                      <NativeSelectOption value="direct">direct — microVM, kernel + rootfs</NativeSelectOption>
                      <NativeSelectOption value="disk">disk — full image via firmware</NativeSelectOption>
                    </NativeSelect>
                  </div>
                </div>
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

              <div class="grid gap-4 sm:grid-cols-3">
                <div class="space-y-2">
                  <Label for="vm-cpus">vCPUs</Label>
                  <Input id="vm-cpus" v-model="form.cpus" type="number" min="1" inputmode="numeric" />
                </div>
                <div class="space-y-2">
                  <Label for="vm-mem">Memory (MiB)</Label>
                  <Input id="vm-mem" v-model="form.memory_mib" type="number" min="64" inputmode="numeric" />
                </div>
                <div class="space-y-2">
                  <Label for="vm-disksize">Disk size</Label>
                  <Input id="vm-disksize" v-model="form.disk_size" placeholder="e.g. 10G" />
                </div>
              </div>

              <div v-if="form.boot === 'direct'" class="space-y-4 rounded-lg border border-border p-4">
                <div class="space-y-2">
                  <Label for="vm-kernel">Kernel</Label>
                  <Input id="vm-kernel" v-model="form.kernel" placeholder="URL or path on the host" />
                </div>
                <div class="space-y-2">
                  <Label for="vm-source">Root filesystem</Label>
                  <div class="*:w-full">
                    <NativeSelect id="vm-source" v-model="form.source">
                      <NativeSelectOption value="image">ext4 image, ready to boot</NativeSelectOption>
                      <NativeSelectOption value="tar">tar from `docker export`, built on the host</NativeSelectOption>
                    </NativeSelect>
                  </div>
                </div>
                <div v-if="form.source === 'image'" class="space-y-2">
                  <Label for="vm-rootfs">Root filesystem image</Label>
                  <Input id="vm-rootfs" v-model="form.rootfs" placeholder="URL or path" />
                </div>
                <template v-else>
                  <div class="space-y-2">
                    <Label for="vm-tar">Root filesystem tar</Label>
                    <Input id="vm-tar" v-model="form.rootfs_tar" placeholder="URL or path" />
                  </div>
                  <div class="space-y-2">
                    <Label for="vm-rootfs-size">Built image size</Label>
                    <Input id="vm-rootfs-size" v-model="form.rootfs_size" placeholder="e.g. 2G — default 1.5× the tar" />
                  </div>
                </template>
              </div>

              <div v-else class="space-y-4 rounded-lg border border-border p-4">
                <div class="space-y-2">
                  <Label for="vm-disk">Disk image</Label>
                  <Input id="vm-disk" v-model="form.disk" placeholder="bootable image URL or path" />
                </div>
                <div class="space-y-2">
                  <Label for="vm-firmware">Firmware</Label>
                  <Input id="vm-firmware" v-model="form.firmware" placeholder="path to -bios firmware; default is qemu's own" />
                </div>
              </div>

              <div class="space-y-4 rounded-lg border border-border p-4">
                <div class="flex items-center gap-2">
                  <Checkbox id="vm-nonet" v-model="form.no_network" />
                  <Label for="vm-nonet" class="font-normal">No network device at all</Label>
                </div>
                <template v-if="!form.no_network">
                  <div class="space-y-2">
                    <Label for="vm-ip">Address</Label>
                    <Input id="vm-ip" v-model="form.ip" placeholder="pin an address; default is the lowest free one" />
                  </div>
                  <div class="space-y-2">
                    <Label for="vm-egress">Egress allowlist</Label>
                    <Input id="vm-egress" v-model="form.egress" placeholder="1.1.1.1, 10.0.0.0/8 — empty denies all outbound" />
                    <p class="text-xs text-muted-foreground">
                      Addresses or CIDRs, comma or space separated. Outbound is deny-by-default.
                    </p>
                  </div>
                  <div class="flex items-center gap-2">
                    <Checkbox id="vm-egress-any" v-model="form.egress_any" />
                    <Label for="vm-egress-any" class="font-normal">Allow any destination in the kernel</Label>
                  </div>
                </template>
              </div>

              <details class="rounded-lg border border-border">
                <summary class="cursor-pointer px-4 py-3 text-sm font-medium">Advanced</summary>
                <div class="space-y-4 border-t border-border p-4">
                  <template v-if="form.boot === 'direct'">
                    <div class="space-y-2">
                      <Label for="vm-kernel-sha">Kernel sha256</Label>
                      <Input id="vm-kernel-sha" v-model="form.kernel_sha256" class="font-mono" />
                    </div>
                    <div class="space-y-2">
                      <Label for="vm-initrd">Initramfs</Label>
                      <Input id="vm-initrd" v-model="form.initrd" placeholder="URL or path; optional" />
                    </div>
                    <div class="space-y-2">
                      <Label for="vm-initrd-sha">Initramfs sha256</Label>
                      <Input id="vm-initrd-sha" v-model="form.initrd_sha256" class="font-mono" />
                    </div>
                    <div class="space-y-2">
                      <Label :for="form.source === 'image' ? 'vm-rootfs-sha' : 'vm-tar-sha'">
                        {{ form.source === 'image' ? 'Root filesystem sha256' : 'Tar sha256' }}
                      </Label>
                      <Input
                        v-if="form.source === 'image'"
                        id="vm-rootfs-sha"
                        v-model="form.rootfs_sha256"
                        class="font-mono"
                      />
                      <Input v-else id="vm-tar-sha" v-model="form.rootfs_tar_sha256" class="font-mono" />
                    </div>
                    <div class="space-y-2">
                      <Label for="vm-append">Kernel command line</Label>
                      <Input id="vm-append" v-model="form.append" class="font-mono" placeholder="default: console + root=/dev/vda" />
                    </div>
                  </template>
                  <div v-else class="space-y-2">
                    <Label for="vm-disk-sha">Disk sha256</Label>
                    <Input id="vm-disk-sha" v-model="form.disk_sha256" class="font-mono" />
                  </div>

                  <template v-if="!form.no_network">
                    <div class="space-y-2">
                      <Label for="vm-rate">Rate limit (Mbit/s)</Label>
                      <Input id="vm-rate" v-model="form.rate_mbit" type="number" min="0" inputmode="numeric" placeholder="0 = unlimited" />
                    </div>
                    <div class="space-y-2">
                      <Label for="vm-burst">Burst (Kbit)</Label>
                      <Input id="vm-burst" v-model="form.burst_kbit" type="number" min="0" inputmode="numeric" placeholder="default: a tenth of a second at the rate" />
                    </div>
                  </template>
                </div>
              </details>

              <FormError id="create-vm-error" :message="createError" />

              <DialogFooter>
                <DialogClose as-child>
                  <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
                </DialogClose>
                <Button type="submit" class="font-mono text-xs" :disabled="creating || !targetable.length">
                  {{ creating ? 'Creating…' : 'Create' }}
                </Button>
              </DialogFooter>
            </form>
          </DialogScrollContent>
        </Dialog>
      </div>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load VMs</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <DataTable
      label="VMs"
      :columns="columns"
      :loading="loading"
      loading-label="Loading VMs…"
      :empty="!items.length"
      class="mt-6"
    >
      <template #empty>
        No VMs yet. Connected clients report what they are running every 30s, so
        anything made with <span class="font-mono">dclient vm create</span> appears
        here on its own. To ask an client for one, post to
        <span class="font-mono">/api/v1/admin/clients/&lt;id&gt;/vms</span>.
      </template>
      <TableRow v-for="v in items" :key="v.id">
        <TableCell>
          <NuxtLink
            :to="`/admin/model/vms/${v.id}`"
            class="font-mono text-primary-text underline decoration-primary-text/40 underline-offset-4 transition-colors hover:decoration-primary-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
            :aria-label="`View details for ${v.name || v.vm_id || 'this VM'}`"
          >
            {{ v.name || v.vm_id || '—' }}
          </NuxtLink>
          <div class="truncate font-mono text-xs text-muted-foreground">
            :{{ v.default_port }}<template v-if="v.public_ports?.length"> · {{ v.public_ports.join(', ') }}</template>
          </div>
          <div class="truncate font-mono text-xs text-muted-foreground">{{ v.vm_id || 'not assigned yet' }}</div>
        </TableCell>
        <TableCell class="font-mono text-muted-foreground">{{ clientLabel(v.client_id) }}</TableCell>
        <TableCell>
          <Badge :variant="statusVariant[displayStatus(v)]" class="font-mono">
            {{ displayStatus(v) }}
          </Badge>
          <div v-if="displayStatus(v) === 'stale'" class="mt-1 text-xs text-muted-foreground">
            was <span class="font-mono">{{ v.status }}</span>, last seen {{ since(v.reported_at) }}
          </div>
          <div
            v-else-if="v.last_error"
            class="mt-1 max-w-56 truncate text-xs text-destructive"
            :title="v.last_error"
          >
            {{ v.last_error }}
          </div>
        </TableCell>
        <TableCell class="font-mono text-muted-foreground">{{ v.boot || '—' }}</TableCell>
        <TableCell class="font-mono text-muted-foreground whitespace-nowrap">
          {{ v.cpus || '—' }}<span v-if="v.cpus"> vcpu</span> · {{ fmtMemory(v.memory_mib) }}
        </TableCell>
        <TableCell class="font-mono text-muted-foreground">{{ v.ip || '—' }}</TableCell>
        <TableCell class="text-muted-foreground whitespace-nowrap">{{ fmtDate(v.created_at) }}</TableCell>
        <TableCell class="text-right">
          <div class="flex items-center justify-end gap-1">
            <Switch
              :model-value="switchOn(v)"
              :disabled="!hasGuest(v) || !!isSettling(v) || working"
              :aria-label="`${switchOn(v) ? 'Stop' : 'Start'} VM ${v.name || v.vm_id || v.id}`"
              class="mr-1"
              @update:model-value="(on: boolean) => togglePower(v, on)"
            />
            <Button
              variant="ghost"
              size="icon"
              class="text-destructive hover:text-destructive"
              :disabled="!!isSettling(v) || working"
              :aria-label="hasGuest(v)
                ? `Destroy VM ${v.name || v.vm_id} and its disk`
                : `Delete the record for VM ${v.name || v.vm_id || v.id}`"
              @click="askRemove(v)"
            >
              <Trash2 class="size-4" aria-hidden="true" />
            </Button>
          </div>
        </TableCell>
      </TableRow>
    </DataTable>

    <nav aria-label="VMs pagination" class="mt-4 flex flex-wrap items-center justify-between gap-3 font-mono text-xs text-muted-foreground">
      <span>{{ total }} total · page {{ page }} / {{ pageCount }}</span>
      <div class="flex gap-2">
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset <= 0 || loading" aria-label="Previous page of VMs" @click="prev">Prev</Button>
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset + limit >= total || loading" aria-label="Next page of VMs" @click="next">Next</Button>
      </div>
    </nav>

    <Dialog :open="!!toStop" @update:open="(v: boolean) => { if (!v) closeDialogs() }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Stop VM</DialogTitle>
          <DialogDescription>
            Shut down <span class="font-mono text-foreground">{{ toStop?.name || toStop?.vm_id }}</span>
            on <span class="font-mono text-foreground">{{ clientLabel(toStop?.client_id ?? '') }}</span>.
            The guest is asked to power off cleanly and killed if it will not.
            Its disk and address are kept, so nothing is lost.
          </DialogDescription>
        </DialogHeader>
        <FormError id="stop-vm-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button class="font-mono text-xs" :disabled="working" @click="confirmStop">
            {{ working ? 'Stopping…' : 'Stop' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog :open="!!toDestroy" @update:open="(v: boolean) => { if (!v) closeDialogs() }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Destroy VM</DialogTitle>
          <DialogDescription>
            Stop <span class="font-mono text-foreground">{{ toDestroy?.name || toDestroy?.vm_id }}</span>
            on <span class="font-mono text-foreground">{{ clientLabel(toDestroy?.client_id ?? '') }}</span>
            and delete its disk and directory on the host. This cannot be undone.
            The record is kept and becomes <span class="font-mono">gone</span>.
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

    <Dialog :open="!!toForget" @update:open="(v: boolean) => { if (!v) closeDialogs() }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete VM record</DialogTitle>
          <DialogDescription>
            Remove
            <span class="font-mono text-foreground">{{ toForget?.name || toForget?.vm_id || toForget?.id }}</span>
            from the registry. Nothing on any host is touched — and if a guest for
            this row does still exist, its host's next inventory report will add it
            straight back.
          </DialogDescription>
        </DialogHeader>
        <FormError id="forget-vm-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="working" @click="confirmForget">
            {{ working ? 'Deleting…' : 'Delete record' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </AdminShell>
</template>
