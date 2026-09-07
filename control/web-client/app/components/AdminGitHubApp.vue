<script setup lang="ts">
import { Check, Copy, ExternalLink, Trash2 } from '@lucide/vue'
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
import { Textarea } from '@/components/ui/textarea'

interface GitHubApp {
  app_id: number
  slug: string
  name: string
  key_set: boolean
  callback_url: string
  updated_at: string
}

const { authFetch } = useAuth()

const app = ref<GitHubApp | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)
const saving = ref(false)
const saved = ref(false)

const form = reactive({ app_id: '', slug: '', name: '', private_key: '' })

const configured = computed(() => !!app.value?.key_set)

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
    const res = await authFetch('/admin/github-app')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    app.value = await res.json()
    form.app_id = app.value?.app_id ? String(app.value.app_id) : ''
    form.slug = app.value?.slug ?? ''
    form.name = app.value?.name ?? ''
    form.private_key = ''
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not load the GitHub app'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

async function save() {
  saving.value = true
  error.value = null
  saved.value = false
  try {
    const res = await authFetch('/admin/github-app', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        app_id: form.app_id.trim(),
        slug: form.slug.trim(),
        name: form.name.trim(),
        private_key: form.private_key,
      }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    app.value = await res.json()
    form.private_key = ''
    saved.value = true
    setTimeout(() => { saved.value = false }, 2000)
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not save the GitHub app'
  }
  finally {
    saving.value = false
  }
}

const removing = ref(false)
const confirmOpen = ref(false)

