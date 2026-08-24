<script setup lang="ts">
import { ArrowLeft, Ban, Trash2 } from '@lucide/vue'
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { TableCell, TableRow } from '@/components/ui/table'
import type { DataTableColumn } from '@/lib/table'

definePageMeta({ middleware: ['auth', 'admin'] })

interface ClientMetrics {
  // "" when the client has never reported. Every number below is zero either
  // way, so this is the only thing that separates an idle host from a silent one.
  reported_at: string
  cpu_count: number
  cpu_percent: number
  load1: number
  load5: number
  load15: number
  mem_total_bytes: number
  mem_used_bytes: number
  disk_total_bytes: number
  disk_used_bytes: number
  uptime_seconds: number
}

interface Client {
  id: string
  machine_id: string
  hostname: string
  status: 'online' | 'offline' | 'revoked'
  connected: boolean
  os: string
  os_version: string
  arch: string
  client_version: string
  last_seen_at: string
  last_ip: string
  created_at: string
  // "" when no domain was configured at enrollment, or several were and the
  // choice was left to an operator.
  domain: string
  domain_id: string
  metrics: ClientMetrics
}

interface DomainOption {
  id: string
  tld: string
}

interface VMRow {
  id: string
  vm_id: string
  name: string
  status: string
  boot: string
  cpus: number
  memory_mib: number
  ip: string
  created_at: string
}

const vmColumns: DataTableColumn[] = [
  { key: 'vm', label: 'VM' },
  { key: 'status', label: 'Status' },
  { key: 'resources', label: 'Resources' },
  { key: 'address', label: 'Address' },
  { key: 'created', label: 'Created' },
]

const route = useRoute()
const { authFetch } = useAuth()
const id = computed(() => String(route.params.id))

const client = ref<Client | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)
const actionError = ref<string | null>(null)

