<script setup lang="ts">
import { Plus, Trash2 } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
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
import { TableCell, TableRow } from '@/components/ui/table'
import type { DataTableColumn } from '@/lib/table'

const columns: DataTableColumn[] = [
  { key: 'tld', label: 'TLD' },
  { key: 'actions', label: 'Actions', align: 'right' },
]

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · domains' })

interface DomainRow {
  id: string
  tld: string
}

const { authFetch } = useAuth()

const items = ref<DomainRow[]>([])
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

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await authFetch('/admin/domains')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = await res.json()
    items.value = data.items ?? []
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load domains'
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
const tld = ref('')

function resetForm() {
  tld.value = ''
  createError.value = null
}

async function create() {
  creating.value = true
  createError.value = null
  try {
    const res = await authFetch('/admin/domains', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tld: tld.value }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    createOpen.value = false
    resetForm()
    await load()
  }
  catch (e) {
    createError.value = e instanceof Error ? e.message : 'Could not create domain'
  }
  finally {
    creating.value = false
  }
}

// --- delete ---
const toDelete = ref<DomainRow | null>(null)
const deleting = ref(false)
const actionError = ref<string | null>(null)

async function confirmDelete() {
  if (!toDelete.value) return
  deleting.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/domains/${toDelete.value.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toDelete.value = null
    await load()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not delete domain'
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
        <p class="eyebrow mb-2 text-primary-text">// admin · domains</p>
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">Domains</h1>
        <p class="mt-2 max-w-2xl text-sm text-muted-foreground">
          DNS suffixes this installation owns. While exactly one exists, every agent that enrolls is
          assigned it automatically.
        </p>
      </div>
      <Dialog v-model:open="createOpen" @update:open="(v: boolean) => !v && resetForm()">
        <Button class="font-mono text-xs" @click="createOpen = true">
          <Plus class="size-4" aria-hidden="true" />
          New domain
        </Button>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add domain</DialogTitle>
            <DialogDescription>
              A suffix like <span class="font-mono">example.com</span> or <span class="font-mono">lab.internal</span>.
            </DialogDescription>
          </DialogHeader>
          <form class="space-y-4" :aria-busy="creating" @submit.prevent="create">
            <div class="space-y-2">
              <Label for="d-tld">TLD</Label>
              <Input id="d-tld" v-model="tld" placeholder="example.com" autocomplete="off" spellcheck="false" />
            </div>

            <FormError id="create-domain-error" :message="createError" />

            <DialogFooter>
              <DialogClose as-child>
                <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
              </DialogClose>
              <Button type="submit" class="font-mono text-xs" :disabled="creating || !tld.trim()">
                {{ creating ? 'Adding…' : 'Add' }}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load domains</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <DataTable
      label="Domains"
      :columns="columns"
      :loading="loading"
      :loading-rows="3"
      loading-label="Loading domains…"
      :empty="!items.length"
      class="mt-6"
    >
      <template #empty>
        No domains yet. Agents will enroll without one until you add exactly one.
      </template>
      <TableRow v-for="d in items" :key="d.id">
        <TableCell class="font-mono">{{ d.tld }}</TableCell>
        <TableCell class="text-right">
          <Button
            variant="ghost"
            size="icon"
            class="text-destructive hover:text-destructive"
            :aria-label="`Delete domain ${d.tld}`"
            @click="toDelete = d"
          >
            <Trash2 class="size-4" aria-hidden="true" />
          </Button>
        </TableCell>
      </TableRow>
    </DataTable>

    <!-- delete confirm -->
    <Dialog :open="!!toDelete" @update:open="(v: boolean) => { if (!v) toDelete = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete domain</DialogTitle>
          <DialogDescription>
            Remove <span class="font-mono text-foreground">{{ toDelete?.tld }}</span>.
            Agents assigned to it are kept, but lose their domain.
          </DialogDescription>
        </DialogHeader>
        <FormError id="delete-domain-error" :message="actionError" />
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
