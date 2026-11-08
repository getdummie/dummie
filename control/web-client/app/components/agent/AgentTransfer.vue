<script setup lang="ts">
import { ArrowUp, Download, File as FileIcon, Folder, RefreshCw, Upload } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { AgentError, type AgentDirEntry } from '@/composables/useAgentSocket'

const props = defineProps<{
  initialPath: string
  connected: boolean
  list: (path: string) => Promise<{ path: string, entries: AgentDirEntry[] }>
  upload: (file: File, onProgress?: (fraction: number) => void, opts?: { dest?: string, overwrite?: boolean }) => Promise<string>
  readRaw: (cwd: string, path: string, type: string, onProgress?: (fraction: number) => void, download?: boolean) => Promise<Blob>
}>()

interface Transfer {
  id: number
  name: string
  kind: 'upload' | 'download'
  progress: number
  state: 'queued' | 'running' | 'done' | 'failed'
  message?: string
}

const { authFetch } = useAuth()
const maxUploadMiB = ref(100)
const pathInput = ref('')
const dir = ref('')
const entries = ref<AgentDirEntry[]>([])
const loading = ref(false)
const listError = ref<string | null>(null)
const overwrite = ref(false)
const dragging = ref(false)
const transfers = ref<Transfer[]>([])
const fileInput = useTemplateRef<HTMLInputElement>('fileInput')
let nextId = 0
let queue = Promise.resolve()

const sorted = computed(() => [...entries.value].sort((a, b) =>
  a.dir === b.dir ? a.name.localeCompare(b.name) : a.dir ? -1 : 1))

const tooOld = (e: unknown) => e instanceof Error && e.message.includes('unknown message')
const upgradeHint = 'The dinit in this VM is too old for this. Run `dinit upgrade` in the console, then reconnect.'

async function loadLimit() {
  const res = await authFetch('/vms/features').catch(() => null)
  if (res?.ok) maxUploadMiB.value = Number((await res.json()).max_upload_mib) || maxUploadMiB.value
}

async function open(path: string) {
  if (!path.trim() || !props.connected) return
  loading.value = true
  listError.value = null
  try {
    const res = await props.list(path.trim())
    dir.value = res.path
    pathInput.value = res.path
    entries.value = res.entries
  }
  catch (e) {
    listError.value = tooOld(e) ? upgradeHint : e instanceof Error ? e.message : 'Could not list the directory'
  }
  finally {
    loading.value = false
  }
}

function up() {
  if (!dir.value || dir.value === '/') return
  void open(dir.value.replace(/\/[^/]+\/?$/, '') || '/')
}

const join = (d: string, name: string) => `${d.replace(/\/$/, '')}/${name}`

function track(t: Omit<Transfer, 'id' | 'progress' | 'state'>) {
  transfers.value.unshift({ ...t, id: ++nextId, progress: 0, state: 'queued' })
  return transfers.value[0]!
}

function enqueue(files: FileList | File[] | null | undefined) {
  if (!files?.length || !dir.value) return
  const dest = dir.value
  const limit = maxUploadMiB.value * 1024 * 1024
  for (const file of Array.from(files)) {
    const t = track({ name: file.name, kind: 'upload' })
    if (file.size > limit) {
      Object.assign(t, { state: 'failed', message: `over the ${maxUploadMiB.value} MiB limit` })
      continue
    }
    const force = overwrite.value
    queue = queue.then(() => runUpload(t, file, dest, force))
  }
}

async function runUpload(t: Transfer, file: File, dest: string, force: boolean) {
  t.state = 'running'
  try {
    const path = await props.upload(file, (f) => { t.progress = f }, { dest, overwrite: force })
    t.state = 'done'
    if (!path.startsWith(`${dest.replace(/\/$/, '')}/`)) t.message = `landed in ${path}; ${upgradeHint}`
    if (dir.value === dest) await open(dest)
  }
  catch (e) {
    t.state = 'failed'
    t.message = e instanceof AgentError && e.code === 'exists'
      ? 'already exists; tick "Replace existing files" to overwrite it'
      : e instanceof Error ? e.message : 'upload failed'
  }
}

async function download(entry: AgentDirEntry) {
  const t = track({ name: entry.name, kind: 'download' })
  t.state = 'running'
  try {
    const blob = await props.readRaw(dir.value, entry.name, 'application/octet-stream', (f) => { t.progress = f }, true)
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = entry.name
    a.click()
    URL.revokeObjectURL(url)
    t.state = 'done'
  }
  catch (e) {
    t.state = 'failed'
    t.message = e instanceof Error ? e.message : 'download failed'
  }
}

function onPick(ev: Event) {
  const input = ev.target as HTMLInputElement
  enqueue(input.files)
  input.value = ''
}

function onDragLeave(ev: DragEvent) {
  if (!(ev.currentTarget as Node).contains(ev.relatedTarget as Node | null)) dragging.value = false
}

function onDrop(ev: DragEvent) {
  dragging.value = false
  enqueue(ev.dataTransfer?.files)
}

