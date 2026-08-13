<script setup lang="ts">
import { ArrowLeft, Check, ChevronDown, ExternalLink, Pencil, Plus, SquareTerminal, Terminal, Trash2 } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useLocalStorage } from '@vueuse/core'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
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
import { TableCell, TableRow } from '@/components/ui/table'
import type { DataTableColumn } from '@/lib/table'

definePageMeta({ middleware: ['auth'] })

interface VM {
  id: string
  agent_id: string
  vm_id: string
  // Unique across the fleet, and the key its http route is published under.
  // Generated when the create request leaves it empty.
  name: string
  default_port: number
  public_ports: number[]
  status: 'pending' | 'running' | 'stopped' | 'failed' | 'gone'
  boot: string
  cpus: number
  memory_mib: number
  disk_mib: number
  ip: string
  // Where this VM answers http: its name under the domain of the host it runs
  // on. "" when that host has no domain, so there is no name to route on.
  url: string
  // The websocket endpoint the browser terminal talks to. "" under the same
  // condition that leaves url empty: the host it runs on has no domain.
  console_url: string
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

const targetColumns: DataTableColumn[] = [
  { key: 'destination', label: 'Destination' },
  { key: 'matched', label: 'Matched on' },
  { key: 'transport', label: 'Transport' },
  { key: 'ports', label: 'Ports' },
  { key: 'note', label: 'Note' },
  { key: 'actions', label: 'Actions', align: 'right' },
]

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

// --- ports ---
//
// Editable because what a VM serves changes; the name and the address are not,
// so this dialog is the whole of what an owner can change about routing.
const portsOpen = ref(false)
const savingPorts = ref(false)
const portsError = ref<string | null>(null)
const portsForm = reactive({ default_port: '8000', public_ports: '' })

// The hostname the server published this VM under -- name plus the domain of
// its host. Taken from the url rather than built here because the domain half
// is not on the VM row, and "" when the host has no domain at all.
const sshHost = computed(() => {
  if (!vm.value?.url) return ''
  try {
    return new URL(vm.value.url).hostname
  }
  catch {
    return ''
  }
})
const sshCommand = computed(() => `ssh ${sshHost.value}`)
const sshCopied = ref(false)

// Where an editor lands inside the guest. Every image this fleet builds runs as
// the same account (the control server's proxyRemoteUser), which is what the
// generated proxy config sends an ssh session to.
const remoteHome = '/home/ubuntu'

// Every editor reaches the VM the same way the SSH button does -- by hostname,
// over the host's proxy, authorised by the key the user already has registered
// -- so none of them needs a username or a port here. What differs is only the
// url each one registers with the OS.
//
// The schemes are not interchangeable. The VS Code forks change the scheme but
// keep the `vscode-remote` authority, so `cursor://cursor-remote/...` is not a
// working substitution; Zed's remote form is `zed://ssh/`, not `zed://` on its
// own. The `ssh-remote+` resolver is contributed by a remote-ssh extension, so
// each editor needs one installed before its link resolves.
const editors = computed(() => {
  const host = sshHost.value
  if (!host) return []
  const vscodeRemote = (scheme: string) =>
    `${scheme}://vscode-remote/ssh-remote+${host}${remoteHome}`
  return [
    { key: 'vscode', label: 'VS Code', href: vscodeRemote('vscode') },
    { key: 'vscodium', label: 'VSCodium', href: vscodeRemote('vscodium') },
    { key: 'cursor', label: 'Cursor', href: vscodeRemote('cursor') },
    { key: 'zed', label: 'Zed', href: `zed://ssh/${host}${remoteHome}` },
  ]
})

// Which editor the button opens on a plain click. Remembered across visits and
// across VMs -- which editor someone uses is a property of their machine, not of
// the guest they are opening -- so it is stored under one key rather than per id.
const editorKey = useLocalStorage('dummie:vm-editor', 'vscode')
const editorMenuOpen = ref(false)

// Falls back to the first entry rather than trusting the stored value: it comes
// from localStorage, so it can name an editor that has since been removed here.
const activeEditor = computed(() =>
  editors.value.find(e => e.key === editorKey.value) ?? editors.value[0])

// Choosing from the menu both switches the default and opens that editor, so
// picking one is never a two-step action.
function chooseEditor(key: string) {
  editorKey.value = key
  editorMenuOpen.value = false
  const target = editors.value.find(e => e.key === key)
  // Assigning location rather than following a link: a custom scheme is handed
  // to the OS, so the page it was clicked from stays where it is.
  if (target) window.location.href = target.href
}

async function copySsh() {
  try {
    await navigator.clipboard.writeText(sshCommand.value)
    sshCopied.value = true
    setTimeout(() => (sshCopied.value = false), 2000)
  }
  catch {
    actionError.value = `Could not copy to the clipboard; the command is: ${sshCommand.value}`
  }
}

/** "8000, 9090" -> [8000, 9090]. Blank entries are dropped, not zeroed. */
function parsePorts(s: string): number[] {
  return s.split(',').map(p => p.trim()).filter(Boolean).map(Number)
}

function openPorts() {
  if (!vm.value) return
  portsForm.default_port = String(vm.value.default_port || 8000)
  portsForm.public_ports = (vm.value.public_ports ?? []).join(', ')
  portsError.value = null
  portsOpen.value = true
}

async function savePorts() {
  const defaultPort = Number(portsForm.default_port)
  if (!Number.isInteger(defaultPort) || defaultPort < 1 || defaultPort > 65535) {
    portsError.value = 'The default port must be a whole number between 1 and 65535.'
    return
  }
  const publicPorts = parsePorts(portsForm.public_ports)
  if (publicPorts.some(p => !Number.isInteger(p) || p < 1 || p > 65535)) {
    portsError.value = 'Public ports must be whole numbers between 1 and 65535, separated by commas.'
    return
  }

  savingPorts.value = true
  portsError.value = null
  try {
    const res = await authFetch(`/vms/${id.value}/ports`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ default_port: defaultPort, public_ports: publicPorts }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    // The response is the saved row, so the page shows what was stored rather
    // than what was typed.
    vm.value = await res.json()
    portsOpen.value = false
  }
  catch (e) {
    portsError.value = e instanceof Error ? e.message : 'Could not save the ports'
  }
  finally {
    savingPorts.value = false
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

      <!-- routing -->
      <section aria-labelledby="ports-heading" class="mt-6 rounded-lg border border-border p-4 sm:p-6">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div>
            <h2 id="ports-heading" class="text-sm font-semibold">Ports</h2>
            <p class="mt-1 max-w-2xl text-sm text-muted-foreground">
              Which ports inside this VM are reachable, and where a request goes when it does not pick
              one. Changes reach the host straight away.
            </p>
          </div>
          <!-- Edit alone up here: it changes what this panel says, while the
               rest act on the VM the panel describes and read better under the
               values they use. -->
          <div class="flex flex-wrap items-center gap-2">
            <Dialog v-model:open="portsOpen">
              <Button variant="outline" size="sm" class="font-mono text-xs" @click="openPorts">
                <Pencil class="size-4" aria-hidden="true" />
                Edit
              </Button>
              <DialogContent>
                <DialogHeader>
                  <DialogTitle>Edit ports</DialogTitle>
                  <DialogDescription>
                    Requests reach this VM as
                    <span class="font-mono text-foreground">{{ vm.name }}</span>, whichever ports it
                    publishes.
                  </DialogDescription>
                </DialogHeader>
                <form class="space-y-4" :aria-busy="savingPorts" @submit.prevent="savePorts">
                  <div class="space-y-2">
                    <Label for="ports-default">Default port</Label>
                    <Input
                      id="ports-default"
                      v-model="portsForm.default_port"
                      type="number"
                      min="1"
                      max="65535"
                      inputmode="numeric"
                      aria-describedby="ports-default-hint"
                    />
                    <p id="ports-default-hint" class="text-xs text-muted-foreground">
                      Where a request goes when it does not pick a port.
                    </p>
                  </div>
                  <div class="space-y-2">
                    <Label for="ports-public">Public ports</Label>
                    <Input
                      id="ports-public"
                      v-model="portsForm.public_ports"
                      placeholder="8000, 9090"
                      inputmode="numeric"
                      aria-describedby="ports-public-hint"
                    />
                    <p id="ports-public-hint" class="text-xs text-muted-foreground">
                      Comma separated. Empty publishes nothing.
                    </p>
                  </div>

                  <FormError id="ports-error" :message="portsError" />

                  <DialogFooter>
                    <DialogClose as-child>
                      <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
                    </DialogClose>
                    <Button type="submit" class="font-mono text-xs" :disabled="savingPorts">
                      {{ savingPorts ? 'Saving…' : 'Save' }}
                    </Button>
                  </DialogFooter>
                </form>
              </DialogContent>
            </Dialog>
          </div>
        </div>

        <dl class="mt-4 grid gap-x-8 gap-y-4 sm:grid-cols-2">
          <div>
            <dt class="eyebrow text-muted-foreground">Default port</dt>
            <dd class="mt-1 flex items-center gap-2 font-mono text-sm">
              {{ vm.default_port }}
              <!-- Only when the server worked out a hostname: the host it runs
                   on has no domain otherwise, and a link to a name nothing
                   resolves is worse than none. -->
              <a
                v-if="vm.url"
                :href="vm.url"
                target="_blank"
                rel="noopener noreferrer"
                class="text-muted-foreground transition-colors hover:text-primary-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                :title="vm.url"
              >
                <ExternalLink class="size-4" aria-hidden="true" />
                <span class="sr-only">Open {{ vm.url }} (opens in a new tab)</span>
              </a>
            </dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Public ports</dt>
            <dd class="mt-1 font-mono text-sm">
              {{ vm.public_ports?.length ? vm.public_ports.join(', ') : 'none' }}
            </dd>
          </div>
        </dl>

        <!-- Ways in, under the routing they use. SSH is kept apart from the
             others: it only copies a command to the clipboard, while the rest
             navigate somewhere. The left group is rendered even when there is
             no hostname so the others stay right-aligned without it. -->
        <div class="mt-6 flex flex-wrap items-center justify-between gap-2">
          <div class="flex items-center gap-2">
            <!-- Only when there is a hostname: without a domain there is
                 nothing to ssh to, and a bare name would copy a command that
                 fails. -->
            <Button
              v-if="sshHost"
              variant="outline"
              size="sm"
              class="font-mono text-xs"
              :title="sshCommand"
              :aria-label="`Copy ${sshCommand} to the clipboard`"
              @click="copySsh"
            >
              <component :is="sshCopied ? Check : Terminal" class="size-4" aria-hidden="true" />
              {{ sshCopied ? 'Copied' : 'SSH' }}
            </Button>
            <span role="status" aria-live="polite" class="sr-only">
              {{ sshCopied ? 'SSH command copied to clipboard' : '' }}
            </span>
          </div>

          <div class="flex flex-wrap items-center gap-2 sm:justify-end">
            <Button
              v-if="vm.url"
              as="a"
              variant="outline"
              size="sm"
              class="font-mono text-xs"
              :href="vm.url"
              target="_blank"
              rel="noopener noreferrer"
              :title="vm.url"
            >
              <ExternalLink class="size-4" aria-hidden="true" />
              Web
              <span class="sr-only">: open {{ vm.url }} in a new tab</span>
            </Button>
            <!-- A new tab, not a route change: a terminal is a session, and
                 navigating the page away from it would drop the shell. Only
                 when the VM is running, since the console opens an ssh
                 connection to it and a stopped VM has nothing listening. -->
            <Button
              v-if="vm.console_url"
              as="a"
              variant="outline"
              size="sm"
              class="font-mono text-xs"
              :href="`/console/${vm.id}`"
              target="_blank"
              rel="noopener"
              :aria-disabled="vm.status !== 'running'"
              :class="vm.status !== 'running' && 'pointer-events-none opacity-50'"
              :title="vm.status === 'running'
                ? `Open a terminal on ${vm.name}`
                : `Cannot open a console: this VM is ${vm.status}`"
            >
              <SquareTerminal class="size-4" aria-hidden="true" />
              Console
              <span class="sr-only">: open a terminal in a new tab</span>
            </Button>
            <!-- One control, two targets: the wide half opens whichever editor
                 was used last, the chevron changes which that is. Same
                 reachability rule as the SSH button -- without a hostname there
                 is nothing for an editor to connect to. -->
            <div v-if="sshHost && activeEditor" class="inline-flex items-center">
              <Button
                as="a"
                variant="outline"
                size="sm"
                class="rounded-r-none border-r-0 font-mono text-xs"
                :href="activeEditor.href"
                :aria-disabled="vm.status !== 'running'"
                :class="vm.status !== 'running' && 'pointer-events-none opacity-50'"
                :title="vm.status === 'running'
                  ? `Open ${sshHost} in ${activeEditor.label} over SSH`
                  : `Cannot open an editor: this VM is ${vm.status}`"
              >
                <EditorIcon :name="activeEditor.key" class="size-4" />
                Open {{ activeEditor.label }}
              </Button>
              <DropdownMenu v-model:open="editorMenuOpen">
                <DropdownMenuTrigger as-child>
                  <Button
                    variant="outline"
                    size="sm"
                    class="rounded-l-none px-2"
                    :disabled="vm.status !== 'running'"
                    aria-label="Choose a different editor"
                  >
                    <ChevronDown class="size-3.5 opacity-60" aria-hidden="true" />
                  </Button>
                </DropdownMenuTrigger>
                <!-- p-0: Command brings its own padding, and the menu's would
                     otherwise inset the search field from the menu edge. -->
                <DropdownMenuContent align="end" class="w-56 p-0">
                  <Command>
                    <CommandInput placeholder="Search editors…" />
                    <CommandList>
                      <CommandEmpty>No editor found.</CommandEmpty>
                      <CommandGroup>
                        <CommandItem
                          v-for="e in editors"
                          :key="e.key"
                          :value="e.label"
                          class="font-mono text-xs"
                          @select="chooseEditor(e.key)"
                        >
                          <EditorIcon :name="e.key" class="size-4" />
                          {{ e.label }}
                          <Check
                            v-if="e.key === activeEditor.key"
                            class="ml-auto size-4"
                            aria-hidden="true"
                          />
                          <span class="sr-only">
                            : open {{ sshHost }} over SSH in {{ e.label }}
                          </span>
                        </CommandItem>
                      </CommandGroup>
                    </CommandList>
                  </Command>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          </div>
        </div>
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

        <DataTable label="Allowed destinations" :columns="targetColumns" :empty="!targets.length" :frame="false">
          <template #empty>
            No destinations recorded.
          </template>
          <TableRow v-for="t in targets" :key="t.id">
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
        </DataTable>
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
