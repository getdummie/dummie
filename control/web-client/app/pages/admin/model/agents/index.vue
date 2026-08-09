<script setup lang="ts">
import { Ban, Trash2 } from '@lucide/vue'
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
import { TableCell, TableRow } from '@/components/ui/table'
import type { DataTableColumn } from '@/lib/table'

const columns: DataTableColumn[] = [
  { key: 'agent', label: 'Agent' },
  { key: 'status', label: 'Status' },
  { key: 'domain', label: 'Domain' },
  { key: 'cpu', label: 'CPU' },
  { key: 'memory', label: 'Memory' },
  { key: 'disk', label: 'Disk' },
  { key: 'os', label: 'OS' },
  { key: 'arch', label: 'Arch' },
  { key: 'version', label: 'Version' },
  { key: 'last_seen', label: 'Last seen' },
  { key: 'actions', label: 'Actions', align: 'right' },
]

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · agents' })

interface AgentRow {
  id: string
  machine_id: string
  hostname: string
  status: 'online' | 'offline' | 'revoked'
  connected: boolean
  os: string
  os_version: string
  arch: string
  agent_version: string
  last_seen_at: string
  last_ip: string
  created_at: string
  // "" when no domain was configured at enrollment, or several were and the
  // choice was left to an operator.
  domain: string
  metrics: AgentMetrics
}

interface AgentMetrics {
  // "" when the agent has never reported. Every number below is zero either
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

const { authFetch } = useAuth()

const items = ref<AgentRow[]>([])
const total = ref(0)
const limit = ref(20)
const offset = ref(0)
const loading = ref(true)
const error = ref<string | null>(null)

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

function pct(used: number, total: number) {
  return total > 0 ? Math.round((used / total) * 100) : 0
}

const statusVariant: Record<AgentRow['status'], 'default' | 'secondary' | 'destructive'> = {
  online: 'default',
  offline: 'secondary',
  revoked: 'destructive',
}

// silent skips the skeletons so the background poll doesn't make the table flash.
async function load(silent = false) {
  if (!silent) loading.value = true
  error.value = null
  try {
    const res = await authFetch(`/admin/agents?limit=${limit.value}&offset=${offset.value}`)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = await res.json()
    items.value = data.items ?? []
    total.value = data.total ?? 0
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load agents'
  }
  finally {
    loading.value = false
  }
}

// Liveness comes from the server's in-memory hub, so a short poll is enough to
// keep the table honest without a second websocket in the browser.
let poll: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  load()
  poll = setInterval(() => load(true), 10_000)
})
onUnmounted(() => clearInterval(poll))

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

// --- revoke ---
const toRevoke = ref<AgentRow | null>(null)
const revoking = ref(false)
const actionError = ref<string | null>(null)

