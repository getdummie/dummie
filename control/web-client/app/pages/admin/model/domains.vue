<script setup lang="ts">
import { Copy, Plus, ShieldCheck, Trash2 } from '@lucide/vue'
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
import { Switch } from '@/components/ui/switch'
import { TableCell, TableRow } from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import type { DataTableColumn } from '@/lib/table'

const columns: DataTableColumn[] = [
  { key: 'tld', label: 'TLD' },
  { key: 'tls', label: 'TLS' },
  { key: 'actions', label: 'Actions', align: 'right' },
]

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · domains' })

interface DomainRow {
  id: string
  tld: string
  tls_enabled: boolean
  has_cert: boolean
  cert_not_after?: string
  cert_error?: string
}

interface DnsRecord {
  name: string
  value: string
}

interface CertificateState {
  domain_id: string
  tld: string
  tls_enabled: boolean
  cert_mode: string
  acme_directory: string
  acme_email: string
  credential_set: boolean
  names: string[]
  fingerprint: string
  not_after?: string
  issued_at?: string
  expires_in_days?: number
  error?: string
  in_flight: boolean
  pending?: DnsRecord[]
}

const { authFetch } = useAuth()

const items = ref<DomainRow[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

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
  loading.value = true
  error.value = null
  try {
    const res = await authFetch('/admin/domains')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = await res.json()
    items.value = data.items ?? []
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load domains'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

// --- create ---
const createOpen = ref(false)
const creating = ref(false)
const createError = ref<string | null>(null)
const tld = ref('')

function resetForm() {
  tld.value = ''
  createError.value = null
}

async function create() {
  creating.value = true
  createError.value = null
  try {
    const res = await authFetch('/admin/domains', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tld: tld.value }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    createOpen.value = false
    resetForm()
    await load()
  }
  catch (e) {
    createError.value = e instanceof Error ? e.message : 'Could not create domain'
  }
  finally {
    creating.value = false
  }
}

// --- delete ---
const toDelete = ref<DomainRow | null>(null)
const deleting = ref(false)
const actionError = ref<string | null>(null)

async function confirmDelete() {
  if (!toDelete.value) return
  deleting.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/domains/${toDelete.value.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toDelete.value = null
    await load()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not delete domain'
  }
  finally {
    deleting.value = false
  }
}

// --- certificate ---

// The token is held apart from `cert`, and never written back into it. The
// server never returns one, so a value in `cert` could only have come from this
// form -- and keeping it there would mean a reload of the dialog re-displaying a
// credential the operator typed. Same bargain the settings screen makes.
const cert = ref<CertificateState | null>(null)
const credential = ref('')
const certOpen = ref(false)
const certBusy = ref(false)
const certError = ref<string | null>(null)

// The uploaded pair, also kept out of `cert` and cleared on every close.
const certPem = ref('')
const keyPem = ref('')

// A guided order publishes its records from a goroutine, so the screen finds out
// by asking. Only while a dialog is open on an order that is running.
let poll: ReturnType<typeof setInterval> | null = null

function stopPolling() {
  if (poll) {
    clearInterval(poll)
    poll = null
  }
}
onBeforeUnmount(stopPolling)

function startPolling(id: string) {
  stopPolling()
  poll = setInterval(async () => {
    const state = await fetchCert(id)
    if (state && !state.in_flight) {
      stopPolling()
      await load()
    }
  }, 2000)
}

async function fetchCert(id: string): Promise<CertificateState | null> {
  try {
    const res = await authFetch(`/admin/domains/${id}/certificate`)
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    cert.value = await res.json()
    return cert.value
  }
  catch (e) {
    certError.value = e instanceof Error ? e.message : 'Could not load the certificate'
    return null
  }
}

async function openCert(d: DomainRow) {
  certError.value = null
  credential.value = ''
  certPem.value = ''
  keyPem.value = ''
  cert.value = null
  certOpen.value = true
  const state = await fetchCert(d.id)
  if (state?.in_flight) startPolling(d.id)
}

function closeCert(open: boolean) {
  certOpen.value = open
  if (!open) {
    stopPolling()
    credential.value = ''
    certPem.value = ''
    keyPem.value = ''
    cert.value = null
  }
}

// One wrapper for every certificate action: they all take the same shape, and
// they all end by re-reading the state the server now holds.
async function certAction(path: string, init: RequestInit, onDone?: () => void) {
  if (!cert.value) return
  const id = cert.value.domain_id
  certBusy.value = true
  certError.value = null
  try {
    const res = await authFetch(`/admin/domains/${id}${path}`, init)
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    await fetchCert(id)
    await load()
    onDone?.()
  }
  catch (e) {
    certError.value = e instanceof Error ? e.message : 'The request failed'
  }
  finally {
    certBusy.value = false
  }
}

function saveSettings() {
  if (!cert.value) return
  return certAction('/tls', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      enabled: cert.value.tls_enabled,
      cert_mode: cert.value.cert_mode,
      acme_directory: cert.value.acme_directory,
      acme_email: cert.value.acme_email,
      acme_credentials: credential.value,
    }),
  }, () => { credential.value = '' })
}

