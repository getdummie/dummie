<script setup lang="ts">
import { ArrowLeft, Plus, Trash2 } from '@lucide/vue'
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'

definePageMeta({ middleware: ['auth'] })

interface VM {
  id: string
  agent_id: string
  vm_id: string
  name: string
  status: 'pending' | 'running' | 'stopped' | 'failed' | 'gone'
  boot: string
  cpus: number
  memory_mib: number
  disk_mib: number
  ip: string
  last_error: string
  created_at: string
  started_at: string
  reported_at: string
}

interface Target {
  id: string
  destination: string
  kind: 'domain' | 'ip'
  transport: '' | 'tcp' | 'udp' | 'any'
  ports: string
  note: string
  created_at: string
}

const route = useRoute()
const { authFetch } = useAuth()
const id = computed(() => String(route.params.id))

const vm = ref<VM | null>(null)
const targets = ref<Target[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const actionError = ref<string | null>(null)

useHead(() => ({
  title: vm.value ? `dummie — vm · ${vm.value.name || vm.value.vm_id}` : 'dummie — vm',
}))

type BadgeVariant = 'default' | 'secondary' | 'outline' | 'destructive'
const statusVariant: Record<VM['status'], BadgeVariant> = {
  running: 'default',
  pending: 'secondary',
  stopped: 'secondary',
  gone: 'outline',
  failed: 'destructive',
}

async function readMessage(res: Response): Promise<string | null> {
  try {
    const b = await res.json()
    return typeof b?.message === 'string' ? b.message : null
  }
  catch {
    return null
  }
}

function fmtMiB(mib: number) {
  if (mib < 1024) return `${mib} MiB`
  const gib = mib / 1024
  return Number.isInteger(gib) ? `${gib} GiB` : `${gib.toFixed(1)} GiB`
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

async function load(quiet = false) {
  if (!quiet) loading.value = true
  error.value = null
  try {
    const res = await authFetch(`/vms/${id.value}`)
    if (res.status === 404) throw new Error('This VM does not exist, or is not yours.')
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    vm.value = await res.json()
    await loadTargets()
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load this VM'
  }
  finally {
    loading.value = false
  }
}

async function loadTargets() {
  const res = await authFetch(`/vms/${id.value}/targets`)
  if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
  targets.value = (await res.json()).items ?? []
}
onMounted(() => load())

// --- power ---
const settleTimeoutMs = 60_000
const settling = ref<{ want: VM['status'], until: number } | null>(null)
let timer: ReturnType<typeof setInterval> | null = null

const isRunning = computed(() => {
  if (settling.value) return settling.value.want === 'running'
  return vm.value?.status === 'running'
})
const switchable = computed(() => {
  const v = vm.value
  return !!v?.vm_id && (v.status === 'running' || v.status === 'stopped')
})

watch(vm, (v) => {
  const s = settling.value
  if (s && v && (v.status === s.want || Date.now() >= s.until)) settling.value = null
})

// Poll only while something is in flight; a settled VM does not need a timer.
watch([() => vm.value?.status, settling], () => {
  const busy = vm.value?.status === 'pending' || !!settling.value
  if (busy && !timer) timer = setInterval(() => load(true), 5000)
  else if (!busy && timer) {
    clearInterval(timer)
    timer = null
  }
}, { immediate: true })

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})

