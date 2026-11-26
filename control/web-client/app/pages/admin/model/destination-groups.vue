<script setup lang="ts">
import { Download, Pencil, Plus, Trash2, Upload } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogScrollContent,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { TableCell, TableRow } from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import TargetFormRow from '@/components/TargetFormRow.vue'
import type { DataTableColumn } from '@/lib/table'
import { blankTarget, targetToForm, toTargetPayload, type DestinationGroup, type TargetForm } from '@/lib/targets'

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · destination groups' })

const columns: DataTableColumn[] = [
  { key: 'name', label: 'Name' },
  { key: 'entries', label: 'Destinations' },
  { key: 'vms', label: 'VMs', align: 'right' },
  { key: 'actions', label: 'Actions', align: 'right' },
]

const { authFetch } = useAuth()

const items = ref<DestinationGroup[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

async function readMessage(res: Response) {
  const b = await res.json().catch(() => null)
  return typeof b?.message === 'string' ? b.message : `HTTP ${res.status}`
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await authFetch('/admin/destination-groups')
    if (!res.ok) throw new Error(await readMessage(res))
    items.value = (await res.json()).items ?? []
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load destination groups'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

const formOpen = ref(false)
const editing = ref<DestinationGroup | null>(null)
const name = ref('')
const description = ref('')
const entries = ref<TargetForm[]>([])
const saving = ref(false)
const formError = ref<string | null>(null)

function openForm(g: DestinationGroup | null) {
  editing.value = g
  name.value = g?.name ?? ''
  description.value = g?.description ?? ''
  entries.value = g ? g.entries.map(e => targetToForm(e, 0)) : [blankTarget()]
  formError.value = null
  formOpen.value = true
}

async function save() {
  if (entries.value.some(e => !e.destination.trim())) {
    formError.value = 'Every destination needs an address or a name. Fill it in or remove the row.'
    return
  }
  saving.value = true
  formError.value = null
  try {
    const body = {
      name: name.value,
      description: description.value,
      entries: entries.value.map(toTargetPayload),
    }
    const res = await authFetch(
      editing.value ? `/admin/destination-groups/${editing.value.id}` : '/admin/destination-groups',
      { method: editing.value ? 'PUT' : 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) },
    )
    if (!res.ok) throw new Error(await readMessage(res))
    formOpen.value = false
    await load()
  }
  catch (e) {
    formError.value = e instanceof Error ? e.message : 'Could not save the group'
  }
  finally {
    saving.value = false
  }
}

const toDelete = ref<DestinationGroup | null>(null)
const deleting = ref(false)
const deleteError = ref<string | null>(null)

async function confirmDelete() {
  if (!toDelete.value) return
  deleting.value = true
  deleteError.value = null
  try {
    const res = await authFetch(`/admin/destination-groups/${toDelete.value.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error(await readMessage(res))
    toDelete.value = null
    await load()
  }
  catch (e) {
    deleteError.value = e instanceof Error ? e.message : 'Could not delete the group'
  }
  finally {
    deleting.value = false
  }
}

function exportGroups() {
  const file = {
    version: 1,
    groups: items.value.map(g => ({ name: g.name, description: g.description, entries: g.entries })),
  }
  const url = URL.createObjectURL(new Blob([JSON.stringify(file, null, 2)], { type: 'application/json' }))
  const a = document.createElement('a')
  a.href = url
  a.download = `destination-groups-${new Date().toISOString().slice(0, 10)}.json`
  a.click()
  URL.revokeObjectURL(url)
}

const picker = ref<HTMLInputElement | null>(null)
const importing = ref(false)
const importError = ref<string | null>(null)
const importResult = ref<string | null>(null)

async function importFile(file: File | undefined) {
  if (!file) return
  importing.value = true
  importError.value = null
  importResult.value = null
  try {
    let parsed: { groups?: { name?: string }[] }
    try {
      parsed = JSON.parse(await file.text())
    }
    catch {
      throw new Error('That file is not valid JSON.')
    }
    const groups = parsed?.groups
    if (!Array.isArray(groups)) throw new Error('That file has no "groups" list in it.')
    const res = await authFetch('/admin/destination-groups/import', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ groups }),
    })
    if (!res.ok) throw new Error(await readMessage(res))
    const out: { name: string, renamed: boolean }[] = (await res.json()).items ?? []
    const renamed = out
      .map((g, i) => g.renamed ? `${groups[i]?.name ?? '?'} → ${g.name}` : '')
      .filter(Boolean)
    importResult.value = `Imported ${out.length} group${out.length === 1 ? '' : 's'}.`
      + (renamed.length ? ` Renamed because the name was taken: ${renamed.join(', ')}.` : '')
    await load()
  }
  catch (e) {
    importError.value = e instanceof Error ? e.message : 'Could not import the groups'
  }
  finally {
    importing.value = false
  }
}
</script>

<template>
  <AdminShell>
    <div class="mt-8 flex flex-wrap items-end justify-between gap-4">
      <div>
        <p class="eyebrow mb-2 text-primary-text">// admin · destination groups</p>
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">Destination groups</h1>
        <p class="mt-2 max-w-2xl text-sm text-muted-foreground">
          Named sets of destinations users can allow on a VM in one go. A VM follows its groups:
          editing one rewrites the policy of every VM it is attached to.
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <input ref="picker" type="file" accept="application/json,.json" class="hidden" @change="importFile(($event.target as HTMLInputElement).files?.[0]); ($event.target as HTMLInputElement).value = ''">
        <Button variant="outline" class="font-mono text-xs" :disabled="importing" @click="picker?.click()">
          <Upload class="size-4" aria-hidden="true" />
          {{ importing ? 'Importing…' : 'Import' }}
        </Button>
        <Button variant="outline" class="font-mono text-xs" :disabled="!items.length" @click="exportGroups">
          <Download class="size-4" aria-hidden="true" />
          Export
        </Button>
        <Button class="font-mono text-xs" @click="openForm(null)">
          <Plus class="size-4" aria-hidden="true" />
          New group
        </Button>
      </div>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load destination groups</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>
    <Alert v-if="importError" variant="destructive" class="mt-6">
      <AlertTitle>Could not import</AlertTitle>
      <AlertDescription>{{ importError }}</AlertDescription>
    </Alert>
    <Alert v-if="importResult" class="mt-6">
      <AlertTitle>Import finished</AlertTitle>
      <AlertDescription>{{ importResult }}</AlertDescription>
    </Alert>

    <DataTable
      label="Destination groups"
      :columns="columns"
      :loading="loading"
      :loading-rows="3"
      loading-label="Loading destination groups…"
      :empty="!items.length"
      class="mt-6"
    >
      <template #empty>
        No destination groups yet.
      </template>
      <TableRow v-for="g in items" :key="g.id">
        <TableCell>
          <span class="font-mono">{{ g.name }}</span>
          <span v-if="g.description" class="block text-xs text-muted-foreground">{{ g.description }}</span>
        </TableCell>
        <TableCell class="font-mono text-xs break-all text-muted-foreground">
          {{ g.entries.map(e => e.destination).join(', ') || '—' }}
        </TableCell>
        <TableCell class="text-right font-mono tabular-nums">{{ g.vm_count ?? 0 }}</TableCell>
        <TableCell class="space-x-1 text-right whitespace-nowrap">
          <Button variant="ghost" size="icon" :aria-label="`Edit group ${g.name}`" @click="openForm(g)">
            <Pencil class="size-4" aria-hidden="true" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            class="text-destructive hover:text-destructive"
            :aria-label="`Delete group ${g.name}`"
            @click="deleteError = null; toDelete = g"
          >
            <Trash2 class="size-4" aria-hidden="true" />
          </Button>
        </TableCell>
      </TableRow>
    </DataTable>

    <Dialog v-model:open="formOpen">
      <DialogScrollContent class="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{{ editing ? `Edit ${editing.name}` : 'New destination group' }}</DialogTitle>
          <DialogDescription>
            <template v-if="editing && editing.vm_count">
              Saving rewrites the policy of the {{ editing.vm_count }} VM{{ editing.vm_count === 1 ? '' : 's' }} using it.
            </template>
            <template v-else>
              Entries follow the same rules as a VM's own destinations.
            </template>
          </DialogDescription>
        </DialogHeader>

        <form class="space-y-4" :aria-busy="saving" @submit.prevent="save">
          <div class="grid gap-4 sm:grid-cols-2">
            <div class="space-y-2">
              <Label for="g-name">Name</Label>
              <Input id="g-name" v-model="name" required placeholder="claude" autocomplete="off" spellcheck="false" aria-describedby="g-name-hint" />
              <p id="g-name-hint" class="text-xs text-muted-foreground">Lowercase letters, digits, '.', '_' and '-'.</p>
            </div>
            <div class="space-y-2">
              <Label for="g-desc">Description</Label>
              <Textarea id="g-desc" v-model="description" rows="2" placeholder="what these are for" />
            </div>
          </div>

          <div class="space-y-3 border-t border-border pt-4">
            <div class="flex items-center justify-between gap-3">
              <p class="text-sm font-medium">Destinations</p>
              <Button type="button" variant="outline" size="sm" class="font-mono text-xs" @click="entries = [...entries, blankTarget()]">
                <Plus class="size-4" aria-hidden="true" />
                Add
              </Button>
            </div>
            <TargetFormRow
              v-for="(e, i) in entries"
              :key="i"
              v-model="entries[i]!"
              :index="i"
              hide-ttl
              @remove="entries = entries.filter((_, n) => n !== i)"
            />
          </div>

          <FormError id="group-form-error" :message="formError" />

          <DialogFooter>
            <DialogClose as-child>
              <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
            </DialogClose>
            <Button type="submit" class="font-mono text-xs" :disabled="saving || !name.trim()">
              {{ saving ? 'Saving…' : 'Save' }}
            </Button>
          </DialogFooter>
        </form>
      </DialogScrollContent>
    </Dialog>

    <Dialog :open="!!toDelete" @update:open="(v: boolean) => !v && (toDelete = null)">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete {{ toDelete?.name }}</DialogTitle>
          <DialogDescription>
            <template v-if="toDelete?.vm_count">
              It is attached to {{ toDelete.vm_count }} VM{{ toDelete.vm_count === 1 ? '' : 's' }}, which
              lose these destinations as soon as it is gone.
            </template>
            <template v-else>No VM uses it.</template>
          </DialogDescription>
        </DialogHeader>
        <FormError id="delete-group-error" :message="deleteError" />
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
