<script setup lang="ts">
import { ArrowLeft, Bot, Check, ChevronDown, Columns2, Copy, Download, Eye, EyeOff, ExternalLink, Info, Monitor, Pencil, RefreshCw, SquareTerminal, Terminal, Trash2 } from '@lucide/vue'
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
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'

definePageMeta({ middleware: ['auth'] })

interface VM {
  id: string
  client_id: string
  vm_id: string
  name: string
  default_port: number
  public_ports: number[]
  default_user: string
  kernel_id: string
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

const route = useRoute()
const { authFetch } = useAuth()
const id = computed(() => String(route.params.id))

const vm = ref<VM | null>(null)
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
    loadDomain()
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load this VM'
  }
  finally {
    loading.value = false
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

// The integration proxy lives under the same fleet domain the VM answers on.
const integrationHostForVM = computed(() =>
  sshDomain.value ? `github.int.${sshDomain.value}` : '',
)

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

const sizeOpen = ref(false)
const savingSize = ref(false)
const sizeError = ref<string | null>(null)
const sizeForm = reactive({ cpus: '1', memory_mib: '512', disk_size: '', kernel_id: '' })

interface Kernel {
  id: string
  name: string
  description: string
}
const kernels = ref<Kernel[]>([])
const kernelsError = ref<string | null>(null)
const selectedKernel = computed(() => kernels.value.find(k => k.id === sizeForm.kernel_id) ?? null)
const currentKernelListed = computed(() => !!vm.value?.kernel_id && kernels.value.some(k => k.id === vm.value?.kernel_id))
const kernelsLoading = ref(false)
const kernelLabel = computed(() => {
  if (selectedKernel.value) return selectedKernel.value.name
  if (kernelsLoading.value) return 'Loading…'
  if (sizeForm.kernel_id) return 'withdrawn kernel'
  return 'custom kernel (not from the catalogue)'
})

async function loadKernels() {
  kernelsError.value = null
  kernelsLoading.value = true
  try {
    const res = await authFetch('/vms/kernels')
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    kernels.value = (await res.json()).items ?? []
  }
  catch (e) {
    kernelsError.value = e instanceof Error ? e.message : 'Could not load kernels'
  }
  finally {
    kernelsLoading.value = false
  }
}

const resizable = computed(() => vm.value?.status === 'running' || vm.value?.status === 'stopped')

function sizeToMiB(s: string): number | null {
  const v = s.trim()
  if (!v) return 0
  const m = /^(\d+)([KkMmGgTt]?)$/.exec(v)
  if (!m) return null
  const mult: Record<string, number> = { '': 1, k: 1 << 10, m: 1 << 20, g: 1 << 30, t: 2 ** 40 }
  const bytes = Number(m[1]) * (mult[m[2]!.toLowerCase()] ?? 1)
  return Math.ceil(bytes / (1 << 20))
}

function miBToSize(mib: number) {
  if (!mib) return ''
  return mib % 1024 === 0 ? `${mib / 1024}G` : `${mib}M`
}

function openSize() {
  if (!vm.value) return
  sizeForm.cpus = String(vm.value.cpus || 1)
  sizeForm.memory_mib = String(vm.value.memory_mib || 512)
  sizeForm.disk_size = miBToSize(vm.value.disk_mib)
  sizeForm.kernel_id = vm.value.kernel_id || ''
  sizeError.value = null
  sizeOpen.value = true
  if (vm.value.boot === 'direct') loadKernels()
}

async function saveSize() {
  const cpus = Number(sizeForm.cpus)
  const memory = Number(sizeForm.memory_mib)
  if (!Number.isInteger(cpus) || cpus < 1) {
    sizeError.value = 'vCPU must be a whole number of at least 1.'
    return
  }
  if (!Number.isInteger(memory) || memory < 64) {
    sizeError.value = 'Memory must be at least 64 MiB.'
    return
  }
  const disk = sizeToMiB(sizeForm.disk_size)
  if (disk === null) {
    sizeError.value = 'Disk size must be a number, optionally with a K, M, G or T suffix — e.g. 4G.'
    return
  }
  if (disk && vm.value && disk < vm.value.disk_mib) {
    sizeError.value = `A disk can only be made bigger. This VM already has ${fmtMiB(vm.value.disk_mib)}.`
    return
  }

  savingSize.value = true
  sizeError.value = null
  try {
    const res = await authFetch(`/vms/${id.value}/size`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        cpus,
        memory_mib: memory,
        disk_size: sizeForm.disk_size.trim(),
        kernel_id: sizeForm.kernel_id,
      }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    vm.value = await res.json()
    sizeOpen.value = false
  }
  catch (e) {
    sizeError.value = e instanceof Error ? e.message : 'Could not save the changes'
    // A restart can fail after the size is saved, so the row may have moved.
    await load(true)
  }
  finally {
    savingSize.value = false
  }
}

const nowMs = ref(Date.now())
let ttlClock: ReturnType<typeof setInterval> | null = null
onMounted(() => {
  ttlClock = setInterval(() => { nowMs.value = Date.now() }, 10_000)
})
onBeforeUnmount(() => {
  if (ttlClock) clearInterval(ttlClock)
})

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


interface CustomDomain {
  domain: string
  status: 'pending_dns' | 'verifying' | 'issuing' | 'active' | 'failed'
  cname_name: string
  cname_host: string
  zone: string
  cname_target: string
  last_error?: string
  url?: string
  cert_not_after?: string
  cert_reused?: boolean
}

const domains = ref<CustomDomain[]>([])
const domainLimit = ref(5)
const domainReused = ref<string[]>([])
const domainInput = ref('')
const domainBusy = ref<string | null>(null)
const domainError = ref<string | null>(null)
let domainPoll: ReturnType<typeof setInterval> | null = null
const domainCopied = ref<{ domain: string, field: 'name' | 'value' } | null>(null)

async function copyDomainRecord(d: CustomDomain, field: 'name' | 'value', value: string) {
  try {
    await navigator.clipboard.writeText(value)
    domainCopied.value = { domain: d.domain, field }
    setTimeout(() => (domainCopied.value = null), 2000)
  }
  catch {
    domainError.value = 'Could not copy to the clipboard'
  }
}

function isCopied(d: CustomDomain, field: 'name' | 'value') {
  return domainCopied.value?.domain === d.domain && domainCopied.value.field === field
}

function isSettling(d: CustomDomain) {
  return d.status === 'verifying' || d.status === 'issuing'
}

function replaceDomain(d: CustomDomain) {
  const i = domains.value.findIndex(x => x.domain === d.domain)
  if (i === -1) domains.value.push(d)
  else domains.value[i] = d
}

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
  if (!res.ok) return
  const body: { domains: CustomDomain[], limit: number } = await res.json()
  domains.value = body.domains
  domainLimit.value = body.limit
  if (domains.value.some(isSettling)) startDomainPoll()
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
  domainBusy.value = ''
  domainError.value = null
  try {
    const res = await authFetch(`/vms/${id.value}/domain`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ domain: wanted }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    const added: CustomDomain = await res.json()
    replaceDomain(added)
    if (added.cert_reused) domainReused.value.push(added.domain)
    domainInput.value = ''
  }
  catch (e) {
    domainError.value = e instanceof Error ? e.message : 'Could not record that domain'
  }
  finally {
    domainBusy.value = null
  }
}

