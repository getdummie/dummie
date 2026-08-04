<script setup lang="ts">
import { RefreshCw, Trash2 } from '@lucide/vue'
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
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · vms' })

interface VMRow {
  id: string
  agent_id: string
  vm_id: string
  name: string
  status: 'pending' | 'running' | 'stopped' | 'failed' | 'gone'
  boot: string
  cpus: number
  memory_mib: number
  ip: string
  last_error: string
  created_at: string
  started_at: string
}

const { authFetch } = useAuth()

const items = ref<VMRow[]>([])
const total = ref(0)
const limit = ref(20)
const offset = ref(0)
const loading = ref(true)
const error = ref<string | null>(null)

// A VM row carries only its agent's id. Rather than widen the API with a join,
// the agent list is fetched alongside and resolved here -- an admin fleet is
// small enough that one extra page of agents is cheaper than a new endpoint.
const hostnames = ref<Record<string, string>>({})

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

function agentLabel(id: string) {
  return hostnames.value[id] || `${id.slice(0, 8)}…`
}

const statusVariant: Record<VMRow['status'], 'default' | 'secondary' | 'outline' | 'destructive'> = {
  running: 'default',
  pending: 'secondary',
  stopped: 'secondary',
  // 'gone' is not an error the way a failed create is -- the VM was removed on
  // its host, which is usually deliberate -- so it reads as muted, not alarming.
  gone: 'outline',
  failed: 'destructive',
}

// silent skips the skeletons so the background poll doesn't make the table flash.
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

// Best-effort: an unresolved hostname falls back to a short id, so a failure
// here must not surface as an error on a table that otherwise loaded fine.
async function loadAgents() {
  try {
    const res = await authFetch('/admin/agents?limit=100')
    if (!res.ok) return
    const data = await res.json()
    const map: Record<string, string> = {}
    for (const a of data.items ?? []) {
      if (a.hostname) map[a.id] = a.hostname
    }
    hostnames.value = map
  }
  catch {
    // keep whatever we already resolved
  }
}

// A pending row becomes running or failed without any action from this page, so
// it has to poll to stay honest. A minute is a long time to watch a create you
// just started, which is what the refresh button is for.
const pollInterval = 60_000

let poll: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  load()
  loadAgents()
  poll = setInterval(() => load(true), pollInterval)
})
onUnmounted(() => clearInterval(poll))

// syncing drives the button's own spinner. It is separate from `loading` so a
// manual refresh spins the icon without also blanking the table into skeletons.
const syncing = ref(false)

// The timer is restarted so a manual refresh doesn't leave a scheduled poll
// firing a moment later.
async function syncNow() {
  if (syncing.value) return
  syncing.value = true
  clearInterval(poll)
  try {
    await Promise.all([load(true), loadAgents()])
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

// --- delete ---
const toDelete = ref<VMRow | null>(null)
const deleting = ref(false)
const actionError = ref<string | null>(null)

async function confirmDelete() {
  if (!toDelete.value) return
  deleting.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/vms/${toDelete.value.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toDelete.value = null
    await load()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not delete vm record'
  }
  finally {
    deleting.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-12 sm:px-6">
    <AdminNav />

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
      </div>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load VMs</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <!-- Self-refreshing, so `aria-live="polite"` lets a screen-reader user hear a
         pending VM turn into a running one without re-reading the table. -->
    <div class="mt-6 overflow-x-auto rounded-lg border border-border" aria-live="polite" :aria-busy="loading">
      <p v-if="loading" class="sr-only">Loading VMs…</p>
      <Table label="VMs">
        <TableHeader>
          <TableRow>
            <TableHead>VM</TableHead>
            <TableHead>Host</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Boot</TableHead>
            <TableHead>Resources</TableHead>
            <TableHead>Address</TableHead>
            <TableHead>Created</TableHead>
            <TableHead class="text-right">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <template v-if="loading">
            <TableRow v-for="n in 5" :key="n" aria-hidden="true">
              <TableCell v-for="c in 8" :key="c"><Skeleton class="h-4 w-full" /></TableCell>
            </TableRow>
          </template>
          <TableEmpty v-else-if="!items.length" :colspan="8">
            No VMs yet. Connected agents report what they are running every 30s, so
            anything made with <span class="font-mono">dagent vm create</span> appears
            here on its own. To ask an agent for one, post to
            <span class="font-mono">/api/v1/admin/agents/&lt;id&gt;/vms</span>.
          </TableEmpty>
          <TableRow v-for="v in items" v-else :key="v.id">
            <TableCell>
              <div class="font-mono">{{ v.name || '—' }}</div>
              <div class="truncate font-mono text-xs text-muted-foreground">{{ v.vm_id || 'not assigned yet' }}</div>
            </TableCell>
            <TableCell class="font-mono text-muted-foreground">{{ agentLabel(v.agent_id) }}</TableCell>
            <TableCell>
              <Badge :variant="statusVariant[v.status]" class="font-mono">{{ v.status }}</Badge>
              <!-- The reason a create failed is the only thing anyone wants from
                   a failed row, so it sits with the status rather than behind a
                   click. title= keeps the full text reachable when truncated. -->
              <div
                v-if="v.status === 'failed' && v.last_error"
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
              <Button
                variant="ghost"
                size="icon"
                class="text-destructive hover:text-destructive"
                :aria-label="`Delete the record for VM ${v.name || v.vm_id || v.id}`"
                @click="toDelete = v"
              >
                <Trash2 class="size-4" aria-hidden="true" />
              </Button>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>

    <nav aria-label="VMs pagination" class="mt-4 flex flex-wrap items-center justify-between gap-3 font-mono text-xs text-muted-foreground">
      <span>{{ total }} total · page {{ page }} / {{ pageCount }}</span>
      <div class="flex gap-2">
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset <= 0 || loading" aria-label="Previous page of VMs" @click="prev">Prev</Button>
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset + limit >= total || loading" aria-label="Next page of VMs" @click="next">Next</Button>
      </div>
    </nav>

    <!-- delete confirm -->
    <Dialog :open="!!toDelete" @update:open="(v: boolean) => { if (!v) toDelete = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete VM record</DialogTitle>
          <DialogDescription>
            Remove
            <span class="font-mono text-foreground">{{ toDelete?.name || toDelete?.vm_id || toDelete?.id }}</span>
            from the registry. This forgets the record only — the guest keeps running
            on its host, and while it does, the host's next inventory report will
            add it straight back. To remove it for real, run
            <span class="font-mono">dagent vm rm</span> on the host.
          </DialogDescription>
        </DialogHeader>
        <FormError id="delete-vm-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="deleting" @click="confirmDelete">
            {{ deleting ? 'Deleting…' : 'Delete record' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
