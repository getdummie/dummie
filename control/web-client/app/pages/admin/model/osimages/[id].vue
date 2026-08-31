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
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'

definePageMeta({ middleware: ['auth', 'admin'] })

interface OSImageConfig {
  user: string
  entrypoint: string[]
  cmd: string[]
  env: string[]
  exposed_ports: number[]
}

interface OSImage {
  id: string
  name: string
  description: string
  file_name: string
  size_bytes: number
  created_at: string
  soft_deleted_at: string
  download_url?: string
  source: string
  oci_ref: string
  oci_digest: string
  status: string
  status_detail: string
  default_port: number
  config: OSImageConfig
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
const ready = computed(() => image.value?.status === 'ready')
const settling = computed(() => image.value?.status === 'pending' || image.value?.status === 'building')

type BadgeVariant = 'default' | 'secondary' | 'outline' | 'destructive'

function statusVariant(s: string): BadgeVariant {
  switch (s) {
    case 'ready': return 'default'
    case 'building': return 'secondary'
    case 'failed': return 'destructive'
    default: return 'outline'
  }
}

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

// The build settles in the background; keep looking until it has.
let poll: ReturnType<typeof setInterval> | null = null
watch(settling, (on) => {
  if (on && !poll) poll = setInterval(load, 5000)
  else if (!on && poll) {
    clearInterval(poll)
    poll = null
  }
})
onUnmounted(() => poll && clearInterval(poll))

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

const descOpen = ref(false)
const savingDesc = ref(false)
const descError = ref<string | null>(null)
const descDraft = ref('')
const portDraft = ref('')

function openDesc() {
  descDraft.value = image.value?.description ?? ''
  portDraft.value = image.value?.default_port ? String(image.value.default_port) : ''
  descError.value = null
  descOpen.value = true
}

async function saveDesc() {
  const port = portDraft.value.trim() ? Number(portDraft.value) : null
  if (port !== null && (!Number.isInteger(port) || port < 1 || port > 65535)) {
    descError.value = 'The default port must be a whole number between 1 and 65535.'
    return
  }
  savingDesc.value = true
  descError.value = null
  try {
    const res = await authFetch(`/admin/osimages/${id.value}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ description: descDraft.value, default_port: port }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
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

const configOpen = ref(false)
const savingConfig = ref(false)
const configError = ref<string | null>(null)
const configDraft = reactive({ user: '', entrypoint: '', cmd: '', env: '', exposed_ports: '' })

function openConfig() {
  const c = image.value?.config
  configDraft.user = c?.user ?? ''
  configDraft.entrypoint = (c?.entrypoint ?? []).join('\n')
  configDraft.cmd = (c?.cmd ?? []).join('\n')
  configDraft.env = (c?.env ?? []).join('\n')
  configDraft.exposed_ports = (c?.exposed_ports ?? []).join(', ')
  configError.value = null
  configOpen.value = true
}

function lines(s: string): string[] {
  return s.split('\n').map(l => l.trim()).filter(l => l !== '')
}

async function saveConfig() {
  const user = configDraft.user.trim()
  if (user !== '' && !/^[^\s:]+(:[^\s:]+)?$/.test(user)) {
    configError.value = 'The user must be a name or uid, optionally followed by one colon and a group.'
    return
  }
  const ports: number[] = []
  for (const p of configDraft.exposed_ports.split(',').map(s => s.trim()).filter(s => s !== '')) {
    const n = Number(p)
    if (!Number.isInteger(n) || n < 1 || n > 65535) {
      configError.value = `"${p}" is not a port between 1 and 65535.`
      return
    }
    ports.push(n)
  }
  const env = lines(configDraft.env)
  const bad = env.find(e => !/^[^\s=]+=/.test(e))
  if (bad) {
    configError.value = `Every environment line must be KEY=VALUE: "${bad}"`
    return
  }

  savingConfig.value = true
  configError.value = null
  try {
    const res = await authFetch(`/admin/osimages/${id.value}/config`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        user,
        entrypoint: lines(configDraft.entrypoint),
        cmd: lines(configDraft.cmd),
        env,
        exposed_ports: ports,
      }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    image.value = await res.json()
    configOpen.value = false
  }
  catch (e) {
    configError.value = e instanceof Error ? e.message : 'Could not save the image configuration'
  }
  finally {
    savingConfig.value = false
  }
}

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
  <AdminShell>

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
            <Badge :variant="statusVariant(image.status)" class="font-mono">{{ image.status }}</Badge>
          </div>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <Button
            v-if="!withdrawn && ready"
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

      <Alert v-if="image.status === 'failed'" variant="destructive" class="mt-4">
        <AlertTitle>This image could not be built</AlertTitle>
        <AlertDescription>{{ image.status_detail || 'No reason was recorded.' }}</AlertDescription>
      </Alert>

      <Alert v-else-if="settling" class="mt-4">
        <AlertTitle>Building</AlertTitle>
        <AlertDescription>
          {{ image.oci_ref }} is being pulled and flattened into a root filesystem. This page updates
          itself when it is done.
        </AlertDescription>
      </Alert>

      <p class="mt-6 flex items-start gap-2 text-sm text-muted-foreground">
        <Lock class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
        <span>
          The name, the file and the container image it came from are permanent: none of them can be
          changed by anyone, including an admin, so a correction to any of them means adding a new
          image and withdrawing this one. Everything else — the description, the default port and the
          image configuration — can be edited.
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
            <dd class="mt-1 font-mono text-sm break-all">{{ image.file_name || '—' }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Size</dt>
            <dd class="mt-1 font-mono text-sm">{{ fmtBytes(image.size_bytes) }}</dd>
          </div>
          <div v-if="image.source === 'oci'" class="sm:col-span-2">
            <dt class="eyebrow text-muted-foreground">Container image</dt>
            <dd class="mt-1 font-mono text-sm break-all">{{ image.oci_ref }}</dd>
          </div>
          <div v-if="image.oci_digest" class="sm:col-span-2 lg:col-span-3">
            <dt class="eyebrow text-muted-foreground">Digest</dt>
            <dd class="mt-1 font-mono text-xs break-all text-muted-foreground">{{ image.oci_digest }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Default port</dt>
            <dd class="mt-1 font-mono text-sm">{{ image.default_port || '—' }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Added</dt>
            <dd class="mt-1 text-sm text-muted-foreground">{{ fmtDate(image.created_at) }}</dd>
          </div>
          <div v-if="withdrawn">
            <dt class="eyebrow text-muted-foreground">Withdrawn</dt>
            <dd class="mt-1 text-sm text-muted-foreground">{{ fmtDate(image.soft_deleted_at) }}</dd>
          </div>
          <div class="sm:col-span-2 lg:col-span-3">
            <dt class="eyebrow flex items-center gap-2 text-muted-foreground">
              Description
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

      <section
        v-if="image.source === 'oci' && ready"
        aria-labelledby="config-heading"
        class="mt-4 rounded-lg border border-border p-4 sm:p-6"
      >
        <div class="flex items-start justify-between gap-4">
          <h2 id="config-heading" class="text-sm font-semibold">Image configuration</h2>
          <Button
            v-if="!withdrawn"
            variant="outline"
            size="sm"
            class="font-mono text-xs"
            @click="openConfig"
          >
            <Pencil class="size-3.5" aria-hidden="true" />
            Edit
          </Button>
        </div>
        <p class="mt-1 max-w-2xl text-sm text-muted-foreground">
          What the container image itself declares. The root filesystem alone does not carry any of
          this, so it is recorded here when the image is built — and it is what the guest init runs
          the VM's workload with. A change applies to VMs created from this image afterwards; VMs
          that already exist keep the configuration they were built with.
        </p>
        <dl class="mt-4 grid gap-x-8 gap-y-4 sm:grid-cols-2">
          <div>
            <dt class="eyebrow text-muted-foreground">User</dt>
            <dd class="mt-1 font-mono text-sm break-all">{{ image.config.user || 'root' }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Exposed ports</dt>
            <dd class="mt-1 font-mono text-sm">
              {{ image.config.exposed_ports.length ? image.config.exposed_ports.join(', ') : '—' }}
            </dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Entrypoint</dt>
            <dd class="mt-1 font-mono text-sm break-all">
              {{ image.config.entrypoint.length ? image.config.entrypoint.join(' ') : '—' }}
            </dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Command</dt>
            <dd class="mt-1 font-mono text-sm break-all">
              {{ image.config.cmd.length ? image.config.cmd.join(' ') : '—' }}
            </dd>
          </div>
          <div class="sm:col-span-2">
            <dt class="eyebrow text-muted-foreground">Environment</dt>
            <dd v-if="image.config.env.length" class="mt-1 overflow-x-auto">
              <ul class="font-mono text-xs">
                <li v-for="e in image.config.env" :key="e" class="py-0.5 break-all">{{ e }}</li>
              </ul>
            </dd>
            <dd v-else class="mt-1 font-mono text-sm">—</dd>
          </div>
        </dl>
      </section>
    </template>

    <Dialog v-model:open="descOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Edit OS image</DialogTitle>
          <DialogDescription>
            A note about <span class="font-mono text-foreground">{{ image?.name }}</span>, and the
            port a VM built from it should default to. The name and the image itself stay as they
            were.
          </DialogDescription>
        </DialogHeader>
        <form class="space-y-4" :aria-busy="savingDesc" @submit.prevent="saveDesc">
          <div class="space-y-2">
            <Label for="oi-desc">Description</Label>
            <Textarea id="oi-desc" v-model="descDraft" rows="4" placeholder="what this image is for" />
          </div>
          <div class="space-y-2">
            <Label for="oi-port">Default port <span class="text-muted-foreground">(optional)</span></Label>
            <Input
              id="oi-port"
              v-model="portDraft"
              inputmode="numeric"
              placeholder="8080"
              autocomplete="off"
              aria-describedby="oi-port-hint"
            />
            <p id="oi-port-hint" class="text-xs text-muted-foreground">
              Offered as the default port of a VM created from this image. Taken from the container
              image's exposed ports when it was built.
            </p>
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

    <Dialog v-model:open="configOpen">
      <DialogContent class="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Edit image configuration</DialogTitle>
          <DialogDescription>
            What the guest init runs a VM's workload as, and with. This starts as what
            <span class="font-mono text-foreground">{{ image?.oci_ref }}</span> declared; editing it
            makes it diverge from the container image, which is the point of it being editable.
          </DialogDescription>
        </DialogHeader>
        <form class="space-y-4" :aria-busy="savingConfig" @submit.prevent="saveConfig">
          <div class="grid gap-4 sm:grid-cols-2">
            <div class="space-y-2">
              <Label for="oi-user">User</Label>
              <Input
                id="oi-user"
                v-model="configDraft.user"
                placeholder="root"
                autocomplete="off"
                spellcheck="false"
                class="font-mono text-sm"
                aria-describedby="oi-user-hint"
              />
              <p id="oi-user-hint" class="text-xs text-muted-foreground">
                A name, a uid, or either with a group after a colon. Empty means root.
              </p>
            </div>
            <div class="space-y-2">
              <Label for="oi-ports">Exposed ports</Label>
              <Input
                id="oi-ports"
                v-model="configDraft.exposed_ports"
                inputmode="numeric"
                placeholder="8080, 9000"
                autocomplete="off"
                class="font-mono text-sm"
                aria-describedby="oi-ports-hint"
              />
              <p id="oi-ports-hint" class="text-xs text-muted-foreground">
                Comma-separated. A record of what the image listens on; it does not itself publish
                anything.
              </p>
            </div>
          </div>

          <div class="space-y-2">
            <Label for="oi-entrypoint">Entrypoint</Label>
            <Textarea
              id="oi-entrypoint"
              v-model="configDraft.entrypoint"
              rows="2"
              spellcheck="false"
              class="font-mono text-sm"
              placeholder="/usr/bin/app"
              aria-describedby="oi-argv-hint"
            />
          </div>
          <div class="space-y-2">
            <Label for="oi-cmd">Command</Label>
            <Textarea
              id="oi-cmd"
              v-model="configDraft.cmd"
              rows="3"
              spellcheck="false"
              class="font-mono text-sm"
              placeholder="--listen 0.0.0.0"
              aria-describedby="oi-argv-hint"
            />
            <p id="oi-argv-hint" class="text-xs text-muted-foreground">
              One argument per line, not a shell command line: the entrypoint is run with the command
              as its arguments, and nothing splits on spaces or expands a variable. To use a shell,
              make it the entrypoint —
              <span class="font-mono">sh</span>, then <span class="font-mono">-c</span>, then the
              script as one argument.
            </p>
          </div>

          <div class="space-y-2">
            <Label for="oi-env">Environment</Label>
            <Textarea
              id="oi-env"
              v-model="configDraft.env"
              rows="8"
              spellcheck="false"
              class="font-mono text-xs"
              placeholder="PORT=8080"
              aria-describedby="oi-env-hint"
            />
            <p id="oi-env-hint" class="text-xs text-muted-foreground">
              One <span class="font-mono">KEY=VALUE</span> per line. These reach the workload, the
              serial console and every SSH session. A
              <span class="font-mono">PATH</span> here replaces the guest init's own.
            </p>
          </div>

          <FormError id="osimage-config-error" :message="configError" />

          <DialogFooter>
            <DialogClose as-child>
              <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
            </DialogClose>
            <Button type="submit" class="font-mono text-xs" :disabled="savingConfig">
              {{ savingConfig ? 'Saving…' : 'Save' }}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

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
  </AdminShell>
</template>
