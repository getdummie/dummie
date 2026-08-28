<script setup lang="ts">
import { ArrowLeft, Check, ChevronDown, Columns2, Copy, ExternalLink, Pencil, Plus, RefreshCw, SquareTerminal, Terminal, Trash2 } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
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
import {
  applyPreset,
  blankTarget,
  destinationPlaceholder as destinationPlaceholderFor,
  domainPortChoices,
  matchSummary as matchSummaryFor,
  portPresets,
  presetWarning as presetWarningFor,
  resetForKind,
  toTargetPayload,
} from '@/lib/targets'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { TableCell, TableRow } from '@/components/ui/table'
import type { DataTableColumn } from '@/lib/table'

definePageMeta({ middleware: ['auth'] })

interface VM {
  id: string
  client_id: string
  vm_id: string
  name: string
  default_port: number
  public_ports: number[]
  status: 'pending' | 'running' | 'stopped' | 'failed' | 'gone'
  boot: string
  cpus: number
  memory_mib: number
  disk_mib: number
  ip: string
  url: string
  console_url: string
  last_error: string
  created_at: string
  started_at: string
  reported_at: string
  expires_at: string
}

interface Target {
  id: string
  destination: string
  kind: 'domain' | 'ip'
  transport: '' | 'tcp' | 'udp' | 'any'
  ports: string
  note: string
  created_at: string
  expires_at: string
}

interface DeniedAttempt {
  kind: 'lookup' | 'packet'
  domain: string
  address: string
  proto: string
  port: number
  app_proto: string
  attempts: number
  last_seen: string
  signature: string
}

const deniedColumns: DataTableColumn[] = [
  { key: 'destination', label: 'Destination' },
  { key: 'what', label: 'What it tried' },
  { key: 'signature', label: 'Denied by' },
  { key: 'attempts', label: 'Attempts', align: 'right' },
  { key: 'last_seen', label: 'Last attempt', align: 'right' },
  { key: 'actions', label: '', align: 'right' },
]

const targetColumns: DataTableColumn[] = [
  { key: 'destination', label: 'Destination' },
  { key: 'matched', label: 'Matched on' },
  { key: 'transport', label: 'Transport' },
  { key: 'ports', label: 'Ports' },
  { key: 'note', label: 'Note' },
  { key: 'expires', label: 'Expires' },
  { key: 'actions', label: 'Actions', align: 'right' },
]

const route = useRoute()
const { authFetch } = useAuth()
const id = computed(() => String(route.params.id))

const vm = ref<VM | null>(null)
const targets = ref<Target[]>([])
const denied = ref<DeniedAttempt[]>([])
const deniedAvailable = ref(true)
const deniedRecording = ref({ packets: false, lookups: false })
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
    if (res.status === 404) {
      if (purging.value) {
        purging.value = null
        await navigateTo('/vms')
        return
      }
      throw new Error('This VM does not exist, or is not yours.')
    }
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    vm.value = await res.json()
    await loadTargets()
    loadDenied()
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

const deniedLoading = ref(true)
const deniedFirstLoad = ref(true)

async function loadDenied() {
  deniedLoading.value = true
  try {
    const res = await authFetch(`/vms/${id.value}/denied`)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = await res.json()
    denied.value = data.items ?? []
    deniedAvailable.value = data.available ?? false
    deniedRecording.value = data.recording ?? { packets: false, lookups: false }
  }
  catch {
    denied.value = []
    deniedAvailable.value = false
    deniedRecording.value = { packets: false, lookups: false }
  }
  finally {
    deniedLoading.value = false
    deniedFirstLoad.value = false
  }
}

const deniedRefreshing = ref(false)

async function refreshDenied() {
  if (deniedRefreshing.value) return
  deniedRefreshing.value = true
  const started = Date.now()
  try {
    await loadDenied()
  }
  finally {
    const shown = Date.now() - started
    if (shown < 450) {
      await new Promise(resolve => setTimeout(resolve, 450 - shown))
    }
    deniedRefreshing.value = false
  }
}
onMounted(() => load())

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

