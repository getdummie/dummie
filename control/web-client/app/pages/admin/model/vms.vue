<script setup lang="ts">
import { Plus, RefreshCw, Trash2 } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
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
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · vms' })

interface VMRow {
  id: string
  agent_id: string
  vm_id: string
  name: string
  status: 'pending' | 'running' | 'stopped' | 'failed' | 'gone'
  boot: string
  cpus: number
  memory_mib: number
  ip: string
  last_error: string
  created_at: string
  started_at: string
}

const { authFetch } = useAuth()

const items = ref<VMRow[]>([])
const total = ref(0)
const limit = ref(20)
const offset = ref(0)
const loading = ref(true)
const error = ref<string | null>(null)

interface AgentOption {
  id: string
  hostname: string
  machine_id: string
  connected: boolean
  status: string
}

// A VM row carries only its agent's id. Rather than widen the API with a join,
// the agent list is fetched alongside and resolved here -- an admin fleet is
// small enough that one extra page of agents is cheaper than a new endpoint.
// The same list is the create form's host picker.
const agents = ref<AgentOption[]>([])
const hostnames = computed<Record<string, string>>(() =>
  Object.fromEntries(agents.value.filter(a => a.hostname).map(a => [a.id, a.hostname])))

// Only a connected agent can be sent a job -- the server rejects the rest with a
// 409 -- so an offline host is shown but not selectable.
const targetable = computed(() => agents.value.filter(a => a.connected && a.status !== 'revoked'))

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
  if (!s) return '—'
  const d = new Date(s)
  return Number.isNaN(d.getTime()) ? s : d.toLocaleString()
}

function fmtMemory(mib: number) {
  if (!mib) return '—'
  return mib >= 1024 ? `${(mib / 1024).toFixed(mib % 1024 ? 1 : 0)} GiB` : `${mib} MiB`
}

function agentLabel(id: string) {
  return hostnames.value[id] || `${id.slice(0, 8)}…`
}

const statusVariant: Record<VMRow['status'], 'default' | 'secondary' | 'outline' | 'destructive'> = {
  running: 'default',
  pending: 'secondary',
  stopped: 'secondary',
  // 'gone' is not an error the way a failed create is -- the VM was removed on
  // its host, which is usually deliberate -- so it reads as muted, not alarming.
  gone: 'outline',
  failed: 'destructive',
}

// silent skips the skeletons so the background poll doesn't make the table flash.
async function load(silent = false) {
  if (!silent) loading.value = true
  error.value = null
  try {
    const res = await authFetch(`/admin/vms?limit=${limit.value}&offset=${offset.value}`)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = await res.json()
    items.value = data.items ?? []
    total.value = data.total ?? 0
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load vms'
  }
  finally {
    loading.value = false
  }
}

// Best-effort: an unresolved hostname falls back to a short id, so a failure
// here must not surface as an error on a table that otherwise loaded fine.
async function loadAgents() {
  try {
    const res = await authFetch('/admin/agents?limit=100')
    if (!res.ok) return
    const data = await res.json()
    agents.value = (data.items ?? []).map((a: AgentOption) => ({
      id: a.id,
      hostname: a.hostname,
      machine_id: a.machine_id,
      connected: a.connected,
      status: a.status,
    }))
  }
  catch {
    // keep whatever we already resolved
  }
}

// A pending row becomes running or failed without any action from this page, so
// it has to poll to stay honest. A minute is a long time to watch a create you
// just started, which is what the refresh button is for.
const pollInterval = 60_000

let poll: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  load()
  loadAgents()
  poll = setInterval(() => load(true), pollInterval)
})
onUnmounted(() => clearInterval(poll))

// syncing drives the button's own spinner. It is separate from `loading` so a
// manual refresh spins the icon without also blanking the table into skeletons.
const syncing = ref(false)

