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
useHead({ title: 'dummie — admin · fsx disks' })

const columns: DataTableColumn[] = [
  { key: 'volume', label: 'Volume' },
  { key: 'vm', label: 'VM' },
  { key: 'image', label: 'Image' },
  { key: 'created', label: 'Created' },
  { key: 'actions', label: 'Actions', align: 'right' },
]

interface DiskRow {
  id: string
  vm_id?: string
  vm_name?: string
  vm_status?: string
  image_id: string
  image_rootfs: string
  volume_id: string
  volume_path: string
  released: boolean
  in_use: boolean
  created_at: string
}

const { authFetch } = useAuth()

const items = ref<DiskRow[]>([])
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

function fmtDate(s?: string) {
  if (!s) return '—'
  const d = new Date(s)
  return Number.isNaN(d.getTime()) ? s : d.toLocaleString()
}

const inUse = (d: DiskRow) => d.in_use

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await authFetch('/admin/fsx-disks')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    items.value = (await res.json()).items ?? []
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load fsx disks'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

const toDelete = ref<DiskRow | null>(null)
const deleting = ref(false)

async function confirmDelete() {
  if (!toDelete.value) return
  deleting.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/fsx-disks/${toDelete.value.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toDelete.value = null
    await load()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not delete the disk'
  }
  finally {
    deleting.value = false
  }
}
</script>

<template>
  <AdminShell>
    <div class="mt-8">
      <p class="eyebrow mb-2 text-primary-text">// admin · fsx disks</p>
      <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">FSx disks</h1>
      <p class="mt-2 max-w-2xl text-sm text-muted-foreground">
        One FSx for OpenZFS volume per VM, cloned from an FSx image. A disk goes with its VM; the
        ones left behind by a VM that is gone are swept every 10 minutes, or can be deleted here.
      </p>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load this page</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <DataTable
      label="FSx disks"
      :columns="columns"
      :loading="loading"
      loading-label="Loading fsx disks…"
      :empty="!items.length"
      class="mt-8"
    >
      <template #empty>
        No FSx disk exists.
      </template>
      <TableRow v-for="d in items" :key="d.id">
        <TableCell>
          <div class="font-mono">{{ d.volume_id }}</div>
          <div class="font-mono text-xs break-all text-muted-foreground">{{ d.volume_path }}</div>
        </TableCell>
        <TableCell>
          <template v-if="d.vm_id">
            <NuxtLink :to="`/admin/model/vms/${d.vm_id}`" class="font-mono underline-offset-4 hover:underline">
              {{ d.vm_name || d.vm_id.slice(0, 8) }}
            </NuxtLink>
            <div class="text-xs text-muted-foreground">{{ d.vm_status }}</div>
          </template>
          <Badge v-if="!d.vm_id || d.released" variant="outline" class="font-mono">released</Badge>
        </TableCell>
        <TableCell class="font-mono text-muted-foreground">{{ d.image_rootfs ? d.image_rootfs.slice(0, 12) : 'moved from a host' }}</TableCell>
        <TableCell class="text-muted-foreground">{{ fmtDate(d.created_at) }}</TableCell>
        <TableCell class="text-right">
          <Button
            variant="ghost"
            size="icon"
            class="text-destructive hover:text-destructive"
            :disabled="inUse(d)"
            :aria-label="inUse(d)
              ? `Cannot delete ${d.volume_id}: it belongs to ${d.vm_name}`
              : `Delete the fsx disk ${d.volume_id}`"
            @click="toDelete = d"
          >
            <Trash2 class="size-4" aria-hidden="true" />
          </Button>
        </TableCell>
      </TableRow>
    </DataTable>

    <Dialog :open="!!toDelete" @update:open="(v: boolean) => { if (!v) toDelete = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete FSx disk</DialogTitle>
          <DialogDescription>
            Deletes the volume <span class="font-mono text-foreground">{{ toDelete?.volume_id }}</span>
            and everything on it. This cannot be undone.
          </DialogDescription>
        </DialogHeader>
        <FormError id="delete-disk-error" :message="actionError" />
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