async function togglePower(run: boolean) {
  actionError.value = null
  settling.value = { want: run ? 'running' : 'stopped', until: Date.now() + settleTimeoutMs }
  try {
    const res = await authFetch(`/vms/${id.value}/${run ? 'start' : 'stop'}`, { method: 'POST' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    await load(true)
  }
  catch (e) {
    settling.value = null
    actionError.value = e instanceof Error ? e.message : `Could not ${run ? 'start' : 'stop'} the VM`
  }
}

// --- destroy ---
const destroyOpen = ref(false)
const destroying = ref(false)

const destroyable = computed(() => {
  const v = vm.value
  return !!v?.vm_id && v.status !== 'gone'
})

async function confirmDestroy() {
  destroying.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/vms/${id.value}/destroy`, { method: 'POST' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    destroyOpen.value = false
    // Stay put and let the poll show it reach 'gone': navigating away would
    // claim the destroy finished when the host has only just been asked.
    settling.value = { want: 'gone', until: Date.now() + settleTimeoutMs }
    await load(true)
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not destroy the VM'
  }
  finally {
    destroying.value = false
  }
}

// --- destinations ---
const addOpen = ref(false)
const adding = ref(false)
const addError = ref<string | null>(null)
// The kind is chosen, not inferred from what has been typed. Inferring it meant
// transport and ports simply did not exist on an empty form, so there was no way
// to discover that an address entry takes them at all.
const blankTarget = { kind: 'domain', destination: '', transport: 'tcp', ports: '', note: '' }
const form = reactive({ ...blankTarget })

function resetTargetForm() {
  Object.assign(form, blankTarget)
  addError.value = null
}

const destinationPlaceholder = computed(() =>
  form.kind === 'domain' ? 'ifconfig.io' : '1.1.1.1 or 10.0.0.0/8')

// Says what the row will actually compile to. The two kinds differ in a way the
// field labels alone do not explain: a domain is matched by name inside the
// TLS/DNS/HTTP buffers and never by address, so it has no transport or port.
const matchSummary = computed(() =>
  form.kind === 'domain'
    ? 'Matched by name in the DNS query, the TLS SNI and the HTTP host — no transport or port applies.'
    : 'Matched by address in the rule header, with the transport and ports below.')

async function addTarget() {
  adding.value = true
  addError.value = null
  try {
    const res = await authFetch(`/vms/${id.value}/targets`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        kind: form.kind,
        destination: form.destination,
        transport: form.transport,
        ports: form.ports,
        note: form.note,
      }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    addOpen.value = false
    resetTargetForm()
    await loadTargets()
  }
  catch (e) {
    addError.value = e instanceof Error ? e.message : 'Could not add the destination'
  }
  finally {
    adding.value = false
  }
}

const toRemove = ref<Target | null>(null)
const removing = ref(false)

async function confirmRemove() {
  if (!toRemove.value) return
  removing.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/vms/${id.value}/targets/${toRemove.value.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toRemove.value = null
    await loadTargets()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not remove the destination'
  }
  finally {
    removing.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-12 sm:px-6">
    <NuxtLink
      to="/vms"
      class="inline-flex items-center gap-1.5 font-mono text-xs text-muted-foreground underline-offset-4 transition-colors hover:text-foreground hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
    >
      <ArrowLeft class="size-3.5" aria-hidden="true" />
      All VMs
    </NuxtLink>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load this VM</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <div v-else-if="loading" class="mt-4 space-y-6" aria-busy="true">
      <p class="sr-only">Loading this VM…</p>
      <Skeleton class="h-9 w-64" aria-hidden="true" />
      <Skeleton class="h-56 w-full rounded-lg" aria-hidden="true" />
      <Skeleton class="h-64 w-full rounded-lg" aria-hidden="true" />
    </div>

    <template v-else-if="vm">
      <div class="mt-4 flex flex-wrap items-end justify-between gap-4">
        <div>
          <p class="eyebrow mb-2 text-primary-text">// vms</p>
          <div class="flex flex-wrap items-center gap-3">
            <h1 class="font-mono text-2xl font-semibold tracking-tight sm:text-3xl">
              {{ vm.name || vm.vm_id || 'unnamed' }}
            </h1>
            <Badge :variant="statusVariant[vm.status]" class="font-mono">{{ vm.status }}</Badge>
          </div>
        </div>
        <div class="flex flex-wrap items-center gap-4">
          <div class="flex items-center gap-2.5">
            <Label for="vm-power" class="font-mono text-xs text-muted-foreground">Power</Label>
            <Switch
              id="vm-power"
              :model-value="isRunning"
              :disabled="!switchable || !!settling"
              :aria-label="switchable
                ? `${isRunning ? 'Stop' : 'Start'} this VM`
                : `Cannot start or stop this VM: it is ${vm.status}`"
              @update:model-value="togglePower"
            />
          </div>
          <!-- Labelled, not icon-only: this is the one irreversible action on
               the page, and it sits next to a switch that is not. -->
          <Button
            variant="outline"
            size="sm"
            class="font-mono text-xs text-destructive hover:text-destructive"
            :disabled="!destroyable"
            :aria-label="destroyable
              ? 'Destroy this VM'
              : `Cannot destroy this VM: it is ${vm.status}`"
            @click="destroyOpen = true"
          >
            <Trash2 class="size-4" aria-hidden="true" />
            Destroy
          </Button>
        </div>
      </div>

      <Alert v-if="actionError" variant="destructive" class="mt-4">
        <AlertTitle>Action failed</AlertTitle>
        <AlertDescription>{{ actionError }}</AlertDescription>
      </Alert>

      <Alert v-if="vm.status === 'failed' && vm.last_error" variant="destructive" class="mt-4">
        <AlertTitle>This VM failed</AlertTitle>
        <AlertDescription>{{ vm.last_error }}</AlertDescription>
      </Alert>

      <!-- details -->
      <section aria-labelledby="details-heading" class="mt-6 rounded-lg border border-border p-4 sm:p-6">
        <h2 id="details-heading" class="text-sm font-semibold">Details</h2>
        <dl class="mt-4 grid gap-x-8 gap-y-4 sm:grid-cols-2 lg:grid-cols-3">
          <div>
            <dt class="eyebrow text-muted-foreground">Address</dt>
            <dd class="mt-1 font-mono text-sm">{{ vm.ip || '—' }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Size</dt>
            <dd class="mt-1 font-mono text-sm">{{ vm.cpus }} vCPU · {{ fmtMiB(vm.memory_mib) }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Disk</dt>
            <!-- Shown even when unrecorded: '0' would read as a diskless VM,
                 and a blank field as one that has no such property at all. -->
            <dd class="mt-1 font-mono text-sm">
              {{ vm.disk_mib ? fmtMiB(vm.disk_mib) : 'not recorded' }}
            </dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Boot mode</dt>
            <dd class="mt-1 font-mono text-sm">{{ vm.boot || '—' }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Host VM id</dt>
            <dd class="mt-1 font-mono text-sm break-all">{{ vm.vm_id || '—' }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Created</dt>
            <dd class="mt-1 text-sm text-muted-foreground">{{ fmtDate(vm.created_at) }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Started</dt>
            <dd class="mt-1 text-sm text-muted-foreground">{{ fmtDate(vm.started_at) }}</dd>
          </div>
          <div>
            <!-- The status is the host's claim as of this moment, not a live
                 reading, so the age of that claim belongs next to it. -->
            <dt class="eyebrow text-muted-foreground">Last confirmed by host</dt>
            <dd class="mt-1 text-sm text-muted-foreground">{{ fmtDate(vm.reported_at) }}</dd>
          </div>
        </dl>
      </section>

      <!-- destinations -->
      <section aria-labelledby="targets-heading" class="mt-6 rounded-lg border border-border">
        <div class="flex flex-wrap items-start justify-between gap-4 p-4 sm:p-6">
          <div>
            <h2 id="targets-heading" class="text-sm font-semibold">Allowed destinations</h2>
            <p class="mt-1 max-w-2xl text-sm text-muted-foreground">
              Domains and addresses this VM is expected to reach. Recorded only for now —
              nothing enforces this list yet.
            </p>
          </div>
          <Dialog v-model:open="addOpen" @update:open="(v: boolean) => !v && resetTargetForm()">
            <Button size="sm" class="font-mono text-xs" @click="addOpen = true">
              <Plus class="size-4" aria-hidden="true" />
              Add
            </Button>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Add a destination</DialogTitle>
                <DialogDescription>
                  A domain, an IP address, or a CIDR range this VM should be able to reach.
                </DialogDescription>
              </DialogHeader>

              <form class="space-y-4" :aria-busy="adding" @submit.prevent="addTarget">
                <div class="space-y-2">
                  <Label for="t-kind">Type</Label>
                  <Select v-model="form.kind">
                    <SelectTrigger id="t-kind" class="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="domain">Domain</SelectItem>
                      <SelectItem value="ip">IP address or CIDR</SelectItem>
                    </SelectContent>
                  </Select>
                  <!-- Live region: choosing a type adds or removes the fields
                       below, which is otherwise a silent reflow. (WCAG 4.1.3) -->
                  <p id="t-kind-hint" role="status" aria-live="polite" class="text-xs text-muted-foreground">
                    {{ matchSummary }}
                  </p>
                </div>

                <div class="space-y-2">
                  <Label for="t-dest">Destination</Label>
                  <Input
                    id="t-dest"
                    v-model="form.destination"
                    required
                    :placeholder="destinationPlaceholder"
                  />
                </div>

                <!-- Transport and ports exist only for an address. A domain is
                     matched in the TLS/DNS/HTTP buffers, where the rule header
                     is `any any` and there is nowhere to put either. -->
                <div v-if="form.kind === 'ip'" class="grid gap-4 sm:grid-cols-2">
                  <div class="space-y-2">
                    <Label for="t-transport">Transport</Label>
                    <Select v-model="form.transport">
                      <SelectTrigger id="t-transport" class="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="tcp">tcp</SelectItem>
                        <SelectItem value="udp">udp</SelectItem>
                        <SelectItem value="any">any</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                  <div class="space-y-2">
                    <Label for="t-ports">Ports</Label>
                    <Input id="t-ports" v-model="form.ports" placeholder="443, 80,443, 1000:2000" aria-describedby="t-ports-hint" />
                    <p id="t-ports-hint" class="text-xs text-muted-foreground">Empty means any port.</p>
                  </div>
                </div>
                <div class="space-y-2">
                  <Label for="t-note">Note</Label>
                  <Input id="t-note" v-model="form.note" placeholder="why this is needed" />
                </div>

                <FormError id="add-target-error" :message="addError" />

                <DialogFooter>
                  <DialogClose as-child>
                    <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
                  </DialogClose>
                  <Button type="submit" class="font-mono text-xs" :disabled="adding">
                    {{ adding ? 'Adding…' : 'Add' }}
                  </Button>
                </DialogFooter>
              </form>
            </DialogContent>
          </Dialog>
        </div>

        <Table label="Allowed destinations">
          <TableHeader>
            <TableRow>
              <TableHead>Destination</TableHead>
              <TableHead>Matched on</TableHead>
              <TableHead>Transport</TableHead>
              <TableHead>Ports</TableHead>
              <TableHead>Note</TableHead>
              <TableHead class="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableEmpty v-if="!targets.length" :colspan="6">
              No destinations recorded.
            </TableEmpty>
            <TableRow v-for="t in targets" v-else :key="t.id">
              <TableCell class="font-mono break-all">{{ t.destination }}</TableCell>
              <TableCell class="font-mono text-xs text-muted-foreground">
                {{ t.kind === 'domain' ? 'dns · tls sni · http host' : 'address' }}
              </TableCell>
              <!-- An em dash, not 'any': these do not apply to a domain row at
                   all, and 'any' would read as "every transport is allowed". -->
              <TableCell class="font-mono text-muted-foreground">{{ t.transport || '—' }}</TableCell>
              <TableCell class="font-mono text-muted-foreground">
                {{ t.kind === 'domain' ? '—' : (t.ports || 'any') }}
              </TableCell>
              <TableCell class="text-muted-foreground">{{ t.note || '—' }}</TableCell>
              <TableCell class="text-right">
                <Button
                  variant="ghost"
                  size="icon"
                  class="text-destructive hover:text-destructive"
                  :aria-label="`Remove destination ${t.destination}`"
                  @click="toRemove = t"
                >
                  <Trash2 class="size-4" aria-hidden="true" />
                </Button>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </section>
    </template>

    <!-- destroy confirm -->
    <Dialog v-model:open="destroyOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Destroy VM</DialogTitle>
          <DialogDescription>
            <span class="font-mono text-foreground">{{ vm?.name || vm?.vm_id }}</span>
            is shut down and its disk is deleted on the host. This cannot be undone.
            The vCPU, memory and disk it holds are returned to your allowance.
          </DialogDescription>
        </DialogHeader>
        <FormError id="destroy-vm-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="destroying" @click="confirmDestroy">
            {{ destroying ? 'Destroying…' : 'Destroy' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- remove confirm -->
    <Dialog :open="!!toRemove" @update:open="(v: boolean) => { if (!v) toRemove = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Remove destination</DialogTitle>
          <DialogDescription>
            <span class="font-mono text-foreground">{{ toRemove?.destination }}</span>
            is removed from this VM's list.
          </DialogDescription>
        </DialogHeader>
        <FormError id="remove-target-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="removing" @click="confirmRemove">
            {{ removing ? 'Removing…' : 'Remove' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
