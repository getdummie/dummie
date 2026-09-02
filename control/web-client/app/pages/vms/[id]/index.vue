<script setup lang="ts">
import { ArrowLeft, Check, ChevronDown, Columns2, Copy, Download, Eye, EyeOff, ExternalLink, Globe, Monitor, Pencil, Plus, RefreshCw, SquareTerminal, Terminal, Trash2 } from '@lucide/vue'
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
  deniedCovered,
  destinationPlaceholder as destinationPlaceholderFor,
  domainPortChoices,
  everywhere,
  isEverywhere,
  matchSummary as matchSummaryFor,
  portPresets,
  presetWarning as presetWarningFor,
  resetForKind,
  targetToForm,
  toTargetPayload,
} from '@/lib/targets'
import type { DeniedAttempt, TargetRecord } from '@/lib/targets'
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
  default_user: string
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

const deniedColumns: DataTableColumn[] = [
  { key: 'destination', label: 'Destination' },
  { key: 'what', label: 'What it tried' },
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
const targets = ref<TargetRecord[]>([])
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

function fmtShortDate(s: string) {
  if (!s) return '—'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  const date = `${d.getDate()} ${d.toLocaleString(undefined, { month: 'short' })}`
  return `${date}, ${d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', hour12: false })}`
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
    loadDomain()
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

const deniedSeconds = ref('')

const deniedWindowLabel = computed(() => {
  const n = Number(deniedSeconds.value)
  if (!deniedSeconds.value || !Number.isFinite(n) || n < 1) return 'since this VM was created, 7 days at most'
  return `the last ${n} second${n === 1 ? '' : 's'}`
})

async function loadDenied() {
  deniedLoading.value = true
  try {
    const seconds = Number(deniedSeconds.value)
    const query = deniedSeconds.value && Number.isFinite(seconds) && seconds >= 1
      ? `?seconds=${Math.floor(seconds)}`
      : ''
    const res = await authFetch(`/vms/${id.value}/denied${query}`)
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

type RdpCredentials = { host: string, port: number, username: string, password: string }

const rdp = ref<RdpCredentials | null>(null)
const rdpError = ref('')
const rdpBusy = ref(false)
const rdpRevealed = ref(false)
const rdpCopied = ref('')

// The password is derived on the server from a secret this client never sees, so
// it is fetched on demand rather than carried in the VM payload.
async function loadRdp() {
  if (!vm.value || vm.value.status !== 'running') return
  rdpBusy.value = true
  rdpError.value = ''
  try {
    const res = await authFetch(`/vms/${vm.value.id}/rdp-credentials`)
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    rdp.value = await res.json()
  }
  catch (e) {
    rdpError.value = e instanceof Error ? e.message : 'Could not read the remote desktop credentials'
  }
  finally {
    rdpBusy.value = false
  }
}

async function rotateRdp() {
  if (!vm.value) return
  rdpBusy.value = true
  rdpError.value = ''
  try {
    const res = await authFetch(`/vms/${vm.value.id}/rdp-rotate`, { method: 'POST' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    rdp.value = await res.json()
    rdpRevealed.value = true
  }
  catch (e) {
    rdpError.value = e instanceof Error ? e.message : 'Could not rotate the password'
  }
  finally {
    rdpBusy.value = false
  }
}

async function copyRdp(field: 'address' | 'username' | 'password') {
  if (!rdp.value) return
  const value = field === 'address'
    ? `${rdp.value.host}:${rdp.value.port}`
    : field === 'username' ? rdp.value.username : rdp.value.password
  try {
    await navigator.clipboard.writeText(value)
    rdpCopied.value = field
    setTimeout(() => (rdpCopied.value = ''), 2000)
  }
  catch {
    rdpError.value = 'Could not copy to the clipboard'
  }
}

// The .rdp download goes through authFetch because the endpoint needs a bearer
// token; a plain link would arrive unauthenticated.
async function downloadRdpFile() {
  if (!vm.value) return
  try {
    const res = await authFetch(`/vms/${vm.value.id}/rdp-file`)
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    const url = URL.createObjectURL(await res.blob())
    const a = document.createElement('a')
    a.href = url
    a.download = `${vm.value.name}.rdp`
    a.click()
    URL.revokeObjectURL(url)
  }
  catch (e) {
    rdpError.value = e instanceof Error ? e.message : 'Could not download the connection file'
  }
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

const userOpen = ref(false)
const savingUser = ref(false)
const userError = ref<string | null>(null)
const userForm = reactive({ default_user: '' })

function openUser() {
  userForm.default_user = vm.value?.default_user ?? ''
  userError.value = null
  userOpen.value = true
}

async function saveUser() {
  const user = userForm.default_user.trim()
  if (user !== '' && !/^[a-zA-Z0-9._][a-zA-Z0-9._-]*$/.test(user)) {
    userError.value = 'A login name can hold letters, digits, dots, underscores and dashes, and cannot start with a dash.'
    return
  }

  savingUser.value = true
  userError.value = null
  try {
    const res = await authFetch(`/vms/${id.value}/user`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ default_user: user }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    vm.value = await res.json()
    userOpen.value = false
  }
  catch (e) {
    userError.value = e instanceof Error ? e.message : 'Could not save the default user'
  }
  finally {
    savingUser.value = false
  }
}

const addOpen = ref(false)
const adding = ref(false)
const addError = ref<string | null>(null)
const editingId = ref<string | null>(null)
const form = reactive(blankTarget())

function resetTargetForm() {
  Object.assign(form, blankTarget())
  editingId.value = null
  addError.value = null
}

function remainingSeconds(expiresAt: string) {
  if (!expiresAt) return 0
  const at = new Date(expiresAt).getTime()
  if (Number.isNaN(at)) return 0
  return Math.max(0, Math.round((at - Date.now()) / 1000))
}

function openEditTarget(t: TargetRecord) {
  Object.assign(form, targetToForm(t, remainingSeconds(t.expires_at)))
  editingId.value = t.id
  addError.value = null
  addOpen.value = true
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

async function saveTarget() {
  const problem = ttlProblem(form.ttl_seconds)
  if (problem) {
    addError.value = problem
    return
  }
  adding.value = true
  addError.value = null
  const target = editingId.value
  try {
    const res = await authFetch(
      target ? `/vms/${id.value}/targets/${target}` : `/vms/${id.value}/targets`,
      {
        method: target ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(toTargetPayload(form)),
      },
    )
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    addOpen.value = false
    resetTargetForm()
    await loadTargets()
  }
  catch (e) {
    addError.value = e instanceof Error
      ? e.message
      : `Could not ${target ? 'save' : 'add'} the destination`
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

const alreadyAllowed = computed(() => {
  const covered = new Set<DeniedAttempt>()
  for (const d of denied.value) {
    if (deniedCovered(d, targets.value)) covered.add(d)
  }
  return covered
})

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

function matchedOn(t: TargetRecord) {
  if (isEverywhere(t)) return 'everything — every name and address'
  if (t.kind !== 'domain') return 'address'
  return t.ports === 'none' ? 'name — resolves only' : 'tls sni · http host'
}

function portsLabel(t: TargetRecord) {
  if (t.kind !== 'domain') return t.ports || 'any'
  return t.ports === 'none' ? 'none' : (t.ports || '443, 80')
}

const allowAll = computed(() => targets.value.find(isEverywhere) ?? null)
const openAllOpen = ref(false)
const openAllTTL = ref('3600')
const openAllNote = ref('')
const openingAll = ref(false)
const openAllError = ref<string | null>(null)

function resetOpenAllForm() {
  openAllTTL.value = '3600'
  openAllNote.value = ''
  openAllError.value = null
}

async function openEverything() {
  const ttl = Number(openAllTTL.value)
  const problem = ttlProblem(openAllTTL.value)
  if (problem || ttl === 0) {
    openAllError.value = problem
      ?? 'An open door needs a TTL. Add 0.0.0.0/0 by hand if you really want it to stay.'
    return
  }
  openingAll.value = true
  openAllError.value = null
  try {
    await postTarget({
      kind: 'ip',
      destination: everywhere,
      transport: 'any',
      ports: '',
      note: openAllNote.value.trim() || 'temporarily allowed everything',
      ttl_seconds: ttl,
    })
    openAllOpen.value = false
    resetOpenAllForm()
    await loadTargets()
  }
  catch (e) {
    openAllError.value = e instanceof Error ? e.message : 'Could not open the policy'
  }
  finally {
    openingAll.value = false
  }
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

const toRemove = ref<TargetRecord | null>(null)
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

interface CustomDomain {
  domain: string
  status: 'pending_dns' | 'verifying' | 'issuing' | 'active' | 'failed'
  cname_name: string
  cname_target: string
  last_error?: string
  url?: string
  cert_not_after?: string
}

const domain = ref<CustomDomain | null>(null)
const domainInput = ref('')
const domainBusy = ref(false)
const domainError = ref<string | null>(null)
let domainPoll: ReturnType<typeof setInterval> | null = null

const domainSettling = computed(
  () => domain.value?.status === 'verifying' || domain.value?.status === 'issuing',
)

const domainStatusLabel: Record<CustomDomain['status'], string> = {
  pending_dns: 'Waiting for your CNAME',
  verifying: 'Checking the CNAME',
  issuing: 'Getting a certificate',
  active: 'Live',
  failed: 'Could not be set up',
}

const domainStatusVariant: Record<CustomDomain['status'], BadgeVariant> = {
  pending_dns: 'outline',
  verifying: 'secondary',
  issuing: 'secondary',
  active: 'default',
  failed: 'destructive',
}

async function loadDomain() {
  const res = await authFetch(`/vms/${id.value}/domain`)
  if (res.status === 404) {
    domain.value = null
    return
  }
  if (!res.ok) return
  domain.value = await res.json()
  if (domainSettling.value) startDomainPoll()
  else stopDomainPoll()
}

function startDomainPoll() {
  if (domainPoll) return
  domainPoll = setInterval(loadDomain, 5000)
}

function stopDomainPoll() {
  if (!domainPoll) return
  clearInterval(domainPoll)
  domainPoll = null
}

onBeforeUnmount(stopDomainPoll)

async function saveDomain() {
  const wanted = domainInput.value.trim()
  if (!wanted) return
  domainBusy.value = true
  domainError.value = null
  try {
    const res = await authFetch(`/vms/${id.value}/domain`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ domain: wanted }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    domain.value = await res.json()
    domainInput.value = ''
  }
  catch (e) {
    domainError.value = e instanceof Error ? e.message : 'Could not record that domain'
  }
  finally {
    domainBusy.value = false
  }
}

async function verifyDomain() {
  domainBusy.value = true
  domainError.value = null
  try {
    const res = await authFetch(`/vms/${id.value}/domain/verify`, { method: 'POST' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    domain.value = await res.json()
    startDomainPoll()
  }
  catch (e) {
    domainError.value = e instanceof Error ? e.message : 'Could not start the check'
  }
  finally {
    domainBusy.value = false
  }
}

async function removeDomain() {
  domainBusy.value = true
  domainError.value = null
  try {
    const res = await authFetch(`/vms/${id.value}/domain`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    domain.value = null
    stopDomainPoll()
  }
  catch (e) {
    domainError.value = e instanceof Error ? e.message : 'Could not remove the domain'
  }
  finally {
    domainBusy.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-6 sm:px-6 sm:py-8">
    <NuxtLink
      to="/vms"
      class="inline-flex items-center gap-1.5 font-mono text-xs text-muted-foreground underline-offset-4 transition-colors hover:text-foreground hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
    >
      <ArrowLeft class="size-3.5" aria-hidden="true" />
      All VMs
    </NuxtLink>

    <Alert v-if="error" variant="destructive" class="mt-4">
      <AlertTitle>Could not load this VM</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <div v-else-if="loading" class="mt-3 space-y-4" aria-busy="true">
      <p class="sr-only">Loading this VM…</p>
      <Skeleton class="h-9 w-64" aria-hidden="true" />
      <Skeleton class="h-56 w-full rounded-lg" aria-hidden="true" />
      <Skeleton class="h-64 w-full rounded-lg" aria-hidden="true" />
    </div>

    <template v-else-if="vm">
      <div class="mt-3 flex flex-wrap items-end justify-between gap-3">
        <div>
          <p class="eyebrow mb-1 text-primary-text">// vms</p>
          <div class="flex flex-wrap items-center gap-3">
            <h1 class="font-mono text-xl font-semibold tracking-tight sm:text-2xl">
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

      <Alert v-if="actionError" variant="destructive" class="mt-3">
        <AlertTitle>Action failed</AlertTitle>
        <AlertDescription>{{ actionError }}</AlertDescription>
      </Alert>

      <Alert v-if="vm.status === 'failed' && vm.last_error" variant="destructive" class="mt-3">
        <AlertTitle>This VM failed</AlertTitle>
        <AlertDescription>{{ vm.last_error }}</AlertDescription>
      </Alert>

      <section aria-labelledby="details-heading" class="mt-4 rounded-lg border border-border p-4">
        <h2 id="details-heading" class="text-sm font-semibold">Details</h2>
        <dl class="mt-3 grid gap-x-6 gap-y-3 sm:grid-cols-2 lg:grid-cols-3">
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

      <section aria-labelledby="ports-heading" class="mt-4 rounded-lg border border-border p-4">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h2 id="ports-heading" class="text-sm font-semibold">Access</h2>
            <p class="mt-0.5 max-w-2xl text-xs text-muted-foreground">
              Which ports inside this VM are reachable, where a request goes when it does not pick
              one, and which account a session logs into. Changes reach the host straight away.
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

        <dl class="mt-3 grid gap-x-6 gap-y-3 sm:grid-cols-2">
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
          <div class="sm:col-span-2">
            <dt class="eyebrow flex items-center gap-2 text-muted-foreground">
              Session user
              <Dialog v-model:open="userOpen">
                <Button
                  variant="ghost"
                  size="icon"
                  class="size-6"
                  aria-label="Edit the session user"
                  @click="openUser"
                >
                  <Pencil class="size-3.5" aria-hidden="true" />
                </Button>
                <DialogContent>
                  <DialogHeader>
                    <DialogTitle>Edit session user</DialogTitle>
                    <DialogDescription>
                      The account an SSH or console session logs into on
                      <span class="font-mono text-foreground">{{ vm.name }}</span>. Leave it blank to
                      follow whatever the OS image declared.
                    </DialogDescription>
                  </DialogHeader>
                  <form class="space-y-4" :aria-busy="savingUser" @submit.prevent="saveUser">
                    <div class="space-y-2">
                      <Label for="vm-user">Session user <span class="text-muted-foreground">(optional)</span></Label>
                      <Input
                        id="vm-user"
                        v-model="userForm.default_user"
                        autocomplete="off"
                        spellcheck="false"
                        class="font-mono text-sm"
                        placeholder="follow the OS image"
                        aria-describedby="vm-user-hint"
                      />
                      <p id="vm-user-hint" class="text-xs text-muted-foreground">
                        The account has to already exist inside the VM. This changes where a session
                        lands, and takes effect on the next one — it does not change the user this
                        VM's workload runs as, which is fixed when the VM is created.
                      </p>
                    </div>

                    <FormError id="vm-user-error" :message="userError" />

                    <DialogFooter>
                      <DialogClose as-child>
                        <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
                      </DialogClose>
                      <Button type="submit" class="font-mono text-xs" :disabled="savingUser">
                        {{ savingUser ? 'Saving…' : 'Save' }}
                      </Button>
                    </DialogFooter>
                  </form>
                </DialogContent>
              </Dialog>
            </dt>
            <dd class="mt-1 font-mono text-sm">
              {{ vm.default_user || 'from the OS image' }}
            </dd>
          </div>
        </dl>


        <div class="mt-4 flex flex-wrap items-center justify-between gap-2">
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

        <div v-if="vm.status === 'running'" class="mt-4 rounded-md border border-border p-3">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div class="flex items-center gap-2">
              <Monitor class="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
              <span class="text-sm font-medium">Remote desktop</span>
            </div>
            <div class="flex flex-wrap items-center gap-2">
              <Button
                v-if="!rdp"
                variant="outline"
                size="sm"
                class="text-xs"
                :disabled="rdpBusy"
                @click="loadRdp"
              >
                Show connection details
              </Button>
              <template v-else>
                <Button
                  variant="outline"
                  size="sm"
                  class="text-xs"
                  @click="downloadRdpFile"
                >
                  <Download class="size-4" aria-hidden="true" />
                  Download .rdp
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  class="text-xs"
                  :disabled="rdpBusy"
                  :title="`Issue a new password for ${vm.name}; the current one stops working`"
                  @click="rotateRdp"
                >
                  <RefreshCw class="size-4" aria-hidden="true" />
                  Rotate password
                </Button>
              </template>
            </div>
          </div>

          <p v-if="!rdp && !rdpError" class="mt-2 text-xs text-muted-foreground">
            Connect with any RDP client. The username and password are issued by this
            server and reach only this VM &mdash; they are not the credentials inside it.
          </p>

          <p v-if="rdpError" class="mt-2 text-xs text-destructive" role="alert">
            {{ rdpError }}
          </p>

          <dl v-if="rdp" class="mt-3 grid gap-2 sm:grid-cols-3">
            <div>
              <dt class="eyebrow text-muted-foreground">Computer</dt>
              <dd class="mt-1 flex min-w-0 items-center gap-1">
                <code class="truncate font-mono text-xs">{{ rdp.host }}:{{ rdp.port }}</code>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  class="shrink-0"
                  aria-label="Copy the address to the clipboard"
                  @click="copyRdp('address')"
                >
                  <component :is="rdpCopied === 'address' ? Check : Copy" aria-hidden="true" />
                </Button>
              </dd>
            </div>
            <div>
              <dt class="eyebrow text-muted-foreground">Username</dt>
              <dd class="mt-1 flex min-w-0 items-center gap-1">
                <code class="truncate font-mono text-xs">{{ rdp.username }}</code>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  class="shrink-0"
                  aria-label="Copy the username to the clipboard"
                  @click="copyRdp('username')"
                >
                  <component :is="rdpCopied === 'username' ? Check : Copy" aria-hidden="true" />
                </Button>
              </dd>
            </div>
            <div>
              <dt class="eyebrow text-muted-foreground">Password</dt>
              <dd class="mt-1 flex min-w-0 items-center gap-1">
                <code class="truncate font-mono text-xs">
                  {{ rdpRevealed ? rdp.password : '\u2022'.repeat(rdp.password.length) }}
                </code>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  class="shrink-0"
                  :aria-label="rdpRevealed ? 'Hide the password' : 'Show the password'"
                  @click="rdpRevealed = !rdpRevealed"
                >
                  <component :is="rdpRevealed ? EyeOff : Eye" aria-hidden="true" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  class="shrink-0"
                  aria-label="Copy the password to the clipboard"
                  @click="copyRdp('password')"
                >
                  <component :is="rdpCopied === 'password' ? Check : Copy" aria-hidden="true" />
                </Button>
              </dd>
            </div>
          </dl>

          <span role="status" aria-live="polite" class="sr-only">
            {{ rdpCopied ? `Remote desktop ${rdpCopied} copied to clipboard` : '' }}
          </span>
        </div>
      </section>

      <section aria-labelledby="domain-heading" class="mt-4 rounded-lg border border-border p-4">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h2 id="domain-heading" class="text-sm font-semibold">Custom domain</h2>
            <p class="mt-0.5 max-w-2xl text-xs text-muted-foreground">
              A domain of your own that answers to this VM, with its own certificate. This VM already
              answers to <span class="font-mono text-foreground">{{ vm.name }}</span>; a custom domain
              is served alongside that, not instead of it.
            </p>
          </div>
          <Badge v-if="domain" :variant="domainStatusVariant[domain.status]" class="font-mono text-xs">
            {{ domainStatusLabel[domain.status] }}
          </Badge>
        </div>

        <Alert v-if="domainError" variant="destructive" class="mt-3">
          <AlertDescription>{{ domainError }}</AlertDescription>
        </Alert>

        <form v-if="!domain" class="mt-3 flex flex-wrap items-end gap-2" @submit.prevent="saveDomain">
          <div class="min-w-0 flex-1 space-y-2">
            <Label for="custom-domain">Domain</Label>
            <Input
              id="custom-domain"
              v-model="domainInput"
              class="font-mono"
              placeholder="www.example.com"
              autocomplete="off"
              spellcheck="false"
            />
          </div>
          <Button type="submit" size="sm" :disabled="domainBusy || !domainInput.trim()">
            {{ domainBusy ? 'Saving…' : 'Add' }}
          </Button>
        </form>

        <template v-else>
          <dl class="mt-3 grid gap-x-6 gap-y-3 sm:grid-cols-2">
            <div>
              <dt class="eyebrow text-muted-foreground">Domain</dt>
              <dd class="mt-1 font-mono text-sm break-all">
                <a
                  v-if="domain.url"
                  :href="domain.url"
                  target="_blank"
                  rel="noopener"
                  class="inline-flex items-center gap-1.5 underline-offset-4 hover:underline"
                >
                  {{ domain.domain }}
                  <ExternalLink class="size-3.5" aria-hidden="true" />
                </a>
                <span v-else>{{ domain.domain }}</span>
              </dd>
            </div>
            <div v-if="domain.cert_not_after">
              <dt class="eyebrow text-muted-foreground">Certificate valid until</dt>
              <dd class="mt-1 text-sm text-muted-foreground">{{ fmtDate(domain.cert_not_after) }}</dd>
            </div>
          </dl>

          <div v-if="domain.status !== 'active'" class="mt-3 rounded-md border border-border bg-muted/40 p-3">
            <p class="text-xs text-muted-foreground">
              Create this record at whoever holds your domain, then confirm it below. It has to be a
              CNAME: an A record pointing at the same address is not accepted, because the CNAME is
              what keeps the name following this VM if it moves.
            </p>
            <dl class="mt-3 grid gap-x-6 gap-y-2 sm:grid-cols-3">
              <div>
                <dt class="eyebrow text-muted-foreground">Type</dt>
                <dd class="mt-1 font-mono text-sm">CNAME</dd>
              </div>
              <div class="min-w-0">
                <dt class="eyebrow text-muted-foreground">Name</dt>
                <dd class="mt-1 font-mono text-sm break-all">{{ domain.cname_name }}</dd>
              </div>
              <div class="min-w-0">
                <dt class="eyebrow text-muted-foreground">Points to</dt>
                <dd class="mt-1 font-mono text-sm break-all">{{ domain.cname_target || '—' }}</dd>
              </div>
            </dl>
          </div>

          <p v-if="domain.last_error" class="mt-3 text-xs text-destructive">{{ domain.last_error }}</p>
          <p v-else-if="domainSettling" class="mt-3 text-xs text-muted-foreground">
            Processing. This page keeps checking; a certificate usually lands within a minute of the
            CNAME being visible.
          </p>

          <div class="mt-3 flex flex-wrap items-center gap-2">
            <Button
              v-if="domain.status !== 'active'"
              size="sm"
              :disabled="domainBusy || domainSettling"
              @click="verifyDomain"
            >
              <RefreshCw class="size-4" aria-hidden="true" />
              {{ domainSettling ? 'Checking…' : 'I have added the CNAME' }}
            </Button>
            <Button variant="outline" size="sm" :disabled="domainBusy" @click="removeDomain">
              <Trash2 class="size-4" aria-hidden="true" />
              Remove
            </Button>
          </div>
        </template>
      </section>

      <section aria-labelledby="targets-heading" class="mt-4 rounded-lg border border-border">
        <div class="flex flex-wrap items-start justify-between gap-3 p-4">
          <div>
            <h2 id="targets-heading" class="text-sm font-semibold">Allowed destinations</h2>
            <p class="mt-0.5 max-w-2xl text-xs text-muted-foreground">
              This list is the whole of what this VM can reach. Nothing else leaves it, and a
              domain that is not here will not even resolve.
            </p>
          </div>
          <div class="flex flex-wrap items-center gap-2">
          <Dialog v-if="!allowAll" v-model:open="openAllOpen" @update:open="(v: boolean) => !v && resetOpenAllForm()">
            <Button size="sm" variant="outline" class="font-mono text-xs" @click="resetOpenAllForm(); openAllOpen = true">
              <Globe class="size-4" aria-hidden="true" />
              Allow everything
            </Button>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Allow everything for a while</DialogTitle>
                <DialogDescription>
                  Records {{ everywhere }} on every transport, which takes this VM off the allowlist
                  altogether: every address is reachable and every name resolves, DNS included. It is
                  withdrawn automatically when the TTL runs out.
                </DialogDescription>
              </DialogHeader>

              <form class="space-y-4" :aria-busy="openingAll" @submit.prevent="openEverything">
                <div class="space-y-2">
                  <Label for="oa-ttl">TTL (seconds)</Label>
                  <Input
                    id="oa-ttl"
                    v-model="openAllTTL"
                    type="number"
                    min="10"
                    step="1"
                    inputmode="numeric"
                    required
                    aria-describedby="oa-ttl-hint"
                  />
                  <p id="oa-ttl-hint" class="text-xs text-muted-foreground">
                    Required here — this button will not leave a VM open forever. Removing the
                    {{ everywhere }} row closes it again sooner.
                  </p>
                </div>
                <div class="space-y-2">
                  <Label for="oa-note">Note</Label>
                  <Input id="oa-note" v-model="openAllNote" placeholder="why this is needed" />
                </div>

                <FormError id="open-all-error" :message="openAllError" />

                <DialogFooter>
                  <DialogClose as-child>
                    <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
                  </DialogClose>
                  <Button type="submit" class="font-mono text-xs" :disabled="openingAll">
                    {{ openingAll ? 'Opening…' : 'Allow everything' }}
                  </Button>
                </DialogFooter>
              </form>
            </DialogContent>
          </Dialog>

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
            <Button size="sm" class="font-mono text-xs" @click="resetTargetForm(); addOpen = true">
              <Plus class="size-4" aria-hidden="true" />
              Add
            </Button>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>{{ editingId ? 'Edit destination' : 'Add a destination' }}</DialogTitle>
                <DialogDescription>
                  A domain, an IP address, or a CIDR range this VM should be able to reach.
                  <template v-if="editingId">
                    Saving rewrites the entry and restarts any TTL on it.
                  </template>
                </DialogDescription>
              </DialogHeader>

              <form class="space-y-4" :aria-busy="adding" @submit.prevent="saveTarget">
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
                    <template v-if="editingId">{{ adding ? 'Saving…' : 'Save' }}</template>
                    <template v-else>{{ adding ? 'Adding…' : 'Add' }}</template>
                  </Button>
                </DialogFooter>
              </form>
            </DialogContent>
          </Dialog>
          </div>
        </div>

        <Alert v-if="allowAll" variant="destructive" class="mx-4 mb-4 w-auto">
          <AlertTitle>Everything is allowed right now</AlertTitle>
          <AlertDescription>
            This VM can reach any address and resolve any name for another
            {{ timeLeft(allowAll.expires_at) || 'unknown amount of time' }}. The rest of this list is
            not being enforced. Remove the {{ everywhere }} row to close it now.
          </AlertDescription>
        </Alert>

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
              <div class="flex items-center justify-end gap-1">
                <Button
                  variant="ghost"
                  size="icon"
                  :aria-label="`Edit destination ${t.destination}`"
                  @click="openEditTarget(t)"
                >
                  <Pencil class="size-4" aria-hidden="true" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  class="text-destructive hover:text-destructive"
                  :aria-label="`Remove destination ${t.destination}`"
                  @click="toRemove = t"
                >
                  <Trash2 class="size-4" aria-hidden="true" />
                </Button>
              </div>
            </TableCell>
          </TableRow>
        </DataTable>
      </section>

      <section aria-labelledby="denied-heading" class="mt-4 rounded-lg border border-border">
        <div class="flex flex-wrap items-start justify-between gap-3 p-4">
          <div>
            <h2 id="denied-heading" class="text-sm font-semibold">Denied</h2>
            <p class="mt-0.5 max-w-2xl text-xs text-muted-foreground">
              Lookups the resolver refused and connections the ruleset dropped, over
              {{ deniedWindowLabel }}. A row in green is already covered by the list above — it was
              denied before that allowance existed.
            </p>
          </div>
          <div class="flex items-center gap-2">
            <Label for="denied-seconds" class="font-mono text-xs whitespace-nowrap text-muted-foreground">
              Last
            </Label>
            <Input
              id="denied-seconds"
              v-model="deniedSeconds"
              type="number"
              min="1"
              step="1"
              inputmode="numeric"
              placeholder="all"
              class="h-8 w-24 font-mono text-xs"
              aria-label="Show denied attempts from the last n seconds"
              @change="refreshDenied"
              @keydown.enter.prevent="refreshDenied"
            />
            <span class="font-mono text-xs text-muted-foreground">s</span>
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
        </div>

        <p v-if="!deniedFirstLoad && !deniedAvailable" class="px-4 pb-4 text-xs text-muted-foreground">
          Neither record could be read, so nothing is being reported here. This says nothing about
          whether this VM has been blocked.
        </p>

        <div v-else-if="deniedLoading && deniedFirstLoad" class="space-y-2 px-4 pb-4" aria-busy="true">
          <p class="sr-only">Loading denied attempts…</p>
          <Skeleton v-for="n in 3" :key="n" class="h-8 w-full" aria-hidden="true" />
        </div>

        <template v-else>
          <p v-if="!deniedRecording.lookups" class="px-4 pb-3 text-xs text-muted-foreground">
            Refused lookups are not being recorded, so names this VM could not resolve are missing
            from this list. Everything the ruleset dropped is still shown.
          </p>
          <p v-if="!deniedRecording.packets" class="px-4 pb-3 text-xs text-muted-foreground">
            Dropped connections are not being recorded, so only names the resolver refused are shown.
          </p>

          <DataTable label="Denied" :columns="deniedColumns" :empty="!denied.length" :frame="false">
            <template #empty>
              Nothing has been denied.
            </template>
            <TableRow
              v-for="d in denied"
              :key="`${d.kind}-${d.domain}-${d.address}-${d.proto}-${d.port}`"
              :class="alreadyAllowed.has(d) && 'bg-primary/10 hover:bg-primary/15'"
            >
              <TableCell class="font-mono break-all">
                {{ deniedDestination(d) }}
                <span v-if="d.domain && d.address" class="block text-xs text-muted-foreground">
                  {{ d.address }}
                </span>
              </TableCell>
              <TableCell class="text-xs">
                <span class="font-mono whitespace-nowrap">{{ deniedWhat(d) }}</span>
                <span
                  v-if="d.signature"
                  class="block max-w-[22rem] truncate text-muted-foreground"
                  :title="d.signature"
                >
                  {{ d.signature }}
                </span>
              </TableCell>
              <TableCell class="text-right font-mono tabular-nums">{{ d.attempts }}</TableCell>
              <TableCell class="text-right text-xs whitespace-nowrap text-muted-foreground">
                {{ fmtShortDate(d.last_seen) }}
              </TableCell>
              <TableCell class="text-right whitespace-nowrap">
                <span v-if="alreadyAllowed.has(d)" class="font-mono text-xs text-primary-text">
                  allowed
                </span>
                <Button
                  v-else
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
