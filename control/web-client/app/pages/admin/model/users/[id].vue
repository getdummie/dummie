<script setup lang="ts">
import { ArrowLeft } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'

definePageMeta({ middleware: ['auth', 'admin'] })

interface AdminUser {
  id: string
  username: string
  email: string
  first_name: string
  last_name: string
  user_type: string
  public_key: string
  vcpu_limit: number
  memory_limit_mib: number
  disk_limit_mib: number
  created_at: string
  updated_at: string
}

const route = useRoute()
const { authFetch } = useAuth()
const id = computed(() => String(route.params.id))

const user = ref<AdminUser | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)

useHead(() => ({
  title: user.value ? `dummie — admin · ${user.value.username}` : 'dummie — admin · user',
}))

async function readMessage(res: Response): Promise<string | null> {
  try {
    const b = await res.json()
    return typeof b?.message === 'string' ? b.message : null
  }
  catch {
    return null
  }
}

// 11th–13th are the exception the mod-10 rule gets wrong.
function ordinal(n: number) {
  if (n % 100 >= 11 && n % 100 <= 13) return `${n}th`
  return `${n}${['th', 'st', 'nd', 'rd'][n % 10] ?? 'th'}`
}

/** "8th August 2026, 12:07 AM" */
function fmtDate(s: string) {
  if (!s) return '—'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  const date = `${ordinal(d.getDate())} ${d.toLocaleString(undefined, { month: 'long' })} ${d.getFullYear()}`
  return `${date}, ${d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })}`
}

const fullName = computed(() => {
  const u = user.value
  if (!u) return '—'
  return [u.first_name, u.last_name].filter(Boolean).join(' ') || '—'
})

// --- quota ---
// Strings, because a number input binds to '' while being cleared and coercing
// that to 0 would silently rewrite what the admin is halfway through typing.
const form = reactive({ vcpu_limit: '', memory_limit_mib: '', disk_limit_mib: '' })
const saving = ref(false)
const saveError = ref<string | null>(null)
const saved = ref(false)

function resetForm(u: AdminUser) {
  form.vcpu_limit = String(u.vcpu_limit)
  form.memory_limit_mib = String(u.memory_limit_mib)
  form.disk_limit_mib = String(u.disk_limit_mib)
}

/** Template-side reset: the loaded user is the source of truth for "unchanged". */
function discardEdits() {
  if (user.value) resetForm(user.value)
  saveError.value = null
}

// Two decimals only when it isn't a whole number of GiB.
function asGiB(mib: number) {
  if (!Number.isFinite(mib) || mib <= 0) return null
  const gib = mib / 1024
  return Number.isInteger(gib) ? `${gib} GiB` : `${gib.toFixed(2)} GiB`
}

const memoryGiB = computed(() => asGiB(Number(form.memory_limit_mib)))
const diskGiB = computed(() => asGiB(Number(form.disk_limit_mib)))

const dirty = computed(() => {
  const u = user.value
  if (!u) return false
  return form.vcpu_limit !== String(u.vcpu_limit)
    || form.memory_limit_mib !== String(u.memory_limit_mib)
    || form.disk_limit_mib !== String(u.disk_limit_mib)
})

// --- public key ---
// Its own form, saved separately: a key and an allowance are unrelated decisions,
// and one PUT that carried both would make fixing a typo in the key also re-send
// numbers the admin never looked at.
const keyForm = reactive({ public_key: '' })
const savingKey = ref(false)
const keyError = ref<string | null>(null)
const keySaved = ref(false)

const keyDirty = computed(() => keyForm.public_key.trim() !== (user.value?.public_key ?? ''))

