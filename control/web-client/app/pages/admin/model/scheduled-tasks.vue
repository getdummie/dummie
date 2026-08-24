<script setup lang="ts">
import { CircleSlash, Play, RefreshCw } from '@lucide/vue'
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
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { TableCell, TableRow } from '@/components/ui/table'
import type { DataTableColumn } from '@/lib/table'

const columns: DataTableColumn[] = [
  { key: 'kind', label: 'Task' },
  { key: 'subject', label: 'Subject' },
  { key: 'runs', label: 'Runs' },
  { key: 'attempts', label: 'Attempts' },
  { key: 'status', label: 'Status' },
  { key: 'detail', label: 'Latest' },
  { key: 'actions', label: 'Actions', align: 'right' },
]

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · scheduled tasks' })

interface TaskRow {
  id: string
  kind: string
  subject_kind: string
  subject_id: string
  status: 'pending' | 'running' | 'done' | 'failed' | 'cancelled'
  payload: Record<string, unknown>
  reason: string
  detail: string
  run_at: string
  // How late a pending task is, in seconds, and 0 when it is not. Computed by the
  // server: a browser clock that is wrong would either invent a fleet-wide
  // problem or hide a real one.
  overdue_seconds: number
  attempts: number
  max_attempts: number
  created_by: string
  locked_by: string
  created_at: string
  updated_at: string
  finished_at: string
}

// The kinds the runner knows. Listed here only to label and filter -- the server
// does not constrain the column, so an unknown kind still renders as itself
// rather than disappearing from the table.
const kindLabels: Record<string, string> = {
  'vm.expire': 'Destroy expired VM',
  'vm_target.expire': 'Withdraw temporary access',
  'tasks.cleanup': 'Prune settled tasks',
}

function kindLabel(kind: string) {
  return kindLabels[kind] ?? kind
}

const { authFetch } = useAuth()

const items = ref<TaskRow[]>([])
const total = ref(0)
const overdue = ref(0)
const lastTickAt = ref('')
const limit = ref(20)
const offset = ref(0)
const loading = ref(true)
const error = ref<string | null>(null)

const status = ref('active')
const kind = ref('')

// A ticking clock so countdowns move on their own. Same reasoning as the VM
// table's staleness clock: how soon a task is due is a fact about the passage of
// time, and a page that only updates on fetch would freeze exactly when the
// control server is the thing that is unreachable.
const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined

// Slow enough not to fight with an admin reading the page, quick enough that an
// expiry firing while it is open is visible without a manual refresh.
const pollMs = 10_000
let poll: ReturnType<typeof setInterval> | undefined

onMounted(() => {
  clock = setInterval(() => { now.value = Date.now() }, 1_000)
  poll = setInterval(() => load(true), pollMs)
  load()
})
onBeforeUnmount(() => {
  if (clock) clearInterval(clock)
  if (poll) clearInterval(poll)
})

async function readMessage(res: Response): Promise<string | null> {
  try {
    const b = await res.json()
    return typeof b?.message === 'string' ? b.message : null
  }
  catch {
    return null
  }
}

// 'active' is not a server status: it is the default question this page answers
// -- what has not settled, plus what needs a human. Sent as the statuses the API
// understands rather than as a word of its own.
const statusParam = computed(() => (status.value === 'active' ? '' : status.value))

async function load(quiet = false) {
  if (!quiet) loading.value = true
  error.value = null
  try {
    const params = new URLSearchParams({
      limit: String(limit.value),
      offset: String(offset.value),
    })
    if (statusParam.value) params.set('status', statusParam.value)
    if (kind.value) params.set('kind', kind.value)

    const res = await authFetch(`/admin/tasks?${params}`)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = await res.json()
    items.value = data.items ?? []
    total.value = data.total ?? 0
    overdue.value = data.overdue ?? 0
    lastTickAt.value = data.last_tick_at ?? ''
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load scheduled tasks'
  }
  finally {
    loading.value = false
  }
}

