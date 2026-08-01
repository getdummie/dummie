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
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'

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
        <p class="eyebrow mb-2 text-primary">// admin · agents</p>
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">Agents</h1>
      </div>
      <span class="font-mono text-xs text-muted-foreground">refreshes every 10s</span>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load agents</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <div class="mt-6 rounded-lg border border-border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Agent</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>OS</TableHead>
            <TableHead>Arch</TableHead>
            <TableHead>Version</TableHead>
            <TableHead>Last seen</TableHead>
            <TableHead class="text-right">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <template v-if="loading">
            <TableRow v-for="n in 5" :key="n">
              <TableCell v-for="c in 7" :key="c"><Skeleton class="h-4 w-full" /></TableCell>
            </TableRow>
          </template>
          <TableEmpty v-else-if="!items.length" :colspan="7">
            No agents enrolled. Create an enrollment key, then run
            <span class="font-mono">dagent connect --key …</span> on a machine.
          </TableEmpty>
          <TableRow v-for="a in items" v-else :key="a.id">
            <TableCell>
              <div class="font-mono">{{ a.hostname || '—' }}</div>
              <div class="truncate text-xs text-muted-foreground">{{ a.machine_id }}</div>
            </TableCell>
            <TableCell>
              <Badge :variant="statusVariant[a.status]" class="font-mono">{{ a.status }}</Badge>
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
                  :title="a.status === 'revoked' ? 'Already revoked' : 'Revoke agent token'"
                  @click="toRevoke = a"
                >
                  <Ban class="size-4" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  class="text-destructive hover:text-destructive"
                  title="Delete agent"
                  @click="toDelete = a"
                >
                  <Trash2 class="size-4" />
                </Button>
              </div>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>

    <div class="mt-4 flex items-center justify-between font-mono text-xs text-muted-foreground">
      <span>{{ total }} total · page {{ page }} / {{ pageCount }}</span>
      <div class="flex gap-2">
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset <= 0 || loading" @click="prev">Prev</Button>
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset + limit >= total || loading" @click="next">Next</Button>
      </div>
    </div>

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
        <p v-if="actionError" class="text-sm text-destructive">{{ actionError }}</p>
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
        <p v-if="actionError" class="text-sm text-destructive">{{ actionError }}</p>
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