async function confirmRevoke() {
  if (!toRevoke.value) return
  revoking.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/agents/${toRevoke.value.id}/revoke`, { method: 'POST' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toRevoke.value = null
    await load()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not revoke agent'
  }
  finally {
    revoking.value = false
  }
}

// --- delete ---
const toDelete = ref<AgentRow | null>(null)
const deleting = ref(false)

async function confirmDelete() {
  if (!toDelete.value) return
  deleting.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/agents/${toDelete.value.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toDelete.value = null
    await load()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not delete agent'
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
        <p class="eyebrow mb-2 text-primary-text">// admin · agents</p>
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">Agents</h1>
      </div>
      <span class="font-mono text-xs text-muted-foreground">refreshes every 10s</span>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load agents</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <!-- This table also self-refreshes every 10s. `aria-live="polite"` lets a
         screen-reader user hear status changes without polling it manually. -->
    <DataTable
      label="Agents"
      :columns="columns"
      :loading="loading"
      loading-label="Loading agents…"
      :empty="!items.length"
      class="mt-6"
    >
      <template #empty>
        No agents enrolled. Create an enrollment key, then run
        <span class="font-mono">dagent connect --key …</span> on a machine.
      </template>
      <TableRow v-for="a in items" :key="a.id">
        <TableCell>
          <!-- Underlined at rest, not just on hover: in a table of plain text
               cells an underline-on-hover link is undiscoverable, and colour
               alone would not carry it either. (WCAG 1.4.1) -->
          <NuxtLink
            :to="`/admin/model/agents/${a.id}`"
            class="font-mono text-primary-text underline decoration-primary-text/40 underline-offset-4 transition-colors hover:decoration-primary-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
            :aria-label="`View details for ${a.hostname || a.machine_id}`"
          >
            {{ a.hostname || '—' }}
          </NuxtLink>
          <div class="truncate text-xs text-muted-foreground">{{ a.machine_id }}</div>
        </TableCell>
        <TableCell>
          <Badge :variant="statusVariant[a.status]" class="font-mono">{{ a.status }}</Badge>
        </TableCell>
        <TableCell class="font-mono text-muted-foreground">{{ a.domain || '—' }}</TableCell>
        <!-- Every metric is zero until the agent reports, so reported_at is
             what decides between a real number and an em dash. -->
        <TableCell class="font-mono text-muted-foreground whitespace-nowrap">
          <template v-if="a.metrics?.reported_at">
            {{ a.metrics.cpu_percent.toFixed(0) }}%
            <span class="text-xs">/ {{ a.metrics.cpu_count }} cpu</span>
            <div class="text-xs">load {{ a.metrics.load1.toFixed(2) }}</div>
          </template>
          <template v-else>—</template>
        </TableCell>
        <TableCell class="font-mono text-muted-foreground whitespace-nowrap">
          <template v-if="a.metrics?.reported_at">
            {{ pct(a.metrics.mem_used_bytes, a.metrics.mem_total_bytes) }}%
            <div class="text-xs">
              {{ fmtBytes(a.metrics.mem_used_bytes) }} / {{ fmtBytes(a.metrics.mem_total_bytes) }}
            </div>
          </template>
          <template v-else>—</template>
        </TableCell>
        <TableCell class="font-mono text-muted-foreground whitespace-nowrap">
          <template v-if="a.metrics?.reported_at">
            {{ pct(a.metrics.disk_used_bytes, a.metrics.disk_total_bytes) }}%
            <div class="text-xs">
              {{ fmtBytes(a.metrics.disk_used_bytes) }} / {{ fmtBytes(a.metrics.disk_total_bytes) }}
            </div>
          </template>
          <template v-else>—</template>
        </TableCell>
        <TableCell class="text-muted-foreground">
          {{ [a.os, a.os_version].filter(Boolean).join(' ') || '—' }}
        </TableCell>
        <TableCell class="font-mono text-muted-foreground">{{ a.arch || '—' }}</TableCell>
        <TableCell class="font-mono text-muted-foreground">{{ a.agent_version || '—' }}</TableCell>
        <TableCell class="text-muted-foreground">{{ fmtDate(a.last_seen_at) }}</TableCell>
        <TableCell class="text-right">
          <div class="flex justify-end gap-1">
            <Button
              variant="ghost"
              size="icon"
              class="text-destructive hover:text-destructive"
              :disabled="a.status === 'revoked'"
              :aria-label="a.status === 'revoked'
                ? `Cannot revoke ${a.hostname || a.machine_id}: already revoked`
                : `Revoke agent token for ${a.hostname || a.machine_id}`"
              @click="toRevoke = a"
            >
              <Ban class="size-4" aria-hidden="true" />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              class="text-destructive hover:text-destructive"
              :aria-label="`Delete agent ${a.hostname || a.machine_id}`"
              @click="toDelete = a"
            >
              <Trash2 class="size-4" aria-hidden="true" />
            </Button>
          </div>
        </TableCell>
      </TableRow>
    </DataTable>

    <nav aria-label="Agents pagination" class="mt-4 flex flex-wrap items-center justify-between gap-3 font-mono text-xs text-muted-foreground">
      <span>{{ total }} total · page {{ page }} / {{ pageCount }}</span>
      <div class="flex gap-2">
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset <= 0 || loading" aria-label="Previous page of agents" @click="prev">Prev</Button>
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset + limit >= total || loading" aria-label="Next page of agents" @click="next">Next</Button>
      </div>
    </nav>

    <!-- revoke confirm -->
    <Dialog :open="!!toRevoke" @update:open="(v: boolean) => { if (!v) toRevoke = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Revoke agent</DialogTitle>
          <DialogDescription>
            Invalidate the token for <span class="font-mono text-foreground">{{ toRevoke?.hostname || toRevoke?.machine_id }}</span>.
            Its live connection is dropped immediately and it cannot reconnect until it enrolls again with a valid key.
          </DialogDescription>
        </DialogHeader>
        <FormError id="revoke-agent-error" :message="actionError" />
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
    <Dialog :open="!!toDelete" @update:open="(v: boolean) => { if (!v) toDelete = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete agent</DialogTitle>
          <DialogDescription>
            Permanently remove
            <span class="font-mono text-foreground">{{ toDelete?.hostname || toDelete?.machine_id }}</span>
            from the registry and drop its connection. The machine can enroll again with a valid key.
          </DialogDescription>
        </DialogHeader>
        <FormError id="delete-agent-error" :message="actionError" />
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
  </div>
</template>
