<script setup lang="ts">
import { Check, Copy, ExternalLink, Pencil, Plus, Trash2 } from '@lucide/vue'
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
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'

interface ProviderRow {
  id: string
  slug: string
  display_name: string
  issuer: string
  client_id: string
  secret_set: boolean
  scopes: string
  enabled: boolean
  allow_signup: boolean
  redirect_uri: string
  created_at: string
  updated_at: string
}

interface Template {
  id: string
  label: string
  slug: string
  display_name: string
  issuer: string
  scopes: string
  hint: string
  issuer_editable: boolean
  docs_url?: string
  steps?: string[]
}

const { authFetch } = useAuth()

const items = ref<ProviderRow[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const toggling = ref<Record<string, boolean>>({})

const templates = ref<Template[]>([])
const redirectPattern = ref('')
const redirectSlugKey = ref('SLUG')

async function readMessage(res: Response): Promise<string | null> {
  try {
    const b = await res.json()
    return typeof b?.message === 'string' ? b.message : null
  }
  catch {
    return null
  }
}

async function loadTemplates() {
  try {
    const res = await authFetch('/admin/oidc/templates')
    if (!res.ok) return
    const data = await res.json()
    templates.value = data.items ?? []
    redirectPattern.value = data.redirect_uri_pattern ?? ''
    redirectSlugKey.value = data.redirect_uri_slug_key ?? 'SLUG'
  }
  catch {
  }
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await authFetch('/admin/oidc/providers')
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    items.value = (await res.json()).items ?? []
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load providers'
  }
  finally {
    loading.value = false
  }
}

onMounted(() => {
  void loadTemplates()
  void load()
})

const CUSTOM = '__custom__'

const dialogOpen = ref(false)
const editing = ref<ProviderRow | null>(null)
const saving = ref(false)
const formError = ref<string | null>(null)
const templateId = ref(CUSTOM)

const blankForm = {
  slug: '',
  display_name: '',
  issuer: '',
  client_id: '',
  client_secret: '',
  scopes: 'openid profile email',
  enabled: true,
  allow_signup: true,
  clear_secret: false,
}

const form = reactive({ ...blankForm })

const activeTemplate = computed(() =>
  editing.value ? null : templates.value.find(t => t.id === templateId.value) ?? null,
)

const issuerLocked = computed(() => !!activeTemplate.value && !activeTemplate.value.issuer_editable)

const previewRedirectURI = computed(() => {
  if (editing.value) return editing.value.redirect_uri
  if (!redirectPattern.value) return ''
  return redirectPattern.value.replace(redirectSlugKey.value, form.slug || 'your-slug')
})

function applyTemplate(id: string) {
  templateId.value = id
  const t = templates.value.find(x => x.id === id)
  if (!t) {
    Object.assign(form, { slug: '', display_name: '', issuer: '', scopes: blankForm.scopes })
    return
  }
  form.slug = t.slug
  form.display_name = t.display_name
  form.issuer = t.issuer
  form.scopes = t.scopes
}

function openAdd() {
  editing.value = null
  Object.assign(form, blankForm)
  formError.value = null
  applyTemplate(templates.value[0]?.id ?? CUSTOM)
  dialogOpen.value = true
}

function openEdit(p: ProviderRow) {
  editing.value = p
  Object.assign(form, {
    slug: p.slug,
    display_name: p.display_name,
    issuer: p.issuer,
    client_id: p.client_id,
    client_secret: '',
    scopes: p.scopes,
    enabled: p.enabled,
    allow_signup: p.allow_signup,
    clear_secret: false,
  })
  formError.value = null
  dialogOpen.value = true
}

