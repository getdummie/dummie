<script setup lang="ts">
import { Check, ChevronsUpDown, Copy, Plus, RefreshCw, Trash2 } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
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
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { TableCell, TableRow } from '@/components/ui/table'
import type { DataTableColumn } from '@/lib/table'
import type { TargetForm } from '@/lib/targets'
import {
  applyPreset,
  blankTarget,
  describeTarget,
  destinationPlaceholder,
  domainPortChoices,
  portPresets,
  resetForKind,
  toTargetPayload,
} from '@/lib/targets'

const maxTargets = 32

const columns: DataTableColumn[] = [
  { key: 'name', label: 'Name' },
  { key: 'status', label: 'Status' },
  { key: 'size', label: 'Size' },
  { key: 'disk', label: 'Disk' },
  { key: 'address', label: 'Address' },
  { key: 'created', label: 'Created' },
  { key: 'power', label: 'Power' },
  { key: 'actions', label: 'Actions', align: 'right' },
]

definePageMeta({ middleware: ['auth'] })
useHead({ title: 'dummie — vms' })

interface VM {
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
  disk_mib: number
  ip: string
  last_error: string
  created_at: string
  spec: Record<string, unknown>
  expires_at: string
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

function fmtBytes(n: number) {
  if (!n) return '—'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${units[i]}`
}

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

const nowMs = ref(Date.now())
let ttlClock: ReturnType<typeof setInterval> | null = null
onMounted(() => {
  ttlClock = setInterval(() => { nowMs.value = Date.now() }, 30_000)
})
onBeforeUnmount(() => {
  if (ttlClock) clearInterval(ttlClock)
})

function expiryLabel(s: string) {
  if (!s) return ''
  const at = new Date(s).getTime()
  if (Number.isNaN(at)) return ''
  const left = at - nowMs.value
  if (left <= 0) return 'due to be destroyed'
  const mins = Math.round(left / 60_000)
  if (mins < 60) return `destroyed in ${Math.max(1, mins)}m`
  const hours = Math.round(mins / 60)
  return hours < 24 ? `destroyed in ${hours}h` : `destroyed in ${Math.round(hours / 24)}d`
}

async function load(quiet = false) {
  if (!quiet) loading.value = true
  error.value = null
  try {
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

const anyPending = computed(() => items.value.some(v => v.status === 'pending'))

const settleTimeoutMs = 60_000
const settling = ref<Record<string, { want: VM['status'], until: number }>>({})

const anySettling = computed(() => Object.keys(settling.value).length > 0)
const deleting = ref<string[]>([])
const anyDeleting = computed(() => deleting.value.length > 0)
let timer: ReturnType<typeof setInterval> | null = null

watch(items, (rows) => {
  const now = Date.now()
  const next: typeof settling.value = {}
  for (const [id, s] of Object.entries(settling.value)) {
    const row = rows.find(r => r.id === id)
    if (row && row.status !== s.want && now < s.until) next[id] = s
  }
  settling.value = next
  deleting.value = deleting.value.filter((id) => {
    const row = rows.find(r => r.id === id)
    return !!row && !row.last_error
  })
})

watch([anyPending, anySettling, anyDeleting], ([pending, waiting, purging]) => {
  if ((pending || waiting || purging) && !timer) {
    timer = setInterval(() => load(true), 5000)
  }
  else if (!pending && !waiting && !purging && timer) {
    clearInterval(timer)
    timer = null
  }
}, { immediate: true })

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})

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

const blockedReason = computed(() => {
  if (!hasPublicKey.value) return 'Cannot create a VM: add an SSH public key in Settings first'
  if (atCapacity.value) return 'Cannot create a VM: your allowance is fully used'
  return null
})

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

interface Kernel {
  id: string
  name: string
  description: string
  size_bytes: number
  created_at: string
  // OS images only: the port the container image exposes.
  default_port?: number
}

const kernels = ref<Kernel[]>([])
const kernelsError = ref<string | null>(null)
const kernelOpen = ref(false)

const osImages = ref<Kernel[]>([])
const osImagesError = ref<string | null>(null)
const osImageOpen = ref(false)

async function loadKernels() {
  kernelsError.value = null
  try {
    const res = await authFetch('/vms/kernels')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    kernels.value = (await res.json()).items ?? []
  }
  catch (e) {
    kernelsError.value = e instanceof Error ? e.message : 'Could not load kernels'
  }
}

async function loadOSImages() {
  osImagesError.value = null
  try {
    const res = await authFetch('/vms/osimages')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    osImages.value = (await res.json()).items ?? []
  }
  catch (e) {
    osImagesError.value = e instanceof Error ? e.message : 'Could not load OS images'
  }
}

const createOpen = ref(false)
const creating = ref(false)
const createError = ref<string | null>(null)

const blankForm = {
  client_id: '',
  name: '',
  cpus: '1',
  memory_mib: '512',
  default_port: '8000',
  public_ports: '',
  disk_size: '2G',
  kernel_id: '',
  osimage_id: '',
  ttl_seconds: '0',
}
const form = reactive({ ...blankForm })

const targets = ref<TargetForm[]>([])

function addTargetRow() {
  targets.value = [...targets.value, blankTarget()]
}

function removeTargetRow(i: number) {
  targets.value = targets.value.filter((_, n) => n !== i)
}

function onTargetKindChange(t: TargetForm) {
  resetForKind(t)
}

function onTargetPresetChange(t: TargetForm, key: string) {
  applyPreset(t, key)
}

const selectedKernel = computed(() => kernels.value.find(k => k.id === form.kernel_id) ?? null)
const selectedOSImage = computed(() => osImages.value.find(o => o.id === form.osimage_id) ?? null)

function pickKernel(id: string) {
  form.kernel_id = id
  kernelOpen.value = false
}

function pickOSImage(id: string) {
  form.osimage_id = id
  osImageOpen.value = false
  const port = osImages.value.find(o => o.id === id)?.default_port
  if (port) form.default_port = String(port)
}

function resetForm() {
  Object.assign(form, blankForm)
  targets.value = []
  createError.value = null
  copiedFrom.value = null
}

const copiedFrom = ref<string | null>(null)

async function openCreate() {
  resetForm()
  copiedFrom.value = null
  createOpen.value = true
  await Promise.all([loadHosts(), loadKernels(), loadOSImages()])
  if (hosts.value.length === 1) form.client_id = hosts.value[0]!.id
  form.kernel_id = kernels.value[0]?.id ?? ''
  form.osimage_id = osImages.value[0]?.id ?? ''
}

function copyable(v: VM) {
  return !!(v.spec?.kernel || v.spec?.rootfs_tar || v.spec?.rootfs)
}

function specStr(spec: Record<string, unknown>, key: string) {
  const v = spec?.[key]
  return typeof v === 'string' ? v : ''
}

async function openCopy(v: VM) {
  resetForm()
  copiedFrom.value = v.name || v.vm_id || 'that VM'

  form.cpus = String(v.cpus || 1)
  form.memory_mib = String(v.memory_mib || 512)
  form.default_port = String(v.default_port || 8000)
  form.public_ports = (v.public_ports ?? []).join(', ')
  form.disk_size = specStr(v.spec, 'disk_size') || '2G'

  createOpen.value = true
  await Promise.all([loadHosts(), loadKernels(), loadOSImages()])
  form.kernel_id = kernels.value[0]?.id ?? ''
  form.osimage_id = osImages.value[0]?.id ?? ''
  if (hosts.value.some(h => h.id === v.client_id)) form.client_id = v.client_id
  else if (hosts.value.length === 1) form.client_id = hosts.value[0]!.id
}

function sizeToMiB(s: string): number | null {
  const v = s.trim()
  if (!v) return 0
  const m = /^(\d+)([KkMmGgTt]?)$/.exec(v)
  if (!m) return null
  const mult: Record<string, number> = { '': 1, k: 1 << 10, m: 1 << 20, g: 1 << 30, t: 2 ** 40 }
  const bytes = Number(m[1]) * (mult[m[2]!.toLowerCase()] ?? 1)
  return Math.ceil(bytes / (1 << 20))
}

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

const namePattern = /^[a-z0-9]+(-[a-z0-9]+)*$/

function parsePorts(s: string): number[] {
  return s.split(',').map(p => p.trim()).filter(Boolean).map(Number)
}

function validate(): string | null {
  if (!form.client_id) return 'Choose a host to run this VM on.'
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
  if (!form.kernel_id) return 'Choose a kernel.'
  if (!form.osimage_id) return 'Choose an OS image.'
  if (form.disk_size.trim() && !/^\d+[KkMmGgTt]?$/.test(form.disk_size.trim())) {
    return 'Disk size must be a number, optionally with a K, M, G or T suffix — e.g. 2G.'
  }
  const ttl = Number(form.ttl_seconds)
  if (!Number.isInteger(ttl) || ttl < 0) return 'TTL must be a whole number of seconds, or 0 for no limit.'
  if (ttl > 0 && ttl < 10) return 'A TTL must be at least 10 seconds. Use 0 for no limit.'
  if (ttl > 30 * 24 * 3600) return 'A TTL must be at most 30 days (2592000 seconds).'

  if (targets.value.length > maxTargets) {
    return `A VM can start with at most ${maxTargets} destinations. Add the rest after it is created.`
  }
  for (const [i, t] of targets.value.entries()) {
    if (!t.destination.trim()) return `Destination ${i + 1} is empty. Fill it in or remove the row.`
    const tttl = Number(t.ttl_seconds)
    if (!Number.isInteger(tttl) || tttl < 0) return `Destination ${i + 1}: TTL must be a whole number of seconds, or 0.`
    if (tttl > 0 && tttl < 10) return `Destination ${i + 1}: a TTL must be at least 10 seconds. Use 0 for permanent.`
    if (tttl > 30 * 24 * 3600) return `Destination ${i + 1}: a TTL must be at most 30 days (2592000 seconds).`
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
        client_id: form.client_id,
        name: form.name,
        cpus: Number(form.cpus),
        memory_mib: Number(form.memory_mib),
        default_port: Number(form.default_port),
        public_ports: parsePorts(form.public_ports),
        disk_size: form.disk_size,
        kernel_id: form.kernel_id,
        osimage_id: form.osimage_id,
        ttl_seconds: Number(form.ttl_seconds),
        targets: targets.value.map(toTargetPayload),
      }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    const created = await res.json()
    createOpen.value = false
    resetForm()
    if (created?.id) await navigateTo(`/vms/${created.id}`)
    else await load(true)
  }
  catch (e) {
    createError.value = e instanceof Error ? e.message : 'Could not create the VM'
  }
  finally {
    creating.value = false
  }
}

function isRunning(v: VM) {
  const want = settling.value[v.id]?.want
  return want ? want === 'running' : v.status === 'running'
}

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

const toDelete = ref<VM | null>(null)
const working = ref(false)
const actionError = ref<string | null>(null)

function deletable(v: VM) {
  return v.status !== 'pending'
}

async function confirmDelete() {
  if (!toDelete.value) return
  const id = toDelete.value.id
  working.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/vms/${id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    if (res.status === 202) deleting.value = [...deleting.value, id]
    toDelete.value = null
    await load(true)
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not delete the VM'
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
                <Select v-model="form.client_id">
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
                <p id="vm-disk-hint" class="text-xs text-muted-foreground">
                  Size of this VM's disk. The guest still has to grow its own filesystem to use the space.
                </p>
                <p class="font-mono text-xs text-muted-foreground">{{ fmtMiB(diskRemaining) }} left</p>
              </div>

              <div class="space-y-2">
                <Label for="vm-ttl">TTL (seconds)</Label>
                <Input
                  id="vm-ttl"
                  v-model="form.ttl_seconds"
                  type="number"
                  min="0"
                  step="1"
                  inputmode="numeric"
                  aria-describedby="vm-ttl-hint"
                />
                <p id="vm-ttl-hint" class="text-xs text-muted-foreground">
                  0 means no limit. Anything else is a time to live in seconds: the VM is destroyed
                  automatically when it runs out and the disk goes with it. The clock starts now and
                  keeps running while the VM is stopped.
                </p>
              </div>

              <div class="space-y-2">
                <Label for="vm-kernel">Kernel</Label>
                <Popover v-model:open="kernelOpen">
                  <PopoverTrigger as-child>
                    <Button
                      id="vm-kernel"
                      type="button"
                      variant="outline"
                      role="combobox"
                      :aria-expanded="kernelOpen"
                      class="w-full justify-between font-mono text-xs font-normal"
                      :disabled="!kernels.length"
                    >
                      <span class="truncate">
                        {{ selectedKernel?.name ?? (kernels.length ? 'Choose a kernel' : 'No kernels available') }}
                      </span>
                      <ChevronsUpDown class="size-4 shrink-0 opacity-50" aria-hidden="true" />
                    </Button>
                  </PopoverTrigger>
                  <PopoverContent class="w-(--reka-popover-trigger-width) p-0">
                    <Command>
                      <CommandInput placeholder="Search kernels…" />
                      <CommandList>
                        <CommandEmpty>No kernel matches that.</CommandEmpty>
                        <CommandGroup>
                          <CommandItem
                            v-for="k in kernels"
                            :key="k.id"
                            :value="k.name"
                            class="gap-2"
                            @select="pickKernel(k.id)"
                          >
                            <Check
                              class="size-4 shrink-0"
                              :class="k.id === form.kernel_id ? 'opacity-100' : 'opacity-0'"
                              aria-hidden="true"
                            />
                            <span class="truncate font-mono text-xs">{{ k.name }}</span>
                            <span class="ml-auto shrink-0 font-mono text-xs text-muted-foreground">
                              {{ fmtBytes(k.size_bytes) }}
                            </span>
                          </CommandItem>
                        </CommandGroup>
                      </CommandList>
                    </Command>
                  </PopoverContent>
                </Popover>
                <p v-if="selectedKernel?.description" class="text-xs text-muted-foreground">
                  {{ selectedKernel.description }}
                </p>
                <p v-else-if="!kernels.length" class="text-xs text-muted-foreground">
                  {{ kernelsError ?? 'No kernels have been uploaded yet. Ask an admin to add one.' }}
                </p>
              </div>

              <div class="space-y-2">
                <Label for="vm-osimage">OS image</Label>
                <Popover v-model:open="osImageOpen">
                  <PopoverTrigger as-child>
                    <Button
                      id="vm-osimage"
                      type="button"
                      variant="outline"
                      role="combobox"
                      :aria-expanded="osImageOpen"
                      class="w-full justify-between font-mono text-xs font-normal"
                      :disabled="!osImages.length"
                    >
                      <span class="truncate">
                        {{ selectedOSImage?.name ?? (osImages.length ? 'Choose an OS image' : 'No OS images available') }}
                      </span>
                      <ChevronsUpDown class="size-4 shrink-0 opacity-50" aria-hidden="true" />
                    </Button>
                  </PopoverTrigger>
                  <PopoverContent class="w-(--reka-popover-trigger-width) p-0">
                    <Command>
                      <CommandInput placeholder="Search OS images…" />
                      <CommandList>
                        <CommandEmpty>No OS image matches that.</CommandEmpty>
                        <CommandGroup>
                          <CommandItem
                            v-for="o in osImages"
                            :key="o.id"
                            :value="o.name"
                            class="gap-2"
                            @select="pickOSImage(o.id)"
                          >
                            <Check
                              class="size-4 shrink-0"
                              :class="o.id === form.osimage_id ? 'opacity-100' : 'opacity-0'"
                              aria-hidden="true"
                            />
                            <span class="truncate font-mono text-xs">{{ o.name }}</span>
                            <span class="ml-auto shrink-0 font-mono text-xs text-muted-foreground">
                              {{ fmtBytes(o.size_bytes) }}
                            </span>
                          </CommandItem>
                        </CommandGroup>
                      </CommandList>
                    </Command>
                  </PopoverContent>
                </Popover>
                <p v-if="selectedOSImage?.description" class="text-xs text-muted-foreground">
                  {{ selectedOSImage.description }}
                </p>
                <p v-else-if="!osImages.length" class="text-xs text-muted-foreground">
                  {{ osImagesError ?? 'No OS images have been uploaded yet. Ask an admin to add one.' }}
                </p>
              </div>

              <div class="space-y-3 border-t border-border pt-4">
                <div class="flex flex-wrap items-start justify-between gap-3">
                  <div>
                    <p class="text-sm font-medium">Allowed destinations</p>
                    <p class="mt-1 text-xs text-muted-foreground">
                      Everything this VM may reach. Leave empty and it reaches nothing until you
                      allow something — you can add these later too.
                    </p>
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    class="font-mono text-xs"
                    :disabled="targets.length >= maxTargets"
                    @click="addTargetRow"
                  >
                    <Plus class="size-4" aria-hidden="true" />
                    Add
                  </Button>
                </div>

                <div
                  v-for="(t, i) in targets"
                  :key="i"
                  class="space-y-3 rounded-lg border border-border p-3"
                >
                  <div class="flex items-center justify-between gap-2">
                    <p class="eyebrow text-muted-foreground">Destination {{ i + 1 }}</p>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      class="size-7 text-destructive hover:text-destructive"
                      :aria-label="`Remove destination ${i + 1}`"
                      @click="removeTargetRow(i)"
                    >
                      <Trash2 class="size-4" aria-hidden="true" />
                    </Button>
                  </div>

                  <div class="grid gap-3 sm:grid-cols-2">
                    <div class="space-y-2">
                      <Label :for="`t-kind-${i}`">Type</Label>
                      <Select v-model="t.kind" @update:model-value="onTargetKindChange(t)">
                        <SelectTrigger :id="`t-kind-${i}`" class="w-full">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="domain">Domain</SelectItem>
                          <SelectItem value="ip">IP address or CIDR</SelectItem>
                        </SelectContent>
                      </Select>
                    </div>
                    <div class="space-y-2">
                      <Label :for="`t-dest-${i}`">Destination</Label>
                      <Input
                        :id="`t-dest-${i}`"
                        v-model="t.destination"
                        :placeholder="destinationPlaceholder(t.kind)"
                      />
                    </div>
                  </div>

                  <div v-if="t.kind === 'domain'" class="space-y-2">
                    <Label :for="`t-dports-${i}`">Allow on</Label>
                    <Select v-model="t.domainPorts">
                      <SelectTrigger :id="`t-dports-${i}`" class="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem v-for="c in domainPortChoices" :key="c.value" :value="c.value">
                          {{ c.label }}
                        </SelectItem>
                      </SelectContent>
                    </Select>
                  </div>

                  <template v-else>
                    <div class="space-y-2">
                      <Label :for="`t-preset-${i}`">Protocol</Label>
                      <Select v-model="t.preset" @update:model-value="(v: unknown) => onTargetPresetChange(t, String(v))">
                        <SelectTrigger :id="`t-preset-${i}`" class="w-full">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem v-for="p in portPresets" :key="p.key" :value="p.key">
                            {{ p.label }}
                          </SelectItem>
                        </SelectContent>
                      </Select>
                    </div>
                    <div class="grid gap-3 sm:grid-cols-2">
                      <div class="space-y-2">
                        <Label :for="`t-transport-${i}`">Transport</Label>
                        <Select v-model="t.transport">
                          <SelectTrigger :id="`t-transport-${i}`" class="w-full">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectItem value="tcp">tcp</SelectItem>
                            <SelectItem value="udp">udp</SelectItem>
                            <SelectItem value="icmp">icmp (ping)</SelectItem>
                            <SelectItem value="any">any</SelectItem>
                          </SelectContent>
                        </Select>
                      </div>
                      <div v-if="t.transport !== 'icmp'" class="space-y-2">
                        <Label :for="`t-ports-${i}`">Ports</Label>
                        <Input
                          :id="`t-ports-${i}`"
                          v-model="t.ports"
                          placeholder="443, 80,443, 1000:2000"
                        />
                      </div>
                    </div>
                  </template>

                  <div class="grid gap-3 sm:grid-cols-2">
                    <div class="space-y-2">
                      <Label :for="`t-note-${i}`">Note</Label>
                      <Input :id="`t-note-${i}`" v-model="t.note" placeholder="why this is needed" />
                    </div>
                    <div class="space-y-2">
                      <Label :for="`t-ttl-${i}`">TTL (seconds)</Label>
                      <Input
                        :id="`t-ttl-${i}`"
                        v-model="t.ttl_seconds"
                        type="number"
                        min="0"
                        step="1"
                        inputmode="numeric"
                      />
                    </div>
                  </div>

                  <p class="font-mono text-xs text-muted-foreground">
                    {{ describeTarget(t) }}{{ Number(t.ttl_seconds) > 0 ? ` · expires in ${t.ttl_seconds}s` : '' }}
                  </p>
                </div>
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
        Delete a VM to free capacity, or ask an admin to raise your limit.
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

    <DataTable
      label="Your VMs"
      :columns="columns"
      :loading="loading"
      :loading-rows="3"
      loading-label="Loading your VMs…"
      :empty="!items.length"
      class="mt-6"
    >
      <template #empty>
        You have no VMs yet.
      </template>
      <TableRow v-for="v in items" :key="v.id">
        <TableCell>
          <NuxtLink
            :to="`/vms/${v.id}`"
            class="font-mono text-primary-text underline decoration-primary-text/40 underline-offset-4 transition-colors hover:decoration-primary-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
            :aria-label="`View details for ${v.name || v.vm_id || 'this VM'}`"
          >
            {{ v.name || v.vm_id || '—' }}
          </NuxtLink>
          <span v-if="v.status === 'failed' && v.last_error" class="mt-1 block text-xs text-destructive">
            {{ v.last_error }}
          </span>
          <span v-if="v.expires_at && v.status !== 'gone'" class="mt-1 block font-mono text-xs text-muted-foreground">
            {{ expiryLabel(v.expires_at) }}
          </span>
        </TableCell>
        <TableCell>
          <Badge :variant="statusVariant[v.status]" class="font-mono">{{ v.status }}</Badge>
        </TableCell>
        <TableCell class="font-mono text-muted-foreground">
          {{ v.cpus }} vCPU · {{ fmtMiB(v.memory_mib) }}
        </TableCell>
        <TableCell class="font-mono text-muted-foreground">
          {{ v.disk_mib ? fmtMiB(v.disk_mib) : '—' }}
        </TableCell>
        <TableCell class="font-mono text-muted-foreground">{{ v.ip || '—' }}</TableCell>
        <TableCell class="text-muted-foreground">{{ fmtDate(v.created_at) }}</TableCell>
        <TableCell>
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
              :disabled="!deletable(v) || deleting.includes(v.id)"
              :aria-label="!deletable(v)
                ? `Cannot delete ${v.name || v.vm_id}: it is still being created`
                : deleting.includes(v.id)
                  ? `Deleting ${v.name || v.vm_id}`
                  : `Delete VM ${v.name || v.vm_id}`"
              @click="toDelete = v"
            >
              <Trash2 class="size-4" aria-hidden="true" />
            </Button>
          </div>
        </TableCell>
      </TableRow>
    </DataTable>

    <Dialog :open="!!toDelete" @update:open="(v: boolean) => { if (!v) toDelete = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete VM</DialogTitle>
          <DialogDescription>
            <span class="font-mono text-foreground">{{ toDelete?.name || toDelete?.vm_id }}</span>
            is shut down, its disk is deleted on the host, and its record here goes
            with it. This cannot be undone. The capacity it holds is returned to your
            allowance.
          </DialogDescription>
        </DialogHeader>
        <FormError id="delete-vm-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="working" @click="confirmDelete">
            {{ working ? 'Deleting…' : 'Delete' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
