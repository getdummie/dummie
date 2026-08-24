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
  { key: 'user', label: 'User' },
  { key: 'status', label: 'Status' },
  { key: 'expires', label: 'Expires' },
  { key: 'ip', label: 'IP' },
  { key: 'created', label: 'Created' },
  { key: 'actions', label: 'Actions', align: 'right' },
]

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · tokens' })

interface TokenRow {
  id: string
  username: string
  email: string
  status: 'active' | 'revoked' | 'expired'
  expires_at: string
  created_at: string
  user_agent: string
  ip: string
}

const { authFetch } = useAuth()

const items = ref<TokenRow[]>([])
const total = ref(0)
const limit = ref(20)
const offset = ref(0)
const loading = ref(true)
const error = ref<string | null>(null)
const notice = ref<string | null>(null)

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
  const d = new Date(s)
  return Number.isNaN(d.getTime()) ? s : d.toLocaleString()
}

const statusVariant: Record<TokenRow['status'], 'default' | 'secondary' | 'destructive'> = {
  active: 'default',
  revoked: 'destructive',
  expired: 'secondary',
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await authFetch(`/admin/tokens?limit=${limit.value}&offset=${offset.value}`)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = await res.json()
    items.value = data.items ?? []
    total.value = data.total ?? 0
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load tokens'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

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

// --- blacklist ---
const toBlacklist = ref<TokenRow | null>(null)
const working = ref(false)
const actionError = ref<string | null>(null)

async function confirmBlacklist() {
  if (!toBlacklist.value) return
  working.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/tokens/${toBlacklist.value.id}/blacklist`, { method: 'POST' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toBlacklist.value = null
    await load()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not blacklist token'
  }
  finally {
    working.value = false
  }
}

// --- delete one ---
const toDelete = ref<TokenRow | null>(null)
const deleting = ref(false)

async function confirmDelete() {
  if (!toDelete.value) return
  deleting.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/tokens/${toDelete.value.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toDelete.value = null
    await load()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not delete token'
  }
  finally {
    deleting.value = false
  }
}

// --- cleanup expired ---
const cleanupOpen = ref(false)
const cleaning = ref(false)
async function confirmCleanup() {
  cleaning.value = true
  actionError.value = null
  notice.value = null
  try {
    const res = await authFetch('/admin/tokens/cleanup', { method: 'POST' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    const data = await res.json()
    cleanupOpen.value = false
    notice.value = `Removed ${data.deleted ?? 0} expired token(s).`
    offset.value = 0
    await load()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not clean up tokens'
  }
  finally {
    cleaning.value = false
  }
}
</script>

<template>
  <AdminShell>

    <div class="mt-8 flex items-end justify-between gap-4">
      <div>
        <p class="eyebrow mb-2 text-primary-text">// admin · tokens</p>
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">Sessions</h1>
      </div>
      <Button variant="outline" class="font-mono text-xs" @click="cleanupOpen = true">
        <Trash2 class="size-4" aria-hidden="true" />
        Cleanup expired
      </Button>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load tokens</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>
    <!-- Success confirmations are status messages: announce them without
         stealing focus. (WCAG 4.1.3) -->
    <div role="status" aria-live="polite">
      <p v-if="notice" class="mt-4 font-mono text-xs text-primary-text">{{ notice }}</p>
    </div>

    <DataTable
      label="Sessions"
      :columns="columns"
      :loading="loading"
      loading-label="Loading sessions…"
      :empty="!items.length"
      class="mt-6"
    >
      <template #empty>
        No tokens found.
      </template>
      <TableRow v-for="t in items" :key="t.id">
        <TableCell>
          <div class="font-mono">{{ t.username }}</div>
          <div class="text-xs text-muted-foreground">{{ t.email }}</div>
        </TableCell>
        <TableCell>
          <Badge :variant="statusVariant[t.status]" class="font-mono">{{ t.status }}</Badge>
        </TableCell>
        <TableCell class="text-muted-foreground">{{ fmtDate(t.expires_at) }}</TableCell>
        <TableCell class="font-mono text-muted-foreground">{{ t.ip || '—' }}</TableCell>
        <TableCell class="text-muted-foreground">{{ fmtDate(t.created_at) }}</TableCell>
        <TableCell class="text-right">
          <div class="flex justify-end gap-1">
            <Button
              variant="ghost"
              size="icon"
              class="text-destructive hover:text-destructive"
              :disabled="t.status !== 'active'"
              :aria-label="t.status === 'active'
                ? `Blacklist ${t.username}'s session`
                : `Cannot blacklist ${t.username}'s session: already inactive`"
              @click="toBlacklist = t"
            >
              <Ban class="size-4" aria-hidden="true" />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              class="text-destructive hover:text-destructive"
              :disabled="t.status === 'active'"
              :aria-label="t.status === 'active'
                ? `Cannot delete ${t.username}'s session: blacklist it first`
                : `Delete ${t.username}'s session`"
              @click="toDelete = t"
            >
              <Trash2 class="size-4" aria-hidden="true" />
            </Button>
          </div>
        </TableCell>
      </TableRow>
    </DataTable>

    <nav aria-label="Sessions pagination" class="mt-4 flex flex-wrap items-center justify-between gap-3 font-mono text-xs text-muted-foreground">
      <span>{{ total }} total · page {{ page }} / {{ pageCount }}</span>
      <div class="flex gap-2">
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset <= 0 || loading" aria-label="Previous page of sessions" @click="prev">Prev</Button>
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset + limit >= total || loading" aria-label="Next page of sessions" @click="next">Next</Button>
      </div>
    </nav>

    <!-- blacklist confirm -->
    <Dialog :open="!!toBlacklist" @update:open="(v: boolean) => { if (!v) toBlacklist = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Blacklist session</DialogTitle>
          <DialogDescription>
            Revoke <span class="font-mono text-foreground">{{ toBlacklist?.username }}</span>'s session.
            It can no longer be refreshed; the session ends within the access-token TTL.
          </DialogDescription>
        </DialogHeader>
        <FormError id="blacklist-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="working" @click="confirmBlacklist">
            {{ working ? 'Blacklisting…' : 'Blacklist' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- delete one confirm -->
    <Dialog :open="!!toDelete" @update:open="(v: boolean) => { if (!v) toDelete = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete token</DialogTitle>
          <DialogDescription>
            Permanently remove this
            <span class="font-mono text-foreground">{{ toDelete?.status }}</span>
            session for <span class="font-mono text-foreground">{{ toDelete?.username }}</span>.
            This cannot be undone.
          </DialogDescription>
        </DialogHeader>
        <FormError id="delete-token-error" :message="actionError" />
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

    <!-- cleanup confirm -->
    <Dialog v-model:open="cleanupOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Cleanup expired tokens</DialogTitle>
          <DialogDescription>
            Permanently delete all refresh-token rows whose expiry has passed. Active and revoked
            sessions are untouched.
          </DialogDescription>
        </DialogHeader>
        <FormError id="cleanup-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="cleaning" @click="confirmCleanup">
            {{ cleaning ? 'Cleaning…' : 'Delete expired' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </AdminShell>
</template>
