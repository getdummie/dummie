<script setup lang="ts">
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { TableCell, TableRow } from '@/components/ui/table'
import type { DataTableColumn } from '@/lib/table'

const columns: DataTableColumn[] = [
  { key: 'time', label: 'Time' },
  { key: 'actor', label: 'Actor' },
  { key: 'action', label: 'Action' },
  { key: 'target', label: 'Target' },
  { key: 'status', label: 'Status' },
  { key: 'ip', label: 'IP' },
]

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · audit log' })

interface AuditRow {
  id: number
  created_at: string
  actor_id: string
  actor_username: string
  auth_method: string
  action: string
  path: string
  params: Record<string, string>
  status: number
  ip: string
  user_agent: string
}

const { authFetch } = useAuth()

const items = ref<AuditRow[]>([])
const total = ref(0)
const limit = ref(50)
const offset = ref(0)
const loading = ref(true)
const error = ref<string | null>(null)

const actor = ref('')
const action = ref('')
const failedOnly = ref(false)

function fmtDate(s: string) {
  const d = new Date(s)
  return Number.isNaN(d.getTime()) ? s : d.toLocaleString()
}

function fmtParams(p: Record<string, string>) {
  return Object.entries(p ?? {}).map(([k, v]) => `${k}: ${v}`).join(', ')
}

async function load() {
  loading.value = true
  error.value = null
  const qs = new URLSearchParams({ limit: String(limit.value), offset: String(offset.value) })
  if (actor.value.trim()) qs.set('actor', actor.value.trim())
  if (action.value.trim()) qs.set('action', action.value.trim())
  if (failedOnly.value) qs.set('failed', '1')
  try {
    const res = await authFetch(`/admin/audit-logs?${qs}`)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = await res.json()
    items.value = data.items ?? []
    total.value = data.total ?? 0
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load the audit log'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

function applyFilters() {
  offset.value = 0
  load()
}

let debounce: ReturnType<typeof setTimeout> | undefined
watch([actor, action], () => {
  clearTimeout(debounce)
  debounce = setTimeout(applyFilters, 300)
})
watch(failedOnly, applyFilters)
onBeforeUnmount(() => clearTimeout(debounce))

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
</script>

<template>
  <AdminShell>
    <div class="mt-8">
      <p class="eyebrow mb-2 text-primary-text">// admin · audit log</p>
      <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">Audit log</h1>
      <p class="mt-2 text-sm text-muted-foreground">
        Every change made through the API and every sign-in, newest first. Reads are not recorded.
      </p>
    </div>

    <div class="mt-6 flex flex-wrap items-end gap-3">
      <div class="w-full space-y-2 sm:w-56">
        <Label for="a-actor">Actor</Label>
        <Input id="a-actor" v-model="actor" placeholder="username" autocomplete="off" />
      </div>
      <div class="w-full space-y-2 sm:w-56">
        <Label for="a-action">Action</Label>
        <Input id="a-action" v-model="action" placeholder="e.g. /vms or auth." autocomplete="off" />
      </div>
      <div class="flex h-9 items-center gap-2">
        <Switch id="a-failed" v-model="failedOnly" />
        <Label for="a-failed">Failures only</Label>
      </div>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load the audit log</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <DataTable
      label="Audit log"
      :columns="columns"
      :loading="loading"
      loading-label="Loading the audit log…"
      :empty="!items.length"
      class="mt-6"
    >
      <template #empty>
        No entries match.
      </template>
      <TableRow v-for="r in items" :key="r.id">
        <TableCell class="whitespace-nowrap text-muted-foreground">{{ fmtDate(r.created_at) }}</TableCell>
        <TableCell>
          <div class="flex items-center gap-2">
            <span class="font-mono" :class="!r.actor_id && 'text-muted-foreground'">{{ r.actor_username || '—' }}</span>
            <Badge v-if="r.auth_method === 'pat'" variant="outline" class="font-mono text-xs">pat</Badge>
          </div>
        </TableCell>
        <TableCell class="font-mono text-xs whitespace-nowrap" :title="r.path">{{ r.action }}</TableCell>
        <TableCell class="max-w-64 truncate font-mono text-xs text-muted-foreground" :title="fmtParams(r.params)">
          {{ fmtParams(r.params) || '—' }}
        </TableCell>
        <TableCell>
          <Badge :variant="r.status >= 400 ? 'destructive' : 'secondary'" class="font-mono">{{ r.status }}</Badge>
        </TableCell>
        <TableCell class="font-mono text-muted-foreground" :title="r.user_agent">{{ r.ip || '—' }}</TableCell>
      </TableRow>
    </DataTable>

    <nav aria-label="Audit log pagination" class="mt-4 flex flex-wrap items-center justify-between gap-3 font-mono text-xs text-muted-foreground">
      <span>{{ total }} total · page {{ page }} / {{ pageCount }}</span>
      <div class="flex gap-2">
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset <= 0 || loading" aria-label="Previous page of the audit log" @click="prev">Prev</Button>
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset + limit >= total || loading" aria-label="Next page of the audit log" @click="next">Next</Button>
      </div>
    </nav>
  </AdminShell>
</template>
