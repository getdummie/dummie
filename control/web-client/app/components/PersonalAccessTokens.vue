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
  { key: 'token', label: 'Token' },
  { key: 'status', label: 'Status' },
  { key: 'last_used', label: 'Last used' },
  { key: 'expires', label: 'Expires' },
  { key: 'actions', label: 'Actions', align: 'right' },
]

interface TokenRow {
  id: string
  label: string
  token_prefix: string
  status: 'active' | 'revoked' | 'expired'
  expires_at: string
  last_used_at: string
  created_at: string
}

const { authFetch } = useAuth()

const items = ref<TokenRow[]>([])
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

const statusVariant: Record<TokenRow['status'], 'default' | 'secondary' | 'destructive'> = {
  active: 'default',
  revoked: 'destructive',
  expired: 'secondary',
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await authFetch('/me/tokens')
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    const data = await res.json()
    items.value = data.items ?? []
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load your tokens'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

// --- create ---
const createOpen = ref(false)
const creating = ref(false)
const createError = ref<string | null>(null)
const form = reactive({ label: '', expires_in_days: '90' })
// The raw token exists only in this ref, only until the dialog is dismissed.
const issued = ref<string | null>(null)
const copied = ref(false)

function resetForm() {
  Object.assign(form, { label: '', expires_in_days: '90' })
  createError.value = null
  issued.value = null
  copied.value = false
}

async function create() {
  creating.value = true
  createError.value = null
  try {
    const body: Record<string, unknown> = { label: form.label }
    if (form.expires_in_days !== '') body.expires_in_days = Number(form.expires_in_days)

    const res = await authFetch('/me/tokens', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    const data = await res.json()
    issued.value = data.token
    await load()
  }
  catch (e) {
    createError.value = e instanceof Error ? e.message : 'Could not create token'
  }
  finally {
    creating.value = false
  }
}

async function copyToken() {
  if (!issued.value) return
  try {
    await navigator.clipboard.writeText(issued.value)
    copied.value = true
    setTimeout(() => (copied.value = false), 2000)
  }
  catch {
    createError.value = 'Could not copy to the clipboard; select the token and copy it manually.'
  }
}

const exampleCommand = computed(() =>
  `curl -H "Authorization: Bearer ${issued.value ?? ''}" ${window.location.origin}/api/v1/vms`)

// --- revoke / delete ---
const toRevoke = ref<TokenRow | null>(null)
const toDelete = ref<TokenRow | null>(null)
const working = ref(false)
const actionError = ref<string | null>(null)

async function confirmRevoke() {
  if (!toRevoke.value) return
  working.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/me/tokens/${toRevoke.value.id}/revoke`, { method: 'POST' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toRevoke.value = null
    await load()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not revoke token'
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
    const res = await authFetch(`/me/tokens/${toDelete.value.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toDelete.value = null
    await load()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not delete token'
  }
  finally {
    working.value = false
  }
}
</script>

<template>
  <section aria-labelledby="pat-heading" class="rounded-lg border border-border p-4 sm:p-6">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div>
        <h2 id="pat-heading" class="text-sm font-semibold">Personal access tokens</h2>
        <p class="mt-1 max-w-prose text-sm text-muted-foreground">
          For reaching the API from a script or a CI job. A token acts as you and can do everything
          you can do with your own VMs — it can never reach the admin API, even if you are an admin.
        </p>
      </div>
      <Dialog v-model:open="createOpen" @update:open="(v: boolean) => !v && resetForm()">
        <Button class="font-mono text-xs" @click="createOpen = true">
          <Plus class="size-4" aria-hidden="true" />
          New token
        </Button>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{{ issued ? 'Token created' : 'Create personal access token' }}</DialogTitle>
            <DialogDescription>
              {{ issued
                ? 'Copy it now — it is stored hashed and will never be shown again.'
                : 'A long-lived credential for calling the API as yourself.' }}
            </DialogDescription>
          </DialogHeader>

          <!-- issued: show the raw token exactly once -->
          <!-- min-w-0: DialogContent is a grid, so without it the long command
               below sets the track's min-content width and stretches the modal. -->
          <div v-if="issued" class="min-w-0 space-y-4">
            <div class="rounded-lg border border-primary-text/40 bg-primary/5 p-3">
              <p class="sr-only">Personal access token:</p>
              <p class="break-all font-mono text-sm">{{ issued }}</p>
            </div>
            <Button variant="outline" class="w-full font-mono text-xs" @click="copyToken">
              <component :is="copied ? Check : Copy" class="size-4" aria-hidden="true" />
              {{ copied ? 'Copied' : 'Copy token' }}
            </Button>
            <!-- The button's own label change isn't reliably re-announced, so
                 the confirmation gets its own live region. (WCAG 4.1.3) -->
            <p role="status" aria-live="polite" class="sr-only">
              {{ copied ? 'Personal access token copied to clipboard' : '' }}
            </p>
            <div class="min-w-0 space-y-2">
              <!-- Wrapping <label> would falsely claim to label the <p>; this is
                   a heading for a static block, so it's marked up as one. -->
              <p id="pat-example-label" class="text-sm font-medium">Use it like this</p>
              <p
                class="max-w-full overflow-x-auto rounded-md border border-border bg-muted/40 p-3 font-mono text-xs whitespace-pre"
                tabindex="0"
                role="region"
                aria-labelledby="pat-example-label"
              >{{ exampleCommand }}</p>
            </div>
            <FormError id="issued-token-error" :message="createError" />
            <DialogFooter>
              <DialogClose as-child>
                <Button type="button" class="font-mono text-xs">Done</Button>
              </DialogClose>
            </DialogFooter>
          </div>

          <!-- create form -->
          <form v-else class="space-y-4" :aria-busy="creating" @submit.prevent="create">
            <div class="space-y-2">
              <Label for="t-label">Label</Label>
              <Input id="t-label" v-model="form.label" maxlength="100" placeholder="ci pipeline" required />
              <p class="text-xs text-muted-foreground">
                What holds this token. It is the only way to tell your tokens apart later.
              </p>
            </div>
            <div class="space-y-2">
              <Label for="t-exp">Expires in</Label>
              <div class="*:w-full">
                <NativeSelect id="t-exp" v-model="form.expires_in_days">
                  <NativeSelectOption value="7">7 days</NativeSelectOption>
                  <NativeSelectOption value="30">30 days</NativeSelectOption>
                  <NativeSelectOption value="90">90 days</NativeSelectOption>
                  <NativeSelectOption value="365">1 year</NativeSelectOption>
                  <NativeSelectOption value="">never</NativeSelectOption>
                </NativeSelect>
              </div>
            </div>

            <FormError id="create-token-error" :message="createError" />

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

    <Alert v-if="error" variant="destructive" class="mt-4">
      <AlertTitle>Could not load your tokens</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <DataTable
      label="Personal access tokens"
      :columns="columns"
      :loading="loading"
      loading-label="Loading your tokens…"
      :empty="!items.length"
      class="mt-4"
    >
      <template #empty>
        You have no tokens yet.
      </template>
      <TableRow v-for="t in items" :key="t.id">
        <TableCell>{{ t.label }}</TableCell>
        <TableCell class="font-mono text-muted-foreground">{{ t.token_prefix }}…</TableCell>
        <TableCell>
          <Badge :variant="statusVariant[t.status]" class="font-mono">{{ t.status }}</Badge>
        </TableCell>
        <TableCell class="text-muted-foreground">{{ t.last_used_at ? fmtDate(t.last_used_at) : 'never' }}</TableCell>
        <TableCell class="text-muted-foreground">{{ fmtDate(t.expires_at) }}</TableCell>
        <TableCell class="text-right">
          <div class="flex justify-end gap-1">
            <Button
              variant="ghost"
              size="icon"
              class="text-destructive hover:text-destructive"
              :disabled="t.status !== 'active'"
              :aria-label="t.status === 'active'
                ? `Revoke token ${t.label}`
                : `Cannot revoke token ${t.label}: already inactive`"
              @click="toRevoke = t"
            >
              <Ban class="size-4" aria-hidden="true" />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              class="text-destructive hover:text-destructive"
              :aria-label="`Delete token ${t.label}`"
              @click="toDelete = t"
            >
              <Trash2 class="size-4" aria-hidden="true" />
            </Button>
          </div>
        </TableCell>
      </TableRow>
    </DataTable>

    <!-- revoke confirm -->
    <Dialog :open="!!toRevoke" @update:open="(v: boolean) => { if (!v) toRevoke = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Revoke token</DialogTitle>
          <DialogDescription>
            Anything still using <span class="font-mono text-foreground">{{ toRevoke?.label }}</span>
            stops working immediately. The row stays in the list so you can see it was revoked.
          </DialogDescription>
        </DialogHeader>
        <FormError id="revoke-token-error" :message="actionError" />
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
          <DialogTitle>Delete token</DialogTitle>
          <DialogDescription>
            Permanently remove <span class="font-mono text-foreground">{{ toDelete?.label }}</span>.
            Anything still using it stops working, and no record of it is kept. This cannot be undone.
          </DialogDescription>
        </DialogHeader>
        <FormError id="delete-token-error" :message="actionError" />
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
  </section>
</template>