async function verifyDomain(d: CustomDomain) {
  domainBusy.value = d.domain
  domainError.value = null
  try {
    const res = await authFetch(`/vms/${id.value}/domain/${encodeURIComponent(d.domain)}/verify`, { method: 'POST' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    replaceDomain(await res.json())
    startDomainPoll()
  }
  catch (e) {
    domainError.value = e instanceof Error ? e.message : 'Could not start the check'
  }
  finally {
    domainBusy.value = null
  }
}

async function removeDomain(d: CustomDomain) {
  domainBusy.value = d.domain
  domainError.value = null
  try {
    const res = await authFetch(`/vms/${id.value}/domain/${encodeURIComponent(d.domain)}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    domains.value = domains.value.filter(x => x.domain !== d.domain)
    domainReused.value = domainReused.value.filter(x => x !== d.domain)
    if (!domains.value.some(isSettling)) stopDomainPoll()
  }
  catch (e) {
    domainError.value = e instanceof Error ? e.message : 'Could not remove the domain'
  }
  finally {
    domainBusy.value = null
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
            <p class="flex items-center gap-1.5 font-mono text-xs text-muted-foreground">
              {{ vm.cpus }} vCPU · {{ fmtMiB(vm.memory_mib) }} ·
              {{ vm.disk_mib ? `${fmtMiB(vm.disk_mib)} disk` : 'disk not recorded' }}
              <Dialog v-model:open="sizeOpen">
                <Button
                  variant="ghost"
                  size="icon"
                  class="size-6"
                  :disabled="!resizable"
                  :aria-label="resizable ? 'Edit this VM' : `Cannot edit this VM: it is ${vm.status}`"
                  @click="openSize"
                >
                  <Pencil class="size-3.5" aria-hidden="true" />
                </Button>
                <DialogContent>
                  <DialogHeader>
                    <DialogTitle>Edit VM</DialogTitle>
                    <DialogDescription>
                      What <span class="font-mono text-foreground">{{ vm.name }}</span> gets the next
                      time it boots.
                      <template v-if="isRunning">
                        Applying restarts it, so anything running inside it stops.
                      </template>
                      <template v-else>
                        This VM is stopped, so it picks the changes up on its next start.
                      </template>
                    </DialogDescription>
                  </DialogHeader>
                  <form class="space-y-4" :aria-busy="savingSize" @submit.prevent="saveSize">
                    <div class="grid gap-4 sm:grid-cols-2">
                      <div class="space-y-2">
                        <Label for="size-cpus">vCPU</Label>
                        <Input id="size-cpus" v-model="sizeForm.cpus" type="number" min="1" step="1" inputmode="numeric" />
                      </div>
                      <div class="space-y-2">
                        <Label for="size-mem">Memory (MiB)</Label>
                        <Input id="size-mem" v-model="sizeForm.memory_mib" type="number" min="64" step="64" inputmode="numeric" />
                      </div>
                    </div>
                    <div class="space-y-2">
                      <Label for="size-disk">Disk size</Label>
                      <Input id="size-disk" v-model="sizeForm.disk_size" placeholder="4G" aria-describedby="size-disk-hint" />
                      <p id="size-disk-hint" class="text-xs text-muted-foreground">
                        A disk can only grow. The guest grows its own filesystem into the new space
                        when it boots.
                      </p>
                    </div>
                    <div v-if="vm.boot === 'direct'" class="space-y-2">
                      <Label for="size-kernel">Kernel</Label>
                      <Select v-model="sizeForm.kernel_id" :disabled="!kernels.length">
                        <SelectTrigger id="size-kernel" class="w-full font-mono text-xs" aria-describedby="size-kernel-hint">
                          <SelectValue>{{ kernelLabel }}</SelectValue>
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem
                            v-if="vm.kernel_id && !currentKernelListed"
                            :value="vm.kernel_id"
                            disabled
                            class="font-mono text-xs"
                          >
                            current (withdrawn)
                          </SelectItem>
                          <SelectItem v-for="k in kernels" :key="k.id" :value="k.id" class="font-mono text-xs">
                            {{ k.name }}
                          </SelectItem>
                        </SelectContent>
                      </Select>
                      <p id="size-kernel-hint" class="text-xs text-muted-foreground">
                        <template v-if="kernelsError">{{ kernelsError }}</template>
                        <template v-else-if="selectedKernel?.description">{{ selectedKernel.description }}</template>
                        <template v-else-if="!vm.kernel_id">
                          This VM's kernel is not from the catalogue. Pick one to replace it.
                        </template>
                      </p>
                    </div>

                    <FormError id="size-error" :message="sizeError" />

                    <DialogFooter>
                      <DialogClose as-child>
                        <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
                      </DialogClose>
                      <Button type="submit" class="font-mono text-xs" :disabled="savingSize">
                        <template v-if="savingSize">
                          {{ isRunning ? 'Restarting…' : 'Applying…' }}
                        </template>
                        <template v-else>
                          {{ isRunning ? 'Apply & restart' : 'Apply' }}
                        </template>
                      </Button>
                    </DialogFooter>
                  </form>
                </DialogContent>
              </Dialog>
            </p>
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

      <section aria-labelledby="ports-heading" class="mt-4 rounded-lg border border-border p-4">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <h2 id="ports-heading" class="text-sm font-semibold">Access</h2>
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

        <TooltipProvider :delay-duration="150">
        <dl class="mt-3 grid gap-x-6 gap-y-3 sm:grid-cols-2">
          <div>
            <dt class="eyebrow flex items-center gap-1.5 text-muted-foreground">
              Default port
              <Tooltip>
                <TooltipTrigger as-child>
                  <button
                    type="button"
                    class="rounded-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                    aria-label="What the default port is"
                  >
                    <Info class="size-3.5" aria-hidden="true" />
                  </button>
                </TooltipTrigger>
                <TooltipContent class="max-w-xs">
                  The port your app listens on inside the VM. Open this VM's link in a browser and
                  it lands here.
                </TooltipContent>
              </Tooltip>
            </dt>
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
            <dt class="eyebrow flex items-center gap-1.5 text-muted-foreground">
              Public ports
              <Tooltip>
                <TooltipTrigger as-child>
                  <button
                    type="button"
                    class="rounded-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                    aria-label="What public ports are"
                  >
                    <Info class="size-3.5" aria-hidden="true" />
                  </button>
                </TooltipTrigger>
                <TooltipContent class="max-w-xs">
                  Any other ports you want reachable from the internet, on top of the default one.
                  Ports you do not list here stay private to the VM.
                </TooltipContent>
              </Tooltip>
            </dt>
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
        </TooltipProvider>


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
            <Button
              v-if="vm.console_url"
              as="a"
              variant="outline"
              size="sm"
              class="font-mono text-xs"
              :href="`/vms/${vm.id}/agent`"
              target="_blank"
              rel="noopener"
              :aria-disabled="vm.status !== 'running'"
              :class="vm.status !== 'running' && 'pointer-events-none opacity-50'"
              :title="vm.status === 'running'
                ? `Chat with a coding agent on ${vm.name}`
                : `Cannot open the agent: this VM is ${vm.status}`"
            >
              <Bot class="size-4" aria-hidden="true" />
              Agent
              <span class="sr-only">: open a coding agent chat in a new tab</span>
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
            Open this VM's desktop in any RDP client. (Only works if remote desktop server is
            already present in the VM)
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

      <VmIntegrations
        :vm-id="id"
        :vm-name="vm.name || vm.vm_id"
        :host="integrationHostForVM"
      />

      <section aria-labelledby="domain-heading" class="mt-4 rounded-lg border border-border p-4">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h2 id="domain-heading" class="text-sm font-semibold">Deploy to your domain</h2>
            <p class="mt-0.5 max-w-2xl text-xs text-muted-foreground">
              Use a domain you already own alongside
              <a
                v-if="vm.url"
                :href="vm.url"
                target="_blank"
                rel="noopener"
                class="font-mono text-foreground underline-offset-4 hover:underline"
              >{{ sshHost || vm.name }}<span class="sr-only"> (opens in a new tab)</span></a>
              <span v-else class="font-mono text-foreground">{{ vm.name }}</span>.
            </p>
          </div>
          <span v-if="domains.length" class="font-mono text-xs text-muted-foreground">
            {{ domains.length }} / {{ domainLimit }}
          </span>
        </div>

        <Alert v-if="domainError" variant="destructive" class="mt-3">
          <AlertDescription>{{ domainError }}</AlertDescription>
        </Alert>

        <ul v-if="domains.length" class="mt-3 space-y-3">
          <li v-for="d in domains" :key="d.domain" class="rounded-md border border-border p-3">
            <div class="flex flex-wrap items-start justify-between gap-2">
              <dl class="grid min-w-0 flex-1 gap-x-6 gap-y-3 sm:grid-cols-2">
                <div class="min-w-0">
                  <dt class="eyebrow text-muted-foreground">Domain</dt>
                  <dd class="mt-1 font-mono text-sm font-semibold break-all">
                    <a
                      v-if="d.url"
                      :href="d.url"
                      target="_blank"
                      rel="noopener"
                      class="inline-flex items-center gap-1.5 underline-offset-4 hover:underline"
                    >
                      {{ d.domain }}
                      <ExternalLink class="size-3.5" aria-hidden="true" />
                    </a>
                    <span v-else>{{ d.domain }}</span>
                  </dd>
                </div>
                <div v-if="d.cert_not_after">
                  <dt class="eyebrow text-muted-foreground">Certificate valid until</dt>
                  <dd class="mt-1 text-sm text-muted-foreground">{{ fmtDate(d.cert_not_after) }}</dd>
                </div>
              </dl>
              <Badge :variant="domainStatusVariant[d.status]" class="font-mono text-xs">
                {{ domainStatusLabel[d.status] }}
              </Badge>
            </div>

            <div class="mt-3 rounded-md border border-border bg-muted/40 p-3">
              <p v-if="domainReused.includes(d.domain)" class="text-xs text-muted-foreground">
                You already had a certificate for this name and it has not expired, so it was reused —
                nothing has to be issued. Point the CNAME at the target below and the name is live.
              </p>
              <p v-else-if="d.status !== 'active'" class="text-xs text-muted-foreground">
                Add this record
                <template v-if="d.zone">
                  to <span class="font-mono font-semibold text-foreground">{{ d.zone }}</span>
                </template>
                at your DNS provider, then confirm below.
              </p>
              <p v-else class="text-xs text-muted-foreground">
                This name is live. Its CNAME has to keep pointing here, or it stops resolving to this VM.
              </p>
              <dl class="mt-3 grid gap-x-6 gap-y-2 sm:grid-cols-3">
                <div>
                  <dt class="eyebrow text-muted-foreground">Type</dt>
                  <dd class="mt-1 font-mono text-sm">CNAME</dd>
                </div>
                <div class="min-w-0">
                  <dt class="eyebrow text-muted-foreground">Name</dt>
                  <template v-if="d.zone">
                    <dd class="mt-1 flex min-w-0 items-center gap-1 font-mono text-sm">
                      <span class="break-all">{{ d.cname_host }}</span>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        class="shrink-0"
                        aria-label="Copy the name to the clipboard"
                        @click="copyDomainRecord(d, 'name', d.cname_host)"
                      >
                        <component :is="isCopied(d, 'name') ? Check : Copy" aria-hidden="true" />
                      </Button>
                    </dd>
                    <dd v-if="d.cname_host === '@'" class="mt-1 text-xs text-muted-foreground">
                      <span class="font-mono">@</span> is the root of
                      <span class="font-mono">{{ d.zone }}</span>. Most providers flatten a CNAME there
                      (or offer ALIAS/ANAME), which works; turn off any CDN proxying. If yours offers
                      neither, use a subdomain like <span class="font-mono">www</span>.
                    </dd>
                  </template>
                  <dd v-else class="mt-1 font-mono text-sm break-all">{{ d.cname_name }}</dd>
                </div>
                <div class="min-w-0">
                  <dt class="eyebrow text-muted-foreground">Value</dt>
                  <dd class="mt-1 flex min-w-0 items-center gap-1 font-mono text-sm">
                    <span class="break-all">{{ d.cname_target || '—' }}</span>
                    <Button
                      v-if="d.cname_target"
                      variant="ghost"
                      size="icon-xs"
                      class="shrink-0"
                      aria-label="Copy the value to the clipboard"
                      @click="copyDomainRecord(d, 'value', d.cname_target)"
                    >
                      <component :is="isCopied(d, 'value') ? Check : Copy" aria-hidden="true" />
                    </Button>
                  </dd>
                </div>
              </dl>
            </div>

            <p v-if="d.last_error" class="mt-3 text-xs text-destructive">{{ d.last_error }}</p>
            <p v-else-if="isSettling(d)" class="mt-3 text-xs text-muted-foreground">
              Processing. This page keeps checking; a certificate usually lands within a minute of the
              CNAME being visible.
            </p>

            <div class="mt-3 flex flex-wrap items-center gap-2">
              <Button
                v-if="d.status !== 'active'"
                size="sm"
                :disabled="domainBusy !== null || isSettling(d)"
                @click="verifyDomain(d)"
              >
                <RefreshCw class="size-4" :class="{ 'animate-spin': isSettling(d) }" aria-hidden="true" />
                {{ isSettling(d) ? 'Checking…' : 'I have added the CNAME' }}
              </Button>
              <Button variant="outline" size="sm" :disabled="domainBusy !== null" @click="removeDomain(d)">
                <Trash2 class="size-4" aria-hidden="true" />
                Remove
              </Button>
            </div>
          </li>
        </ul>
        <span role="status" aria-live="polite" class="sr-only">
          {{ domainCopied ? `Record ${domainCopied.field} copied to clipboard` : '' }}
        </span>

        <form
          v-if="domains.length < domainLimit"
          class="mt-3 flex flex-wrap items-end gap-2"
          @submit.prevent="saveDomain"
        >
          <div class="min-w-0 flex-1">
            <Input
              id="custom-domain"
              aria-label="Domain"
              v-model="domainInput"
              class="font-mono"
              placeholder="www.example.com"
              autocomplete="off"
              spellcheck="false"
            />
          </div>
          <Button type="submit" size="sm" :disabled="domainBusy !== null || !domainInput.trim()">
            {{ domainBusy === '' ? 'Saving…' : 'Add' }}
          </Button>
        </form>
        <p v-else class="mt-3 text-xs text-muted-foreground">
          This VM is at its limit of {{ domainLimit }} domains. Remove one to add another.
        </p>
      </section>

      <VmDestinations class="mt-4" :vm-id="id" />

      <dl class="mt-6 flex flex-wrap gap-x-6 gap-y-2 text-xs text-muted-foreground">
        <div class="flex gap-1.5">
          <dt>Created</dt>
          <dd class="text-foreground">{{ fmtDate(vm.created_at) }}</dd>
        </div>
        <div class="flex gap-1.5">
          <dt>Started</dt>
          <dd class="text-foreground">{{ fmtDate(vm.started_at) }}</dd>
        </div>
        <div class="flex gap-1.5">
          <dt>Last confirmed by host</dt>
          <dd class="text-foreground">{{ fmtDate(vm.reported_at) }}</dd>
        </div>
        <div v-if="vm.expires_at" class="flex gap-1.5">
          <dt>Destroyed</dt>
          <dd :class="timeLeft(vm.expires_at) === 'overdue' ? 'text-destructive' : 'text-foreground'">
            {{ timeLeft(vm.expires_at) === 'overdue' ? 'overdue' : `in ${timeLeft(vm.expires_at)}` }}
            · {{ fmtDate(vm.expires_at) }}
          </dd>
        </div>
      </dl>
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

  </div>
</template>
