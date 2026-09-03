<script setup lang="ts">
import { Trash2 } from '@lucide/vue'
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
import { TableCell, TableRow } from '@/components/ui/table'
import type { DataTableColumn } from '@/lib/table'

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · custom domains' })

const domainColumns: DataTableColumn[] = [
  { key: 'domain', label: 'Domain' },
  { key: 'status', label: 'Status' },
  { key: 'vm', label: 'VM' },
  { key: 'owner', label: 'Owner' },
  { key: 'expires', label: 'Certificate' },
  { key: 'actions', label: 'Actions', align: 'right' },
]

const certColumns: DataTableColumn[] = [
  { key: 'domain', label: 'Domain' },
  { key: 'owner', label: 'Owner' },
  { key: 'bound', label: 'Bound to' },
  { key: 'issued', label: 'Issued' },
  { key: 'expires', label: 'Expires' },
  { key: 'actions', label: 'Actions', align: 'right' },
]

type DomainStatus = 'pending_dns' | 'verifying' | 'issuing' | 'active' | 'failed'

interface DomainRow {
  id: string
  domain: string
  status: DomainStatus
  vm_id: string
  vm_name: string
  vm_status: string
  client: string
  owner: string
  email: string
  cert_fingerprint?: string
  cert_not_after?: string
  last_error?: string
  created_at?: string
}

interface CertRow {
  id: string
  domain: string
  owner: string
  email: string
  vm_name?: string
  domain_status?: string
  cert_fingerprint?: string
  cert_not_after: string
  cert_issued_at?: string
  expires_in_days: number
  expired: boolean
  attached: boolean
}

const { authFetch } = useAuth()

const domains = ref<DomainRow[]>([])
const certs = ref<CertRow[]>([])
const loadingDomains = ref(true)
const loadingCerts = ref(true)
const error = ref<string | null>(null)
const actionError = ref<string | null>(null)

async function readMessage(res: Response): Promise<string | null> {
  try {
    const b = await res.json()
    return typeof b?.message === 'string' ? b.message : null
  }
  catch {
    return null
  }
}

function fmtDate(s?: string) {
  if (!s) return '—'
  const d = new Date(s)
  return Number.isNaN(d.getTime()) ? s : d.toLocaleDateString()
}

const statusVariant: Record<DomainStatus, 'default' | 'secondary' | 'destructive' | 'outline'> = {
  pending_dns: 'outline',
  verifying: 'secondary',
  issuing: 'secondary',
  active: 'default',
  failed: 'destructive',
}

async function loadDomains() {
  loadingDomains.value = true
  try {
    const res = await authFetch('/admin/custom-domains')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    domains.value = (await res.json()).items ?? []
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load custom domains'
  }
  finally {
    loadingDomains.value = false
  }
}

async function loadCerts() {
  loadingCerts.value = true
  try {
    const res = await authFetch('/admin/custom-domain-certs')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    certs.value = (await res.json()).items ?? []
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load stored certificates'
  }
  finally {
    loadingCerts.value = false
  }
}

async function load() {
  error.value = null
  await Promise.all([loadDomains(), loadCerts()])
}
onMounted(load)

const toUnbind = ref<DomainRow | null>(null)
const unbinding = ref(false)

async function confirmUnbind() {
  if (!toUnbind.value) return
  unbinding.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/custom-domains/${toUnbind.value.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toUnbind.value = null
    await load()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not remove the custom domain'
  }
  finally {
    unbinding.value = false
  }
}

const toDeleteCert = ref<CertRow | null>(null)
const deletingCert = ref(false)

