<script setup lang="ts">
import { ArrowLeft, ArrowRightLeft, HardDriveUpload, Trash2 } from '@lucide/vue'
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
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { TableCell, TableRow } from '@/components/ui/table'
import type { DataTableColumn } from '@/lib/table'

definePageMeta({ middleware: ['auth', 'admin'] })

interface AdminVM {
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
  disk_backend: 'local' | 'fsx'
  disk_moving: boolean
  host_moving: boolean
  local_disk_kept: boolean
  ip: string
  spec: Record<string, unknown>
  last_error: string
  created_at: string
  started_at: string
  created_by: string
  reported_at: string
}

interface Target {
  id: string
  destination: string
  kind: 'domain' | 'ip'
  transport: '' | 'tcp' | 'udp' | 'any'
  ports: string
  note: string
  created_at: string
}

const targetColumns: DataTableColumn[] = [
  { key: 'destination', label: 'Destination' },
  { key: 'matched', label: 'Matched on' },
  { key: 'transport', label: 'Transport' },
  { key: 'ports', label: 'Ports' },
  { key: 'note', label: 'Note' },
]

function matchedOn(t: Target) {
  if (t.kind === 'ip' && t.destination === '0.0.0.0/0' && t.transport === 'any' && !t.ports) {
    return 'everything — every name and address'
  }
  if (t.kind !== 'domain') return 'address'
  return t.ports === 'none' ? 'name — resolves only' : 'tls sni · http host'
}

function portsLabel(t: Target) {
  if (t.kind !== 'domain') return t.ports || 'any'
  return t.ports === 'none' ? 'none' : (t.ports || '443, 80')
}

const route = useRoute()
const { authFetch } = useAuth()
const id = computed(() => String(route.params.id))

const vm = ref<AdminVM | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)
const actionError = ref<string | null>(null)

useHead(() => ({
  title: vm.value ? `dummie — admin · vm · ${vm.value.name || vm.value.vm_id}` : 'dummie — admin · vm',
}))

async function readMessage(res: Response): Promise<string | null> {
  try {
    const b = await res.json()
    return typeof b?.message === 'string' ? b.message : null
  }
  catch {
    return null
  }
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

function fmtMiB(mib: number) {
  if (mib < 1024) return `${mib} MiB`
  const gib = mib / 1024
  return Number.isInteger(gib) ? `${gib} GiB` : `${gib.toFixed(1)} GiB`
}

const staleAfterMs = 2 * 60_000
const hostReported = new Set(['running', 'stopped'])
const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined

const settleTimeoutMs = 60_000
const settling = ref<{ verb: string, running: boolean, from: string, until: number } | null>(null)

const displayStatus = computed(() => {
  const v = vm.value
  if (!v) return ''
  if (settling.value) return settling.value.verb
  if (!hostReported.has(v.status)) return v.status
  const at = v.reported_at ? new Date(v.reported_at).getTime() : Number.NaN
  if (Number.isNaN(at) || now.value - at > staleAfterMs) return 'stale'
  return v.status
})

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

const hostname = ref('')
const hostFor = ref('')
const onlineHosts = ref<{ id: string, hostname: string }[]>([])
const owner = ref<{ id: string, username: string } | null>(null)

async function loadHost(clientID: string) {
  hostFor.value = clientID
  try {
    const res = await authFetch('/admin/clients?limit=100')
    if (!res.ok) return
    const items: { id: string, hostname: string, status: string }[] = (await res.json()).items ?? []
    hostname.value = items.find(a => a.id === clientID)?.hostname ?? ''
    onlineHosts.value = items.filter(a => a.status === 'online' && a.id !== clientID)
  }
  catch {
  }
}

async function loadOwner(userID: string) {
  try {
    const res = await authFetch(`/admin/users/${userID}`)
    if (!res.ok) return
    const u = await res.json()
    owner.value = { id: u.id, username: u.username }
  }
  catch {
  }
}

const targets = ref<Target[]>([])
const targetsLoading = ref(true)
const targetsError = ref<string | null>(null)

async function loadTargets() {
  targetsLoading.value = true
  targetsError.value = null
  try {
    const res = await authFetch(`/admin/vms/${id.value}/targets`)
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    targets.value = (await res.json()).items ?? []
  }
  catch (e) {
    targetsError.value = e instanceof Error ? e.message : 'Failed to load destinations'
  }
  finally {
    targetsLoading.value = false
  }
}

const specJSON = computed(() => JSON.stringify(vm.value?.spec ?? {}, null, 2))
const hasSpec = computed(() => Object.keys(vm.value?.spec ?? {}).length > 0)

async function load(quiet = false) {
  if (!quiet) loading.value = true
  error.value = null
  try {
    const res = await authFetch(`/admin/vms/${id.value}`)
    if (res.status === 404) throw new Error('This VM does not exist, or its record was deleted.')
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    const data: AdminVM = await res.json()
    vm.value = data
    if (data.client_id && data.client_id !== hostFor.value) loadHost(data.client_id)
    if (!owner.value && data.created_by) loadOwner(data.created_by)
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load this VM'
  }
  finally {
    loading.value = false
  }
}
onMounted(() => {
  load()
  loadTargets()
  clock = setInterval(() => (now.value = Date.now()), 10_000)
})

watch(vm, (v) => {
  const s = settling.value
  if (s && v && (v.status !== s.from || Date.now() >= s.until)) settling.value = null
})

let timer: ReturnType<typeof setInterval> | null = null
watch([() => vm.value?.status, () => vm.value?.disk_moving, () => vm.value?.host_moving, settling], () => {
  const busy = vm.value?.status === 'pending' || !!vm.value?.disk_moving || !!vm.value?.host_moving || !!settling.value
  if (busy && !timer) timer = setInterval(() => load(true), 5000)
  else if (!busy && timer) {
    clearInterval(timer)
    timer = null
  }
}, { immediate: true })

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
  clearInterval(clock)
})

