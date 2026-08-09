<script setup lang="ts">
import { ArrowLeft, Trash2 } from '@lucide/vue'
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
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { TableCell, TableRow } from '@/components/ui/table'
import type { DataTableColumn } from '@/lib/table'

definePageMeta({ middleware: ['auth', 'admin'] })

interface AdminVM {
  id: string
  agent_id: string
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
  spec: Record<string, unknown>
  last_error: string
  created_at: string
  started_at: string
  // "" for a VM nobody here asked for: adopted from a host's inventory, or made
  // before ownership was recorded.
  created_by: string
  // When the host last confirmed this VM; "" if it never has.
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

function fmtMiB(mib: number) {
  if (mib < 1024) return `${mib} MiB`
  const gib = mib / 1024
  return Number.isInteger(gib) ? `${gib} GiB` : `${gib.toFixed(1)} GiB`
}

// --- staleness ---
//
// Same reasoning as the list: 'running' is what the host claimed when it last
// reported, not a live reading, so a claim nobody has renewed in four missed
// 30s reports stops being shown as fact. The clock ticks on its own, because a
// control server that has gone unreachable is exactly when a stale 'running'
// most needs to stop being believed.
const staleAfterMs = 2 * 60_000
const hostReported = new Set(['running', 'stopped'])
const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined

const settleTimeoutMs = 60_000
// `from` is the status at the moment the action was sent, which is how we
// recognise that the row has moved. `until` bounds the wait, so an agent that
// never answers leaves the page telling the truth rather than spinning forever.
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

// --- host and owner ---
//
// A VM row carries ids, not names. Both are resolved with a second request and
// fall back to the id, so neither lookup failing can break the page it decorates.
const hostname = ref('')
const owner = ref<{ id: string, username: string } | null>(null)

async function loadHost(agentID: string) {
  try {
    const res = await authFetch('/admin/agents?limit=100')
    if (!res.ok) return
    const data = await res.json()
    const match = (data.items ?? []).find((a: { id: string }) => a.id === agentID)
    if (match?.hostname) hostname.value = match.hostname
  }
  catch {
    // the id stays on screen
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
    // the id stays on screen
  }
}

// --- allowed destinations ---
//
// Its own request and its own error: nothing here can be acted on, so a failure
// to read the list is a gap in one card rather than a reason to blank the page.
// Fetched once — this page cannot change the list, and neither can the host.
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
    if (!hostname.value && data.agent_id) loadHost(data.agent_id)
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
  // Well under staleAfterMs, so the badge turns within a few seconds of the
  // claim actually going stale rather than on the next poll.
  clock = setInterval(() => (now.value = Date.now()), 10_000)
})

// A settle is done once the server has moved the row off the status it had, or
// once the wait runs out — true as well for a change someone else made.
watch(vm, (v) => {
  const s = settling.value
  if (s && v && (v.status !== s.from || Date.now() >= s.until)) settling.value = null
})

// Poll only while something is in flight; a settled VM does not need a timer.
let timer: ReturnType<typeof setInterval> | null = null
watch([() => vm.value?.status, settling], () => {
  const busy = vm.value?.status === 'pending' || !!settling.value
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

// --- stop / destroy / forget ---
//
// Three different things, deliberately not collapsed into one control:
//   stop     shuts the guest down, keeps its disk
//   destroy  deletes the guest and its disk on the host
//   forget   removes only this record, leaving whatever is on the host alone
//
// A row with no guest behind it — a failed create, or one already gone — can
// only be forgotten, so that is what the trash affordance offers it.
const working = ref(false)
const stopOpen = ref(false)
const destroyOpen = ref(false)
const forgetOpen = ref(false)

const hasGuest = computed(() => !!vm.value?.vm_id && vm.value.status !== 'gone')
const switchable = computed(() => {
  const v = vm.value
  return !!v?.vm_id && (v.status === 'running' || v.status === 'stopped')
})
// The switch shows the stored status, not displayStatus: a stale VM was last
// known to be running, and flipping the control off would claim we know it
// stopped. The badge is where that doubt belongs.
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
    await load(true)
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : failure
  }
  finally {
    working.value = false
  }
}