async function confirmDeleteCert() {
  if (!toDeleteCert.value) return
  deletingCert.value = true
  actionError.value = null
  try {
    const res = await authFetch(`/admin/custom-domain-certs/${toDeleteCert.value.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toDeleteCert.value = null
    await load()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not delete the certificate'
  }
  finally {
    deletingCert.value = false
  }
}
</script>

<template>
  <AdminShell>
    <div class="mt-8">
      <p class="eyebrow mb-2 text-primary-text">// admin · custom domains</p>
      <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">Custom domains</h1>
      <p class="mt-2 max-w-2xl text-sm text-muted-foreground">
        Names a user pointed at one of their VMs with a CNAME, and the certificates obtained for
        them. A certificate belongs to the name and the user, not to the VM: deleting a VM leaves it
        here so the same user can rebuild and pick it straight back up. Expired ones no VM is bound
        to are reclaimed daily.
      </p>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load this page</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <h2 class="mt-8 text-sm font-semibold">Bindings</h2>
    <DataTable
      label="Custom domains"
      :columns="domainColumns"
      :loading="loadingDomains"
      loading-label="Loading custom domains…"
      :empty="!domains.length"
      class="mt-3"
    >
      <template #empty>
        No custom domain has been claimed.
      </template>
      <TableRow v-for="d in domains" :key="d.id">
        <TableCell>
          <div class="font-mono break-all">{{ d.domain }}</div>
          <div v-if="d.last_error" class="text-xs text-destructive">{{ d.last_error }}</div>
        </TableCell>
        <TableCell>
          <Badge :variant="statusVariant[d.status]" class="font-mono">{{ d.status }}</Badge>
        </TableCell>
        <TableCell>
          <NuxtLink :to="`/admin/model/vms/${d.vm_id}`" class="font-mono underline-offset-4 hover:underline">
            {{ d.vm_name }}
          </NuxtLink>
          <div class="text-xs text-muted-foreground">{{ d.client || '—' }} · {{ d.vm_status }}</div>
        </TableCell>
        <TableCell>
          <div class="font-mono">{{ d.owner || '—' }}</div>
          <div class="text-xs text-muted-foreground">{{ d.email }}</div>
        </TableCell>
        <TableCell class="text-muted-foreground">{{ fmtDate(d.cert_not_after) }}</TableCell>
        <TableCell class="text-right">
          <Button
            variant="ghost"
            size="icon"
            class="text-destructive hover:text-destructive"
            :aria-label="`Remove the custom domain ${d.domain}`"
            @click="toUnbind = d"
          >
            <Trash2 class="size-4" aria-hidden="true" />
          </Button>
        </TableCell>
      </TableRow>
    </DataTable>

    <h2 class="mt-10 text-sm font-semibold">Stored certificates</h2>
    <DataTable
      label="Stored custom domain certificates"
      :columns="certColumns"
      :loading="loadingCerts"
      loading-label="Loading certificates…"
      :empty="!certs.length"
      class="mt-3"
    >
      <template #empty>
        No certificate has been obtained for a custom domain.
      </template>
      <TableRow v-for="c in certs" :key="c.id">
        <TableCell>
          <div class="font-mono break-all">{{ c.domain }}</div>
          <div v-if="c.cert_fingerprint" class="truncate font-mono text-xs text-muted-foreground">
            {{ c.cert_fingerprint }}
          </div>
        </TableCell>
        <TableCell>
          <div class="font-mono">{{ c.owner || '—' }}</div>
          <div class="text-xs text-muted-foreground">{{ c.email }}</div>
        </TableCell>
        <TableCell>
          <span v-if="c.attached" class="font-mono">{{ c.vm_name }}</span>
          <span v-else class="font-mono text-muted-foreground">unattached</span>
        </TableCell>
        <TableCell class="text-muted-foreground">{{ fmtDate(c.cert_issued_at) }}</TableCell>
        <TableCell>
          <Badge :variant="c.expired ? 'destructive' : 'secondary'" class="font-mono">
            {{ c.expired ? 'expired' : `${c.expires_in_days}d` }}
          </Badge>
          <div class="text-xs text-muted-foreground">{{ fmtDate(c.cert_not_after) }}</div>
        </TableCell>
        <TableCell class="text-right">
          <Button
            variant="ghost"
            size="icon"
            class="text-destructive hover:text-destructive"
            :disabled="c.attached"
            :aria-label="c.attached
              ? `Cannot delete the certificate for ${c.domain}: a VM is still bound to it`
              : `Delete the certificate for ${c.domain}`"
            @click="toDeleteCert = c"
          >
            <Trash2 class="size-4" aria-hidden="true" />
          </Button>
        </TableCell>
      </TableRow>
    </DataTable>

    <Dialog :open="!!toUnbind" @update:open="(v: boolean) => { if (!v) toUnbind = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Remove custom domain</DialogTitle>
          <DialogDescription>
            <span class="font-mono text-foreground">{{ toUnbind?.domain }}</span> stops being served
            by <span class="font-mono text-foreground">{{ toUnbind?.vm_name }}</span>. The
            certificate stays stored, so the owner can claim the name again without a new one.
          </DialogDescription>
        </DialogHeader>
        <FormError id="unbind-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="unbinding" @click="confirmUnbind">
            {{ unbinding ? 'Removing…' : 'Remove' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog :open="!!toDeleteCert" @update:open="(v: boolean) => { if (!v) toDeleteCert = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete certificate</DialogTitle>
          <DialogDescription>
            Permanently delete the certificate and private key stored for
            <span class="font-mono text-foreground">{{ toDeleteCert?.domain }}</span>. The owner has
            to prove control of the name again to get a new one. This cannot be undone.
          </DialogDescription>
        </DialogHeader>
        <FormError id="delete-cert-error" :message="actionError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="deletingCert" @click="confirmDeleteCert">
            {{ deletingCert ? 'Deleting…' : 'Delete' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </AdminShell>
</template>