// The timer is restarted so a manual refresh doesn't leave a scheduled poll
// firing a moment later.
async function syncNow() {
  if (syncing.value) return
  syncing.value = true
  clearInterval(poll)
  try {
    await Promise.all([load(true), loadAgents()])
  }
  finally {
    poll = setInterval(() => load(true), pollInterval)
    syncing.value = false
  }
}

const page = computed(() => Math.floor(offset.value / limit.value) + 1)
const pageCount = computed(() => Math.max(1, Math.ceil(total.value / limit.value)))
function next() {
  if (offset.value + limit.value < total.value) {
    offset.value += limit.value
    load()
  }
}
function prev() {
  if (offset.value > 0) {
    offset.value = Math.max(0, offset.value - limit.value)
    load()
  }
}

// --- create ---
//
// The form mirrors the two boot modes dagent actually has, which take disjoint
// inputs. Rather than accept everything and let the agent reject the wrong
// combination minutes later, only the fields belonging to the chosen mode are
// rendered — the shape of the form is the validation.
const createOpen = ref(false)
const creating = ref(false)
const createError = ref<string | null>(null)

const blankForm = {
  agent_id: '',
  name: '',
  boot: 'direct',
  // 'image' is a ready-made ext4 rootfs; 'tar' is a `docker export` the agent
  // builds one from. Direct boot needs exactly one of them.
  source: 'image',
  kernel: '',
  kernel_sha256: '',
  initrd: '',
  initrd_sha256: '',
  rootfs: '',
  rootfs_sha256: '',
  rootfs_tar: '',
  rootfs_tar_sha256: '',
  rootfs_size: '',
  append: '',
  disk: '',
  disk_sha256: '',
  firmware: '',
  disk_size: '',
  cpus: '1',
  memory_mib: '1024',
  no_network: false,
  ip: '',
  egress: '',
  egress_any: false,
  rate_mbit: '',
  burst_kbit: '',
}
const form = reactive({ ...blankForm })

function openCreate() {
  Object.assign(form, blankForm)
  createError.value = null
  // Preselect the only sensible default; with several hosts the choice is real
  // and is left to the operator.
  form.agent_id = targetable.value.length === 1 ? targetable.value[0]!.id : ''
  createOpen.value = true
  // A host that enrolled since the last poll should be pickable now.
  loadAgents()
}

// Mirrors the checks in the agent's createVM so a mistake is caught here rather
// than becoming a failed row a minute later.
function validate(): string | null {
  if (!form.agent_id) return 'Choose a host to run this VM on.'
  const cpus = Number(form.cpus)
  const memory = Number(form.memory_mib)
  if (!Number.isInteger(cpus) || cpus < 1) return 'cpus must be a whole number of at least 1.'
  if (!Number.isInteger(memory) || memory < 64) return 'memory must be at least 64 MiB.'
  if (form.boot === 'direct') {
    if (!form.kernel.trim()) return 'Direct boot needs a kernel.'
    if (form.source === 'image' && !form.rootfs.trim()) return 'Direct boot needs a root filesystem image.'
    if (form.source === 'tar' && !form.rootfs_tar.trim()) return 'Direct boot needs a root filesystem tar.'
  }
  else if (!form.disk.trim()) {
    return 'Disk boot needs a bootable disk image.'
  }
  return null
}

