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
import { Textarea } from '@/components/ui/textarea'
import type { DataTableColumn } from '@/lib/table'

const columns: DataTableColumn[] = [
  { key: 'name', label: 'Name' },
  { key: 'file', label: 'File' },
  { key: 'size', label: 'Size' },
  { key: 'description', label: 'Description' },
  { key: 'created', label: 'Uploaded' },
  { key: 'actions', label: 'Actions', align: 'right' },
]

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · kernels' })

interface KernelRow {
  id: string
  name: string
  description: string
  file_name: string
  size_bytes: number
  created_at: string
  soft_deleted_at: string
}

const { authFetch } = useAuth()

const items = ref<KernelRow[]>([])
const total = ref(0)
const limit = ref(20)
const offset = ref(0)
const loading = ref(true)
const error = ref<string | null>(null)
const actionError = ref<string | null>(null)

async function readMessage(res: Response): Promise<string | null> {
  try {
    const b = await res.json()
    return typeof b?.message === 'string' ? b.message : null
  }
  catch {
    return null
  }
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

function ordinal(n: number) {
  if (n % 100 >= 11 && n % 100 <= 13) return `${n}th`
  return `${n}${['th', 'st', 'nd', 'rd'][n % 10] ?? 'th'}`
}

function fmtDate(s: string) {
  if (!s) return '—'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  return `${ordinal(d.getDate())} ${d.toLocaleString(undefined, { month: 'long' })} ${d.getFullYear()}`
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await authFetch(`/admin/kernels?limit=${limit.value}&offset=${offset.value}`)
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    const data = await res.json()
    items.value = data.items ?? []
    total.value = data.total ?? 0
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load kernels'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)
watch(offset, load)

const page = computed(() => Math.floor(offset.value / limit.value) + 1)
const pageCount = computed(() => Math.max(1, Math.ceil(total.value / limit.value)))

const createOpen = ref(false)
const creating = ref(false)
const createError = ref<string | null>(null)
const form = reactive({ name: '', description: '' })
const file = ref<File | null>(null)
const fileKey = ref(0)

function onFile(e: Event) {
  file.value = (e.target as HTMLInputElement).files?.[0] ?? null
}

function resetForm() {
  form.name = ''
  form.description = ''
  file.value = null
  fileKey.value++
  createError.value = null
}

async function create() {
  if (!file.value) {
    createError.value = 'Choose a kernel image to upload.'
    return
  }
  creating.value = true
  createError.value = null
  try {
    const body = new FormData()
    body.set('name', form.name.trim())
    body.set('description', form.description.trim())
    body.set('file', file.value)
    const res = await authFetch('/admin/kernels', { method: 'POST', body })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    createOpen.value = false
    resetForm()
    offset.value = 0
    await load()
  }
  catch (e) {
    createError.value = e instanceof Error ? e.message : 'Could not upload the kernel'
  }
  finally {
    creating.value = false
  }
}

const toDelete = ref<KernelRow | null>(null)
const deleting = ref(false)

async function confirmDelete() {
  if (!toDelete.value) return
  deleting.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/kernels/${toDelete.value.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toDelete.value = null
    await load()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not withdraw the kernel'
  }
  finally {
    deleting.value = false
  }
}
</script>

<template>
  <AdminShell>

    <div class="mt-8 flex flex-wrap items-end justify-between gap-4">
      <div>
        <p class="eyebrow mb-2 text-primary-text">// admin · kernels</p>
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">Kernels</h1>
        <p class="mt-2 max-w-2xl text-sm text-muted-foreground">
          Kernel images this installation can boot from. Once uploaded, neither an entry's name nor
          its file can be changed by anyone; only its description can be edited.
        </p>
      </div>
      <Dialog v-model:open="createOpen" @update:open="(v: boolean) => !v && resetForm()">
        <Button class="font-mono text-xs" @click="createOpen = true">
          <Plus class="size-4" aria-hidden="true" />
          New kernel
        </Button>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Upload kernel</DialogTitle>
            <DialogDescription>
              The name and the file are permanent — a mistake in either is fixed by uploading another
              kernel and withdrawing this one. The description can be edited later.
            </DialogDescription>
          </DialogHeader>
          <form class="space-y-4" :aria-busy="creating" @submit.prevent="create">
            <div class="space-y-2">
              <Label for="k-name">Name</Label>
              <Input
                id="k-name"
                v-model="form.name"
                required
                maxlength="128"
                placeholder="vmlinuz-6.6.10"
                autocomplete="off"
                spellcheck="false"
              />
            </div>
            <div class="space-y-2">
              <Label for="k-desc">Description <span class="text-muted-foreground">(optional)</span></Label>
              <Textarea id="k-desc" v-model="form.description" rows="3" placeholder="what this kernel is for" />
            </div>
            <div class="space-y-2">
              <Label for="k-file">Kernel image</Label>
              <Input id="k-file" :key="fileKey" type="file" required aria-describedby="k-file-hint" @change="onFile" />
              <p id="k-file-hint" class="text-xs text-muted-foreground">
                {{ file ? `${file.name} · ${fmtBytes(file.size)}` : 'Stored in object storage; large uploads take a while.' }}
              </p>
            </div>

            <FormError id="create-kernel-error" :message="createError" />

            <DialogFooter>
              <DialogClose as-child>
                <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
              </DialogClose>
              <Button type="submit" class="font-mono text-xs" :disabled="creating || !form.name.trim() || !file">
                {{ creating ? 'Uploading…' : 'Upload' }}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load kernels</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <Alert v-if="actionError" variant="destructive" class="mt-4">
      <AlertTitle>Action failed</AlertTitle>
      <AlertDescription>{{ actionError }}</AlertDescription>
    </Alert>

    <DataTable
      label="Kernels"
      :columns="columns"
      :loading="loading"
      :loading-rows="3"
      loading-label="Loading kernels…"
      :empty="!items.length"
      class="mt-6"
    >
      <template #empty>
        No kernels yet.
      </template>
      <TableRow v-for="k in items" :key="k.id">
        <TableCell class="font-mono">
          <NuxtLink
            :to="`/admin/model/kernels/${k.id}`"
            class="text-primary-text underline decoration-primary-text/40 underline-offset-4 transition-colors hover:decoration-primary-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
          >
            {{ k.name }}
          </NuxtLink>
        </TableCell>
        <TableCell class="font-mono text-xs break-all text-muted-foreground">{{ k.file_name }}</TableCell>
        <TableCell class="font-mono text-muted-foreground">{{ fmtBytes(k.size_bytes) }}</TableCell>
        <TableCell class="max-w-xs truncate text-muted-foreground" :title="k.description">
          {{ k.description || '—' }}
        </TableCell>
        <TableCell class="text-muted-foreground">{{ fmtDate(k.created_at) }}</TableCell>
        <TableCell class="text-right">
          <Button
            variant="ghost"
            size="icon"
            class="text-destructive hover:text-destructive"
            :aria-label="`Withdraw kernel ${k.name}`"
            @click="toDelete = k"
          >
            <Trash2 class="size-4" aria-hidden="true" />
          </Button>
        </TableCell>
      </TableRow>
    </DataTable>

    <nav aria-label="Kernels pagination" class="mt-4 flex flex-wrap items-center justify-between gap-3 font-mono text-xs text-muted-foreground">
      <span>{{ total }} total · page {{ page }} / {{ pageCount }}</span>
      <div class="flex gap-2">
        <Button
          variant="outline"
          size="sm"
          class="font-mono text-xs"
          :disabled="offset <= 0 || loading"
          aria-label="Previous page of kernels"
          @click="offset = Math.max(0, offset - limit)"
        >
          Prev
        </Button>
        <Button
          variant="outline"
          size="sm"
          class="font-mono text-xs"
          :disabled="offset + limit >= total || loading"
          aria-label="Next page of kernels"
          @click="offset += limit"
        >
          Next
        </Button>
      </div>
    </nav>

    <Dialog :open="!!toDelete" @update:open="(v: boolean) => { if (!v) toDelete = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Withdraw kernel</DialogTitle>
          <DialogDescription>
            <span class="font-mono text-foreground">{{ toDelete?.name }}</span>
            stops being listed and stops being downloadable. The record and the uploaded file are
            both kept, and the name cannot be used again.
          </DialogDescription>
        </DialogHeader>
        <FormError id="delete-kernel-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="deleting" @click="confirmDelete">
            {{ deleting ? 'Withdrawing…' : 'Withdraw' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </AdminShell>
</template>
