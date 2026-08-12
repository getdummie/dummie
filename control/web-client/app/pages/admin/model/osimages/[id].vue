<script setup lang="ts">
import { ArrowLeft, Download, Lock, Pencil, Trash2 } from '@lucide/vue'
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
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'

definePageMeta({ middleware: ['auth', 'admin'] })

interface OSImage {
  id: string
  name: string
  description: string
  file_name: string
  size_bytes: number
  created_at: string
  // "" while the image is offered; when it was withdrawn otherwise.
  soft_deleted_at: string
  // Short-lived, minted for this response. Absent on a withdrawn image and
  // when the server could not reach object storage.
  download_url?: string
}

const route = useRoute()
const { authFetch } = useAuth()
const id = computed(() => String(route.params.id))

const image = ref<OSImage | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)
const actionError = ref<string | null>(null)

useHead(() => ({
  title: image.value ? `dummie — admin · os image · ${image.value.name}` : 'dummie — admin · os image',
}))

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

// 11th–13th are the exception the mod-10 rule gets wrong.
function ordinal(n: number) {
  if (n % 100 >= 11 && n % 100 <= 13) return `${n}th`
  return `${n}${['th', 'st', 'nd', 'rd'][n % 10] ?? 'th'}`
}

function fmtDate(s: string) {
  if (!s) return '—'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  const date = `${ordinal(d.getDate())} ${d.toLocaleString(undefined, { month: 'long' })} ${d.getFullYear()}`
  return `${date}, ${d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })}`
}

const withdrawn = computed(() => !!image.value?.soft_deleted_at)

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await authFetch(`/admin/osimages/${id.value}`)
    if (res.status === 404) throw new Error('This OS image does not exist.')
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    image.value = await res.json()
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load this OS image'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

// The link the server hands back expires within minutes, so the page fetches a
// fresh one at the moment of the click rather than handing out the stale one it
// loaded with.
const downloading = ref(false)

