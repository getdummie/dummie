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
  // When a temporary VM is due to be destroyed, and "" for one with no limit.
  // Read from the pending scheduled task -- the deadline is not a column on the
  // VM, so this is empty the moment the expiry is cancelled or has run.
  expires_at: string
}

interface Target {
  id: string
  destination: string
  kind: 'domain' | 'ip'
  transport: '' | 'tcp' | 'udp' | 'any'
  // Suricata's port syntax for an address. For a domain it is which of the two
  // web ports the name is allowed on -- '' for both, or 'none', which lets the
  // name resolve and grants nothing.
  ports: string
  note: string
  created_at: string
  // When a temporary allowance is due to be withdrawn, and "" for a permanent
  // one. Same source as the VM's: the scheduled task that will do it.
  expires_at: string
}

// What a domain row's ports field can say. Only 80 and 443 because those are the
// ports suricata is told to look for http and tls on, and a rule on any other
// would load and never match.
//
// "both" is sent as '80,443' rather than as the empty string the server also
// accepts for it. Not a preference: reka-ui reserves '' to mean "nothing is
// selected", so an item carrying it throws on mount and takes the whole dropdown --
// and the dialog around it -- with it. The server normalises '80,443' to '' anyway,
// so nothing downstream can tell the difference.
// domainPortChoices and portPresets now live in lib/targets, shared with the
// new-VM dialog's starting allowlist.

// Something this VM tried to do that policy stopped. Two shapes: a 'lookup' the
// resolver refused, which never became a packet at all, and a 'packet' the ruleset
// dropped, which carries where it was going. Attempts is per destination over the
// window the server queries, not per packet.
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
// Whether the answer above is trustworthy. An empty list means two different
// things -- nothing was denied, or nothing is collecting events -- and the
// panel has to be able to say which.
const deniedAvailable = ref(true)
// Which of the two records answered. They fail independently — dropped packets come
// from suricata, refused lookups from the resolver's log and its own migration — so
// "nothing is recorded" and "half of it is recorded" are different things to say.
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
    // Not awaited with the rest: this one reads clickhouse, and a slow or
    // missing event store must not hold up the page it is a panel on.
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
// Skeletons stand in for a table that is not there yet, not for one being re-read.
// Without this, refreshing throws the rows away and puts them back, which reads as
// the list having changed when it has not.
const deniedFirstLoad = ref(true)

// Never throws: a failure here reports itself in the panel rather than
// replacing the whole page with an error, since nothing else on it depends on
// the event store being reachable.
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

// refreshDenied is loadDenied with a floor on how briefly the spinner may appear.
//
// The read is usually faster than a frame, so without this the icon's state changes
// and changes back inside one paint and the button looks dead -- the user cannot
// tell a refresh that returned the same rows from a click that did nothing. The
// delay is feedback, not work.
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
    { key: 'vscode-insiders', label: 'VS Code Insiders', href: vscodeRemote('vscode-insiders') },
    { key: 'vscodium', label: 'VSCodium', href: vscodeRemote('vscodium') },
    { key: 'cursor', label: 'Cursor', href: vscodeRemote('cursor') },
    { key: 'windsurf', label: 'Windsurf', href: vscodeRemote('windsurf') },
    { key: 'antigravity', label: 'Antigravity', href: vscodeRemote('antigravity') },
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
const form = reactive(blankTarget())

function resetTargetForm() {
  Object.assign(form, blankTarget())
  addError.value = null
}

// Change handlers rather than watchers on form.kind, and that is not a style
// choice: a watcher flushes after the current call stack, so prefilling the form
// from a denial -- which sets the kind and then the fields that go with it -- would
// have the reset land afterwards and wipe them. These only run when someone
// actually operates the control.
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

// --- reading a denial -------------------------------------------------------

// What the guest was trying to reach, preferring the name when one was seen. A
// blocked https request has both; an ssh to a bare address has only the address.
function deniedDestination(d: DeniedAttempt) {
  return d.domain || d.address || '—'
}

