<script setup lang="ts">
// The VM's allowed and denied destinations, shared by /vms/[id] and the agent's
// network tab; it loads and edits them on its own.
import { ClockPlus, Globe, Pencil, Pin, Plus, RefreshCw, Trash2 } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
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
import { TableCell, TableRow } from '@/components/ui/table'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import {
  applyPreset,
  blankTarget,
  deniedCovered,
  deniedKey,
  deniedParent,
  destinationPlaceholder as destinationPlaceholderFor,
  domainPortChoices,
  everywhere,
  isEverywhere,
  matchSummary as matchSummaryFor,
  portPresets,
  presetWarning as presetWarningFor,
  resetForKind,
  targetToForm,
  tempAllowPayload,
  toTargetPayload,
} from '@/lib/targets'
import type { DeniedAttempt, TargetRecord } from '@/lib/targets'
import type { DataTableColumn } from '@/lib/table'

const props = defineProps<{ vmId: string }>()

const { authFetch } = useAuth()
const id = computed(() => props.vmId)

const targets = ref<TargetRecord[]>([])
const denied = ref<DeniedAttempt[]>([])
const deniedAvailable = ref(true)
const deniedRecording = ref({ packets: false, lookups: false })
const loadError = ref<string | null>(null)

async function readMessage(res: Response): Promise<string | null> {
  try {
    const b = await res.json()
    return typeof b?.message === 'string' ? b.message : null
  }
  catch {
    return null
  }
}

async function load() {
  loadError.value = null
  loadDenied()
  try {
    await loadTargets()
  }
  catch (e) {
    loadError.value = e instanceof Error ? e.message : 'Could not load the allowed destinations'
  }
}
onMounted(load)

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