// Every field on the wire is omitempty, so an empty one is simply left out --
// sending "" would otherwise override an agent-side default with nothing.
function buildSpec() {
  const spec: Record<string, unknown> = { boot: form.boot }
  const put = (key: string, value: string) => {
    const v = value.trim()
    if (v) spec[key] = v
  }
  const putNum = (key: string, value: string) => {
    const v = Number(value)
    if (value.trim() && Number.isFinite(v) && v > 0) spec[key] = v
  }

  put('name', form.name)
  spec.cpus = Number(form.cpus)
  spec.memory_mib = Number(form.memory_mib)
  put('disk_size', form.disk_size)

  if (form.boot === 'direct') {
    put('kernel', form.kernel)
    put('kernel_sha256', form.kernel_sha256)
    put('initrd', form.initrd)
    put('initrd_sha256', form.initrd_sha256)
    put('append', form.append)
    if (form.source === 'image') {
      put('rootfs', form.rootfs)
      put('rootfs_sha256', form.rootfs_sha256)
    }
    else {
      put('rootfs_tar', form.rootfs_tar)
      put('rootfs_tar_sha256', form.rootfs_tar_sha256)
      put('rootfs_size', form.rootfs_size)
    }
  }
  else {
    put('disk', form.disk)
    put('disk_sha256', form.disk_sha256)
    put('firmware', form.firmware)
  }

  if (form.no_network) {
    spec.no_network = true
  }
  else {
    put('ip', form.ip)
    if (form.egress_any) spec.egress_any = true
    const egress = form.egress.split(/[\s,]+/).map(s => s.trim()).filter(Boolean)
    if (egress.length) spec.egress = egress
    putNum('rate_mbit', form.rate_mbit)
    putNum('burst_kbit', form.burst_kbit)
  }
  return spec
}