// Starting asks for no confirmation: it is cheap, reversible by the same switch,
// and destroys nothing. Stopping kills whatever the guest was doing, so it does.
function togglePower(on: boolean) {
  if (on) act(`/admin/vms/${id.value}/start`, 'POST', 'Could not start this VM', { verb: 'starting', running: true })
  else stopOpen.value = true
}
function confirmStop() {
  act(`/admin/vms/${id.value}/stop`, 'POST', 'Could not stop this VM', { verb: 'stopping', running: false })
}
function confirmDestroy() {
  act(`/admin/vms/${id.value}/destroy`, 'POST', 'Could not destroy this VM', { verb: 'destroying', running: false })
}

// Forgetting leaves nothing on this page to look at, so it is the one action
// that navigates away.
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
  <div class="mx-auto max-w-6xl px-4 py-12 sm:px-6">
    <AdminNav />

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
          </div>
          <!-- A stale badge without the age is as unhelpful as the wrong status
               was: the age is what says whether the host missed one report or
               went down an hour ago. -->
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
          <!-- Labelled, not icon-only: these are the irreversible actions on the
               page, and they sit next to a switch that is not. -->
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

      <!-- Any recorded failure, not just a failed create: a stop the host refused
           leaves the status alone and only sets this, which would otherwise be
           invisible. -->
      <Alert v-if="vm.last_error" variant="destructive" class="mt-4">
        <AlertTitle>Last reported failure</AlertTitle>
        <AlertDescription>{{ vm.last_error }}</AlertDescription>
      </Alert>

      <!-- details -->
      <section aria-labelledby="details-heading" class="mt-6 rounded-lg border border-border p-4 sm:p-6">
        <h2 id="details-heading" class="text-sm font-semibold">Details</h2>
        <dl class="mt-4 grid gap-x-8 gap-y-4 sm:grid-cols-2 lg:grid-cols-3">
          <div>
            <dt class="eyebrow text-muted-foreground">Host</dt>
            <dd class="mt-1 text-sm">
              <NuxtLink
                :to="`/admin/model/agents/${vm.agent_id}`"
                class="font-mono break-all text-primary-text underline decoration-primary-text/40 underline-offset-4 transition-colors hover:decoration-primary-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
              >
                {{ hostname || vm.agent_id }}
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
              <!-- A VM adopted from a host's inventory has no owner here, which
                   is a real state rather than missing data. -->
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
            <!-- Shown even when unrecorded: '0' would read as a diskless VM,
                 and a blank field as one that has no such property at all. -->
            <dd class="mt-1 font-mono text-sm">
              {{ vm.disk_mib ? fmtMiB(vm.disk_mib) : 'not recorded' }}
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
            <!-- The status is the host's claim as of this moment, not a live
                 reading, so the age of that claim belongs next to it. -->
            <dt class="eyebrow text-muted-foreground">Last confirmed by host</dt>
            <dd class="mt-1 text-sm text-muted-foreground">{{ fmtDate(vm.reported_at) }}</dd>
          </div>
        </dl>
      </section>

      <!-- routing -->
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

      <!-- destinations -->
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
              {{ t.kind === 'domain' ? 'dns · tls sni · http host' : 'address' }}
            </TableCell>
            <!-- An em dash, not 'any': these do not apply to a domain row at
                 all, and 'any' would read as "every transport is allowed". -->
            <TableCell class="font-mono text-muted-foreground">{{ t.transport || '—' }}</TableCell>
            <TableCell class="font-mono text-muted-foreground">
              {{ t.kind === 'domain' ? '—' : (t.ports || 'any') }}
            </TableCell>
            <TableCell class="text-muted-foreground">{{ t.note || '—' }}</TableCell>
          </TableRow>
        </DataTable>
      </section>

      <!-- spec -->
      <section aria-labelledby="spec-heading" class="mt-6 rounded-lg border border-border p-4 sm:p-6">
        <h2 id="spec-heading" class="text-sm font-semibold">Spec</h2>
        <p class="mt-1 max-w-2xl text-sm text-muted-foreground">
          Exactly what the host was asked to build. Empty for a VM adopted from an agent's
          inventory, since nothing here asked for it.
        </p>
        <pre
          v-if="hasSpec"
          class="mt-4 overflow-x-auto rounded-lg border border-border bg-muted/40 p-4 font-mono text-xs"
        >{{ specJSON }}</pre>
        <p v-else class="mt-4 font-mono text-xs text-muted-foreground">No spec recorded.</p>
      </section>
    </template>

    <!-- stop confirm -->
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

    <!-- destroy confirm -->
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

    <!-- forget confirm -->
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
  </div>
</template>