function discardKeyEdits() {
  keyForm.public_key = user.value?.public_key ?? ''
  keyError.value = null
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await authFetch(`/admin/users/${id.value}`)
    if (res.status === 404) throw new Error('This user does not exist, or was deleted.')
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    const data: AdminUser = await res.json()
    user.value = data
    resetForm(data)
    keyForm.public_key = data.public_key
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load user'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

async function saveQuota() {
  saving.value = true
  saveError.value = null
  saved.value = false
  try {
    const res = await authFetch(`/admin/users/${id.value}/quota`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        vcpu_limit: Number(form.vcpu_limit),
        memory_limit_mib: Number(form.memory_limit_mib),
        disk_limit_mib: Number(form.disk_limit_mib),
      }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    const data: AdminUser = await res.json()
    user.value = data
    resetForm(data)
    saved.value = true
  }
  catch (e) {
    saveError.value = e instanceof Error ? e.message : 'Could not save quota'
  }
  finally {
    saving.value = false
  }
}

async function saveKey() {
  savingKey.value = true
  keyError.value = null
  keySaved.value = false
  try {
    const res = await authFetch(`/admin/users/${id.value}/public_key`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ public_key: keyForm.public_key }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    const data: AdminUser = await res.json()
    user.value = data
    // From the response, not from what was typed: the server canonicalises the
    // key, so echoing the input back would leave the form looking dirty.
    keyForm.public_key = data.public_key
    keySaved.value = true
  }
  catch (e) {
    keyError.value = e instanceof Error ? e.message : 'Could not save the public key'
  }
  finally {
    savingKey.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-12 sm:px-6">
    <AdminNav />

    <NuxtLink
      to="/admin/model/users"
      class="mt-8 inline-flex items-center gap-1.5 font-mono text-xs text-muted-foreground underline-offset-4 transition-colors hover:text-foreground hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
    >
      <ArrowLeft class="size-3.5" aria-hidden="true" />
      All users
    </NuxtLink>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load user</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <div v-else-if="loading" class="mt-4 space-y-6" aria-busy="true">
      <p class="sr-only">Loading user…</p>
      <Skeleton class="h-9 w-64" aria-hidden="true" />
      <Skeleton class="h-48 w-full rounded-lg" aria-hidden="true" />
      <Skeleton class="h-56 w-full rounded-lg" aria-hidden="true" />
    </div>

    <template v-else-if="user">
      <div class="mt-4">
        <p class="eyebrow mb-2 text-primary-text">// admin · users</p>
        <div class="flex flex-wrap items-center gap-3">
          <h1 class="font-mono text-2xl font-semibold tracking-tight sm:text-3xl">{{ user.username }}</h1>
          <Badge :variant="user.user_type === 'admin' ? 'default' : 'secondary'" class="font-mono">
            {{ user.user_type }}
          </Badge>
        </div>
      </div>

      <!-- details -->
      <section aria-labelledby="details-heading" class="mt-6 rounded-lg border border-border p-4 sm:p-6">
        <h2 id="details-heading" class="text-sm font-semibold">Account</h2>
        <dl class="mt-4 grid gap-x-8 gap-y-4 sm:grid-cols-2">
          <div>
            <dt class="eyebrow text-muted-foreground">Email</dt>
            <dd class="mt-1 text-sm break-all">{{ user.email }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Name</dt>
            <dd class="mt-1 text-sm">{{ fullName }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">User ID</dt>
            <dd class="mt-1 font-mono text-xs break-all text-muted-foreground">{{ user.id }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Role</dt>
            <dd class="mt-1 font-mono text-sm">{{ user.user_type }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Created</dt>
            <dd class="mt-1 text-sm text-muted-foreground">{{ fmtDate(user.created_at) }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Last updated</dt>
            <dd class="mt-1 text-sm text-muted-foreground">{{ fmtDate(user.updated_at) }}</dd>
          </div>
        </dl>
      </section>

      <!-- public key -->
      <section aria-labelledby="key-heading" class="mt-6 rounded-lg border border-border p-4 sm:p-6">
        <h2 id="key-heading" class="text-sm font-semibold">SSH public key</h2>
        <p class="mt-1 max-w-2xl text-sm text-muted-foreground">
          The key this user's VMs are built to accept. Without one they cannot create a VM at all,
          so setting it here unblocks an account without needing its owner to sign in.
        </p>

        <!-- The blocked state is the reason an admin is on this page, so it is
             stated rather than left to be inferred from an empty field. -->
        <Alert v-if="!user.public_key" class="mt-4">
          <AlertTitle>No key on file</AlertTitle>
          <AlertDescription>This user cannot create VMs until a key is added.</AlertDescription>
        </Alert>

        <form class="mt-4 space-y-4" :aria-busy="savingKey" @submit.prevent="saveKey">
          <div class="space-y-2">
            <Label for="u-key">Public key</Label>
            <Textarea
              id="u-key"
              v-model="keyForm.public_key"
              rows="3"
              spellcheck="false"
              class="font-mono text-xs break-all"
              placeholder="ssh-ed25519 AAAA… user@host"
              aria-describedby="u-key-hint"
            />
            <p id="u-key-hint" class="text-xs text-muted-foreground">
              One key, as it appears in a <span class="font-mono">.pub</span> file. Clearing this
              field removes the key and stops them creating anything new.
            </p>
          </div>

          <FormError id="key-error" :message="keyError" />

          <div class="flex items-center gap-3">
            <Button type="submit" class="font-mono text-xs" :disabled="savingKey || !keyDirty">
              {{ savingKey ? 'Saving…' : 'Save' }}
            </Button>
            <Button
              v-if="keyDirty"
              type="button"
              variant="outline"
              class="font-mono text-xs"
              :disabled="savingKey"
              @click="discardKeyEdits"
            >
              Reset
            </Button>
            <p role="status" aria-live="polite" class="font-mono text-xs text-muted-foreground">
              {{ keySaved && !keyDirty ? 'Saved' : '' }}
            </p>
          </div>
        </form>
      </section>

      <!-- quota -->
      <section aria-labelledby="quota-heading" class="mt-6 rounded-lg border border-border p-4 sm:p-6">
        <h2 id="quota-heading" class="text-sm font-semibold">Resource allowance</h2>
        <p class="mt-1 max-w-2xl text-sm text-muted-foreground">
          What this user is entitled to, totalled across every VM they hold. Checked when they create
          a VM from their own VMs page; nothing enforces it on the host.
        </p>

        <form class="mt-4 space-y-4" :aria-busy="saving" @submit.prevent="saveQuota">
          <div class="grid gap-4 sm:max-w-md">
            <div class="space-y-2">
              <Label for="q-vcpu">vCPU</Label>
              <Input id="q-vcpu" v-model="form.vcpu_limit" type="number" min="1" step="1" inputmode="numeric" />
              <p class="font-mono text-xs text-muted-foreground">Default 2</p>
            </div>
            <div class="space-y-2">
              <Label for="q-mem">Memory (MiB)</Label>
              <Input id="q-mem" v-model="form.memory_limit_mib" type="number" min="128" step="128" inputmode="numeric" aria-describedby="q-mem-hint" />
              <p id="q-mem-hint" class="font-mono text-xs text-muted-foreground">
                Default 1024 (1 GiB)<span v-if="memoryGiB"> · currently {{ memoryGiB }}</span>
              </p>
            </div>
            <div class="space-y-2">
              <Label for="q-disk">Disk (MiB)</Label>
              <Input id="q-disk" v-model="form.disk_limit_mib" type="number" min="1024" step="1024" inputmode="numeric" aria-describedby="q-disk-hint" />
              <p id="q-disk-hint" class="font-mono text-xs text-muted-foreground">
                Default 10240 (10 GiB)<span v-if="diskGiB"> · currently {{ diskGiB }}</span>
              </p>
            </div>
          </div>

          <FormError id="quota-error" :message="saveError" />

          <div class="flex items-center gap-3">
            <Button type="submit" class="font-mono text-xs" :disabled="saving || !dirty">
              {{ saving ? 'Saving…' : 'Save' }}
            </Button>
            <Button
              v-if="dirty"
              type="button"
              variant="outline"
              class="font-mono text-xs"
              :disabled="saving"
              @click="discardEdits"
            >
              Reset
            </Button>
            <!-- The button label alone doesn't announce success, so the
                 outcome gets its own live region. (WCAG 4.1.3) -->
            <p role="status" aria-live="polite" class="font-mono text-xs text-muted-foreground">
              {{ saved && !dirty ? 'Saved' : '' }}
            </p>
          </div>
        </form>
      </section>
    </template>
  </div>
</template>