const deniedColumns: DataTableColumn[] = [
  { key: 'destination', label: 'Destination' },
  { key: 'attempts', label: 'Attempts', align: 'right' },
  { key: 'last_seen', label: 'Last blocked at', align: 'right' },
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


function fmtShortDate(s: string) {
  if (!s) return '—'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  const date = `${d.getDate()} ${d.toLocaleString(undefined, { month: 'short' })}`
  return `${date}, ${d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', hour12: false })}`
}


async function loadTargets() {
  const res = await authFetch(`/vms/${id.value}/targets`)
  if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
  targets.value = (await res.json()).items ?? []
}

const deniedLoading = ref(true)
const deniedFirstLoad = ref(true)

const deniedWindows = [
  { value: '300', label: 'Last 5 minutes' },
  { value: '900', label: 'Last 15 minutes' },
  { value: '3600', label: 'Last hour' },
  { value: '21600', label: 'Last 6 hours' },
  { value: '86400', label: 'Last 24 hours' },
  { value: '604800', label: 'Last 7 days' },
]

const deniedSeconds = ref('604800')

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

const alreadyAllowed = computed(() => {
  const covered = new Map<DeniedAttempt, string | null>()
  for (const d of denied.value) {
    if (deniedCovered(d, targets.value)) covered.set(d, deniedParent(d, targets.value))
  }
  return covered
})

const allowingDenied = ref<string | null>(null)
const allowDeniedError = ref<string | null>(null)

async function allowDenied(d: DeniedAttempt) {
  allowingDenied.value = deniedKey(d)
  allowDeniedError.value = null
  try {
    await postTarget(tempAllowPayload(d))
    await loadTargets()
  }
  catch (e) {
    allowDeniedError.value = e instanceof Error ? e.message : 'Could not allow the destination'
  }
  finally {
    allowingDenied.value = null
  }
}

const targetActionError = ref<string | null>(null)
const makingPermanent = ref<string | null>(null)

async function makePermanent(t: TargetRecord) {
  makingPermanent.value = t.id
  targetActionError.value = null
  try {
    const res = await authFetch(`/vms/${id.value}/targets/${t.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(toTargetPayload(targetToForm(t, 0))),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    await loadTargets()
  }
  catch (e) {
    targetActionError.value = e instanceof Error ? e.message : 'Could not make the allowance permanent'
  }
  finally {
    makingPermanent.value = null
  }
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
const openAllMinutes = ref('60')
const openAllNote = ref('')
const openingAll = ref(false)
const openAllError = ref<string | null>(null)

function resetOpenAllForm() {
  openAllMinutes.value = '60'
  openAllNote.value = ''
  openAllError.value = null
}

async function openEverything() {
  const problem = ttlMinutesProblem(openAllMinutes.value)
  if (problem) {
    openAllError.value = problem
    return
  }
  const ttl = Number(openAllMinutes.value) * 60
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


function ttlProblem(raw: string) {
  const ttl = Number(raw)
  if (!Number.isInteger(ttl) || ttl < 0) return 'TTL must be a whole number of seconds, or 0 for no limit.'
  if (ttl > 0 && ttl < 10) return 'A TTL must be at least 10 seconds. Use 0 for no limit.'
  if (ttl > 30 * 24 * 3600) return 'A TTL must be at most 30 days (2592000 seconds).'
  return null
}

function ttlMinutesProblem(raw: string) {
  const mins = Number(raw)
  if (!Number.isInteger(mins) || mins < 1) return 'TTL must be a whole number of minutes, at least 1.'
  if (mins > 30 * 24 * 60) return 'A TTL must be at most 30 days (43200 minutes).'
  return null
}


function timeAgo(s: string) {
  if (!s) return '—'
  const at = new Date(s).getTime()
  if (Number.isNaN(at)) return s
  const secs = Math.round((nowMs.value - at) / 1000)
  if (secs < 0 || secs >= 86_400) return fmtShortDate(s)
  if (secs < 10) return 'just now'
  if (secs < 60) return `${secs} seconds ago`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins} ${mins === 1 ? 'min' : 'mins'} ago`
  const hours = Math.floor(mins / 60)
  return `${hours} ${hours === 1 ? 'hour' : 'hours'} ago`
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

const removing = ref<string | null>(null)

async function removeTarget(t: TargetRecord) {
  removing.value = t.id
  targetActionError.value = null
  try {
    const res = await authFetch(`/vms/${id.value}/targets/${t.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    await loadTargets()
  }
  catch (e) {
    targetActionError.value = e instanceof Error ? e.message : 'Could not remove the destination'
  }
  finally {
    removing.value = null
  }
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <section aria-labelledby="targets-heading" class="rounded-lg border border-border">
      <div class="flex flex-wrap items-center justify-between gap-3 p-4">
        <h2 id="targets-heading" class="text-sm font-semibold">Allowed destinations</h2>
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
                <Label for="oa-ttl">TTL (minutes)</Label>
                <Input
                  id="oa-ttl"
                  v-model="openAllMinutes"
                  type="number"
                  min="1"
                  max="43200"
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

      <FormError v-if="loadError" id="targets-load-error" :message="loadError" class="mx-4 mb-3" />
      <FormError v-if="targetActionError" id="target-action-error" :message="targetActionError" class="mx-4 mb-3" />
      <TooltipProvider :delay-duration="150">
      <DataTable label="Allowed destinations" :columns="targetColumns" :empty="!targets.length" :frame="false">
        <template #empty>
          No destinations recorded.
        </template>
        <TableRow v-for="t in targets" :key="t.id">
          <TableCell class="font-mono break-all">
            <Hostname :name="t.destination" />
          </TableCell>
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
            <div v-if="timeLeft(t.expires_at) !== 'overdue'" class="flex items-center justify-end gap-1">
              <Tooltip v-if="t.expires_at">
                <TooltipTrigger as-child>
                  <Button
                    variant="ghost"
                    size="icon"
                    :disabled="makingPermanent === t.id"
                    :aria-label="`Allow ${t.destination} permanently`"
                    @click="makePermanent(t)"
                  >
                    <Pin :class="['size-4', makingPermanent === t.id && 'animate-pulse']" aria-hidden="true" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>Allow permanently</TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger as-child>
                  <Button
                    variant="ghost"
                    size="icon"
                    :aria-label="`Edit destination ${t.destination}`"
                    @click="openEditTarget(t)"
                  >
                    <Pencil class="size-4" aria-hidden="true" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>Edit</TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger as-child>
                  <Button
                    variant="ghost"
                    size="icon"
                    class="text-destructive hover:text-destructive"
                    :disabled="removing === t.id"
                    :aria-label="`Remove destination ${t.destination}`"
                    @click="removeTarget(t)"
                  >
                    <Trash2 :class="['size-4', removing === t.id && 'animate-pulse']" aria-hidden="true" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>Remove</TooltipContent>
              </Tooltip>
            </div>
          </TableCell>
        </TableRow>
      </DataTable>
      </TooltipProvider>
    </section>

    <section aria-labelledby="denied-heading" class="rounded-lg border border-border">
      <div class="flex flex-wrap items-center justify-between gap-3 p-4">
        <h2 id="denied-heading" class="text-sm font-semibold">Denied destinations</h2>
        <div class="flex items-center gap-2">
          <Select v-model="deniedSeconds" @update:model-value="refreshDenied">
            <SelectTrigger id="denied-window" size="sm" class="w-40 font-mono text-xs" aria-label="Time window">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-for="w in deniedWindows" :key="w.value" :value="w.value" class="font-mono text-xs">
                {{ w.label }}
              </SelectItem>
            </SelectContent>
          </Select>
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

        <FormError v-if="allowDeniedError" id="allow-denied-error" :message="allowDeniedError" class="mx-4 mb-3" />
        <DataTable label="Denied destinations" :columns="deniedColumns" :empty="!denied.length" :frame="false">
          <template #empty>
            Nothing has been denied.
          </template>
          <TableRow
            v-for="d in denied"
            :key="deniedKey(d)"
            :class="alreadyAllowed.has(d) && 'bg-primary/10 hover:bg-primary/15'"
          >
            <TableCell class="font-mono break-all">
              <Hostname :name="deniedDestination(d)" />
              <span v-if="d.domain && d.address" class="block text-xs text-muted-foreground">
                {{ d.address }}
              </span>
            </TableCell>
            <TableCell class="text-right font-mono tabular-nums">{{ d.attempts }}</TableCell>
            <TableCell class="text-right text-xs whitespace-nowrap text-muted-foreground">
              {{ alreadyAllowed.has(d) ? '' : timeAgo(d.last_seen) }}
            </TableCell>
            <TableCell class="text-right whitespace-nowrap">
              <span v-if="alreadyAllowed.has(d)" class="font-mono text-xs text-primary-text">
                <template v-if="alreadyAllowed.get(d)">
                  allowed via <span class="font-semibold">{{ alreadyAllowed.get(d) }}</span>
                </template>
                <template v-else>allowed</template>
              </span>
              <Button
                v-else
                variant="outline"
                size="sm"
                class="font-mono text-xs"
                :disabled="allowingDenied === deniedKey(d)"
                :aria-label="`Temporarily allow ${deniedDestination(d)} for 10 minutes`"
                @click="allowDenied(d)"
              >
                <ClockPlus :class="['size-3.5', allowingDenied === deniedKey(d) && 'animate-pulse']" aria-hidden="true" />
                {{ allowingDenied === deniedKey(d) ? 'Allowing…' : 'Temporarily allow' }}
              </Button>
            </TableCell>
          </TableRow>
        </DataTable>
      </template>
    </section>
  </div>
</template>