async function remove() {
  removing.value = true
  error.value = null
  try {
    const res = await authFetch('/admin/github-app', { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    confirmOpen.value = false
    await load()
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not remove the GitHub app'
  }
  finally {
    removing.value = false
  }
}

const copied = ref(false)

async function copyCallback() {
  if (!app.value?.callback_url) return
  try {
    await navigator.clipboard.writeText(app.value.callback_url)
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  }
  catch {
    error.value = 'Could not copy to the clipboard'
  }
}

const dirty = computed(() =>
  form.private_key !== ''
  || form.app_id.trim() !== (app.value?.app_id ? String(app.value.app_id) : '')
  || form.slug.trim() !== (app.value?.slug ?? '')
  || form.name.trim() !== (app.value?.name ?? ''),
)

const permissions = [
  ['Contents', 'Read & write'],
  ['Metadata', 'Read-only'],
  ['Pull requests', 'Read & write'],
  ['Issues', 'Read & write'],
]
</script>

<template>
  <section class="mt-12">
    <div class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <p class="eyebrow mb-2 text-primary-text">// admin · integrations</p>
        <h2 class="text-xl font-semibold tracking-tight">GitHub app</h2>
        <p class="mt-2 max-w-2xl text-sm text-muted-foreground">
          Backs the GitHub integration, so a VM can clone a private repository from
          <code class="rounded bg-muted px-1 py-0.5 font-mono text-xs">github.int.&lt;tld&gt;</code>
          without ever holding a credential. This private key is the only long-lived secret
          involved — it stays on this server, and each request mints a short-lived token scoped to
          one repository.
        </p>
      </div>
      <Badge v-if="!loading" :variant="configured ? 'default' : 'outline'" class="shrink-0">
        {{ configured ? 'Configured' : 'Not configured' }}
      </Badge>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Something went wrong</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <div class="mt-6 rounded-lg border border-border p-4 sm:p-6" :aria-busy="loading">
      <template v-if="loading">
        <div class="space-y-3" aria-hidden="true">
          <Skeleton class="h-4 w-40" />
          <Skeleton class="h-9 w-full max-w-sm" />
          <Skeleton class="h-4 w-32" />
          <Skeleton class="h-24 w-full" />
        </div>
      </template>

      <template v-else>
        <div class="rounded-md bg-muted/40 p-4 text-sm">
          <p class="font-medium">Creating the app on GitHub</p>
          <ol class="mt-2 list-decimal space-y-1.5 pl-5 text-muted-foreground">
            <li>
              <a
                href="https://github.com/settings/apps/new"
                target="_blank"
                rel="noreferrer"
                class="inline-flex items-center gap-1 underline underline-offset-2"
              >
                Register a new GitHub app
                <ExternalLink class="size-3" aria-hidden="true" />
              </a>
              — or under an organisation's settings, to install it there.
            </li>
            <li>
              Set <span class="font-medium">Setup URL</span> to the callback below and tick
              <span class="font-medium">Redirect on update</span>. Without it GitHub has nowhere to
              return after an install, so it leaves the user on its own settings page and the
              integration never finishes connecting.
            </li>
            <li>Untick <span class="font-medium">Webhook → Active</span>; nothing here listens for them.</li>
            <li>
              Set <span class="font-medium">Where can this GitHub App be installed?</span> to
              <span class="font-medium">Any account</span>. The default, "Only on this account",
              locks it to whoever created it — users then cannot install it into their own account
              and only ever see that one account's repositories.
            </li>
            <li>
              Grant these repository permissions:
              <span class="mt-1.5 flex flex-wrap gap-1.5">
                <Badge v-for="[p, level] in permissions" :key="p" variant="secondary" class="font-normal">
                  {{ p }}: {{ level }}
                </Badge>
              </span>
              <span class="mt-1.5 block">
                Grant all of them even for read-only use. Raising an app's permissions later makes
                every installer re-approve it, while an integration marked read-only narrows the
                token down at mint time and needs no approval.
              </span>
            </li>
            <li>Generate a private key, then paste it below with the app ID and slug.</li>
          </ol>

          <div class="mt-4 flex flex-wrap items-center gap-2">
            <span class="text-xs text-muted-foreground">Setup URL</span>
            <code class="min-w-0 rounded bg-muted px-1.5 py-0.5 font-mono text-xs break-all">
              {{ app?.callback_url }}
            </code>
            <Button variant="ghost" size="sm" aria-label="Copy the setup URL" @click="copyCallback">
              <component :is="copied ? Check : Copy" class="size-3.5" aria-hidden="true" />
              {{ copied ? 'Copied' : 'Copy' }}
            </Button>
          </div>
        </div>

        <form class="mt-6 space-y-4" @submit.prevent="save">
          <div class="grid gap-4 sm:grid-cols-3">
            <div class="space-y-1.5">
              <Label for="gh-app-id">App ID</Label>
              <Input
                id="gh-app-id"
                v-model="form.app_id"
                inputmode="numeric"
                placeholder="123456"
                spellcheck="false"
                class="font-mono text-sm"
              />
            </div>
            <div class="space-y-1.5">
              <Label for="gh-app-slug">Slug</Label>
              <Input
                id="gh-app-slug"
                v-model="form.slug"
                placeholder="my-dummie-app"
                spellcheck="false"
                class="font-mono text-sm"
              />
              <p class="text-xs text-muted-foreground">
                The name in <span class="font-mono">github.com/apps/&lt;slug&gt;</span>. The install link uses it.
              </p>
            </div>
            <div class="space-y-1.5">
              <Label for="gh-app-name">Display name</Label>
              <Input id="gh-app-name" v-model="form.name" placeholder="dummie" spellcheck="false" class="text-sm" />
            </div>
          </div>

          <div class="space-y-1.5">
            <Label for="gh-app-key">Private key (PEM)</Label>
            <Textarea
              id="gh-app-key"
              v-model="form.private_key"
              rows="6"
              :placeholder="app?.key_set
                ? 'A key is stored. Paste a new one only to replace it.'
                : '-----BEGIN RSA PRIVATE KEY-----'"
              spellcheck="false"
              autocomplete="off"
              class="font-mono text-xs"
            />
            <p class="text-xs text-muted-foreground">
              {{ app?.key_set
                ? 'Stored, and never sent back to this page. Leave empty to keep it.'
                : 'The .pem GitHub downloaded when you generated the key.' }}
              It is checked for signing before it is saved.
            </p>
          </div>

          <div class="flex flex-wrap items-center gap-2">
            <Button type="submit" :disabled="saving || !dirty">
              {{ saving ? 'Saving…' : 'Save' }}
            </Button>
            <span v-if="saved" class="text-sm text-muted-foreground">Saved</span>
            <Button
              v-if="configured"
              type="button"
              variant="ghost"
              class="ml-auto text-destructive hover:text-destructive"
              @click="confirmOpen = true"
            >
              <Trash2 class="size-4" aria-hidden="true" />
              Remove
            </Button>
          </div>
        </form>
      </template>
    </div>

    <Dialog v-model:open="confirmOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Remove the GitHub app?</DialogTitle>
          <DialogDescription>
            Every integration stops working immediately: no new tokens can be minted, so clones and
            pushes from VMs will be refused. Tokens already issued keep working until they expire.
            The app itself stays on GitHub — this only forgets its credentials here.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <DialogClose as-child>
            <Button variant="secondary">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" :disabled="removing" @click="remove">
            {{ removing ? 'Removing…' : 'Remove' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </section>
</template>
