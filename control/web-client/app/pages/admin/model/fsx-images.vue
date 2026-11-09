<script setup lang="ts">
import { Trash2 } from '@lucide/vue'
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

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · fsx images' })

const columns: DataTableColumn[] = [
  { key: 'rootfs', label: 'Image' },
  { key: 'status', label: 'Status' },
  { key: 'volume', label: 'Volume' },
  { key: 'disks', label: 'Disks' },
  { key: 'updated', label: 'Updated' },
  { key: 'actions', label: 'Actions', align: 'right' },
]

interface ImageRow {
  id: string
  rootfs: string
  osimage_id?: string
  osimage_name?: string
  size_bytes: number
  volume_id?: string
  volume_path?: string
  snapshot_id?: string
  status: 'building' | 'ready'
  claimed: boolean
  disk_count: number
  created_at: string
  updated_at: string
}

const { authFetch } = useAuth()

const items = ref<ImageRow[]>([])
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

function fmtDate(s?: string) {
  if (!s) return '—'
  const d = new Date(s)
  return Number.isNaN(d.getTime()) ? s : d.toLocaleString()
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await authFetch('/admin/fsx-images')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    items.value = (await res.json()).items ?? []
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load fsx images'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

const toDelete = ref<ImageRow | null>(null)
const deleting = ref(false)

async function confirmDelete() {
  if (!toDelete.value) return
  deleting.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/fsx-images/${toDelete.value.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toDelete.value = null
    await load()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not delete the image'
  }
  finally {
    deleting.value = false
  }
}
</script>

<template>
  <AdminShell>
    <div class="mt-8">
      <p class="eyebrow mb-2 text-primary-text">// admin · fsx images</p>
      <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">FSx images</h1>
      <p class="mt-2 max-w-2xl text-sm text-muted-foreground">
        Golden volumes on FSx for OpenZFS, one per built rootfs. Every FSx disk is cloned from one
        of these, so an image can only be deleted once no disk uses it.
      </p>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load this page</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <DataTable
      label="FSx images"
      :columns="columns"
      :loading="loading"
      loading-label="Loading fsx images…"
      :empty="!items.length"
      class="mt-8"
    >
      <template #empty>
        No FSx image has been built.
      </template>
      <TableRow v-for="i in items" :key="i.id">
        <TableCell>
          <div class="font-mono break-all">{{ i.rootfs.slice(0, 12) }}</div>
          <div class="text-xs text-muted-foreground">
            {{ i.osimage_name || 'os image gone' }} · {{ fmtBytes(i.size_bytes) }}
          </div>
        </TableCell>
        <TableCell>
          <Badge :variant="i.status === 'ready' ? 'default' : 'secondary'" class="font-mono">
            {{ i.status }}
          </Badge>
          <div v-if="i.claimed" class="text-xs text-muted-foreground">claimed by a task</div>
        </TableCell>
        <TableCell>
          <div class="font-mono">{{ i.volume_id || '—' }}</div>
          <div v-if="i.snapshot_id" class="font-mono text-xs text-muted-foreground">{{ i.snapshot_id }}</div>
        </TableCell>
        <TableCell class="font-mono">{{ i.disk_count }}</TableCell>
        <TableCell class="text-muted-foreground">{{ fmtDate(i.updated_at) }}</TableCell>
        <TableCell class="text-right">
          <Button
            variant="ghost"
            size="icon"
            class="text-destructive hover:text-destructive"
            :disabled="i.disk_count > 0"
            :aria-label="i.disk_count > 0
              ? `Cannot delete ${i.rootfs}: disks are cloned from it`
              : `Delete the fsx image ${i.rootfs}`"
            @click="toDelete = i"
          >
            <Trash2 class="size-4" aria-hidden="true" />
          </Button>
        </TableCell>
      </TableRow>
    </DataTable>

    <Dialog :open="!!toDelete" @update:open="(v: boolean) => { if (!v) toDelete = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete FSx image</DialogTitle>
          <DialogDescription>
            Deletes the volume
            <span class="font-mono text-foreground">{{ toDelete?.volume_id || '(none yet)' }}</span>
            and its golden snapshot. The next FSx VM from this OS image builds it again. This cannot
            be undone.
          </DialogDescription>
        </DialogHeader>
        <FormError id="delete-image-error" :message="actionError" />
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