const purging = ref<{ until: number } | null>(null)

watch(vm, (v) => {
  const s = settling.value
  if (s && v && (v.status === s.want || Date.now() >= s.until)) settling.value = null
  const p = purging.value
  if (p && v && (v.last_error || Date.now() >= p.until)) purging.value = null
})

watch([() => vm.value?.status, settling, purging], () => {
  const busy = vm.value?.status === 'pending' || !!settling.value || !!purging.value
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

const deleteOpen = ref(false)
const deleting = ref(false)

const deletable = computed(() => vm.value?.status !== 'pending')

async function confirmDelete() {
  deleting.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/vms/${id.value}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    deleteOpen.value = false
    if (res.status !== 202) {
      await navigateTo('/vms')
      return
    }
    purging.value = { until: Date.now() + settleTimeoutMs }
    await load(true)
  }
  catch (e) {
    purging.value = null
    actionError.value = e instanceof Error ? e.message : 'Could not delete the VM'
  }
  finally {
    deleting.value = false
  }
}

const portsOpen = ref(false)
const savingPorts = ref(false)
const portsError = ref<string | null>(null)
const portsForm = reactive({ default_port: '8000', public_ports: '' })

const sshHost = computed(() => {
  if (!vm.value?.url) return ''
  try {
    return new URL(vm.value.url).hostname
  }
  catch {
    return ''
  }
})

const sshDomain = computed(() => {
  const host = sshHost.value
  const name = vm.value?.name
  if (!host || !name) return host
  return host.startsWith(`${name}.`) ? host.slice(name.length + 1) : host
})

const sshDestination = computed(() =>
  vm.value?.name ? `${vm.value.name}@${sshDomain.value}` : sshDomain.value,
)
const sshCommand = computed(() => `ssh ${sshDestination.value}`)
const sshCopied = ref(false)

const remoteHome = '/home/ubuntu'

const editors = computed(() => {
  const host = sshDestination.value
  if (!sshHost.value) return []
  const vscodeRemote = (scheme: string) =>
    `${scheme}://vscode-remote/ssh-remote+${host}${remoteHome}`
  return [
    { key: 'vscode', label: 'VS Code', href: vscodeRemote('vscode') },
    { key: 'vscode-insiders', label: 'VS Code Insiders', href: vscodeRemote('vscode-insiders') },
    { key: 'vscodium', label: 'VSCodium', href: vscodeRemote('vscodium') },
    { key: 'cursor', label: 'Cursor', href: vscodeRemote('cursor') },
    { key: 'windsurf', label: 'Windsurf', href: vscodeRemote('windsurf') },
    { key: 'antigravity', label: 'Antigravity', href: vscodeRemote('antigravity') },
    { key: 'zed', label: 'Zed', href: `zed://ssh/${host}${remoteHome}` },
  ]
})

const editorKey = useLocalStorage('dummie:vm-editor', 'vscode')
const editorMenuOpen = ref(false)

const activeEditor = computed(() =>
  editors.value.find(e => e.key === editorKey.value) ?? editors.value[0])

function chooseEditor(key: string) {
  editorKey.value = key
  editorMenuOpen.value = false
  const target = editors.value.find(e => e.key === key)
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

const addOpen = ref(false)
const adding = ref(false)
const addError = ref<string | null>(null)
const form = reactive(blankTarget())

function resetTargetForm() {
  Object.assign(form, blankTarget())
  addError.value = null
}

function onKindChange() {
  resetForKind(form)
}

function onPresetChange(key: string) {
  applyPreset(form, key)
}

const destinationPlaceholder = computed(() => destinationPlaceholderFor(form.kind))
const matchSummary = computed(() => matchSummaryFor(form.kind))
const presetWarning = computed(() => presetWarningFor(form.preset))

async function addTarget() {
  const problem = ttlProblem(form.ttl_seconds)
  if (problem) {
    addError.value = problem
    return
  }
  adding.value = true
  addError.value = null
  try {
    const res = await authFetch(`/vms/${id.value}/targets`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(toTargetPayload(form)),
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

function deniedDestination(d: DeniedAttempt) {
  return d.domain || d.address || '—'
}

function deniedWhat(d: DeniedAttempt) {
  if (d.kind === 'lookup') return 'dns lookup'
  if (d.proto === 'icmp') return 'ping (icmp)'

  const preset = portPresets.find(p => p.transport === d.proto && p.ports === String(d.port))
  const named = preset ? preset.label.replace(/ \(.*\)$/, '') : ''
  const identified = d.app_proto && d.app_proto !== 'failed' ? d.app_proto : ''

  const label = named || identified
  const where = `${d.proto} ${d.port}`
  return label ? `${label} — ${where}` : where
}

function allowDenied(d: DeniedAttempt) {
  resetTargetForm()
  const byName = d.kind === 'lookup' || (!!d.domain && (d.port === 443 || d.port === 80))

  if (byName) {
    form.kind = 'domain'
    form.destination = d.domain
    form.domainPorts = d.kind === 'lookup' ? '80,443' : String(d.port)
  }
  else {
    form.kind = 'ip'
    form.destination = d.address
    form.transport = d.proto === 'icmp' ? 'icmp' : (d.proto || 'tcp')
    form.ports = d.proto === 'icmp' ? '' : String(d.port)
    form.preset = portPresets.find(p => p.transport === form.transport && p.ports === form.ports)?.key ?? 'custom'
  }
  form.note = `seen denied — ${deniedWhat(d)}`
  addOpen.value = true
}

function matchedOn(t: Target) {
  if (t.kind !== 'domain') return 'address'
  return t.ports === 'none' ? 'name — resolves only' : 'tls sni · http host'
}

function portsLabel(t: Target) {
  if (t.kind !== 'domain') return t.ports || 'any'
  return t.ports === 'none' ? 'none' : (t.ports || '443, 80')
}

const resolveOpen = ref(false)
const resolveHost = ref('')
const resolvePreset = ref('ssh')
const resolveTransport = ref('tcp')
const resolvePorts = ref('')
const resolving = ref(false)
const resolveError = ref<string | null>(null)
const resolveAddresses = ref<string[]>([])
const resolveChosen = ref<string[]>([])
const resolveTruncated = ref(false)
const addingResolved = ref(false)
const resolveTTL = ref('0')

function resetResolveForm() {
  resolveHost.value = ''
  resolvePreset.value = 'ssh'
  resolveTransport.value = 'tcp'
  resolvePorts.value = ''
  resolveError.value = null
  resolveAddresses.value = []
  resolveChosen.value = []
  resolveTruncated.value = false
  resolveTTL.value = '0'
}

const nowMs = ref(Date.now())
let ttlClock: ReturnType<typeof setInterval> | null = null
onMounted(() => {
  ttlClock = setInterval(() => { nowMs.value = Date.now() }, 30_000)
})
onBeforeUnmount(() => {
  if (ttlClock) clearInterval(ttlClock)
})

function ttlProblem(raw: string) {
  const ttl = Number(raw)
  if (!Number.isInteger(ttl) || ttl < 0) return 'TTL must be a whole number of seconds, or 0 for no limit.'
  if (ttl > 0 && ttl < 10) return 'A TTL must be at least 10 seconds. Use 0 for no limit.'
  if (ttl > 30 * 24 * 3600) return 'A TTL must be at most 30 days (2592000 seconds).'
  return null
}

function timeLeft(s: string) {
  if (!s) return ''
  const at = new Date(s).getTime()
  if (Number.isNaN(at)) return ''
  const left = at - nowMs.value
  if (left <= 0) return 'overdue'
  const mins = Math.round(left / 60_000)
  if (mins < 60) return `${Math.max(1, mins)}m`
  const hours = Math.round(mins / 60)
  return hours < 24 ? `${hours}h` : `${Math.round(hours / 24)}d`
}

const resolveSpec = computed(() => {
  const preset = portPresets.find(p => p.key === resolvePreset.value)
  if (preset && preset.key !== 'custom') {
    return {
      transport: preset.transport,
      ports: preset.ports,
      label: preset.label.replace(/ \(.*\)$/, ''),
    }
  }
  const transport = resolveTransport.value
  const ports = transport === 'icmp' ? '' : resolvePorts.value
  return {
    transport,
    ports,
    label: transport === 'icmp' ? 'ping' : `${transport} ${ports || 'any port'}`,
  }
})

async function lookupHost() {
  resolving.value = true
  resolveError.value = null
  resolveAddresses.value = []
  resolveChosen.value = []
  try {
    const res = await authFetch(`/vms/${id.value}/targets/resolve`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ host: resolveHost.value }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    const data = await res.json()
    resolveAddresses.value = data.addresses ?? []
    resolveChosen.value = [...resolveAddresses.value]
    resolveTruncated.value = data.truncated ?? false
    if (data.error) resolveError.value = data.error
    else if (!resolveAddresses.value.length) resolveError.value = 'That name has no IPv4 addresses.'
  }
  catch (e) {
    resolveError.value = e instanceof Error ? e.message : 'Could not resolve that name'
  }
  finally {
    resolving.value = false
  }
}

function toggleResolved(addr: string, on: boolean) {
  resolveChosen.value = on
    ? [...resolveChosen.value, addr]
    : resolveChosen.value.filter(a => a !== addr)
}

async function postTarget(body: Record<string, string | number>) {
  const res = await authFetch(`/vms/${id.value}/targets`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok && res.status !== 409) {
    throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
  }
}

async function addResolved() {
  if (!resolveChosen.value.length) return
  const problem = ttlProblem(resolveTTL.value)
  if (problem) {
    resolveError.value = problem
    return
  }
  const spec = resolveSpec.value
  addingResolved.value = true
  resolveError.value = null
  try {
    const ttl = Number(resolveTTL.value)
    await postTarget({
      kind: 'domain',
      destination: resolveHost.value,
      ports: 'none',
      note: `lookup for ${spec.label}`,
      ttl_seconds: ttl,
    })
    for (const addr of resolveChosen.value) {
      await postTarget({
        kind: 'ip',
        destination: addr,
        transport: spec.transport,
        ports: spec.ports,
        note: `${resolveHost.value} — ${spec.label}`,
        ttl_seconds: ttl,
      })
    }
    resolveOpen.value = false
    resetResolveForm()
    await loadTargets()
  }
  catch (e) {
    resolveError.value = e instanceof Error ? e.message : 'Could not record the destinations'
  }
  finally {
    addingResolved.value = false
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
          <Button
            variant="outline"
            size="sm"
            class="font-mono text-xs text-destructive hover:text-destructive"
            :disabled="!deletable || !!purging"
            :aria-label="deletable
              ? 'Delete this VM'
              : `Cannot delete this VM: it is ${vm.status}`"
            @click="deleteOpen = true"
          >
            <Trash2 class="size-4" aria-hidden="true" />
            {{ purging ? 'Deleting…' : 'Delete' }}
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

      <section aria-labelledby="details-heading" class="mt-6 rounded-lg border border-border p-4 sm:p-6">
        <h2 id="details-heading" class="text-sm font-semibold">Details</h2>
        <dl class="mt-4 grid gap-x-8 gap-y-4 sm:grid-cols-2 lg:grid-cols-3">
          <div>
            <dt class="eyebrow text-muted-foreground">Size</dt>
            <dd class="mt-1 font-mono text-sm">{{ vm.cpus }} vCPU · {{ fmtMiB(vm.memory_mib) }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Disk</dt>
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
            <dt class="eyebrow text-muted-foreground">Last confirmed by host</dt>
            <dd class="mt-1 text-sm text-muted-foreground">{{ fmtDate(vm.reported_at) }}</dd>
          </div>
          <div v-if="vm.expires_at">
            <dt class="eyebrow text-muted-foreground">Destroyed</dt>
            <dd class="mt-1 text-sm">
              <span :class="timeLeft(vm.expires_at) === 'overdue' ? 'text-destructive' : ''">
                {{ timeLeft(vm.expires_at) === 'overdue' ? 'overdue' : `in ${timeLeft(vm.expires_at)}` }}
              </span>
              <span class="text-muted-foreground"> · {{ fmtDate(vm.expires_at) }}</span>
            </dd>
          </div>
        </dl>
      </section>

      <section aria-labelledby="ports-heading" class="mt-6 rounded-lg border border-border p-4 sm:p-6">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div>
            <h2 id="ports-heading" class="text-sm font-semibold">Ports</h2>
            <p class="mt-1 max-w-2xl text-sm text-muted-foreground">
              Which ports inside this VM are reachable, and where a request goes when it does not pick
              one. Changes reach the host straight away.
            </p>
          </div>
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

        <div class="mt-6 flex flex-wrap items-center justify-between gap-2">
          <div class="flex min-w-0 items-center gap-2">
            <div
              v-if="sshHost"
              class="flex min-w-0 items-center gap-2 rounded-md border border-border bg-muted/40 py-1 pl-3 pr-1"
            >
              <Terminal class="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
              <code class="truncate font-mono text-xs">{{ sshCommand }}</code>
              <Button
                variant="ghost"
                size="icon-xs"
                class="shrink-0"
                :title="`Copy ${sshCommand} to the clipboard`"
                :aria-label="`Copy ${sshCommand} to the clipboard`"
                @click="copySsh"
              >
                <component :is="sshCopied ? Check : Copy" aria-hidden="true" />
              </Button>
            </div>
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
            <Button
              v-if="vm.console_url"
              as="a"
              variant="outline"
              size="sm"
              class="font-mono text-xs"
              :href="`/vms/${vm.id}/console`"
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
            <Button
              v-if="vm.url"
              as="a"
              variant="outline"
              size="sm"
              class="font-mono text-xs"
              :href="`/vms/${vm.id}/workspace`"
              target="_blank"
              rel="noopener"
              :aria-disabled="vm.status !== 'running'"
              :class="vm.status !== 'running' && 'pointer-events-none opacity-50'"
              :title="vm.status === 'running'
                ? `Open the desktop, mobile and console panes for ${vm.name}`
                : `Cannot open a workspace: this VM is ${vm.status}`"
            >
              <Columns2 class="size-4" aria-hidden="true" />
              Workspace
              <span class="sr-only">: open previews and a terminal in a new tab</span>
            </Button>
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

      <section aria-labelledby="targets-heading" class="mt-6 rounded-lg border border-border">
        <div class="flex flex-wrap items-start justify-between gap-4 p-4 sm:p-6">
          <div>
            <h2 id="targets-heading" class="text-sm font-semibold">Allowed destinations</h2>
            <p class="mt-1 max-w-2xl text-sm text-muted-foreground">
              This list is the whole of what this VM can reach. Nothing else leaves it, and a
              domain that is not here will not even resolve.
            </p>
          </div>
          <div class="flex flex-wrap items-center gap-2">
          <Dialog v-model:open="resolveOpen" @update:open="(v: boolean) => !v && resetResolveForm()">
            <Button size="sm" variant="outline" class="font-mono text-xs" @click="resolveOpen = true">
              From a hostname
            </Button>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Allow a hostname on another port</DialogTitle>
                <DialogDescription>
                  For anything that is not https or http. The name is resolved now and its addresses
                  are recorded, because ssh and the rest carry no hostname for the policy to check.
                </DialogDescription>
              </DialogHeader>

              <form class="space-y-4" :aria-busy="resolving || addingResolved" @submit.prevent="lookupHost">
                <div class="space-y-2">
                  <Label for="r-host">Hostname</Label>
                  <div class="flex gap-2">
                    <Input id="r-host" v-model="resolveHost" required placeholder="github.com" />
                    <Button type="submit" variant="outline" class="font-mono text-xs shrink-0" :disabled="resolving || !resolveHost">
                      {{ resolving ? 'Resolving…' : 'Resolve' }}
                    </Button>
                  </div>
                </div>

                <div class="space-y-2">
                  <Label for="r-preset">Protocol</Label>
                  <Select v-model="resolvePreset">
                    <SelectTrigger id="r-preset" class="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem v-for="p in portPresets" :key="p.key" :value="p.key">
                        {{ p.label }}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                </div>

                <div v-if="resolvePreset === 'custom'" class="grid gap-4 sm:grid-cols-2">
                  <div class="space-y-2">
                    <Label for="r-transport">Transport</Label>
                    <Select v-model="resolveTransport">
                      <SelectTrigger id="r-transport" class="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="tcp">tcp</SelectItem>
                        <SelectItem value="udp">udp</SelectItem>
                        <SelectItem value="icmp">icmp (ping)</SelectItem>
                        <SelectItem value="any">any</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                  <div v-if="resolveTransport !== 'icmp'" class="space-y-2">
                    <Label for="r-ports">Ports</Label>
                    <Input id="r-ports" v-model="resolvePorts" placeholder="8080, 5000:5010" aria-describedby="r-ports-hint" />
                    <p id="r-ports-hint" class="text-xs text-muted-foreground">Empty means any port.</p>
                  </div>
                </div>

                <div v-if="resolveAddresses.length" class="space-y-2">
                  <p class="text-sm font-medium">Addresses</p>
                  <div v-for="addr in resolveAddresses" :key="addr" class="flex items-center gap-2">
                    <Checkbox
                      :id="`r-addr-${addr}`"
                      :model-value="resolveChosen.includes(addr)"
                      @update:model-value="(v: boolean) => toggleResolved(addr, v)"
                    />
                    <Label :for="`r-addr-${addr}`" class="font-mono text-xs font-normal">{{ addr }}</Label>
                  </div>
                  <p class="text-xs text-muted-foreground">
                    These addresses are what the name answers with right now. If they change, this
                    list does not follow — the allowance will need editing.
                  </p>
                  <p v-if="resolveTruncated" class="text-xs text-muted-foreground">
                    The name returned more addresses than are shown. A name behind a large pool is
                    better allowed by its CIDR range, if its operator publishes one.
                  </p>
                </div>

                <div class="space-y-2">
                  <Label for="r-ttl">TTL (seconds)</Label>
                  <Input
                    id="r-ttl"
                    v-model="resolveTTL"
                    type="number"
                    min="0"
                    step="1"
                    inputmode="numeric"
                    aria-describedby="r-ttl-hint"
                  />
                  <p id="r-ttl-hint" class="text-xs text-muted-foreground">
                    0 keeps these until you remove them. Anything else applies to every entry this
                    writes, the lookup included — an address that expired while its name stayed
                    resolvable would be half an allowance.
                  </p>
                </div>

                <FormError id="resolve-error" :message="resolveError" />
              </form>

              <DialogFooter>
                <DialogClose as-child>
                  <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
                </DialogClose>
                <Button
                  type="button"
                  class="font-mono text-xs"
                  :disabled="addingResolved || !resolveChosen.length"
                  @click="addResolved"
                >
                  {{ addingResolved ? 'Adding…' : `Add ${resolveChosen.length || ''} entr${resolveChosen.length === 1 ? 'y' : 'ies'}` }}
                </Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>

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
                  <Select v-model="form.kind" @update:model-value="onKindChange">
                    <SelectTrigger id="t-kind" class="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="domain">Domain</SelectItem>
                      <SelectItem value="ip">IP address or CIDR</SelectItem>
                    </SelectContent>
                  </Select>
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

                <div v-if="form.kind === 'domain'" class="space-y-2">
                  <Label for="t-domain-ports">Allow on</Label>
                  <Select v-model="form.domainPorts">
                    <SelectTrigger id="t-domain-ports" class="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem v-for="c in domainPortChoices" :key="c.value" :value="c.value">
                        {{ c.label }}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                  <p v-if="form.domainPorts === 'none'" class="text-xs text-muted-foreground">
                    The guest will be able to resolve this name and reach nothing at it. Pair it with
                    an address entry to allow ssh or another protocol.
                  </p>
                </div>

                <template v-if="form.kind === 'ip'">
                  <div class="space-y-2">
                    <Label for="t-preset">Protocol</Label>
                    <Select v-model="form.preset" @update:model-value="onPresetChange">
                      <SelectTrigger id="t-preset" class="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem v-for="p in portPresets" :key="p.key" :value="p.key">
                          {{ p.label }}
                        </SelectItem>
                      </SelectContent>
                    </Select>
                    <p v-if="presetWarning" class="text-xs text-muted-foreground">{{ presetWarning }}</p>
                  </div>
                  <div class="grid gap-4 sm:grid-cols-2">
                    <div class="space-y-2">
                      <Label for="t-transport">Transport</Label>
                      <Select v-model="form.transport">
                        <SelectTrigger id="t-transport" class="w-full">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="tcp">tcp</SelectItem>
                          <SelectItem value="udp">udp</SelectItem>
                          <SelectItem value="icmp">icmp (ping)</SelectItem>
                          <SelectItem value="any">any</SelectItem>
                        </SelectContent>
                      </Select>
                      <p v-if="form.transport === 'any'" class="text-xs text-muted-foreground">
                        Allows tcp and udp. To allow only ping, choose icmp.
                      </p>
                    </div>
                    <div v-if="form.transport !== 'icmp'" class="space-y-2">
                      <Label for="t-ports">Ports</Label>
                      <Input id="t-ports" v-model="form.ports" placeholder="443, 80,443, 1000:2000" aria-describedby="t-ports-hint" />
                      <p id="t-ports-hint" class="text-xs text-muted-foreground">Empty means any port.</p>
                    </div>
                  </div>
                </template>
                <div class="space-y-2">
                  <Label for="t-note">Note</Label>
                  <Input id="t-note" v-model="form.note" placeholder="why this is needed" />
                </div>
                <div class="space-y-2">
                  <Label for="t-ttl">TTL (seconds)</Label>
                  <Input
                    id="t-ttl"
                    v-model="form.ttl_seconds"
                    type="number"
                    min="0"
                    step="1"
                    inputmode="numeric"
                    aria-describedby="t-ttl-hint"
                  />
                  <p id="t-ttl-hint" class="text-xs text-muted-foreground">
                    0 keeps this until you remove it. Anything else is a time to live in seconds: the
                    allowance is removed for you when it runs out, and the host's policy is rewritten
                    without it.
                  </p>
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
        </div>

        <DataTable label="Allowed destinations" :columns="targetColumns" :empty="!targets.length" :frame="false">
          <template #empty>
            No destinations recorded.
          </template>
          <TableRow v-for="t in targets" :key="t.id">
            <TableCell class="font-mono break-all">{{ t.destination }}</TableCell>
            <TableCell class="font-mono text-xs text-muted-foreground">
              {{ matchedOn(t) }}
            </TableCell>
            <TableCell class="font-mono text-muted-foreground">{{ t.transport || '—' }}</TableCell>
            <TableCell class="font-mono text-muted-foreground">{{ portsLabel(t) }}</TableCell>
            <TableCell class="text-muted-foreground">{{ t.note || '—' }}</TableCell>
            <TableCell class="font-mono text-xs">
              <span v-if="!t.expires_at" class="text-muted-foreground">—</span>
              <span v-else :class="timeLeft(t.expires_at) === 'overdue' ? 'text-destructive' : 'text-muted-foreground'">
                {{ timeLeft(t.expires_at) }}
              </span>
            </TableCell>
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

      <section aria-labelledby="denied-heading" class="mt-6 rounded-lg border border-border">
        <div class="flex flex-wrap items-start justify-between gap-4 p-4 sm:p-6">
          <div>
            <h2 id="denied-heading" class="text-sm font-semibold">Denied</h2>
            <p class="mt-1 max-w-2xl text-sm text-muted-foreground">
              Everything this VM tried to do that policy stopped, since it was created and at most
              7 days back — lookups the resolver refused and connections the ruleset dropped, whatever
              protocol or port they used. Each one is either a destination worth allowing above, or
              something the guest should not have been reaching at all.
            </p>
          </div>
          <Button
            variant="outline"
            size="sm"
            class="font-mono text-xs"
            :disabled="deniedRefreshing"
            aria-label="Refresh denied attempts"
            @click="refreshDenied"
          >
            <RefreshCw :class="['size-4', deniedRefreshing && 'animate-spin']" aria-hidden="true" />
            <span class="sr-only sm:not-sr-only">Refresh</span>
          </Button>
        </div>

        <p v-if="!deniedFirstLoad && !deniedAvailable" class="px-4 pb-6 text-sm text-muted-foreground sm:px-6">
          Neither record could be read, so nothing is being reported here. This says nothing about
          whether this VM has been blocked.
        </p>

        <div v-else-if="deniedLoading && deniedFirstLoad" class="space-y-2 px-4 pb-6 sm:px-6" aria-busy="true">
          <p class="sr-only">Loading denied attempts…</p>
          <Skeleton v-for="n in 3" :key="n" class="h-8 w-full" aria-hidden="true" />
        </div>

        <template v-else>
          <p v-if="!deniedRecording.lookups" class="px-4 pb-4 text-sm text-muted-foreground sm:px-6">
            Refused lookups are not being recorded, so names this VM could not resolve are missing
            from this list. Everything the ruleset dropped is still shown.
          </p>
          <p v-if="!deniedRecording.packets" class="px-4 pb-4 text-sm text-muted-foreground sm:px-6">
            Dropped connections are not being recorded, so only names the resolver refused are shown.
          </p>

          <DataTable label="Denied" :columns="deniedColumns" :empty="!denied.length" :frame="false">
            <template #empty>
              Nothing has been denied.
            </template>
            <TableRow v-for="d in denied" :key="`${d.kind}-${d.domain}-${d.address}-${d.proto}-${d.port}`">
              <TableCell class="font-mono break-all">
                {{ deniedDestination(d) }}
                <span v-if="d.domain && d.address" class="block text-xs text-muted-foreground">
                  {{ d.address }}
                </span>
              </TableCell>
              <TableCell class="font-mono text-xs text-muted-foreground">{{ deniedWhat(d) }}</TableCell>
              <TableCell class="text-xs text-muted-foreground">{{ d.signature || '—' }}</TableCell>
              <TableCell class="text-right font-mono tabular-nums">{{ d.attempts }}</TableCell>
              <TableCell class="text-right text-sm text-muted-foreground">{{ fmtDate(d.last_seen) }}</TableCell>
              <TableCell class="text-right">
                <Button
                  variant="outline"
                  size="sm"
                  class="font-mono text-xs"
                  :aria-label="`Allow ${deniedDestination(d)}`"
                  @click="allowDenied(d)"
                >
                  Allow
                </Button>
              </TableCell>
            </TableRow>
          </DataTable>
        </template>
      </section>
    </template>

    <Dialog v-model:open="deleteOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete VM</DialogTitle>
          <DialogDescription>
            <span class="font-mono text-foreground">{{ vm?.name || vm?.vm_id }}</span>
            is shut down, its disk is deleted on the host, and its record here goes
            with it. This cannot be undone. The vCPU, memory and disk it holds are
            returned to your allowance.
          </DialogDescription>
        </DialogHeader>
        <FormError id="delete-vm-error" :message="actionError" />
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