function upload() {
  return certAction('/certificate', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ cert_pem: certPem.value, key_pem: keyPem.value }),
  }, () => { certPem.value = ''; keyPem.value = '' })
}

async function issue() {
  const id = cert.value?.domain_id
  await certAction('/certificate/issue', { method: 'POST' })
  if (id && cert.value?.in_flight) startPolling(id)
}

const continueOrder = () => certAction('/certificate/continue', { method: 'POST' })
const cancelOrder = () => certAction('/certificate/cancel', { method: 'POST' })
const removeCert = () => certAction('/certificate', { method: 'DELETE' })

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
  }
  catch {
    // Clipboard access is denied outside a secure context, which is exactly where
    // a fleet that has not got its certificate yet is. The value is on screen.
  }
}

// --- row status ---

function daysUntil(iso?: string): number | null {
  if (!iso) return null
  return Math.floor((new Date(iso).getTime() - Date.now()) / 86_400_000)
}

function tlsLabel(d: DomainRow): string {
  if (d.cert_error) return 'error'
  if (!d.has_cert) return 'no certificate'
  const days = daysUntil(d.cert_not_after)
  if (days === null) return d.tls_enabled ? 'on' : 'off'
  if (days < 0) return 'expired'
  if (!d.tls_enabled) return `off · ${days}d left`
  return `on · ${days}d left`
}

// Anything that needs a person before it becomes an outage reads as a warning:
// an expiry inside the renewal window that nothing is going to act on, an error
// from the last attempt, or a certificate that has already run out.
function tlsVariant(d: DomainRow): 'default' | 'secondary' | 'destructive' | 'outline' {
  const days = daysUntil(d.cert_not_after)
  if (d.cert_error || (days !== null && days < 0)) return 'destructive'
  if (!d.has_cert) return 'outline'
  if (days !== null && days < 30) return 'secondary'
  return d.tls_enabled ? 'default' : 'outline'
}

const isACME = computed(() => cert.value != null && cert.value.cert_mode !== 'upload')

// Listed rather than written inline in the template: SelectValue renders the
// selected item's own text, so the option labels have to be short enough to read
// inside the trigger. The reasoning behind each one is in the help text below it.
const certModes = [
  { value: 'upload', label: 'upload — I supply the certificate' },
  { value: 'acme_manual', label: 'acme_manual — guided, I create the DNS records' },
  { value: 'acme_cloudflare', label: 'acme_cloudflare — automatic, renews unattended' },
]