async function download() {
  downloading.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/osimages/${id.value}`)
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    const fresh: OSImage = await res.json()
    image.value = fresh
    if (!fresh.download_url) throw new Error('No download link is available for this OS image.')
    window.location.assign(fresh.download_url)
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not start the download'
  }
  finally {
    downloading.value = false
  }
}

// --- description ---
//
// The one thing about a saved image that can change: a note about the file, not
// part of what the name means.
const descOpen = ref(false)
const savingDesc = ref(false)
const descError = ref<string | null>(null)
const descDraft = ref('')

function openDesc() {
  descDraft.value = image.value?.description ?? ''
  descError.value = null
  descOpen.value = true
}

async function saveDesc() {
  savingDesc.value = true
  descError.value = null
  try {
    const res = await authFetch(`/admin/osimages/${id.value}/description`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ description: descDraft.value }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    // The response is the saved row, so the page shows what was stored.
    image.value = await res.json()
    descOpen.value = false
  }
  catch (e) {
    descError.value = e instanceof Error ? e.message : 'Could not save the description'
  }
  finally {
    savingDesc.value = false
  }
}

// --- withdraw ---
const deleteOpen = ref(false)
const deleting = ref(false)

async function confirmDelete() {
  deleting.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/osimages/${id.value}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    deleteOpen.value = false
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
  <div class="mx-auto max-w-6xl px-4 py-12 sm:px-6">
    <AdminNav />

    <NuxtLink
      to="/admin/model/osimages"
      class="mt-8 inline-flex items-center gap-1.5 font-mono text-xs text-muted-foreground underline-offset-4 transition-colors hover:text-foreground hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
    >
      <ArrowLeft class="size-3.5" aria-hidden="true" />
      All OS images
    </NuxtLink>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load this OS image</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <div v-else-if="loading" class="mt-4 space-y-6" aria-busy="true">
      <p class="sr-only">Loading this OS image…</p>
      <Skeleton class="h-9 w-64" aria-hidden="true" />
      <Skeleton class="h-56 w-full rounded-lg" aria-hidden="true" />
    </div>

    <template v-else-if="image">
      <div class="mt-4 flex flex-wrap items-end justify-between gap-4">
        <div>
          <p class="eyebrow mb-2 text-primary-text">// admin · os images</p>
          <div class="flex flex-wrap items-center gap-3">
            <h1 class="font-mono text-2xl font-semibold tracking-tight break-all sm:text-3xl">
              {{ image.name }}
            </h1>
            <Badge v-if="withdrawn" variant="outline" class="font-mono">withdrawn</Badge>
          </div>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <Button
            v-if="!withdrawn"
            variant="outline"
            size="sm"
            class="font-mono text-xs"
            :disabled="downloading"
            @click="download"
          >
            <Download class="size-4" aria-hidden="true" />
            {{ downloading ? 'Preparing…' : 'Download' }}
          </Button>
          <Button
            v-if="!withdrawn"
            variant="outline"
            size="sm"
            class="font-mono text-xs text-destructive hover:text-destructive"
            @click="deleteOpen = true"
          >
            <Trash2 class="size-4" aria-hidden="true" />
            Withdraw
          </Button>
        </div>
      </div>

      <Alert v-if="actionError" variant="destructive" class="mt-4">
        <AlertTitle>Action failed</AlertTitle>
        <AlertDescription>{{ actionError }}</AlertDescription>
      </Alert>

      <!-- Said plainly rather than left for someone to discover by looking for
           an edit button that is not there. -->
      <p class="mt-6 flex items-start gap-2 text-sm text-muted-foreground">
        <Lock class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
        <span>
          The name and the file are permanent: neither can be changed by anyone, including an admin,
          so a correction to either means uploading a new image and withdrawing this one. Only the
          description can be edited.
        </span>
      </p>

      <section aria-labelledby="details-heading" class="mt-4 rounded-lg border border-border p-4 sm:p-6">
        <h2 id="details-heading" class="text-sm font-semibold">Details</h2>
        <dl class="mt-4 grid gap-x-8 gap-y-4 sm:grid-cols-2 lg:grid-cols-3">
          <div>
            <dt class="eyebrow text-muted-foreground">Name</dt>
            <dd class="mt-1 font-mono text-sm break-all">{{ image.name }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">File</dt>
            <dd class="mt-1 font-mono text-sm break-all">{{ image.file_name }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Size</dt>
            <dd class="mt-1 font-mono text-sm">{{ fmtBytes(image.size_bytes) }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Uploaded</dt>
            <dd class="mt-1 text-sm text-muted-foreground">{{ fmtDate(image.created_at) }}</dd>
          </div>
          <div v-if="withdrawn">
            <dt class="eyebrow text-muted-foreground">Withdrawn</dt>
            <dd class="mt-1 text-sm text-muted-foreground">{{ fmtDate(image.soft_deleted_at) }}</dd>
          </div>
          <div class="sm:col-span-2 lg:col-span-3">
            <dt class="eyebrow flex items-center gap-2 text-muted-foreground">
              Description
              <!-- Withdrawn rows are closed to every change, this one included:
                   they record what a name used to mean. -->
              <Button
                v-if="!withdrawn"
                variant="ghost"
                size="icon"
                class="size-6"
                aria-label="Edit the description"
                @click="openDesc"
              >
                <Pencil class="size-3.5" aria-hidden="true" />
              </Button>
            </dt>
            <dd class="mt-1 max-w-2xl text-sm whitespace-pre-wrap">
              {{ image.description || '—' }}
            </dd>
          </div>
        </dl>
      </section>
    </template>

    <!-- description -->
    <Dialog v-model:open="descOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Edit description</DialogTitle>
          <DialogDescription>
            A note about <span class="font-mono text-foreground">{{ image?.name }}</span>. The name
            and the file it points at stay as they were uploaded.
          </DialogDescription>
        </DialogHeader>
        <form class="space-y-4" :aria-busy="savingDesc" @submit.prevent="saveDesc">
          <div class="space-y-2">
            <Label for="oi-desc">Description</Label>
            <Textarea id="oi-desc" v-model="descDraft" rows="4" placeholder="what this image is for" />
          </div>

          <FormError id="osimage-desc-error" :message="descError" />

          <DialogFooter>
            <DialogClose as-child>
              <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
            </DialogClose>
            <Button type="submit" class="font-mono text-xs" :disabled="savingDesc">
              {{ savingDesc ? 'Saving…' : 'Save' }}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <!-- withdraw confirm -->
    <Dialog v-model:open="deleteOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Withdraw OS image</DialogTitle>
          <DialogDescription>
            <span class="font-mono text-foreground">{{ image?.name }}</span>
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
  </div>
</template>
