<script setup lang="ts">
import { Plus, Trash2 } from '@lucide/vue'
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
import { TableCell, TableRow } from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import type { DataTableColumn } from '@/lib/table'

const columns: DataTableColumn[] = [
  { key: 'name', label: 'Name' },
  { key: 'source', label: 'Source' },
  { key: 'status', label: 'Status' },
  { key: 'size', label: 'Size' },
  { key: 'description', label: 'Description' },
  { key: 'created', label: 'Added' },
  { key: 'actions', label: 'Actions', align: 'right' },
]

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · os images' })

interface OSImageRow {
  id: string
  name: string
  description: string
  file_name: string
  size_bytes: number
  created_at: string
  soft_deleted_at: string
  source: string
  oci_ref: string
  status: string
  status_detail: string
}

const { authFetch } = useAuth()

const items = ref<OSImageRow[]>([])
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
    const res = await authFetch(`/admin/osimages?limit=${limit.value}&offset=${offset.value}`)
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    const data = await res.json()
    items.value = data.items ?? []
    total.value = data.total ?? 0
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load OS images'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)
watch(offset, load)

// A queued image settles on its own in the background, so the table keeps looking
// until nothing is in flight rather than making an admin reload the page.
const settling = computed(() => items.value.some(o => o.status === 'pending' || o.status === 'building'))
let poll: ReturnType<typeof setInterval> | null = null
watch(settling, (on) => {
  if (on && !poll) poll = setInterval(load, 5000)
  else if (!on && poll) {
    clearInterval(poll)
    poll = null
  }
}, { immediate: true })
onUnmounted(() => poll && clearInterval(poll))

type BadgeVariant = 'default' | 'secondary' | 'outline' | 'destructive'

function statusVariant(s: string): BadgeVariant {
  switch (s) {
    case 'ready': return 'default'
    case 'building': return 'secondary'
    case 'failed': return 'destructive'
    default: return 'outline'
  }
}

const page = computed(() => Math.floor(offset.value / limit.value) + 1)
const pageCount = computed(() => Math.max(1, Math.ceil(total.value / limit.value)))

const createOpen = ref(false)
const creating = ref(false)
const createError = ref<string | null>(null)
const form = reactive({ name: '', description: '', ociRef: '' })
const source = ref<'oci' | 'upload'>('oci')
const file = ref<File | null>(null)
const fileKey = ref(0)

function onFile(e: Event) {
  file.value = (e.target as HTMLInputElement).files?.[0] ?? null
}

function resetForm() {
  form.name = ''
  form.description = ''
  form.ociRef = ''
  source.value = 'oci'
  file.value = null
  fileKey.value++
  createError.value = null
}

const canCreate = computed(() =>
  !!form.name.trim() && (source.value === 'oci' ? !!form.ociRef.trim() : !!file.value))

async function create() {
  if (!canCreate.value) return
  creating.value = true
  createError.value = null
  try {
    const init: RequestInit = source.value === 'oci'
      ? {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            name: form.name.trim(),
            description: form.description.trim(),
            oci_ref: form.ociRef.trim(),
          }),
        }
      : (() => {
          const body = new FormData()
          body.set('name', form.name.trim())
          body.set('description', form.description.trim())
          body.set('file', file.value as File)
          return { method: 'POST', body }
        })()
    const res = await authFetch('/admin/osimages', init)
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    createOpen.value = false
    resetForm()
    offset.value = 0
    await load()
  }
  catch (e) {
    createError.value = e instanceof Error ? e.message : 'Could not create the OS image'
  }
  finally {
    creating.value = false
  }
}

const toDelete = ref<OSImageRow | null>(null)
const deleting = ref(false)