async function save() {
  saving.value = true
  formError.value = null
  try {
    const editingRow = editing.value
    const path = editingRow ? `/admin/oidc/providers/${editingRow.id}` : '/admin/oidc/providers'
    const res = await authFetch(path, {
      method: editingRow ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        slug: form.slug,
        display_name: form.display_name,
        issuer: form.issuer,
        client_id: form.client_id,
        client_secret: form.client_secret,
        clear_secret: form.clear_secret,
        scopes: form.scopes,
        enabled: form.enabled,
        allow_signup: form.allow_signup,
      }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    dialogOpen.value = false
    await load()
  }
  catch (e) {
    formError.value = e instanceof Error ? e.message : 'Could not save the provider'
  }
  finally {
    saving.value = false
  }
}

async function toggle(p: ProviderRow, enabled: boolean) {
  const previous = p.enabled
  p.enabled = enabled
  toggling.value = { ...toggling.value, [p.id]: true }
  error.value = null
  try {
    const res = await authFetch(`/admin/oidc/providers/${p.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        display_name: p.display_name,
        issuer: p.issuer,
        client_id: p.client_id,
        scopes: p.scopes,
        enabled,
      }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
  }
  catch (e) {
    p.enabled = previous
    error.value = e instanceof Error ? e.message : 'Could not change the provider'
  }
  finally {
    const next = { ...toggling.value }
    delete next[p.id]
    toggling.value = next
  }
}

const confirming = ref<ProviderRow | null>(null)
const deleting = ref(false)

async function remove() {
  const target = confirming.value
  if (!target) return
  deleting.value = true
  error.value = null
  try {
    const res = await authFetch(`/admin/oidc/providers/${target.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    confirming.value = null
    await load()
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not delete the provider'
  }
  finally {
    deleting.value = false
  }
}

const copiedKey = ref<string | null>(null)

async function copy(key: string, value: string) {
  try {
    await navigator.clipboard.writeText(value)
    copiedKey.value = key
    setTimeout(() => {
      if (copiedKey.value === key) copiedKey.value = null
    }, 2000)
  }
  catch {
    error.value = 'Could not copy to the clipboard'
  }
}
</script>

<template>
  <section class="mt-12">
    <div class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <p class="eyebrow mb-2 text-primary-text">// admin · single sign-on</p>
        <h2 class="text-xl font-semibold tracking-tight">Sign-in providers</h2>
        <p class="mt-2 max-w-2xl text-sm text-muted-foreground">
          Any number of OIDC providers. Each one becomes a button on the sign-in page, and each
          decides for itself whether a first sign-in may create an account — independently of the
          email-and-password setting above. An identity is only matched to an existing account on an
          email the provider has verified.
        </p>
      </div>
      <Button class="shrink-0" @click="openAdd">
        <Plus class="size-4" aria-hidden="true" />
        Add provider
      </Button>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Something went wrong</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <div class="mt-6 divide-y divide-border rounded-lg border border-border" aria-live="polite" :aria-busy="loading">
      <template v-if="loading">
        <div v-for="n in 2" :key="n" class="space-y-2 p-4" aria-hidden="true">
          <Skeleton class="h-4 w-40" />
          <Skeleton class="h-3 w-full max-w-md" />
        </div>
      </template>

      <p v-else-if="!items.length" class="p-6 text-center text-sm text-muted-foreground">
        No providers yet. Add one to offer single sign-on.
      </p>

      <div
        v-for="p in items"
        v-else
        :key="p.id"
        class="flex flex-col gap-3 p-4 sm:flex-row sm:items-start sm:gap-4"
      >
        <div class="min-w-0 flex-1">
          <p :id="`oidc-${p.id}-label`" class="flex flex-wrap items-center gap-2 text-sm font-medium">
            {{ p.display_name }}
            <Badge v-if="!p.enabled" variant="secondary">Disabled</Badge>
            <Badge v-if="!p.allow_signup" variant="outline">Existing accounts only</Badge>
            <Badge v-if="!p.secret_set" variant="outline">Public client (PKCE)</Badge>
          </p>
          <p class="mt-1 font-mono text-xs break-all text-muted-foreground">
            {{ p.slug }} · {{ p.issuer }}
          </p>
          <p class="mt-1 font-mono text-xs break-all text-muted-foreground">
            {{ p.scopes }}
          </p>
          <div class="mt-2 flex flex-wrap items-center gap-2">
            <span class="text-xs text-muted-foreground">Redirect URI</span>
            <code class="min-w-0 rounded bg-muted px-1.5 py-0.5 font-mono text-xs break-all">{{ p.redirect_uri }}</code>
            <Button
              variant="ghost"
              size="sm"
              :aria-label="`Copy the redirect URI for ${p.display_name}`"
              @click="copy(p.id, p.redirect_uri)"
            >
              <component :is="copiedKey === p.id ? Check : Copy" class="size-3.5" aria-hidden="true" />
              {{ copiedKey === p.id ? 'Copied' : 'Copy' }}
            </Button>
          </div>
        </div>

        <div class="flex shrink-0 items-center gap-2 sm:mt-1">
          <Switch
            :model-value="p.enabled"
            :disabled="toggling[p.id]"
            :aria-labelledby="`oidc-${p.id}-label`"
            @update:model-value="(v: boolean) => toggle(p, v)"
          />
          <Button variant="secondary" size="sm" :aria-label="`Edit ${p.display_name}`" @click="openEdit(p)">
            <Pencil class="size-3.5" aria-hidden="true" />
            Edit
          </Button>
          <Button
            variant="ghost"
            size="sm"
            class="text-destructive"
            :aria-label="`Delete ${p.display_name}`"
            @click="confirming = p"
          >
            <Trash2 class="size-3.5" aria-hidden="true" />
          </Button>
        </div>
      </div>
    </div>

    <Dialog v-model:open="dialogOpen">
      <DialogContent class="max-h-[90vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{{ editing ? `Edit ${editing.display_name}` : 'Add a sign-in provider' }}</DialogTitle>
          <DialogDescription>
            The endpoints are discovered from the issuer, so this is everything the server needs.
          </DialogDescription>
        </DialogHeader>

        <form class="space-y-4" @submit.prevent="save">
          <fieldset v-if="!editing && templates.length" class="space-y-2">
            <legend class="mb-2 text-sm font-medium">Provider</legend>
            <div class="flex flex-wrap gap-2">
              <Button
                v-for="t in templates"
                :key="t.id"
                type="button"
                :variant="templateId === t.id ? 'default' : 'secondary'"
                size="sm"
                :aria-pressed="templateId === t.id"
                @click="applyTemplate(t.id)"
              >
                {{ t.label }}
              </Button>
              <Button
                type="button"
                :variant="templateId === CUSTOM ? 'default' : 'secondary'"
                size="sm"
                :aria-pressed="templateId === CUSTOM"
                @click="applyTemplate(CUSTOM)"
              >
                Other provider
              </Button>
            </div>
          </fieldset>

          <div v-if="activeTemplate" class="space-y-2 rounded-md border border-border bg-muted/40 p-3">
            <p v-if="activeTemplate.hint" class="text-xs text-muted-foreground">{{ activeTemplate.hint }}</p>
            <ol v-if="activeTemplate.steps?.length" class="list-decimal space-y-1 pl-4 text-xs text-muted-foreground">
              <li v-for="(step, i) in activeTemplate.steps" :key="i">{{ step }}</li>
            </ol>
            <a
              v-if="activeTemplate.docs_url"
              :href="activeTemplate.docs_url"
              target="_blank"
              rel="noopener noreferrer"
              class="inline-flex items-center gap-1 text-xs text-primary-text underline underline-offset-4"
            >
              Open {{ activeTemplate.label }} credentials
              <ExternalLink class="size-3" aria-hidden="true" />
            </a>
          </div>

          <div v-if="previewRedirectURI" class="space-y-2">
            <Label as="p">Redirect URI to register</Label>
            <div class="flex items-start gap-2">
              <code class="min-w-0 flex-1 rounded bg-muted px-2 py-1.5 font-mono text-xs break-all">{{ previewRedirectURI }}</code>
              <Button
                type="button"
                variant="secondary"
                size="sm"
                class="shrink-0"
                aria-label="Copy the redirect URI"
                @click="copy('form', previewRedirectURI)"
              >
                <component :is="copiedKey === 'form' ? Check : Copy" class="size-3.5" aria-hidden="true" />
                {{ copiedKey === 'form' ? 'Copied' : 'Copy' }}
              </Button>
            </div>
          </div>

          <div class="space-y-2">
            <Label for="oidc-name">Display name</Label>
            <Input id="oidc-name" v-model="form.display_name" placeholder="Google" required />
            <p class="text-xs text-muted-foreground">What the button on the sign-in page says.</p>
          </div>

          <div class="space-y-2">
            <Label for="oidc-slug">Slug</Label>
            <Input
              id="oidc-slug"
              v-model="form.slug"
              placeholder="google"
              :disabled="!!editing"
              required
              spellcheck="false"
              class="font-mono text-sm"
            />
            <p class="text-xs text-muted-foreground">
              {{ editing
                ? 'Fixed: it is part of the redirect URI your provider has registered.'
                : 'Lowercase letters, numbers and dashes. Becomes part of the redirect URI above.' }}
            </p>
          </div>

          <div class="space-y-2">
            <Label for="oidc-issuer">Issuer</Label>
            <Input
              id="oidc-issuer"
              v-model="form.issuer"
              placeholder="https://accounts.google.com"
              :readonly="issuerLocked"
              required
              spellcheck="false"
              class="font-mono text-sm"
              :class="issuerLocked && 'text-muted-foreground'"
            />
            <p v-if="issuerLocked" class="text-xs text-muted-foreground">
              Fixed for {{ activeTemplate?.label }}. Choose "Other provider" to set your own.
            </p>
          </div>

          <div class="space-y-2">
            <Label for="oidc-client-id">Client ID</Label>
            <Input id="oidc-client-id" v-model="form.client_id" required spellcheck="false" class="font-mono text-sm" />
          </div>

          <div class="space-y-2">
            <Label for="oidc-client-secret">Client secret</Label>
            <Input
              id="oidc-client-secret"
              v-model="form.client_secret"
              type="password"
              autocomplete="new-password"
              :disabled="form.clear_secret"
              :placeholder="editing && editing.secret_set ? '••••••••' : 'Leave empty for a public client'"
              class="font-mono text-sm"
            />
            <p class="text-xs text-muted-foreground">
              Never shown again once saved. Leave it empty for a public client that authenticates with PKCE alone.
            </p>
            <label v-if="editing && editing.secret_set" class="flex items-center gap-2 text-xs text-muted-foreground">
              <input v-model="form.clear_secret" type="checkbox" class="size-3.5 accent-current">
              Remove the stored secret and use PKCE alone
            </label>
          </div>

          <div class="space-y-2">
            <Label for="oidc-scopes">Scopes</Label>
            <Input id="oidc-scopes" v-model="form.scopes" spellcheck="false" class="font-mono text-sm" />
            <p class="text-xs text-muted-foreground">
              openid is always requested. email is what an account is matched on.
            </p>
          </div>

          <div class="space-y-3 rounded-md border border-border p-3">
            <div class="flex items-start gap-3">
              <Switch id="oidc-enabled" v-model="form.enabled" class="mt-0.5" />
              <div>
                <Label for="oidc-enabled">Offer this provider on the sign-in page</Label>
                <p class="mt-1 text-xs text-muted-foreground">Off hides the button and refuses the flow.</p>
              </div>
            </div>
            <div class="flex items-start gap-3">
              <Switch id="oidc-allow-signup" v-model="form.allow_signup" class="mt-0.5" />
              <div>
                <Label for="oidc-allow-signup">Let a first sign-in create an account</Label>
                <p class="mt-1 text-xs text-muted-foreground">
                  Off means only accounts that already exist here can sign in through this provider.
                  Separate from the email-and-password setting.
                </p>
              </div>
            </div>
          </div>

          <FormError id="oidc-form-error" :message="formError" />

          <DialogFooter>
            <DialogClose as-child>
              <Button type="button" variant="secondary">Cancel</Button>
            </DialogClose>
            <Button type="submit" :disabled="saving">
              {{ saving ? 'Saving…' : (editing ? 'Save changes' : 'Add provider') }}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <Dialog :open="!!confirming" @update:open="(v: boolean) => { if (!v) confirming = null }">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Delete {{ confirming?.display_name }}?</DialogTitle>
          <DialogDescription>
            The provider and the links from it to accounts here are removed. The accounts themselves stay,
            but anyone who only ever signed in through this provider will have no way back in until an
            admin gives them one.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="secondary">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" :disabled="deleting" @click="remove">
            {{ deleting ? 'Deleting…' : 'Delete provider' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </section>
</template>
