<script setup lang="ts">
import { Ban, Check, Copy, Plus, Trash2 } from '@lucide/vue'
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
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { TableCell, TableRow } from '@/components/ui/table'
import type { DataTableColumn } from '@/lib/table'

const columns: DataTableColumn[] = [
  { key: 'label', label: 'Label' },
  { key: 'key', label: 'Key' },
  { key: 'status', label: 'Status' },
  { key: 'uses', label: 'Uses' },
  { key: 'expires', label: 'Expires' },
  { key: 'created', label: 'Created' },
  { key: 'actions', label: 'Actions', align: 'right' },
]

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · agent keys' })

interface KeyRow {
  id: string
  label: string
  key_prefix: string
  uses: number
  max_uses: number | null
  expires_at: string
  status: 'active' | 'revoked' | 'expired' | 'exhausted'
  created_at: string
}

const { authFetch } = useAuth()

const items = ref<KeyRow[]>([])
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
  if (!s) return 'never'
  const d = new Date(s)
  return Number.isNaN(d.getTime()) ? s : d.toLocaleString()
}

const statusVariant: Record<KeyRow['status'], 'default' | 'secondary' | 'destructive'> = {
  active: 'default',
  revoked: 'destructive',
  expired: 'secondary',
  exhausted: 'secondary',
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await authFetch(`/admin/agent-keys?limit=${limit.value}&offset=${offset.value}`)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = await res.json()
    items.value = data.items ?? []
    total.value = data.total ?? 0
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load enrollment keys'
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

// --- create ---
const createOpen = ref(false)
const creating = ref(false)
const createError = ref<string | null>(null)
const form = reactive({ label: '', max_uses: '', expires_in_hours: '24' })
// The raw key exists only in this ref, only until the dialog is dismissed.
const issuedKey = ref<string | null>(null)
const copied = ref(false)

function resetForm() {
  Object.assign(form, { label: '', max_uses: '', expires_in_hours: '24' })
  createError.value = null
  issuedKey.value = null
  copied.value = false
}

async function create() {
  creating.value = true
  createError.value = null
  try {
    const body: Record<string, unknown> = { label: form.label }
    if (form.max_uses !== '') body.max_uses = Number(form.max_uses)
    if (form.expires_in_hours !== '') body.expires_in_hours = Number(form.expires_in_hours)

    const res = await authFetch('/admin/agent-keys', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    const data = await res.json()
    issuedKey.value = data.key
    offset.value = 0
    await load()
  }
  catch (e) {
    createError.value = e instanceof Error ? e.message : 'Could not create key'
  }
  finally {
    creating.value = false
  }
}

async function copyKey() {
  if (!issuedKey.value) return
  try {
    await navigator.clipboard.writeText(issuedKey.value)
    copied.value = true
    setTimeout(() => (copied.value = false), 2000)
  }
  catch {
    createError.value = 'Could not copy to the clipboard; select the key and copy it manually.'
  }
}

const connectCommand = computed(() =>
  `dagent connect --control-url ${window.location.origin} --key ${issuedKey.value ?? ''}`)

// --- revoke / delete ---
const toRevoke = ref<KeyRow | null>(null)
const toDelete = ref<KeyRow | null>(null)
const working = ref(false)
const actionError = ref<string | null>(null)

async function confirmRevoke() {
  if (!toRevoke.value) return
  working.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/agent-keys/${toRevoke.value.id}/revoke`, { method: 'POST' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toRevoke.value = null
    await load()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not revoke key'
  }
  finally {
    working.value = false
  }
}

async function confirmDelete() {
  if (!toDelete.value) return
  working.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/agent-keys/${toDelete.value.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toDelete.value = null
    await load()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not delete key'
  }
  finally {
    working.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-12 sm:px-6">
    <AdminNav />

    <div class="mt-8 flex items-end justify-between gap-4">
      <div>
        <p class="eyebrow mb-2 text-primary-text">// admin · agent keys</p>
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">Enrollment keys</h1>
      </div>
      <Dialog v-model:open="createOpen" @update:open="(v: boolean) => !v && resetForm()">
        <Button class="font-mono text-xs" @click="createOpen = true">
          <Plus class="size-4" aria-hidden="true" />
          New key
        </Button>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{{ issuedKey ? 'Key created' : 'Create enrollment key' }}</DialogTitle>
            <DialogDescription>
              {{ issuedKey
                ? 'Copy it now — it is stored hashed and will never be shown again.'
                : 'A one-time credential a machine uses to enroll itself.' }}
            </DialogDescription>
          </DialogHeader>

          <!-- issued: show the raw key exactly once -->
          <!-- min-w-0: DialogContent is a grid, so without it the long command
               below sets the track's min-content width and stretches the modal. -->
          <div v-if="issuedKey" class="min-w-0 space-y-4">
            <div class="rounded-lg border border-primary-text/40 bg-primary/5 p-3">
              <p class="sr-only">Enrollment key:</p>
              <p class="break-all font-mono text-sm">{{ issuedKey }}</p>
            </div>
            <Button variant="outline" class="w-full font-mono text-xs" @click="copyKey">
              <component :is="copied ? Check : Copy" class="size-4" aria-hidden="true" />
              {{ copied ? 'Copied' : 'Copy key' }}
            </Button>
            <!-- The button's own label change isn't reliably re-announced, so
                 the confirmation gets its own live region. (WCAG 4.1.3) -->
            <p role="status" aria-live="polite" class="sr-only">
              {{ copied ? 'Enrollment key copied to clipboard' : '' }}
            </p>
            <div class="min-w-0 space-y-2">
              <!-- Wrapping <label> would falsely claim to label the <p>; this is
                   a heading for a static block, so it's marked up as one. -->
              <p id="connect-command-label" class="text-sm font-medium">Run on the target machine</p>
              <p
                class="max-w-full overflow-x-auto rounded-md border border-border bg-muted/40 p-3 font-mono text-xs whitespace-pre"
                tabindex="0"
                role="region"
                aria-labelledby="connect-command-label"
              >{{ connectCommand }}</p>
            </div>
            <FormError id="issued-key-error" :message="createError" />
            <DialogFooter>
              <DialogClose as-child>
                <Button type="button" class="font-mono text-xs">Done</Button>
              </DialogClose>
            </DialogFooter>
          </div>

          <!-- create form -->
          <form v-else class="space-y-4" :aria-busy="creating" @submit.prevent="create">
            <div class="space-y-2">
              <Label for="k-label">Label</Label>
              <Input id="k-label" v-model="form.label" placeholder="staging fleet" />
            </div>
            <div class="space-y-2">
              <Label for="k-max">Max uses</Label>
              <Input id="k-max" v-model="form.max_uses" type="number" min="1" placeholder="leave empty for unlimited" />
            </div>
            <div class="space-y-2">
              <Label for="k-exp">Expires in</Label>
              <div class="*:w-full">
                <NativeSelect id="k-exp" v-model="form.expires_in_hours">
                  <NativeSelectOption value="1">1 hour</NativeSelectOption>
                  <NativeSelectOption value="24">24 hours</NativeSelectOption>
                  <NativeSelectOption value="168">7 days</NativeSelectOption>
                  <NativeSelectOption value="">never</NativeSelectOption>
                </NativeSelect>
              </div>
            </div>

            <FormError id="create-key-error" :message="createError" />

            <DialogFooter>
              <DialogClose as-child>
                <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
              </DialogClose>
              <Button type="submit" class="font-mono text-xs" :disabled="creating">
                {{ creating ? 'Creating…' : 'Create' }}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load enrollment keys</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <DataTable
      label="Enrollment keys"
      :columns="columns"
      :loading="loading"
      loading-label="Loading enrollment keys…"
      :empty="!items.length"
      class="mt-6"
    >
      <template #empty>
        No enrollment keys yet.
      </template>
      <TableRow v-for="k in items" :key="k.id">
        <TableCell>{{ k.label || '—' }}</TableCell>
        <TableCell class="font-mono text-muted-foreground">{{ k.key_prefix }}…</TableCell>
        <TableCell>
          <Badge :variant="statusVariant[k.status]" class="font-mono">{{ k.status }}</Badge>
        </TableCell>
        <TableCell class="font-mono text-muted-foreground">{{ k.uses }} / {{ k.max_uses ?? '∞' }}</TableCell>
        <TableCell class="text-muted-foreground">{{ fmtDate(k.expires_at) }}</TableCell>
        <TableCell class="text-muted-foreground">{{ fmtDate(k.created_at) }}</TableCell>
        <TableCell class="text-right">
          <div class="flex justify-end gap-1">
            <Button
              variant="ghost"
              size="icon"
              class="text-destructive hover:text-destructive"
              :disabled="k.status !== 'active'"
              :aria-label="k.status === 'active'
                ? `Revoke enrollment key ${k.label || k.key_prefix}`
                : `Cannot revoke enrollment key ${k.label || k.key_prefix}: already inactive`"
              @click="toRevoke = k"
            >
              <Ban class="size-4" aria-hidden="true" />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              class="text-destructive hover:text-destructive"
              :aria-label="`Delete enrollment key ${k.label || k.key_prefix}`"
              @click="toDelete = k"
            >
              <Trash2 class="size-4" aria-hidden="true" />
            </Button>
          </div>
        </TableCell>
      </TableRow>
    </DataTable>

    <nav aria-label="Enrollment keys pagination" class="mt-4 flex flex-wrap items-center justify-between gap-3 font-mono text-xs text-muted-foreground">
      <span>{{ total }} total · page {{ page }} / {{ pageCount }}</span>
      <div class="flex gap-2">
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset <= 0 || loading" aria-label="Previous page of enrollment keys" @click="prev">Prev</Button>
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset + limit >= total || loading" aria-label="Next page of enrollment keys" @click="next">Next</Button>
      </div>
    </nav>

    <!-- revoke confirm -->
    <Dialog :open="!!toRevoke" @update:open="(v: boolean) => { if (!v) toRevoke = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Revoke enrollment key</DialogTitle>
          <DialogDescription>
            No new machine will be able to enroll with
            <span class="font-mono text-foreground">{{ toRevoke?.key_prefix }}…</span>.
            Agents that already enrolled with it keep working — revoke them individually to cut them off.
          </DialogDescription>
        </DialogHeader>
        <FormError id="revoke-key-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="working" @click="confirmRevoke">
            {{ working ? 'Revoking…' : 'Revoke' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- delete confirm -->
    <Dialog :open="!!toDelete" @update:open="(v: boolean) => { if (!v) toDelete = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete enrollment key</DialogTitle>
          <DialogDescription>
            Permanently remove <span class="font-mono text-foreground">{{ toDelete?.key_prefix }}…</span>.
            This also drops the record of which agents enrolled with it. This cannot be undone.
          </DialogDescription>
        </DialogHeader>
        <FormError id="delete-key-error" :message="actionError" />
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
