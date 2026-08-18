<script setup lang="ts">
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'

definePageMeta({ middleware: ['auth'] })
useHead({ title: 'dummie — settings' })

// The same shape /admin/users/:id returns, because it is the same endpoint's DTO.
// Only two of these fields are writable here; the rest are an admin's to set and
// are shown so a user can read what they have been given.
interface Profile {
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

const { authFetch, user: sessionUser } = useAuth()

const profile = ref<Profile | null>(null)
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

/** "8th August 2026, 12:07 AM" */
function fmtDate(s: string) {
  if (!s) return '—'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  const date = `${ordinal(d.getDate())} ${d.toLocaleString(undefined, { month: 'long' })} ${d.getFullYear()}`
  return `${date}, ${d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })}`
}

const form = reactive({ first_name: '', last_name: '', public_key: '' })
const saving = ref(false)
const saveError = ref<string | null>(null)
const saved = ref(false)

function resetForm(p: Profile) {
  form.first_name = p.first_name
  form.last_name = p.last_name
  form.public_key = p.public_key
}

function discardEdits() {
  if (profile.value) resetForm(profile.value)
  saveError.value = null
}

const dirty = computed(() => {
  const p = profile.value
  if (!p) return false
  return form.first_name !== p.first_name
    || form.last_name !== p.last_name
    || form.public_key.trim() !== p.public_key
})

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await authFetch('/me')
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    const data: Profile = await res.json()
    profile.value = data
    resetForm(data)
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load your profile'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

async function save() {
  saving.value = true
  saveError.value = null
  saved.value = false
  try {
    const res = await authFetch('/me', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        first_name: form.first_name,
        last_name: form.last_name,
        public_key: form.public_key,
      }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    const data: Profile = await res.json()
    profile.value = data
    // From the response, not from what was typed: the server trims the name and
    // canonicalises the key, so echoing the input back leaves the form dirty.
    resetForm(data)
    // The session copy feeds the sidebar's name and initials, and it was minted
    // at sign-in — nothing else would refresh it until the next token refresh.
    if (sessionUser.value) {
      sessionUser.value = { ...sessionUser.value, first_name: data.first_name, last_name: data.last_name }
    }
    saved.value = true
  }
  catch (e) {
    saveError.value = e instanceof Error ? e.message : 'Could not save your profile'
  }
  finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-3xl px-4 py-12 sm:px-6">
    <div>
      <p class="eyebrow mb-2 text-primary-text">// settings</p>
      <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">Your profile</h1>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load your profile</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <div v-else-if="loading" class="mt-6 space-y-6" aria-busy="true">
      <p class="sr-only">Loading your profile…</p>
      <Skeleton class="h-44 w-full rounded-lg" aria-hidden="true" />
      <Skeleton class="h-64 w-full rounded-lg" aria-hidden="true" />
    </div>

    <template v-else-if="profile">
      <!-- account: read-only -->
      <section aria-labelledby="account-heading" class="mt-6 rounded-lg border border-border p-4 sm:p-6">
        <h2 id="account-heading" class="text-sm font-semibold">Account</h2>
        <p class="mt-1 text-sm text-muted-foreground">
          Set when your account was created. Ask an admin to change any of it.
        </p>
        <dl class="mt-4 grid gap-x-8 gap-y-4 sm:grid-cols-2">
          <div>
            <dt class="eyebrow text-muted-foreground">Username</dt>
            <dd class="mt-1 font-mono text-sm break-all">{{ profile.username }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Email</dt>
            <dd class="mt-1 text-sm break-all">{{ profile.email }}</dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Role</dt>
            <dd class="mt-1">
              <Badge :variant="profile.user_type === 'admin' ? 'default' : 'secondary'" class="font-mono">
                {{ profile.user_type }}
              </Badge>
            </dd>
          </div>
          <div>
            <dt class="eyebrow text-muted-foreground">Member since</dt>
            <dd class="mt-1 text-sm text-muted-foreground">{{ fmtDate(profile.created_at) }}</dd>
          </div>
        </dl>

        <h3 class="mt-6 text-sm font-semibold">Allowance</h3>
        <dl class="mt-3 grid gap-4 sm:grid-cols-3">
          <div class="rounded-lg border border-border p-3">
            <dt class="eyebrow text-muted-foreground">vCPU</dt>
            <dd class="mt-1 font-mono text-lg">{{ profile.vcpu_limit }}</dd>
          </div>
          <div class="rounded-lg border border-border p-3">
            <dt class="eyebrow text-muted-foreground">Memory</dt>
            <dd class="mt-1 font-mono text-lg">{{ fmtMiB(profile.memory_limit_mib) }}</dd>
          </div>
          <div class="rounded-lg border border-border p-3">
            <dt class="eyebrow text-muted-foreground">Disk</dt>
            <dd class="mt-1 font-mono text-lg">{{ fmtMiB(profile.disk_limit_mib) }}</dd>
          </div>
        </dl>
        <p class="mt-3 text-sm text-muted-foreground">
          What you may hold across every VM at once. See how much of it is in use on
          <NuxtLink
            to="/vms"
            class="text-primary-text underline decoration-primary-text/40 underline-offset-4 transition-colors hover:decoration-primary-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
          >
            your VMs
          </NuxtLink>.
        </p>
      </section>

      <!-- editable -->
      <form class="mt-6 space-y-6" :aria-busy="saving" @submit.prevent="save">
        <section aria-labelledby="name-heading" class="rounded-lg border border-border p-4 sm:p-6">
          <h2 id="name-heading" class="text-sm font-semibold">Name</h2>
          <p class="mt-1 text-sm text-muted-foreground">
            How you are shown around the app. Neither field is used to sign in.
          </p>
          <div class="mt-4 grid gap-4 sm:grid-cols-2">
            <div class="space-y-2">
              <Label for="p-first">First name</Label>
              <Input id="p-first" v-model="form.first_name" autocomplete="given-name" maxlength="100" />
            </div>
            <div class="space-y-2">
              <Label for="p-last">Last name</Label>
              <Input id="p-last" v-model="form.last_name" autocomplete="family-name" maxlength="100" />
            </div>
          </div>
        </section>

        <section aria-labelledby="key-heading" class="rounded-lg border border-border p-4 sm:p-6">
          <h2 id="key-heading" class="text-sm font-semibold">SSH public key</h2>
          <p class="mt-1 text-sm text-muted-foreground">
            Built into every VM you create, so you can log into it. It has to be there before the VM
            boots — changing it here does not reach VMs you already have.
          </p>

          <!-- Stated up front: this is the one setting whose absence blocks the
               thing the user came to the app to do. -->
          <Alert v-if="!profile.public_key" class="mt-4">
            <AlertTitle>You cannot create VMs yet</AlertTitle>
            <AlertDescription>Add your public key below to create your first VM.</AlertDescription>
          </Alert>

          <div class="mt-4 space-y-2">
            <Label for="p-key">Public key</Label>
            <Textarea
              id="p-key"
              v-model="form.public_key"
              rows="3"
              spellcheck="false"
              autocomplete="off"
              class="font-mono text-xs break-all"
              placeholder="ssh-ed25519 AAAA… you@laptop"
              aria-describedby="p-key-hint"
            />
            <!-- Names the file, because "paste your public key" is the step where
                 the private one gets pasted instead. -->
            <p id="p-key-hint" class="text-xs text-muted-foreground">
              The contents of your <span class="font-mono">.pub</span> file — usually
              <span class="font-mono">~/.ssh/id_ed25519.pub</span>. Never paste the matching private
              key, which has no <span class="font-mono">.pub</span> on the end.
            </p>
          </div>
        </section>

        <FormError id="profile-error" :message="saveError" />

        <div class="flex items-center gap-3">
          <Button type="submit" class="font-mono text-xs" :disabled="saving || !dirty">
            {{ saving ? 'Saving…' : 'Save changes' }}
          </Button>
          <Button
            v-if="dirty"
            type="button"
            variant="outline"
            class="font-mono text-xs"
            :disabled="saving"
            @click="discardEdits"
          >
            Discard
          </Button>
          <!-- The button label alone doesn't announce success, so the outcome
               gets its own live region. (WCAG 4.1.3) -->
          <p role="status" aria-live="polite" class="font-mono text-xs text-muted-foreground">
            {{ saved && !dirty ? 'Saved' : '' }}
          </p>
        </div>
      </form>

      <!-- Outside the profile form: these save themselves, and nesting them
           would put their buttons inside a form that submits the profile. -->
      <PersonalAccessTokens class="mt-6" />
    </template>
  </div>
</template>