async function create() {
  const problem = validate()
  if (problem) {
    createError.value = problem
    return
  }
  creating.value = true
  createError.value = null
  try {
    const res = await authFetch(`/admin/agents/${form.agent_id}/vms`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(buildSpec()),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    createOpen.value = false
    // The row lands as 'pending' and settles when the agent reports back, so the
    // newest page is where the operator wants to be looking.
    offset.value = 0
    await load()
  }
  catch (e) {
    createError.value = e instanceof Error ? e.message : 'Could not create VM'
  }
  finally {
    creating.value = false
  }
}

// --- delete ---
const toDelete = ref<VMRow | null>(null)
const deleting = ref(false)
const actionError = ref<string | null>(null)

async function confirmDelete() {
  if (!toDelete.value) return
  deleting.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/vms/${toDelete.value.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toDelete.value = null
    await load()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not delete vm record'
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
        <p class="eyebrow mb-2 text-primary-text">// admin · vms</p>
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">VMs</h1>
      </div>
      <div class="flex shrink-0 items-center gap-3">
        <span class="hidden font-mono text-xs text-muted-foreground sm:inline">refreshes every 1m</span>
        <Button
          variant="outline"
          size="sm"
          class="font-mono text-xs"
          :disabled="syncing || loading"
          @click="syncNow"
        >
          <RefreshCw class="size-3.5" :class="syncing && 'animate-spin'" aria-hidden="true" />
          {{ syncing ? 'Syncing…' : 'Sync now' }}
        </Button>

        <Dialog v-model:open="createOpen">
          <Button class="font-mono text-xs" @click="openCreate">
            <Plus class="size-4" aria-hidden="true" />
            New VM
          </Button>
          <!-- Scrolling content: the direct-boot form is taller than a short
               viewport, and a modal that clips its own submit button is unusable. -->
          <DialogScrollContent class="sm:max-w-xl">
            <DialogHeader>
              <DialogTitle>New VM</DialogTitle>
              <DialogDescription>
                Pushed to a connected agent, which resolves the images and boots it.
                That takes minutes — the row appears as <span class="font-mono">pending</span>
                and settles when the host reports back.
              </DialogDescription>
            </DialogHeader>

            <form class="space-y-4" :aria-busy="creating" @submit.prevent="create">
              <div class="space-y-2">
                <Label for="vm-agent">Host</Label>
                <div class="*:w-full">
                  <NativeSelect id="vm-agent" v-model="form.agent_id">
                    <NativeSelectOption value="" disabled>Choose a host…</NativeSelectOption>
                    <NativeSelectOption v-for="a in targetable" :key="a.id" :value="a.id">
                      {{ a.hostname || a.machine_id }}
                    </NativeSelectOption>
                  </NativeSelect>
                </div>
                <p v-if="!targetable.length" class="text-xs text-muted-foreground">
                  No agent is connected. A VM can only be pushed to a live host.
                </p>
              </div>

              <div class="grid gap-4 sm:grid-cols-2">
                <div class="space-y-2">
                  <Label for="vm-name">Name</Label>
                  <Input id="vm-name" v-model="form.name" placeholder="defaults to the generated id" />
                </div>
                <div class="space-y-2">
                  <Label for="vm-boot">Boot mode</Label>
                  <div class="*:w-full">
                    <NativeSelect id="vm-boot" v-model="form.boot">
                      <NativeSelectOption value="direct">direct — microVM, kernel + rootfs</NativeSelectOption>
                      <NativeSelectOption value="disk">disk — full image via firmware</NativeSelectOption>
                    </NativeSelect>
                  </div>
                </div>
              </div>

              <div class="grid gap-4 sm:grid-cols-3">
                <div class="space-y-2">
                  <Label for="vm-cpus">vCPUs</Label>
                  <Input id="vm-cpus" v-model="form.cpus" type="number" min="1" inputmode="numeric" />
                </div>
                <div class="space-y-2">
                  <Label for="vm-mem">Memory (MiB)</Label>
                  <Input id="vm-mem" v-model="form.memory_mib" type="number" min="64" inputmode="numeric" />
                </div>
                <div class="space-y-2">
                  <Label for="vm-disksize">Disk size</Label>
                  <Input id="vm-disksize" v-model="form.disk_size" placeholder="e.g. 10G" />
                </div>
              </div>

              <!-- direct boot -->
              <div v-if="form.boot === 'direct'" class="space-y-4 rounded-lg border border-border p-4">
                <div class="space-y-2">
                  <Label for="vm-kernel">Kernel</Label>
                  <Input id="vm-kernel" v-model="form.kernel" placeholder="URL or path on the host" />
                </div>
                <div class="space-y-2">
                  <Label for="vm-source">Root filesystem</Label>
                  <div class="*:w-full">
                    <NativeSelect id="vm-source" v-model="form.source">
                      <NativeSelectOption value="image">ext4 image, ready to boot</NativeSelectOption>
                      <NativeSelectOption value="tar">tar from `docker export`, built on the host</NativeSelectOption>
                    </NativeSelect>
                  </div>
                </div>
                <div v-if="form.source === 'image'" class="space-y-2">
                  <Label for="vm-rootfs">Root filesystem image</Label>
                  <Input id="vm-rootfs" v-model="form.rootfs" placeholder="URL or path" />
                </div>
                <template v-else>
                  <div class="space-y-2">
                    <Label for="vm-tar">Root filesystem tar</Label>
                    <Input id="vm-tar" v-model="form.rootfs_tar" placeholder="URL or path" />
                  </div>
                  <div class="space-y-2">
                    <Label for="vm-rootfs-size">Built image size</Label>
                    <Input id="vm-rootfs-size" v-model="form.rootfs_size" placeholder="e.g. 2G — default 1.5× the tar" />
                  </div>
                </template>
              </div>

              <!-- disk boot -->
              <div v-else class="space-y-4 rounded-lg border border-border p-4">
                <div class="space-y-2">
                  <Label for="vm-disk">Disk image</Label>
                  <Input id="vm-disk" v-model="form.disk" placeholder="bootable image URL or path" />
                </div>
                <div class="space-y-2">
                  <Label for="vm-firmware">Firmware</Label>
                  <Input id="vm-firmware" v-model="form.firmware" placeholder="path to -bios firmware; default is qemu's own" />
                </div>
              </div>

              <!-- network -->
              <div class="space-y-4 rounded-lg border border-border p-4">
                <div class="flex items-center gap-2">
                  <Checkbox id="vm-nonet" v-model="form.no_network" />
                  <Label for="vm-nonet" class="font-normal">No network device at all</Label>
                </div>
                <template v-if="!form.no_network">
                  <div class="space-y-2">
                    <Label for="vm-ip">Address</Label>
                    <Input id="vm-ip" v-model="form.ip" placeholder="pin an address; default is the lowest free one" />
                  </div>
                  <div class="space-y-2">
                    <Label for="vm-egress">Egress allowlist</Label>
                    <Input id="vm-egress" v-model="form.egress" placeholder="1.1.1.1, 10.0.0.0/8 — empty denies all outbound" />
                    <p class="text-xs text-muted-foreground">
                      Addresses or CIDRs, comma or space separated. Outbound is deny-by-default.
                    </p>
                  </div>
                  <div class="flex items-center gap-2">
                    <Checkbox id="vm-egress-any" v-model="form.egress_any" />
                    <Label for="vm-egress-any" class="font-normal">Allow any destination in the kernel</Label>
                  </div>
                </template>
              </div>

              <!-- Everything below is a refinement of a working VM, so it starts
                   folded rather than making the common case look complicated. -->
              <details class="rounded-lg border border-border">
                <summary class="cursor-pointer px-4 py-3 text-sm font-medium">Advanced</summary>
                <div class="space-y-4 border-t border-border p-4">
                  <template v-if="form.boot === 'direct'">
                    <div class="space-y-2">
                      <Label for="vm-kernel-sha">Kernel sha256</Label>
                      <Input id="vm-kernel-sha" v-model="form.kernel_sha256" class="font-mono" />
                    </div>
                    <div class="space-y-2">
                      <Label for="vm-initrd">Initramfs</Label>
                      <Input id="vm-initrd" v-model="form.initrd" placeholder="URL or path; optional" />
                    </div>
                    <div class="space-y-2">
                      <Label for="vm-initrd-sha">Initramfs sha256</Label>
                      <Input id="vm-initrd-sha" v-model="form.initrd_sha256" class="font-mono" />
                    </div>
                    <div class="space-y-2">
                      <Label :for="form.source === 'image' ? 'vm-rootfs-sha' : 'vm-tar-sha'">
                        {{ form.source === 'image' ? 'Root filesystem sha256' : 'Tar sha256' }}
                      </Label>
                      <Input
                        v-if="form.source === 'image'"
                        id="vm-rootfs-sha"
                        v-model="form.rootfs_sha256"
                        class="font-mono"
                      />
                      <Input v-else id="vm-tar-sha" v-model="form.rootfs_tar_sha256" class="font-mono" />
                    </div>
                    <div class="space-y-2">
                      <Label for="vm-append">Kernel command line</Label>
                      <Input id="vm-append" v-model="form.append" class="font-mono" placeholder="default: console + root=/dev/vda" />
                    </div>
                  </template>
                  <div v-else class="space-y-2">
                    <Label for="vm-disk-sha">Disk sha256</Label>
                    <Input id="vm-disk-sha" v-model="form.disk_sha256" class="font-mono" />
                  </div>

                  <template v-if="!form.no_network">
                    <div class="space-y-2">
                      <Label for="vm-rate">Rate limit (Mbit/s)</Label>
                      <Input id="vm-rate" v-model="form.rate_mbit" type="number" min="0" inputmode="numeric" placeholder="0 = unlimited" />
                    </div>
                    <div class="space-y-2">
                      <Label for="vm-burst">Burst (Kbit)</Label>
                      <Input id="vm-burst" v-model="form.burst_kbit" type="number" min="0" inputmode="numeric" placeholder="default: a tenth of a second at the rate" />
                    </div>
                  </template>
                </div>
              </details>

              <FormError id="create-vm-error" :message="createError" />

              <DialogFooter>
                <DialogClose as-child>
                  <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
                </DialogClose>
                <Button type="submit" class="font-mono text-xs" :disabled="creating || !targetable.length">
                  {{ creating ? 'Creating…' : 'Create' }}
                </Button>
              </DialogFooter>
            </form>
          </DialogScrollContent>
        </Dialog>
      </div>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load VMs</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <!-- Self-refreshing, so `aria-live="polite"` lets a screen-reader user hear a
         pending VM turn into a running one without re-reading the table. -->
    <div class="mt-6 overflow-x-auto rounded-lg border border-border" aria-live="polite" :aria-busy="loading">
      <p v-if="loading" class="sr-only">Loading VMs…</p>
      <Table label="VMs">
        <TableHeader>
          <TableRow>
            <TableHead>VM</TableHead>
            <TableHead>Host</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Boot</TableHead>
            <TableHead>Resources</TableHead>
            <TableHead>Address</TableHead>
            <TableHead>Created</TableHead>
            <TableHead class="text-right">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <template v-if="loading">
            <TableRow v-for="n in 5" :key="n" aria-hidden="true">
              <TableCell v-for="c in 8" :key="c"><Skeleton class="h-4 w-full" /></TableCell>
            </TableRow>
          </template>
          <TableEmpty v-else-if="!items.length" :colspan="8">
            No VMs yet. Connected agents report what they are running every 30s, so
            anything made with <span class="font-mono">dagent vm create</span> appears
            here on its own. To ask an agent for one, post to
            <span class="font-mono">/api/v1/admin/agents/&lt;id&gt;/vms</span>.
          </TableEmpty>
          <TableRow v-for="v in items" v-else :key="v.id">
            <TableCell>
              <div class="font-mono">{{ v.name || '—' }}</div>
              <div class="truncate font-mono text-xs text-muted-foreground">{{ v.vm_id || 'not assigned yet' }}</div>
            </TableCell>
            <TableCell class="font-mono text-muted-foreground">{{ agentLabel(v.agent_id) }}</TableCell>
            <TableCell>
              <Badge :variant="statusVariant[v.status]" class="font-mono">{{ v.status }}</Badge>
              <!-- The reason a create failed is the only thing anyone wants from
                   a failed row, so it sits with the status rather than behind a
                   click. title= keeps the full text reachable when truncated. -->
              <div
                v-if="v.status === 'failed' && v.last_error"
                class="mt-1 max-w-56 truncate text-xs text-destructive"
                :title="v.last_error"
              >
                {{ v.last_error }}
              </div>
            </TableCell>
            <TableCell class="font-mono text-muted-foreground">{{ v.boot || '—' }}</TableCell>
            <TableCell class="font-mono text-muted-foreground whitespace-nowrap">
              {{ v.cpus || '—' }}<span v-if="v.cpus"> vcpu</span> · {{ fmtMemory(v.memory_mib) }}
            </TableCell>
            <TableCell class="font-mono text-muted-foreground">{{ v.ip || '—' }}</TableCell>
            <TableCell class="text-muted-foreground whitespace-nowrap">{{ fmtDate(v.created_at) }}</TableCell>
            <TableCell class="text-right">
              <Button
                variant="ghost"
                size="icon"
                class="text-destructive hover:text-destructive"
                :aria-label="`Delete the record for VM ${v.name || v.vm_id || v.id}`"
                @click="toDelete = v"
              >
                <Trash2 class="size-4" aria-hidden="true" />
              </Button>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>

    <nav aria-label="VMs pagination" class="mt-4 flex flex-wrap items-center justify-between gap-3 font-mono text-xs text-muted-foreground">
      <span>{{ total }} total · page {{ page }} / {{ pageCount }}</span>
      <div class="flex gap-2">
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset <= 0 || loading" aria-label="Previous page of VMs" @click="prev">Prev</Button>
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset + limit >= total || loading" aria-label="Next page of VMs" @click="next">Next</Button>
      </div>
    </nav>

    <!-- delete confirm -->
    <Dialog :open="!!toDelete" @update:open="(v: boolean) => { if (!v) toDelete = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete VM record</DialogTitle>
          <DialogDescription>
            Remove
            <span class="font-mono text-foreground">{{ toDelete?.name || toDelete?.vm_id || toDelete?.id }}</span>
            from the registry. This forgets the record only — the guest keeps running
            on its host, and while it does, the host's next inventory report will
            add it straight back. To remove it for real, run
            <span class="font-mono">dagent vm rm</span> on the host.
          </DialogDescription>
        </DialogHeader>
        <FormError id="delete-vm-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="deleting" @click="confirmDelete">
            {{ deleting ? 'Deleting…' : 'Delete record' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
