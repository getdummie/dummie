<script setup lang="ts">
import { Pencil, Plus, Trash2 } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
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
import type { GitFlavor, GitLabInstance } from '@/composables/useIntegrations'
import { flavorLabel } from '@/composables/useIntegrations'

const { authFetch } = useAuth()

const items = ref<GitLabInstance[]>([])
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
    const res = await authFetch('/admin/gitlab-instances')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    items.value = (await res.json()).items ?? []
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not load the git instances'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

// editing is null for a new instance; the name is the subdomain, so it is
// only chosen once.
const formOpen = ref(false)
const editing = ref<GitLabInstance | null>(null)
const form = reactive<{ name: string, base_url: string, flavor: GitFlavor }>({ name: '', base_url: '', flavor: 'gitlab' })
const flavors: GitFlavor[] = ['gitlab', 'gitea']
const saving = ref(false)

const nameValid = computed(() =>
  /^[a-z0-9]+(-[a-z0-9]+)*$/.test(form.name) && form.name.length >= 2 && form.name.length <= 63
  && form.name !== 'github' && form.name !== 'llm',
)
const urlValid = computed(() => /^https:\/\/[^/?#\s]+(\/[^?#\s]*)?$/.test(form.base_url.trim()))

function openForm(i: GitLabInstance | null) {
  editing.value = i
  form.name = i?.name ?? ''
  form.base_url = i?.base_url ?? ''
  form.flavor = i?.flavor ?? 'gitlab'
  error.value = null
  formOpen.value = true
}

async function save() {
  saving.value = true
  error.value = null
  try {
    const res = editing.value
      ? await authFetch(`/admin/gitlab-instances/${editing.value.id}`, {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ base_url: form.base_url.trim() }),
        })
      : await authFetch('/admin/gitlab-instances', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: form.name.trim(), base_url: form.base_url.trim(), flavor: form.flavor }),
        })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    formOpen.value = false
    await load()
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not save the instance'
  }
  finally {
    saving.value = false
  }
}

const removing = ref<GitLabInstance | null>(null)
const removeBusy = ref(false)