function applyFilters() {
  offset.value = 0
  load()
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

// The runner ticks once a second, so anything past half a minute means it is not
// running -- the process is down, or it was started without a database.
const runnerStalled = computed(() => {
  if (!lastTickAt.value) return true
  const at = new Date(lastTickAt.value).getTime()
  return Number.isNaN(at) || now.value - at > 30_000
})

function fmtDate(s: string) {
  if (!s) return '—'
  const d = new Date(s)
  return Number.isNaN(d.getTime()) ? s : d.toLocaleString()
}

function relative(ms: number) {
  const secs = Math.round(Math.abs(ms) / 1000)
  if (secs < 60) return `${secs}s`
  const mins = Math.round(secs / 60)
  if (mins < 60) return `${mins}m`
  const hours = Math.round(mins / 60)
  return hours < 24 ? `${hours}h` : `${Math.round(hours / 24)}d`
}

// When the task runs, as a sentence. A settled task says when it finished
// instead: "due in 3h" for something that already ran is noise.
function runsLabel(t: TaskRow) {
  if (t.status === 'done' || t.status === 'cancelled' || t.status === 'failed') {
    return t.finished_at ? `${relative(now.value - new Date(t.finished_at).getTime())} ago` : '—'
  }
  const at = new Date(t.run_at).getTime()
  if (Number.isNaN(at)) return t.run_at
  const delta = at - now.value
  return delta > 0 ? `in ${relative(delta)}` : `${relative(delta)} late`
}

type BadgeVariant = 'default' | 'secondary' | 'outline' | 'destructive'

function statusVariant(s: string): BadgeVariant {
  switch (s) {
    case 'running': return 'default'
    case 'pending': return 'secondary'
    case 'failed': return 'destructive'
    default: return 'outline'
  }
}

// What the task acts on, named from the payload rather than the subject id: the
// row it points at may be deleted by the time anyone reads this, which is the
// whole reason the payload carries a snapshot.
function subjectLabel(t: TaskRow) {
  const p = t.payload ?? {}
  const vm = typeof p.vm_name === 'string' ? p.vm_name : ''
  const dest = typeof p.destination === 'string' ? p.destination : ''
  if (dest && vm) return `${dest} · ${vm}`
  return dest || vm || (t.subject_id ? `${t.subject_id.slice(0, 8)}…` : 'the fleet')
}

// A link to the subject when there is still one to link to. A vm_target task
// carries the VM it belonged to in its payload, so both kinds resolve to a VM.
function subjectLink(t: TaskRow) {
  if (t.subject_kind === 'vm' && t.subject_id) return `/admin/model/vms/${t.subject_id}`
  const p = t.payload ?? {}
  return typeof p.vm_id === 'string' && p.vm_id ? `/admin/model/vms/${p.vm_id}` : ''
}

// --- actions ---
const detail = ref<TaskRow | null>(null)
const toCancel = ref<TaskRow | null>(null)
const acting = ref(false)
const actionError = ref<string | null>(null)

async function confirmCancel() {
  if (!toCancel.value) return
  acting.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/tasks/${toCancel.value.id}/cancel`, { method: 'POST' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toCancel.value = null
    await load(true)
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not cancel the task'
  }
  finally {
    acting.value = false
  }
}

async function runNow(t: TaskRow) {
  acting.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/tasks/${t.id}/run-now`, { method: 'POST' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    await load(true)
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not run the task'
  }
  finally {
    acting.value = false
  }
}
</script>

<template>
  <AdminShell>

    <div class="mt-8 flex flex-wrap items-end justify-between gap-4">
      <div>
        <p class="eyebrow mb-2 text-primary-text">// admin · scheduled tasks</p>
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">Scheduled tasks</h1>
        <p class="mt-2 max-w-2xl text-sm text-muted-foreground">
          Work this control plane owes the future: TTLs on VMs and on temporary destinations. The
          hosts hold no timers of their own, so nothing here happens while the control server is
          down — a task that is late stays late until it runs.
        </p>
      </div>
      <Button variant="outline" class="font-mono text-xs" :disabled="loading" @click="load()">
        <RefreshCw class="size-4" aria-hidden="true" />
        Refresh
      </Button>
    </div>

    <!-- Runner health. Two facts, and both are about this process rather than the
         table: is the poller alive, and is it keeping up. -->
    <div class="mt-6 grid gap-3 sm:grid-cols-2">
      <div class="rounded-lg border border-border p-4">
        <p class="eyebrow text-muted-foreground">Runner</p>
        <p class="mt-1 flex items-center gap-2 text-sm">
          <Badge :variant="runnerStalled ? 'destructive' : 'default'" class="font-mono text-xs">
            {{ runnerStalled ? 'not ticking' : 'ticking' }}
          </Badge>
          <span class="text-muted-foreground">
            last pass {{ lastTickAt ? `${relative(now - new Date(lastTickAt).getTime())} ago` : 'never' }}
          </span>
        </p>
      </div>
      <div class="rounded-lg border border-border p-4">
        <p class="eyebrow text-muted-foreground">Overdue</p>
        <p class="mt-1 flex items-center gap-2 text-sm">
          <Badge :variant="overdue > 0 ? 'destructive' : 'outline'" class="font-mono text-xs">
            {{ overdue }}
          </Badge>
          <span class="text-muted-foreground">
            due more than a minute ago and still waiting
          </span>
        </p>
      </div>
    </div>

    <div class="mt-6 flex flex-wrap items-end gap-3">
      <div class="space-y-2">
        <Label for="t-status">Status</Label>
        <NativeSelect id="t-status" v-model="status" class="w-44" @change="applyFilters">
          <NativeSelectOption value="active">Unsettled + failed</NativeSelectOption>
          <NativeSelectOption value="pending">Pending</NativeSelectOption>
          <NativeSelectOption value="running">Running</NativeSelectOption>
          <NativeSelectOption value="failed">Failed</NativeSelectOption>
          <NativeSelectOption value="done">Done</NativeSelectOption>
          <NativeSelectOption value="cancelled">Cancelled</NativeSelectOption>
          <NativeSelectOption value="all">Everything</NativeSelectOption>
        </NativeSelect>
      </div>
      <div class="space-y-2">
        <Label for="t-kind">Task</Label>
        <NativeSelect id="t-kind" v-model="kind" class="w-56" @change="applyFilters">
          <NativeSelectOption value="">Every kind</NativeSelectOption>
          <NativeSelectOption value="vm.expire">Destroy expired VM</NativeSelectOption>
          <NativeSelectOption value="vm_target.expire">Withdraw temporary access</NativeSelectOption>
          <NativeSelectOption value="tasks.cleanup">Prune settled tasks</NativeSelectOption>
        </NativeSelect>
      </div>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load scheduled tasks</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>
    <Alert v-if="actionError" variant="destructive" class="mt-6">
      <AlertTitle>That action did not go through</AlertTitle>
      <AlertDescription>{{ actionError }}</AlertDescription>
    </Alert>

    <DataTable
      label="Scheduled tasks"
      :columns="columns"
      :loading="loading"
      loading-label="Loading scheduled tasks…"
      :empty="!items.length"
      class="mt-6"
    >
      <template #empty>
        Nothing scheduled. A VM or a destination created with a TTL shows up here.
      </template>
      <TableRow v-for="t in items" :key="t.id">
        <TableCell>
          <button
            type="button"
            class="text-left font-medium underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
            @click="detail = t"
          >
            {{ kindLabel(t.kind) }}
          </button>
          <p v-if="t.reason" class="mt-0.5 text-xs text-muted-foreground">{{ t.reason }}</p>
        </TableCell>
        <TableCell class="font-mono text-xs break-all">
          <NuxtLink v-if="subjectLink(t)" :to="subjectLink(t)" class="underline-offset-4 hover:underline">
            {{ subjectLabel(t) }}
          </NuxtLink>
          <span v-else>{{ subjectLabel(t) }}</span>
        </TableCell>
        <TableCell class="font-mono text-xs whitespace-nowrap">
          <span :class="t.overdue_seconds > 0 && 'text-destructive'">{{ runsLabel(t) }}</span>
          <p class="mt-0.5 text-muted-foreground">{{ fmtDate(t.run_at) }}</p>
        </TableCell>
        <TableCell class="font-mono text-xs">{{ t.attempts }} / {{ t.max_attempts }}</TableCell>
        <TableCell>
          <Badge :variant="statusVariant(t.status)" class="font-mono text-xs">{{ t.status }}</Badge>
        </TableCell>
        <TableCell class="max-w-xs text-xs text-muted-foreground">{{ t.detail || '—' }}</TableCell>
        <TableCell class="text-right">
          <div class="flex justify-end gap-1">
            <Button
              v-if="t.status === 'pending' || t.status === 'failed'"
              variant="ghost"
              size="icon"
              :disabled="acting"
              :aria-label="`Run ${kindLabel(t.kind)} now`"
              @click="runNow(t)"
            >
              <Play class="size-4" aria-hidden="true" />
            </Button>
            <Button
              v-if="t.status === 'pending'"
              variant="ghost"
              size="icon"
              class="text-destructive hover:text-destructive"
              :disabled="acting"
              :aria-label="`Cancel ${kindLabel(t.kind)}`"
              @click="toCancel = t"
            >
              <CircleSlash class="size-4" aria-hidden="true" />
            </Button>
          </div>
        </TableCell>
      </TableRow>
    </DataTable>

    <nav aria-label="Scheduled tasks pagination" class="mt-4 flex flex-wrap items-center justify-between gap-3 font-mono text-xs text-muted-foreground">
      <span>{{ total }} total · page {{ page }} / {{ pageCount }}</span>
      <div class="flex gap-2">
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset <= 0 || loading" aria-label="Previous page of scheduled tasks" @click="prev">Prev</Button>
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset + limit >= total || loading" aria-label="Next page of scheduled tasks" @click="next">Next</Button>
      </div>
    </nav>

    <!-- detail: the debugging half of this page, payload included as sent -->
    <Dialog :open="!!detail" @update:open="(v: boolean) => { if (!v) detail = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ detail ? kindLabel(detail.kind) : '' }}</DialogTitle>
          <DialogDescription>{{ detail?.reason || 'No reason was recorded.' }}</DialogDescription>
        </DialogHeader>
        <dl v-if="detail" class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 font-mono text-xs">
          <dt class="text-muted-foreground">kind</dt>
          <dd class="break-all">{{ detail.kind }}</dd>
          <dt class="text-muted-foreground">status</dt>
          <dd>{{ detail.status }}</dd>
          <dt class="text-muted-foreground">subject</dt>
          <dd class="break-all">{{ detail.subject_kind || '—' }} {{ detail.subject_id }}</dd>
          <dt class="text-muted-foreground">runs at</dt>
          <dd>{{ fmtDate(detail.run_at) }}</dd>
          <dt class="text-muted-foreground">attempts</dt>
          <dd>{{ detail.attempts }} / {{ detail.max_attempts }}</dd>
          <dt class="text-muted-foreground">held by</dt>
          <dd class="break-all">{{ detail.locked_by || '—' }}</dd>
          <dt class="text-muted-foreground">created</dt>
          <dd>{{ fmtDate(detail.created_at) }}</dd>
          <dt class="text-muted-foreground">finished</dt>
          <dd>{{ fmtDate(detail.finished_at) }}</dd>
          <dt class="text-muted-foreground">latest</dt>
          <dd class="break-words">{{ detail.detail || '—' }}</dd>
          <dt class="text-muted-foreground">payload</dt>
          <dd><pre class="overflow-x-auto break-all whitespace-pre-wrap">{{ JSON.stringify(detail.payload, null, 2) }}</pre></dd>
        </dl>
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Close</Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- cancel confirm: what cancelling means depends on the kind, so it says so -->
    <Dialog :open="!!toCancel" @update:open="(v: boolean) => { if (!v) toCancel = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Cancel task</DialogTitle>
          <DialogDescription>
            <template v-if="toCancel?.kind === 'vm.expire'">
              <span class="font-mono text-foreground">{{ subjectLabel(toCancel) }}</span>
              will no longer be destroyed. It becomes an ordinary VM and lives until somebody
              destroys it.
            </template>
            <template v-else-if="toCancel?.kind === 'vm_target.expire'">
              <span class="font-mono text-foreground">{{ subjectLabel(toCancel) }}</span>
              stops being temporary: the destination stays allowed until it is removed by hand.
            </template>
            <template v-else>
              This task will not run. Nothing reschedules it.
            </template>
          </DialogDescription>
        </DialogHeader>
        <FormError id="cancel-task-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Keep it</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="acting" @click="confirmCancel">
            {{ acting ? 'Cancelling…' : 'Cancel task' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </AdminShell>
</template>