useHead(() => ({
  title: client.value
    ? `dummie — admin · client · ${client.value.hostname || client.value.machine_id}`
    : 'dummie — admin · client',
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

function fmtMiB(mib: number) {
  if (!mib) return '—'
  return mib >= 1024 ? `${(mib / 1024).toFixed(mib % 1024 ? 1 : 0)} GiB` : `${mib} MiB`
}

function pct(used: number, total: number) {
  return total > 0 ? Math.round((used / total) * 100) : 0
}

function fmtUptime(secs: number) {
  if (!secs) return '—'
  const days = Math.floor(secs / 86400)
  const hours = Math.floor((secs % 86400) / 3600)
  const mins = Math.floor((secs % 3600) / 60)
  if (days) return `${days}d ${hours}h`
  if (hours) return `${hours}h ${mins}m`
  return `${mins}m`
}

const statusVariant: Record<Client['status'], 'default' | 'secondary' | 'destructive'> = {
  online: 'default',
  offline: 'secondary',
  revoked: 'destructive',
}

// Reported once, then never again: the numbers below it would all read as a
// perfectly idle host, which is the wrong thing to believe about a silent one.
const reported = computed(() => !!client.value?.metrics?.reported_at)

async function load(quiet = false) {
  if (!quiet) loading.value = true
  error.value = null
  try {
    const res = await authFetch(`/admin/clients/${id.value}`)
    if (res.status === 404) throw new Error('This client does not exist, or its record was deleted.')
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    client.value = await res.json()
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load this client'
  }
  finally {
    loading.value = false
  }
}

// --- domain assignment ---
//
// A draft rather than an edit in place, so the Save button can appear only when
// the selection differs from what the server holds -- and so a background reload
// of the client cannot silently discard a choice mid-edit.
// noDomain is the select's stand-in for "no domain". reka-ui treats the empty
// string as "nothing is selected", which would show the placeholder and make
// clearing an assignment impossible to express.
const noDomain = 'none'

const domains = ref<DomainOption[]>([])
const domainDraft = ref(noDomain)
const savingDomain = ref(false)
const domainError = ref<string | null>(null)

// Seeded from the client once it arrives, and re-seeded whenever the server's
// answer changes -- which is what resets the draft after a save.
watch(() => client.value?.domain_id, (v) => { domainDraft.value = v || noDomain }, { immediate: true })

const domainDirty = computed(() => domainDraft.value !== (client.value?.domain_id || noDomain))

async function loadDomains() {
  try {
    const res = await authFetch('/admin/domains')
    if (!res.ok) return
    domains.value = (await res.json()).items ?? []
  }
  catch {
    // The select falls back to "none assigned" plus whatever is already set. A
    // failure here should not take the rest of the page with it.
  }
}
onMounted(loadDomains)

async function saveDomain() {
  savingDomain.value = true
  domainError.value = null
  try {
    const res = await authFetch(`/admin/clients/${id.value}/domain`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ domain_id: domainDraft.value === noDomain ? '' : domainDraft.value }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    await load(true)
  }
  catch (e) {
    domainError.value = e instanceof Error ? e.message : 'Could not set the domain'
  }
  finally {
    savingDomain.value = false
  }
}

// --- vms on this host ---
//
// Its own request and its own error, so a failure to list them is a gap in one
// card rather than a reason to blank the page. Paged at the API; a host with
// more than this many is better read from the VMs page, which links back here.
const vms = ref<VMRow[]>([])
const vmsTotal = ref(0)
const vmsLimit = 20
const vmsLoading = ref(true)
const vmsError = ref<string | null>(null)

async function loadVMs(quiet = false) {
  if (!quiet) vmsLoading.value = true
  vmsError.value = null
  try {
    const res = await authFetch(`/admin/clients/${id.value}/vms?limit=${vmsLimit}`)
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    const data = await res.json()
    vms.value = data.items ?? []
    vmsTotal.value = data.total ?? 0
  }
  catch (e) {
    vmsError.value = e instanceof Error ? e.message : 'Failed to load VMs'
  }
  finally {
    vmsLoading.value = false
  }
}

// Liveness and the metrics snapshot both come from the client's own reports, so
// this page polls for the same reason the list does.
let poll: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  load()
  loadVMs()
  poll = setInterval(() => {
    load(true)
    loadVMs(true)
  }, 10_000)
})
onUnmounted(() => clearInterval(poll))

// --- revoke / delete ---
//
// Revoking invalidates the client's token and kicks its socket: the machine stays
// on the record but cannot talk to the control plane again without re-enrolling.
// Deleting removes the record entirely.
const revokeOpen = ref(false)
const deleteOpen = ref(false)
const revoking = ref(false)
const deleting = ref(false)

async function confirmRevoke() {
  revoking.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/clients/${id.value}/revoke`, { method: 'POST' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    revokeOpen.value = false
    await load(true)
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not revoke client'
  }
  finally {
    revoking.value = false
  }
}

// Deleting leaves nothing on this page to look at, so it is the one action that
// navigates away.
async function confirmDelete() {
  deleting.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/clients/${id.value}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    await navigateTo('/admin/model/clients')
  }
  catch (e) {
    deleteOpen.value = false
    actionError.value = e instanceof Error ? e.message : 'Could not delete client'
  }
  finally {
    deleting.value = false
  }
}
</script>

<template>
  <AdminShell>

    <NuxtLink
      to="/admin/model/clients"
      class="mt-8 inline-flex items-center gap-1.5 font-mono text-xs text-muted-foreground underline-offset-4 transition-colors hover:text-foreground hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
    >
      <ArrowLeft class="size-3.5" aria-hidden="true" />
      All clients
    </NuxtLink>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load this client</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <div v-else-if="loading" class="mt-4 space-y-6" aria-busy="true">
      <p class="sr-only">Loading this client…</p>
      <Skeleton class="h-9 w-64" aria-hidden="true" />
      <Skeleton class="h-56 w-full rounded-lg" aria-hidden="true" />
      <Skeleton class="h-64 w-full rounded-lg" aria-hidden="true" />
    </div>

    <template v-else-if="client">
      <div class="mt-4 flex flex-wrap items-end justify-between gap-4">
        <div>
          <p class="eyebrow mb-2 text-primary-text">// admin · clients</p>
          <div class="flex flex-wrap items-center gap-3">
            <h1 class="font-mono text-2xl font-semibold tracking-tight sm:text-3xl">
              {{ client.hostname || client.machine_id }}
            </h1>
            <Badge :variant="statusVariant[client.status]" class="font-mono">{{ client.status }}</Badge>
          </div>
          <!-- 'online' is the stored status; `connected` is whether this process
               is holding the socket right now. They disagree when a server was
               restarted under a live client, which is worth seeing. -->
          <p class="mt-2 font-mono text-xs text-muted-foreground">
            {{ client.connected ? 'socket connected' : 'no live socket' }} · last seen {{ fmtDate(client.last_seen_at) }}
          </p>
        </div>
        <div class="flex flex-wrap items-center gap-3">
          <Button
            variant="outline"
            size="sm"
            class="font-mono text-xs text-destructive hover:text-destructive"
            :disabled="client.status === 'revoked' || revoking"
            :aria-label="client.status === 'revoked'
              ? 'Cannot revoke this client: already revoked'
              : 'Revoke this client\'s token'"
            @click="revokeOpen = true"
          >
            <Ban class="size-4" aria-hidden="true" />
            Revoke
          </Button>
          <Button
            variant="outline"
            size="sm"
            class="font-mono text-xs text-destructive hover:text-destructive"
            :disabled="deleting"
            aria-label="Delete this client record"
            @click="deleteOpen = true"
          >
            <Trash2 class="size-4" aria-hidden="true" />
            Delete
          </Button>
        </div>
      </div>

      <Alert v-if="actionError" variant="destructive" class="mt-4">
        <AlertTitle>Action failed</AlertTitle>
        <AlertDescription>{{ actionError }}</AlertDescription>
      </Alert>

      <Alert v-if="client.status === 'revoked'" variant="destructive" class="mt-4">
        <AlertTitle>This client is revoked</AlertTitle>
        <AlertDescription>
          Its token no longer works and its socket was closed. The machine has to enroll again
          with a fresh key to come back.
        </AlertDescription>
      </Alert>

      <!-- details -->
      <section aria-labelledby="details-heading" class="mt-6 rounded-lg border border-border p-4 sm:p-6">
        <h2 id="details-heading" class="text-sm font-semibold">Host</h2>
        <dl class="mt-4 grid gap-x-8 gap-y-4 sm:grid-cols-2 lg:grid-cols-3">
          <div>
            <dt class="eyebrow text-muted-foreground">Hostname</dt>
            <dd class="mt-1 font-mono text-sm break-all">{{ client.hostname || '—' }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Machine id</dt>
            <dd class="mt-1 font-mono text-xs break-all text-muted-foreground">{{ client.machine_id }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Domain</dt>
            <!-- No domain is a real state: none was configured at enrollment, or
                 several were and the choice was left to an operator. Editable
                 because enrollment only ever assigns one on a row's first insert,
                 so a host that enrolled before the domain existed has no other
                 way to get one -- and a host with no domain publishes no VMs and
                 can never be served over TLS. -->
            <dd class="mt-1 flex items-center gap-2">
              <Select v-model="domainDraft" :disabled="savingDomain">
                <SelectTrigger id="client-domain" class="max-w-xs font-mono text-sm" aria-label="Domain">
                  <SelectValue placeholder="none assigned" />
                </SelectTrigger>
                <SelectContent>
                  <!-- reka-ui reserves the empty string for "nothing selected",
                       so clearing the domain needs a sentinel of its own. -->
                  <SelectItem value="none">none assigned</SelectItem>
                  <SelectItem v-for="d in domains" :key="d.id" :value="d.id">
                    {{ d.tld }}
                  </SelectItem>
                </SelectContent>
              </Select>
              <Button
                v-if="domainDirty"
                class="font-mono text-xs"
                :disabled="savingDomain"
                @click="saveDomain"
              >
                {{ savingDomain ? 'Saving…' : 'Save' }}
              </Button>
            </dd>
            <FormError id="client-domain-error" :message="domainError" />
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">OS</dt>
            <dd class="mt-1 text-sm">{{ [client.os, client.os_version].filter(Boolean).join(' ') || '—' }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Arch</dt>
            <dd class="mt-1 font-mono text-sm">{{ client.arch || '—' }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Client version</dt>
            <dd class="mt-1 font-mono text-sm">{{ client.client_version || '—' }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Last IP</dt>
            <dd class="mt-1 font-mono text-sm break-all">{{ client.last_ip || '—' }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Enrolled</dt>
            <dd class="mt-1 text-sm text-muted-foreground">{{ fmtDate(client.created_at) }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Client id</dt>
            <dd class="mt-1 font-mono text-xs break-all text-muted-foreground">{{ client.id }}</dd>
          </div>
        </dl>
      </section>

      <!-- metrics -->
      <section aria-labelledby="metrics-heading" class="mt-6 rounded-lg border border-border p-4 sm:p-6">
        <h2 id="metrics-heading" class="text-sm font-semibold">Resources</h2>
        <p class="mt-1 max-w-2xl text-sm text-muted-foreground">
          The host's own snapshot, as of
          <span class="font-mono">{{ reported ? fmtDate(client.metrics.reported_at) : 'never' }}</span>.
        </p>

        <!-- Every number is zero until the client reports, so a silent host is
             said to be silent rather than shown as a perfectly idle one. -->
        <p v-if="!reported" class="mt-4 font-mono text-xs text-muted-foreground">
          This client has never reported its metrics.
        </p>
        <dl v-else class="mt-4 grid gap-x-8 gap-y-4 sm:grid-cols-2 lg:grid-cols-4">
          <div>
            <dt class="eyebrow text-muted-foreground">CPU</dt>
            <dd class="mt-1 font-mono text-sm">
              {{ client.metrics.cpu_percent.toFixed(0) }}%
              <span class="text-xs text-muted-foreground">of {{ client.metrics.cpu_count }} cpu</span>
            </dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Load</dt>
            <dd class="mt-1 font-mono text-sm">
              {{ client.metrics.load1.toFixed(2) }} · {{ client.metrics.load5.toFixed(2) }} ·
              {{ client.metrics.load15.toFixed(2) }}
            </dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Memory</dt>
            <dd class="mt-1 font-mono text-sm">
              {{ pct(client.metrics.mem_used_bytes, client.metrics.mem_total_bytes) }}%
              <span class="text-xs text-muted-foreground">
                {{ fmtBytes(client.metrics.mem_used_bytes) }} / {{ fmtBytes(client.metrics.mem_total_bytes) }}
              </span>
            </dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Disk</dt>
            <dd class="mt-1 font-mono text-sm">
              {{ pct(client.metrics.disk_used_bytes, client.metrics.disk_total_bytes) }}%
              <span class="text-xs text-muted-foreground">
                {{ fmtBytes(client.metrics.disk_used_bytes) }} / {{ fmtBytes(client.metrics.disk_total_bytes) }}
              </span>
            </dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Uptime</dt>
            <dd class="mt-1 font-mono text-sm">{{ fmtUptime(client.metrics.uptime_seconds) }}</dd>
          </div>
        </dl>
      </section>

      <!-- vms -->
      <section aria-labelledby="vms-heading" class="mt-6 rounded-lg border border-border">
        <div class="p-4 sm:p-6">
          <h2 id="vms-heading" class="text-sm font-semibold">VMs on this host</h2>
          <p class="mt-1 max-w-2xl text-sm text-muted-foreground">
            Everything this client is running, as it last reported.
            <span v-if="vmsTotal > vmsLimit">
              Showing the newest {{ vmsLimit }} of {{ vmsTotal }}.
            </span>
          </p>
          <Alert v-if="vmsError" variant="destructive" class="mt-4">
            <AlertTitle>Could not load VMs</AlertTitle>
            <AlertDescription>{{ vmsError }}</AlertDescription>
          </Alert>
        </div>
        <DataTable
          v-if="!vmsError"
          label="VMs on this host"
          :columns="vmColumns"
          :loading="vmsLoading"
          :loading-rows="3"
          loading-label="Loading VMs…"
          :empty="!vms.length"
          :frame="false"
        >
          <template #empty>
            This host is not running any VMs.
          </template>
          <TableRow v-for="v in vms" :key="v.id">
            <TableCell>
              <NuxtLink
                :to="`/admin/model/vms/${v.id}`"
                class="font-mono text-primary-text underline decoration-primary-text/40 underline-offset-4 transition-colors hover:decoration-primary-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                :aria-label="`View details for ${v.name || v.vm_id || 'this VM'}`"
              >
                {{ v.name || v.vm_id || '—' }}
              </NuxtLink>
            </TableCell>
            <TableCell class="font-mono text-muted-foreground">{{ v.status }}</TableCell>
            <TableCell class="font-mono text-muted-foreground whitespace-nowrap">
              {{ v.cpus || '—' }}<span v-if="v.cpus"> vcpu</span> · {{ fmtMiB(v.memory_mib) }}
            </TableCell>
            <TableCell class="font-mono text-muted-foreground">{{ v.ip || '—' }}</TableCell>
            <TableCell class="text-muted-foreground whitespace-nowrap">{{ fmtDate(v.created_at) }}</TableCell>
          </TableRow>
        </DataTable>
      </section>
    </template>

    <!-- revoke confirm -->
    <Dialog v-model:open="revokeOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Revoke client</DialogTitle>
          <DialogDescription>
            <span class="font-mono text-foreground">{{ client?.hostname || client?.machine_id }}</span>
            loses its token and its socket is closed straight away. VMs already running on the
            host keep running, but nothing here can reach them until it enrolls again.
          </DialogDescription>
        </DialogHeader>
        <FormError id="revoke-client-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="revoking" @click="confirmRevoke">
            {{ revoking ? 'Revoking…' : 'Revoke' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- delete confirm -->
    <Dialog v-model:open="deleteOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete client</DialogTitle>
          <DialogDescription>
            Removes the record for
            <span class="font-mono text-foreground">{{ client?.hostname || client?.machine_id }}</span>
            and closes its socket. Nothing is asked of the machine itself, so an client still
            installed there re-enrolls if it has a valid key.
          </DialogDescription>
        </DialogHeader>
        <FormError id="delete-client-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="deleting" @click="confirmDelete">
            {{ deleting ? 'Deleting…' : 'Delete' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </AdminShell>
</template>