const working = ref(false)
const stopOpen = ref(false)
const destroyOpen = ref(false)
const forgetOpen = ref(false)
const moveOpen = ref(false)
const dropOpen = ref(false)

const hasGuest = computed(() => !!vm.value?.vm_id && vm.value.status !== 'gone')
const switchable = computed(() => {
  const v = vm.value
  return !!v?.vm_id && !v.disk_moving && !v.host_moving && (v.status === 'running' || v.status === 'stopped')
})
const movable = computed(() => {
  const v = vm.value
  return !!v && v.disk_backend === 'local' && v.status === 'stopped' && !v.disk_moving
})
const hostMovable = computed(() => {
  const v = vm.value
  return !!v && v.disk_backend === 'fsx' && v.status === 'stopped' && !v.host_moving
})
const hostOpen = ref(false)
const hostTarget = ref('')

async function confirmHostMove() {
  working.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/vms/${id.value}/move-host`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ client_id: hostTarget.value }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    hostOpen.value = false
    await load(true)
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not change the host'
  }
  finally {
    working.value = false
  }
}
const isRunning = computed(() => settling.value?.running ?? vm.value?.status === 'running')

async function act(path: string, method: string, failure: string, settle: { verb: string, running: boolean } | null) {
  const v = vm.value
  if (!v) return
  working.value = true
  actionError.value = null
  try {
    const res = await authFetch(path, { method })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    if (settle) settling.value = { ...settle, from: v.status, until: Date.now() + settleTimeoutMs }
    stopOpen.value = false
    destroyOpen.value = false
    moveOpen.value = false
    dropOpen.value = false
    await load(true)
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : failure
  }
  finally {
    working.value = false
  }
}

function togglePower(on: boolean) {
  if (on) act(`/admin/vms/${id.value}/start`, 'POST', 'Could not start this VM', { verb: 'starting', running: true })
  else stopOpen.value = true
}
function confirmStop() {
  act(`/admin/vms/${id.value}/stop`, 'POST', 'Could not stop this VM', { verb: 'stopping', running: false })
}
function confirmMove() {
  act(`/admin/vms/${id.value}/move-disk`, 'POST', 'Could not move the disk', null)
}
function confirmDrop() {
  act(`/admin/vms/${id.value}/drop-local-disk`, 'POST', 'Could not delete the local disk', null)
}
function confirmDestroy() {
  act(`/admin/vms/${id.value}/destroy`, 'POST', 'Could not destroy this VM', { verb: 'destroying', running: false })
}

const forgetting = ref(false)
async function confirmForget() {
  forgetting.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/vms/${id.value}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    await navigateTo('/admin/model/vms')
  }
  catch (e) {
    forgetOpen.value = false
    actionError.value = e instanceof Error ? e.message : 'Could not delete this VM record'
  }
  finally {
    forgetting.value = false
  }
}
</script>

<template>
  <AdminShell>

    <NuxtLink
      to="/admin/model/vms"
      class="mt-8 inline-flex items-center gap-1.5 font-mono text-xs text-muted-foreground underline-offset-4 transition-colors hover:text-foreground hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
    >
      <ArrowLeft class="size-3.5" aria-hidden="true" />
      All VMs
    </NuxtLink>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load this VM</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <div v-else-if="loading" class="mt-4 space-y-6" aria-busy="true">
      <p class="sr-only">Loading this VM…</p>
      <Skeleton class="h-9 w-64" aria-hidden="true" />
      <Skeleton class="h-56 w-full rounded-lg" aria-hidden="true" />
      <Skeleton class="h-64 w-full rounded-lg" aria-hidden="true" />
    </div>

    <template v-else-if="vm">
      <div class="mt-4 flex flex-wrap items-end justify-between gap-4">
        <div>
          <p class="eyebrow mb-2 text-primary-text">// admin · vms</p>
          <div class="flex flex-wrap items-center gap-3">
            <h1 class="font-mono text-2xl font-semibold tracking-tight sm:text-3xl">
              {{ vm.name || vm.vm_id || 'unnamed' }}
            </h1>
            <Badge :variant="statusVariant[displayStatus] ?? 'secondary'" class="font-mono">
              {{ displayStatus }}
            </Badge>
            <Badge v-if="vm.disk_moving" variant="secondary" class="font-mono">moving disk to FSx…</Badge>
            <Badge v-if="vm.host_moving" variant="secondary" class="font-mono">changing host…</Badge>
          </div>
          <p v-if="displayStatus === 'stale'" class="mt-2 text-xs text-muted-foreground">
            was <span class="font-mono">{{ vm.status }}</span>, last confirmed {{ since(vm.reported_at) }}
          </p>
        </div>
        <div class="flex flex-wrap items-center gap-4">
          <div class="flex items-center gap-2.5">
            <Label for="vm-power" class="font-mono text-xs text-muted-foreground">Power</Label>
            <Switch
              id="vm-power"
              :model-value="isRunning"
              :disabled="!switchable || !!settling || working"
              :aria-label="switchable
                ? `${isRunning ? 'Stop' : 'Start'} this VM`
                : `Cannot start or stop this VM: it is ${vm.status}`"
              @update:model-value="togglePower"
            />
          </div>
          <Button
            v-if="movable"
            variant="outline"
            size="sm"
            class="font-mono text-xs"
            :disabled="working"
            @click="actionError = null; moveOpen = true"
          >
            <HardDriveUpload class="size-4" aria-hidden="true" />
            Move disk to FSx
          </Button>
          <Button
            v-if="hostMovable"
            variant="outline"
            size="sm"
            class="font-mono text-xs"
            :disabled="working"
            @click="actionError = null; hostTarget = ''; loadHost(vm.client_id); hostOpen = true"
          >
            <ArrowRightLeft class="size-4" aria-hidden="true" />
            Change host
          </Button>
          <Button
            v-if="vm.local_disk_kept"
            variant="outline"
            size="sm"
            class="font-mono text-xs text-destructive hover:text-destructive"
            :disabled="working"
            @click="actionError = null; dropOpen = true"
          >
            <Trash2 class="size-4" aria-hidden="true" />
            Delete local disk
          </Button>
          <Button
            v-if="hasGuest"
            variant="outline"
            size="sm"
            class="font-mono text-xs text-destructive hover:text-destructive"
            :disabled="working"
            aria-label="Destroy this VM on its host"
            @click="destroyOpen = true"
          >
            <Trash2 class="size-4" aria-hidden="true" />
            Destroy
          </Button>
          <Button
            v-else
            variant="outline"
            size="sm"
            class="font-mono text-xs text-destructive hover:text-destructive"
            :disabled="working"
            aria-label="Delete this VM record"
            @click="forgetOpen = true"
          >
            <Trash2 class="size-4" aria-hidden="true" />
            Delete record
          </Button>
        </div>
      </div>

      <Alert v-if="actionError" variant="destructive" class="mt-4">
        <AlertTitle>Action failed</AlertTitle>
        <AlertDescription>{{ actionError }}</AlertDescription>
      </Alert>

      <Alert v-if="vm.last_error" variant="destructive" class="mt-4">
        <AlertTitle>Last reported failure</AlertTitle>
        <AlertDescription>{{ vm.last_error }}</AlertDescription>
      </Alert>

      <section aria-labelledby="details-heading" class="mt-6 rounded-lg border border-border p-4 sm:p-6">
        <h2 id="details-heading" class="text-sm font-semibold">Details</h2>
        <dl class="mt-4 grid gap-x-8 gap-y-4 sm:grid-cols-2 lg:grid-cols-3">
          <div>
            <dt class="eyebrow text-muted-foreground">Host</dt>
            <dd class="mt-1 text-sm">
              <NuxtLink
                :to="`/admin/model/clients/${vm.client_id}`"
                class="font-mono break-all text-primary-text underline decoration-primary-text/40 underline-offset-4 transition-colors hover:decoration-primary-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
              >
                {{ hostname || vm.client_id }}
              </NuxtLink>
            </dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Owner</dt>
            <dd class="mt-1 text-sm">
              <NuxtLink
                v-if="owner"
                :to="`/admin/model/users/${owner.id}`"
                class="font-mono text-primary-text underline decoration-primary-text/40 underline-offset-4 transition-colors hover:decoration-primary-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
              >
                {{ owner.username }}
              </NuxtLink>
              <span v-else-if="vm.created_by" class="font-mono text-xs break-all text-muted-foreground">
                {{ vm.created_by }}
              </span>
              <span v-else class="text-muted-foreground">not recorded</span>
            </dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Address</dt>
            <dd class="mt-1 font-mono text-sm">{{ vm.ip || '—' }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Size</dt>
            <dd class="mt-1 font-mono text-sm">{{ vm.cpus }} vCPU · {{ fmtMiB(vm.memory_mib) }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Disk</dt>
            <dd class="mt-1 font-mono text-sm">
              {{ vm.disk_mib ? fmtMiB(vm.disk_mib) : 'not recorded' }}
              {{ vm.disk_backend === 'fsx' ? 'on FSx' : 'on the host' }}
              <span v-if="vm.local_disk_kept" class="block text-xs text-muted-foreground">
                its old local disk is still on the host
              </span>
            </dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Boot mode</dt>
            <dd class="mt-1 font-mono text-sm">{{ vm.boot || '—' }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Host VM id</dt>
            <dd class="mt-1 font-mono text-sm break-all">{{ vm.vm_id || 'not assigned yet' }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Record id</dt>
            <dd class="mt-1 font-mono text-xs break-all text-muted-foreground">{{ vm.id }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Created</dt>
            <dd class="mt-1 text-sm text-muted-foreground">{{ fmtDate(vm.created_at) }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Started</dt>
            <dd class="mt-1 text-sm text-muted-foreground">{{ fmtDate(vm.started_at) }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Last confirmed by host</dt>
            <dd class="mt-1 text-sm text-muted-foreground">{{ fmtDate(vm.reported_at) }}</dd>
          </div>
        </dl>
      </section>

      <section aria-labelledby="routing-heading" class="mt-6 rounded-lg border border-border p-4 sm:p-6">
        <h2 id="routing-heading" class="text-sm font-semibold">Routing</h2>
        <p class="mt-1 max-w-2xl text-sm text-muted-foreground">
          Which ports inside this VM are reachable, and where a request goes when it does not pick
          one. Its owner changes these from their own VM page.
        </p>
        <dl class="mt-4 grid gap-x-8 gap-y-4 sm:grid-cols-2">
          <div>
            <dt class="eyebrow text-muted-foreground">Default port</dt>
            <dd class="mt-1 font-mono text-sm">{{ vm.default_port }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Public ports</dt>
            <dd class="mt-1 font-mono text-sm">
              {{ vm.public_ports?.length ? vm.public_ports.join(', ') : 'none' }}
            </dd>
          </div>
        </dl>
      </section>

      <section aria-labelledby="targets-heading" class="mt-6 rounded-lg border border-border">
        <div class="p-4 sm:p-6">
          <h2 id="targets-heading" class="text-sm font-semibold">Allowed destinations</h2>
          <p class="mt-1 max-w-2xl text-sm text-muted-foreground">
            Domains and addresses this VM's owner declared it needs to reach. Recorded only for
            now — nothing enforces this list yet. Its owner adds and removes entries from their
            own VM page.
          </p>
          <Alert v-if="targetsError" variant="destructive" class="mt-4">
            <AlertTitle>Could not load destinations</AlertTitle>
            <AlertDescription>{{ targetsError }}</AlertDescription>
          </Alert>
        </div>
        <DataTable
          v-if="!targetsError"
          label="Allowed destinations"
          :columns="targetColumns"
          :loading="targetsLoading"
          :loading-rows="2"
          loading-label="Loading destinations…"
          :empty="!targets.length"
          :frame="false"
        >
          <template #empty>
            No destinations recorded.
          </template>
          <TableRow v-for="t in targets" :key="t.id">
            <TableCell class="font-mono break-all">{{ t.destination }}</TableCell>
            <TableCell class="font-mono text-xs text-muted-foreground">
              {{ matchedOn(t) }}
            </TableCell>
            <TableCell class="font-mono text-muted-foreground">{{ t.transport || '—' }}</TableCell>
            <TableCell class="font-mono text-muted-foreground">{{ portsLabel(t) }}</TableCell>
            <TableCell class="text-muted-foreground">{{ t.note || '—' }}</TableCell>
          </TableRow>
        </DataTable>
      </section>

      <section aria-labelledby="spec-heading" class="mt-6 rounded-lg border border-border p-4 sm:p-6">
        <h2 id="spec-heading" class="text-sm font-semibold">Spec</h2>
        <p class="mt-1 max-w-2xl text-sm text-muted-foreground">
          Exactly what the host was asked to build. Empty for a VM adopted from an client's
          inventory, since nothing here asked for it.
        </p>
        <pre
          v-if="hasSpec"
          class="mt-4 overflow-x-auto rounded-lg border border-border bg-muted/40 p-4 font-mono text-xs"
        >{{ specJSON }}</pre>
        <p v-else class="mt-4 font-mono text-xs text-muted-foreground">No spec recorded.</p>
      </section>
    </template>

    <Dialog v-model:open="stopOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Stop VM</DialogTitle>
          <DialogDescription>
            <span class="font-mono text-foreground">{{ vm?.name || vm?.vm_id }}</span>
            is shut down on its host. Its disk is kept, so it can be started again.
          </DialogDescription>
        </DialogHeader>
        <FormError id="stop-vm-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="working" @click="confirmStop">
            {{ working ? 'Stopping…' : 'Stop' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog v-model:open="destroyOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Destroy VM</DialogTitle>
          <DialogDescription>
            <span class="font-mono text-foreground">{{ vm?.name || vm?.vm_id }}</span>
            is shut down and its disk is deleted on the host. This cannot be undone, and it is
            someone else's VM: the capacity it holds is returned to their allowance.
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

    <Dialog v-model:open="moveOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Move disk to FSx</DialogTitle>
          <DialogDescription>
            Copies the disk of <span class="font-mono text-foreground">{{ vm?.name || vm?.vm_id }}</span>
            onto its own FSx for OpenZFS volume, and boots it from there from then on. It cannot be
            started until the copy is done. The local disk stays on the host until you delete it.
          </DialogDescription>
        </DialogHeader>
        <FormError id="move-disk-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button class="font-mono text-xs" :disabled="working" @click="confirmMove">
            {{ working ? 'Starting…' : 'Move' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog v-model:open="hostOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Change host</DialogTitle>
          <DialogDescription>
            Hands <span class="font-mono text-foreground">{{ vm?.name || vm?.vm_id }}</span> over to
            another host. Its disk stays on FSx. It gets a new address there, and its URL follows the
            new host's domain. Any local disk left on the current host is deleted.
          </DialogDescription>
        </DialogHeader>
        <div class="space-y-2">
          <Label for="move-host">Move to</Label>
          <Select v-model="hostTarget">
            <SelectTrigger id="move-host" class="w-full font-mono text-xs">
              <SelectValue placeholder="choose a connected host" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-for="h in onlineHosts" :key="h.id" :value="h.id" class="font-mono text-xs">
                {{ h.hostname || h.id }}
              </SelectItem>
            </SelectContent>
          </Select>
        </div>
        <FormError id="move-host-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button class="font-mono text-xs" :disabled="working || !hostTarget" @click="confirmHostMove">
            {{ working ? 'Starting…' : 'Move' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog v-model:open="dropOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete local disk</DialogTitle>
          <DialogDescription>
            Deletes the disk <span class="font-mono text-foreground">{{ vm?.name || vm?.vm_id }}</span>
            booted from before it moved to FSx. Boot it from FSx at least once first; this cannot be
            undone.
          </DialogDescription>
        </DialogHeader>
        <FormError id="drop-disk-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="working" @click="confirmDrop">
            {{ working ? 'Deleting…' : 'Delete' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog v-model:open="forgetOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete VM record</DialogTitle>
          <DialogDescription>
            Removes this row only. Nothing is asked of the host, so if a guest does still exist
            there it keeps running and reappears on the next inventory report.
          </DialogDescription>
        </DialogHeader>
        <FormError id="forget-vm-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="forgetting" @click="confirmForget">
            {{ forgetting ? 'Deleting…' : 'Delete' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </AdminShell>
</template>