const acmeDirectories = [
  { value: 'staging', label: 'staging — untrusted, effectively no rate limit' },
  { value: 'production', label: 'production — real, 50 per domain per week' },
]
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-12 sm:px-6">
    <AdminNav />

    <div class="mt-8 flex items-end justify-between gap-4">
      <div>
        <p class="eyebrow mb-2 text-primary-text">// admin · domains</p>
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">Domains</h1>
        <p class="mt-2 max-w-2xl text-sm text-muted-foreground">
          DNS suffixes this installation owns. While exactly one exists, every client that enrolls is
          assigned it automatically. Each can be served over TLS with a wildcard certificate.
        </p>
      </div>
      <Dialog v-model:open="createOpen" @update:open="(v: boolean) => !v && resetForm()">
        <Button class="font-mono text-xs" @click="createOpen = true">
          <Plus class="size-4" aria-hidden="true" />
          New domain
        </Button>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add domain</DialogTitle>
            <DialogDescription>
              A suffix like <span class="font-mono">example.com</span> or <span class="font-mono">lab.internal</span>.
            </DialogDescription>
          </DialogHeader>
          <form class="space-y-4" :aria-busy="creating" @submit.prevent="create">
            <div class="space-y-2">
              <Label for="d-tld">TLD</Label>
              <Input id="d-tld" v-model="tld" placeholder="example.com" autocomplete="off" spellcheck="false" />
            </div>

            <FormError id="create-domain-error" :message="createError" />

            <DialogFooter>
              <DialogClose as-child>
                <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
              </DialogClose>
              <Button type="submit" class="font-mono text-xs" :disabled="creating || !tld.trim()">
                {{ creating ? 'Adding…' : 'Add' }}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load domains</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <DataTable
      label="Domains"
      :columns="columns"
      :loading="loading"
      :loading-rows="3"
      loading-label="Loading domains…"
      :empty="!items.length"
      class="mt-6"
    >
      <template #empty>
        No domains yet. Clients will enroll without one until you add exactly one.
      </template>
      <TableRow v-for="d in items" :key="d.id">
        <TableCell class="font-mono">{{ d.tld }}</TableCell>
        <TableCell>
          <Badge :variant="tlsVariant(d)" class="font-mono text-xs">{{ tlsLabel(d) }}</Badge>
        </TableCell>
        <TableCell class="space-x-1 text-right">
          <Button
            variant="ghost"
            size="icon"
            :aria-label="`Certificate for ${d.tld}`"
            @click="openCert(d)"
          >
            <ShieldCheck class="size-4" aria-hidden="true" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            class="text-destructive hover:text-destructive"
            :aria-label="`Delete domain ${d.tld}`"
            @click="toDelete = d"
          >
            <Trash2 class="size-4" aria-hidden="true" />
          </Button>
        </TableCell>
      </TableRow>
    </DataTable>

    <!-- certificate -->
    <Dialog :open="certOpen" @update:open="closeCert">
      <DialogContent class="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Certificate for {{ cert?.tld ?? '…' }}</DialogTitle>
          <DialogDescription>
            One wildcard certificate serves every guest on this domain, and every browser terminal.
          </DialogDescription>
        </DialogHeader>

        <div v-if="cert" class="space-y-6">
          <!-- what the certificate has to cover -->
          <div class="rounded-md border border-border p-3">
            <p class="text-xs text-muted-foreground">
              Must cover, on one certificate:
            </p>
            <ul class="mt-2 space-y-1 font-mono text-xs">
              <li v-for="n in cert.names" :key="n">{{ n }}</li>
            </ul>
            <p class="mt-2 text-xs text-muted-foreground">
              A wildcard matches one label, so
              <span class="font-mono">*.{{ cert.tld }}</span> does not cover the consoles —
              both names are required, which is why only DNS-01 can prove this.
            </p>
          </div>

          <!-- current state -->
          <div v-if="cert.fingerprint" class="space-y-1 text-sm">
            <p>
              Issued {{ cert.issued_at?.slice(0, 10) }}, expires {{ cert.not_after?.slice(0, 10) }}
              <span v-if="cert.expires_in_days !== undefined" class="text-muted-foreground">
                ({{ cert.expires_in_days }} days)
              </span>
            </p>
            <p class="truncate font-mono text-xs text-muted-foreground">{{ cert.fingerprint }}</p>
          </div>
          <p v-else class="text-sm text-muted-foreground">No certificate yet.</p>

          <Alert v-if="cert.error" variant="destructive">
            <AlertTitle>The last attempt failed</AlertTitle>
            <AlertDescription>{{ cert.error }}</AlertDescription>
          </Alert>

          <!-- settings -->
          <div class="space-y-4 border-t border-border pt-4">
            <div class="space-y-2">
              <Label for="c-mode">How it is obtained</Label>
              <Select v-model="cert.cert_mode">
                <SelectTrigger id="c-mode" class="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem v-for="m in certModes" :key="m.value" :value="m.value">
                    {{ m.label }}
                  </SelectItem>
                </SelectContent>
              </Select>
              <p class="text-xs text-muted-foreground">
                Only <span class="font-mono">acme_cloudflare</span> renews on its own. The other two
                need someone present, so they are warned about as they approach expiry instead.
              </p>
            </div>

            <template v-if="isACME">
              <div class="space-y-2">
                <Label for="c-dir">Directory</Label>
                <Select v-model="cert.acme_directory">
                  <SelectTrigger id="c-dir" class="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem v-for="d in acmeDirectories" :key="d.value" :value="d.value">
                      {{ d.label }}
                    </SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div class="space-y-2">
                <Label for="c-email">Account email</Label>
                <Input id="c-email" v-model="cert.acme_email" type="email" placeholder="ops@example.com" autocomplete="off" />
              </div>
            </template>

            <div v-if="cert.cert_mode === 'acme_cloudflare'" class="space-y-2">
              <Label for="c-token">
                Cloudflare API token
                <span class="ml-1 font-normal text-muted-foreground">
                  · {{ cert.credential_set ? 'set' : 'not set' }}
                </span>
              </Label>
              <Input
                id="c-token"
                v-model="credential"
                type="password"
                autocomplete="new-password"
                :placeholder="cert.credential_set ? '••••••••' : 'Zone:DNS:Edit on this zone'"
              />
              <p class="text-xs text-muted-foreground">
                Stored on this server only and never shown again. Never sent to a qemu host —
                it can rewrite the zone, and the hosts have no use for it.
                Leave blank to keep the stored one.
              </p>
            </div>

            <div class="flex items-start gap-3">
              <Switch
                id="c-enabled"
                :model-value="cert.tls_enabled"
                :disabled="!cert.fingerprint"
                @update:model-value="(v: boolean) => { if (cert) cert.tls_enabled = v }"
              />
              <div class="space-y-1">
                <Label for="c-enabled">Serve this domain over TLS</Label>
                <p class="text-xs text-muted-foreground">
                  Opens port 443 on every host on this domain and makes their session cookies
                  Secure. Needs a certificate first.
                </p>
              </div>
            </div>

            <Button class="font-mono text-xs" :disabled="certBusy" @click="saveSettings">
              {{ certBusy ? 'Saving…' : 'Save settings' }}
            </Button>
          </div>

          <!-- upload -->
          <div v-if="cert.cert_mode === 'upload'" class="space-y-3 border-t border-border pt-4">
            <div class="space-y-2">
              <Label for="c-cert">Certificate chain (PEM)</Label>
              <Textarea id="c-cert" v-model="certPem" rows="5" class="font-mono text-xs" placeholder="-----BEGIN CERTIFICATE-----" spellcheck="false" />
              <p class="text-xs text-muted-foreground">Leaf first, then any intermediates.</p>
            </div>
            <div class="space-y-2">
              <Label for="c-key">Private key (PEM)</Label>
              <Textarea id="c-key" v-model="keyPem" rows="5" class="font-mono text-xs" placeholder="-----BEGIN PRIVATE KEY-----" spellcheck="false" />
            </div>
            <Button class="font-mono text-xs" :disabled="certBusy || !certPem.trim() || !keyPem.trim()" @click="upload">
              {{ certBusy ? 'Installing…' : 'Install certificate' }}
            </Button>
          </div>

          <!-- acme -->
          <div v-else class="space-y-3 border-t border-border pt-4">
            <div v-if="cert.in_flight && cert.pending?.length" class="space-y-3">
              <p class="text-sm">
                Create these TXT records, wait for them to propagate, then continue.
              </p>
              <div v-for="(r, i) in cert.pending" :key="i" class="space-y-1 rounded-md border border-border p-3">
                <div class="flex items-center justify-between gap-2">
                  <span class="truncate font-mono text-xs">{{ r.name }}</span>
                  <Button variant="ghost" size="icon" aria-label="Copy record name" @click="copy(r.name)">
                    <Copy class="size-3.5" aria-hidden="true" />
                  </Button>
                </div>
                <div class="flex items-center justify-between gap-2">
                  <span class="truncate font-mono text-xs text-muted-foreground">{{ r.value }}</span>
                  <Button variant="ghost" size="icon" aria-label="Copy record value" @click="copy(r.value)">
                    <Copy class="size-3.5" aria-hidden="true" />
                  </Button>
                </div>
              </div>
              <div class="flex gap-2">
                <Button class="font-mono text-xs" :disabled="certBusy" @click="continueOrder">
                  The records are live
                </Button>
                <Button variant="outline" class="font-mono text-xs" :disabled="certBusy" @click="cancelOrder">
                  Cancel order
                </Button>
              </div>
            </div>

            <div v-else-if="cert.in_flight" class="flex items-center gap-3">
              <p class="text-sm text-muted-foreground">Order running…</p>
              <!-- Only offered for a guided order. An automated one is already
                   mid-conversation with the CA and the DNS provider, and there is
                   no safe point to stop it at; a button that did nothing would be
                   worse than no button. -->
              <Button
                v-if="cert.cert_mode === 'acme_manual'"
                variant="outline"
                class="font-mono text-xs"
                :disabled="certBusy"
                @click="cancelOrder"
              >
                Cancel
              </Button>
            </div>

            <Button v-else class="font-mono text-xs" :disabled="certBusy" @click="issue">
              {{ cert.fingerprint ? 'Renew now' : 'Request a certificate' }}
            </Button>
          </div>

          <FormError id="cert-error" :message="certError" />

          <div v-if="cert.fingerprint" class="border-t border-border pt-4">
            <Button variant="destructive" class="font-mono text-xs" :disabled="certBusy" @click="removeCert">
              Remove certificate
            </Button>
            <p class="mt-2 text-xs text-muted-foreground">
              Takes every host on this domain back to plain HTTP.
            </p>
          </div>
        </div>

        <p v-else-if="!certError" class="py-6 text-sm text-muted-foreground">Loading…</p>
        <FormError v-else id="cert-load-error" :message="certError" />
      </DialogContent>
    </Dialog>

    <!-- delete confirm -->
    <Dialog :open="!!toDelete" @update:open="(v: boolean) => { if (!v) toDelete = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete domain</DialogTitle>
          <DialogDescription>
            Remove <span class="font-mono text-foreground">{{ toDelete?.tld }}</span>.
            Clients assigned to it are kept, but lose their domain. Its certificate is deleted too.
          </DialogDescription>
        </DialogHeader>
        <FormError id="delete-domain-error" :message="actionError" />
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