async function remove() {
  if (!removing.value) return
  removeBusy.value = true
  error.value = null
  try {
    const res = await authFetch(`/admin/gitlab-instances/${removing.value.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    removing.value = null
    await load()
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not remove the instance'
  }
  finally {
    removeBusy.value = false
  }
}
</script>

<template>
  <section class="mt-12">
    <div class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <p class="eyebrow mb-2 text-primary-text">// admin · integrations</p>
        <h2 class="text-xl font-semibold tracking-tight">Git instances</h2>
        <p class="mt-2 max-w-2xl text-sm text-muted-foreground">
          GitLab, Forgejo and Gitea instances. Each is served at
          <code class="rounded bg-muted px-1 py-0.5 font-mono text-xs">&lt;name&gt;.int.&lt;tld&gt;</code>.
          Users add their own personal access token for it, and VMs clone and push through the
          proxy without holding the token.
        </p>
      </div>
      <Button variant="secondary" class="shrink-0" @click="openForm(null)">
        <Plus class="size-4" aria-hidden="true" />
        Add instance
      </Button>
    </div>

    <Alert v-if="error && !formOpen" variant="destructive" class="mt-6">
      <AlertTitle>Something went wrong</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <div class="mt-6 rounded-lg border border-border" :aria-busy="loading">
      <div v-if="loading" class="space-y-2 p-4" aria-hidden="true">
        <Skeleton class="h-4 w-40" />
        <Skeleton class="h-3 w-full max-w-md" />
      </div>

      <p v-else-if="!items.length" class="p-4 text-sm text-muted-foreground">
        No git instances yet.
      </p>

      <ul v-else class="divide-y divide-border">
        <li v-for="i in items" :key="i.id" class="flex flex-col gap-2 p-4 sm:flex-row sm:items-center sm:gap-4">
          <div class="min-w-0 flex-1">
            <p class="flex flex-wrap items-center gap-2 text-sm font-medium">
              <span class="font-mono">{{ i.name }}<span class="text-muted-foreground">.int.&lt;tld&gt;</span></span>
              <span class="text-xs font-normal text-muted-foreground">{{ flavorLabel(i.flavor) }}</span>
            </p>
            <p class="mt-0.5 truncate font-mono text-xs text-muted-foreground">{{ i.base_url }}</p>
          </div>
          <div class="flex shrink-0 items-center gap-2">
            <Button variant="ghost" size="sm" :aria-label="`Edit ${i.name}`" @click="openForm(i)">
              <Pencil class="size-4" aria-hidden="true" />
            </Button>
            <Button
              variant="ghost"
              size="sm"
              class="text-destructive hover:text-destructive"
              :aria-label="`Remove ${i.name}`"
              @click="removing = i"
            >
              <Trash2 class="size-4" aria-hidden="true" />
            </Button>
          </div>
        </li>
      </ul>
    </div>

    <Dialog v-model:open="formOpen">
      <DialogContent>
        <form @submit.prevent="save">
          <DialogHeader>
            <DialogTitle>{{ editing ? `Edit ${editing.name}` : 'Add a git instance' }}</DialogTitle>
            <DialogDescription>
              {{ editing ? 'The name is the subdomain, so only the URL can change.' : 'The name becomes the subdomain VMs clone from.' }}
            </DialogDescription>
          </DialogHeader>

          <Alert v-if="error" variant="destructive" class="mt-4">
            <AlertDescription>{{ error }}</AlertDescription>
          </Alert>

          <div class="mt-4 space-y-4">
            <div v-if="!editing" class="space-y-1.5">
              <Label id="gitlab-flavor-label">Type</Label>
              <div class="flex flex-wrap gap-2" role="radiogroup" aria-labelledby="gitlab-flavor-label">
                <Button
                  v-for="f in flavors"
                  :key="f"
                  type="button"
                  size="sm"
                  role="radio"
                  :aria-checked="form.flavor === f"
                  :variant="form.flavor === f ? 'default' : 'outline'"
                  @click="form.flavor = f"
                >
                  {{ flavorLabel(f) }}
                </Button>
              </div>
            </div>
            <div v-if="!editing" class="space-y-1.5">
              <Label for="gitlab-name">Name</Label>
              <Input
                id="gitlab-name"
                v-model="form.name"
                placeholder="techgitlab"
                spellcheck="false"
                autocomplete="off"
                class="font-mono text-sm"
              />
              <p class="text-xs text-muted-foreground">
                Lowercase letters, digits and single dashes. Not <span class="font-mono">github</span> or <span class="font-mono">llm</span>.
              </p>
            </div>
            <div class="space-y-1.5">
              <Label for="gitlab-url">URL</Label>
              <Input
                id="gitlab-url"
                v-model="form.base_url"
                :placeholder="form.flavor === 'gitea' ? 'https://forgejo.example.com' : 'https://gitlab.example.com'"
                spellcheck="false"
                autocomplete="off"
                class="font-mono text-sm"
              />
            </div>
          </div>

          <DialogFooter class="mt-6">
            <DialogClose as-child>
              <Button type="button" variant="secondary">Cancel</Button>
            </DialogClose>
            <Button type="submit" :disabled="saving || !urlValid || (!editing && !nameValid)">
              {{ saving ? 'Saving…' : 'Save' }}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <Dialog :open="!!removing" @update:open="(v: boolean) => { if (!v) removing = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Remove {{ removing?.name }}?</DialogTitle>
          <DialogDescription>
            Every user's integration for this instance is deleted with it, along with their stored
            tokens, and VMs lose access on their next git command.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="secondary" @click="removing = null">Cancel</Button>
          <Button variant="destructive" :disabled="removeBusy" @click="remove">
            {{ removeBusy ? 'Removing…' : 'Remove' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </section>
</template>