function fmtBytes(n: number) {
  if (n < 1024) return `${n} B`
  const units = ['KiB', 'MiB', 'GiB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`
}

watch(() => props.connected, (ok) => {
  if (ok) void open(dir.value || props.initialPath)
}, { immediate: true })
watch(() => props.initialPath, (p) => {
  if (!dir.value && p) void open(p)
})
onMounted(loadLimit)
</script>

<template>
  <div
    class="relative flex min-h-0 flex-1 flex-col"
    @dragover.prevent="dragging = true"
    @dragleave="onDragLeave"
    @drop.prevent="onDrop"
  >
    <form class="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2" @submit.prevent="open(pathInput)">
      <Button type="button" variant="ghost" size="icon" class="size-7 shrink-0" aria-label="parent directory" :disabled="!dir || dir === '/'" @click="up">
        <ArrowUp class="size-4" aria-hidden="true" />
      </Button>
      <Label for="transfer-path" class="sr-only">Directory</Label>
      <Input id="transfer-path" v-model="pathInput" class="h-7 min-w-0 flex-1 font-mono text-xs" spellcheck="false" placeholder="~/app" />
      <Button type="button" variant="ghost" size="icon" class="size-7 shrink-0" aria-label="refresh" :disabled="loading || !connected" @click="open(dir || pathInput)">
        <RefreshCw class="size-4" :class="loading && 'animate-spin'" aria-hidden="true" />
      </Button>
      <Button type="button" size="sm" class="h-7 shrink-0 font-mono text-xs" :disabled="!dir || !connected" @click="fileInput?.click()">
        <Upload class="size-3.5" aria-hidden="true" />
        Upload
      </Button>
      <input ref="fileInput" type="file" multiple class="hidden" @change="onPick">
    </form>

    <div class="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-border px-3 py-1.5 font-mono text-xs text-muted-foreground">
      <div class="flex items-center gap-2">
        <Checkbox id="transfer-overwrite" :model-value="overwrite" @update:model-value="(v) => overwrite = v === true" />
        <Label for="transfer-overwrite" class="font-mono text-xs font-normal">Replace existing files</Label>
      </div>
      <span>Drop files anywhere here · up to {{ maxUploadMiB }} MiB each</span>
    </div>

    <p v-if="!connected" class="p-3 font-mono text-xs text-muted-foreground">Waiting for the connection to the VM…</p>
    <p v-else-if="listError" class="p-3 font-mono text-xs text-destructive" role="alert">{{ listError }}</p>

    <ul v-else class="min-h-0 flex-1 overflow-y-auto py-1" aria-label="directory contents">
      <li v-if="!loading && !sorted.length" class="px-3 py-2 font-mono text-xs text-muted-foreground">Empty directory.</li>
      <li v-for="e in sorted" :key="e.name" class="group flex items-center gap-2 px-3 py-1 font-mono text-xs hover:bg-muted/50">
        <button
          v-if="e.dir"
          type="button"
          class="flex min-w-0 flex-1 items-center gap-2 text-left focus-visible:outline-2 focus-visible:outline-ring"
          @click="open(join(dir, e.name))"
        >
          <Folder class="size-3.5 shrink-0 text-primary-text" aria-hidden="true" />
          <span class="truncate">{{ e.name }}/</span>
        </button>
        <template v-else>
          <FileIcon class="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
          <span class="min-w-0 flex-1 truncate" :title="e.name">{{ e.name }}</span>
          <span class="shrink-0 text-muted-foreground">{{ fmtBytes(e.size) }}</span>
          <Button variant="ghost" size="icon" class="size-6 shrink-0" :aria-label="`download ${e.name}`" @click="download(e)">
            <Download class="size-3.5" aria-hidden="true" />
          </Button>
        </template>
      </li>
    </ul>

    <section v-if="transfers.length" class="max-h-48 shrink-0 overflow-y-auto border-t border-border px-3 py-2" aria-label="transfers">
      <div class="mb-1 flex items-center justify-between">
        <p class="eyebrow text-muted-foreground">transfers</p>
        <Button variant="ghost" size="sm" class="h-6 px-2 font-mono text-xs" @click="transfers = transfers.filter(t => t.state === 'queued' || t.state === 'running')">
          Clear finished
        </Button>
      </div>
      <ul class="space-y-1" aria-live="polite">
        <li v-for="t in transfers" :key="t.id" class="font-mono text-xs">
          <div class="flex items-center gap-2">
            <component :is="t.kind === 'upload' ? Upload : Download" class="size-3 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span class="min-w-0 flex-1 truncate">{{ t.name }}</span>
            <span :class="t.state === 'failed' ? 'text-destructive' : 'text-muted-foreground'">
              {{ t.state === 'running' ? `${Math.round(t.progress * 100)}%` : t.state }}
            </span>
          </div>
          <p v-if="t.message" class="pl-5 break-all" :class="t.state === 'failed' ? 'text-destructive' : 'text-muted-foreground'">{{ t.message }}</p>
        </li>
      </ul>
    </section>

    <div
      v-if="dragging"
      class="pointer-events-none absolute inset-0 flex items-center justify-center border-2 border-dashed border-primary bg-background/80 font-mono text-sm"
    >
      Drop to upload into {{ dir }}
    </div>
  </div>
</template>