// The name of the thing on that port, so a row reads "ssh" rather than "tcp 22".
// Falls back to what suricata identified the traffic as, then to the numbers.
//
// Two sources because they know different things: suricata names the protocol only
// once it has seen payload, and most of these are dropped at the handshake with no
// payload at all -- but the port is in the packet either way.
function deniedWhat(d: DeniedAttempt) {
  if (d.kind === 'lookup') return 'dns lookup'
  if (d.proto === 'icmp') return 'ping (icmp)'

  const preset = portPresets.find(p => p.transport === d.proto && p.ports === String(d.port))
  const named = preset ? preset.label.replace(/ \(.*\)$/, '') : ''
  // 'failed' is suricata saying detection ran and found nothing, which is not a
  // protocol name and would read as an error in the machinery rather than a fact
  // about the traffic.
  const identified = d.app_proto && d.app_proto !== 'failed' ? d.app_proto : ''

  const label = named || identified
  const where = `${d.proto} ${d.port}`
  return label ? `${label} — ${where}` : where
}

// Opens the add form already filled in from a denial, which is the whole point of
// showing these: the row a user reads and then has to retype somewhere else is the
// row they will get wrong.
//
// A refused lookup can only become a name allowance -- there is no address to offer,
// because nothing was ever sent. A dropped packet becomes an address allowance on
// the transport and port it was actually using, except on 443 or 80 where a name
// was seen: there the name is the better allowance, since it does not grant the
// whole address.
function allowDenied(d: DeniedAttempt) {
  resetTargetForm()
  const byName = d.kind === 'lookup' || (!!d.domain && (d.port === 443 || d.port === 80))

  if (byName) {
    form.kind = 'domain'
    form.destination = d.domain
    // A refused lookup says nothing about which port the guest wanted, so it gets
    // both web ports; a blocked request names the one it was actually using.
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

// What the row is actually checked against. A lookup-only domain is the one worth
// spelling out: it is in the list, and it grants no access at all.
function matchedOn(t: Target) {
  if (t.kind !== 'domain') return 'address'
  return t.ports === 'none' ? 'name — resolves only' : 'tls sni · http host'
}

function portsLabel(t: Target) {
  if (t.kind !== 'domain') return t.ports || 'any'
  return t.ports === 'none' ? 'none' : (t.ports || '443, 80')
}

// --- allow a hostname on a port that is not 443 or 80 ----------------------
//
// ssh, postgres and everything else carry no destination name in the traffic, so
// there is nothing for a rule to check a hostname against and the access has to be
// granted by address. This turns the name a user is thinking of into the two rows
// that express it: the name itself as a lookup-only domain so the guest can
// resolve it, and one address row per answer.
//
// Deliberately a one-time helper rather than something that re-resolves. Addresses
// move, and a list that quietly followed them would be an allowlist nobody had
// read.
const resolveOpen = ref(false)
const resolveHost = ref('')
const resolvePreset = ref('ssh')
// Used when the preset is 'custom'. The presets are a shortcut for the ports people
// reach for most, not the limit of what an allowance can say -- so the raw transport
// and ports are reachable here too, exactly as they are in the main form.
const resolveTransport = ref('tcp')
const resolvePorts = ref('')
const resolving = ref(false)
const resolveError = ref<string | null>(null)
const resolveAddresses = ref<string[]>([])
const resolveChosen = ref<string[]>([])
const resolveTruncated = ref(false)
const addingResolved = ref(false)
// Seconds, '0' for permanent, matching the main form. Kept separate from it
// because both dialogs can be filled in before either is submitted.
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

// A ticking clock for the countdowns on this page. Coarse: the labels are in
// minutes, so a faster tick would re-render for no visible change.
const nowMs = ref(Date.now())
let ttlClock: ReturnType<typeof setInterval> | null = null
onMounted(() => {
  ttlClock = setInterval(() => { nowMs.value = Date.now() }, 30_000)
})
onBeforeUnmount(() => {
  if (ttlClock) clearInterval(ttlClock)
})

// How long is left, or that the deadline has passed. Past it the row says
// "overdue" rather than counting up: the allowance is still in force, and the
// control plane has not caught up -- which is exactly what a reader needs to know.
// The bounds the server enforces, checked here so a typo is caught before the
// request. Both dialogs that take a TTL use this rather than each spelling the
// numbers out.
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

// What the address rows will actually say, whichever way the user got there. One
// place so the rows written and the note describing them cannot disagree.
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
    // Pre-checked: the user asked for this name, and unchecking is the rarer
    // intention than checking all of them one by one.
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
  // A duplicate is not a failure here: this flow writes several rows and one of
  // them already being on the list is the normal result of running it twice.
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
    // The lookup-only row first. Without it the guest cannot resolve the name, and
    // the address rows below would only be reachable by typing an IP.
    // The same TTL on every row this flow writes, including the lookup-only one:
    // leaving the name resolvable after its addresses expire would be an allowance
    // that half-survives, which is harder to reason about than either outcome.
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
          <!-- Only for a temporary VM. A "TTL: none" row on every other VM
               would be a field nobody reads, and this one has to be read. -->
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
             others: it is a command to run elsewhere, shown in full so it can be
             read as well as copied, while the rest navigate somewhere. The left
             group is rendered even when there is no hostname so the others stay
             right-aligned without it. -->
        <div class="mt-6 flex flex-wrap items-center justify-between gap-2">
          <div class="flex min-w-0 items-center gap-2">
            <!-- Only when there is a hostname: without a domain there is
                 nothing to ssh to, and a bare name would show a command that
                 fails. -->
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
            <!-- The two web frames and a shell in one screen. A new tab for the
                 same reason the console is: it holds a live session. -->
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
              This list is the whole of what this VM can reach. Nothing else leaves it, and a
              domain that is not here will not even resolve.
            </p>
          </div>
          <div class="flex flex-wrap items-center gap-2">
          <!-- ssh, postgres and the rest carry no hostname for a rule to check, so
               they are allowed by address. This turns a name into those rows. -->
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

                <!-- The presets are a shortcut for the common ports, not the limit
                     of what can be allowed. -->
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
                  <!-- icmp has no ports: its type and code sit where a port would
                       be, and a rule header cannot address them. -->
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

                <!-- A domain chooses between the two ports its name can be
                     checked on, or neither. Not a free port field: the ports
                     suricata looks for http and tls on come from a per-host
                     config, so a rule on 8443 would load and never match. -->
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

                <!-- Transport and ports exist only for an address. A domain is
                     matched by the name in the traffic, and the header of the
                     rule that does it names no address. -->
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
                    <!-- icmp has no ports: its type and code sit where a port
                         would be, and a rule header cannot address them. -->
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
            <!-- An em dash, not 'any': transport does not apply to a domain row at
                 all, and 'any' would read as "every transport is allowed". -->
            <TableCell class="font-mono text-muted-foreground">{{ t.transport || '—' }}</TableCell>
            <TableCell class="font-mono text-muted-foreground">{{ portsLabel(t) }}</TableCell>
            <TableCell class="text-muted-foreground">{{ t.note || '—' }}</TableCell>
            <!-- An em dash for a permanent allowance, which is most of them. The
                 ones with a deadline are the ones worth reading. -->
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

      <!-- denied egress -->
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
          <!-- This list changes on its own as the guest keeps trying, so it is the
               one panel on the page worth re-reading without reloading everything. -->
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

        <!-- Said plainly rather than shown as an empty table: with nothing
             recording, "nothing was denied" and "nothing is being recorded" look
             identical, and only one of them is reassuring. -->
        <p v-if="!deniedFirstLoad && !deniedAvailable" class="px-4 pb-6 text-sm text-muted-foreground sm:px-6">
          Neither record could be read, so nothing is being reported here. This says nothing about
          whether this VM has been blocked.
        </p>

        <div v-else-if="deniedLoading && deniedFirstLoad" class="space-y-2 px-4 pb-6 sm:px-6" aria-busy="true">
          <p class="sr-only">Loading denied attempts…</p>
          <Skeleton v-for="n in 3" :key="n" class="h-8 w-full" aria-hidden="true" />
        </div>

        <template v-else>
          <!-- One record readable and the other not is a partial list, and saying
               which half is missing is the difference between a list that can be
               trusted and one that merely looks complete. -->
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
                <!-- Both, when both are known: the name is what the user recognises
                     and the address is what the packet was actually going to. -->
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