async function confirmDelete() {
  if (!toDelete.value) return
  deleting.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/osimages/${toDelete.value.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toDelete.value = null
    await load()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not withdraw the OS image'
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
        <p class="eyebrow mb-2 text-primary-text">// admin · os images</p>
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">OS images</h1>
        <p class="mt-2 max-w-2xl text-sm text-muted-foreground">
          Operating system images a guest can be built from — either a container image this server
          pulls and flattens for you, or a root filesystem tar you upload. Once an entry is built,
          neither its name nor its file can be changed by anyone; only the description and the
          default port can be edited.
        </p>
      </div>
      <Dialog v-model:open="createOpen" @update:open="(v: boolean) => !v && resetForm()">
        <Button class="font-mono text-xs" @click="createOpen = true">
          <Plus class="size-4" aria-hidden="true" />
          New OS image
        </Button>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>New OS image</DialogTitle>
            <DialogDescription>
              The name and the image itself are permanent — a mistake in either is fixed by adding
              another image and withdrawing this one. The description can be edited later.
            </DialogDescription>
          </DialogHeader>
          <form class="space-y-4" :aria-busy="creating" @submit.prevent="create">
            <div class="space-y-2">
              <Label for="oi-name">Name</Label>
              <Input
                id="oi-name"
                v-model="form.name"
                required
                maxlength="128"
                placeholder="marimo"
                autocomplete="off"
                spellcheck="false"
              />
            </div>

            <Tabs v-model="source">
              <TabsList class="w-full">
                <TabsTrigger value="oci" class="font-mono text-xs">Container image</TabsTrigger>
                <TabsTrigger value="upload" class="font-mono text-xs">Upload tar</TabsTrigger>
              </TabsList>
              <TabsContent value="oci" class="mt-4 space-y-2">
                <Label for="oi-ref">Image reference</Label>
                <Input
                  id="oi-ref"
                  v-model="form.ociRef"
                  maxlength="512"
                  placeholder="ghcr.io/marimo-team/marimo:latest-sql"
                  autocomplete="off"
                  spellcheck="false"
                  aria-describedby="oi-ref-hint"
                />
                <p id="oi-ref-hint" class="text-xs text-muted-foreground">
                  Pulled and flattened into a root filesystem in the background. The tag is resolved
                  to a digest, so the stored image never changes under you.
                </p>
              </TabsContent>
              <TabsContent value="upload" class="mt-4 space-y-2">
                <Label for="oi-file">Image file</Label>
                <Input id="oi-file" :key="fileKey" type="file" aria-describedby="oi-file-hint" @change="onFile" />
                <p id="oi-file-hint" class="text-xs text-muted-foreground">
                  {{ file ? `${file.name} · ${fmtBytes(file.size)}` : 'Stored in object storage; large uploads take a while.' }}
                </p>
              </TabsContent>
            </Tabs>

            <div class="space-y-2">
              <Label for="oi-desc">Description <span class="text-muted-foreground">(optional)</span></Label>
              <Textarea id="oi-desc" v-model="form.description" rows="3" placeholder="what this image is for" />
            </div>

            <FormError id="create-osimage-error" :message="createError" />

            <DialogFooter>
              <DialogClose as-child>
                <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
              </DialogClose>
              <Button type="submit" class="font-mono text-xs" :disabled="creating || !canCreate">
                {{ creating ? 'Saving…' : source === 'oci' ? 'Add' : 'Upload' }}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load OS images</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <Alert v-if="actionError" variant="destructive" class="mt-4">
      <AlertTitle>Action failed</AlertTitle>
      <AlertDescription>{{ actionError }}</AlertDescription>
    </Alert>

    <DataTable
      label="OS images"
      :columns="columns"
      :loading="loading"
      :loading-rows="3"
      loading-label="Loading OS images…"
      :empty="!items.length"
      class="mt-6"
    >
      <template #empty>
        No OS images yet.
      </template>
      <TableRow v-for="o in items" :key="o.id">
        <TableCell class="font-mono">
          <NuxtLink
            :to="`/admin/model/osimages/${o.id}`"
            class="text-primary-text underline decoration-primary-text/40 underline-offset-4 transition-colors hover:decoration-primary-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
          >
            {{ o.name }}
          </NuxtLink>
        </TableCell>
        <TableCell class="max-w-xs font-mono text-xs break-all text-muted-foreground">
          {{ o.source === 'oci' ? o.oci_ref : o.file_name }}
        </TableCell>
        <TableCell>
          <Badge :variant="statusVariant(o.status)" class="font-mono text-xs" :title="o.status_detail">
            {{ o.status }}
          </Badge>
        </TableCell>
        <TableCell class="font-mono text-muted-foreground">{{ fmtBytes(o.size_bytes) }}</TableCell>
        <TableCell class="max-w-xs truncate text-muted-foreground" :title="o.description">
          {{ o.description || '—' }}
        </TableCell>
        <TableCell class="text-muted-foreground">{{ fmtDate(o.created_at) }}</TableCell>
        <TableCell class="text-right">
          <Button
            variant="ghost"
            size="icon"
            class="text-destructive hover:text-destructive"
            :aria-label="`Withdraw OS image ${o.name}`"
            @click="toDelete = o"
          >
            <Trash2 class="size-4" aria-hidden="true" />
          </Button>
        </TableCell>
      </TableRow>
    </DataTable>

    <nav aria-label="OS images pagination" class="mt-4 flex flex-wrap items-center justify-between gap-3 font-mono text-xs text-muted-foreground">
      <span>{{ total }} total · page {{ page }} / {{ pageCount }}</span>
      <div class="flex gap-2">
        <Button
          variant="outline"
          size="sm"
          class="font-mono text-xs"
          :disabled="offset <= 0 || loading"
          aria-label="Previous page of OS images"
          @click="offset = Math.max(0, offset - limit)"
        >
          Prev
        </Button>
        <Button
          variant="outline"
          size="sm"
          class="font-mono text-xs"
          :disabled="offset + limit >= total || loading"
          aria-label="Next page of OS images"
          @click="offset += limit"
        >
          Next
        </Button>
      </div>
    </nav>

    <Dialog :open="!!toDelete" @update:open="(v: boolean) => { if (!v) toDelete = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Withdraw OS image</DialogTitle>
          <DialogDescription>
            <span class="font-mono text-foreground">{{ toDelete?.name }}</span>
            stops being listed and stops being downloadable. The record and the uploaded file are
            both kept, and the name cannot be used again.
          </DialogDescription>
        </DialogHeader>
        <FormError id="delete-osimage-error" :message="actionError" />
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
