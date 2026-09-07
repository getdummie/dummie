<script setup lang="ts">
import { GitBranch, Plus, Trash2, TriangleAlert } from '@lucide/vue'
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
import type { Integration } from '@/composables/useIntegrations'

definePageMeta({ middleware: ['auth'] })
useHead({ title: 'dummie — integrations' })

const api = useIntegrations()

const items = ref<Integration[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

async function load() {
  loading.value = true
  error.value = null
  try {
    items.value = await api.list()
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not load integrations'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

const addOpen = ref(false)
const name = ref('')
const creating = ref(false)

// Mirrors the integrations_name_shape constraint, so a bad name is caught here
// rather than as a server error.
const nameValid = computed(() => /^[a-z0-9]+(-[a-z0-9]+)*$/.test(name.value) && name.value.length >= 3 && name.value.length <= 52)

function openAdd() {
  name.value = ''
  error.value = null
  addOpen.value = true
}

// A real navigation, not a fetch: the install URL is on github.com.
async function create() {
  creating.value = true
  error.value = null
  try {
    const created = await api.create(name.value.trim())
    window.location.href = await api.installURL(created.id)
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not create the integration'
    creating.value = false
    addOpen.value = false
    await load()
  }
}

const removing = ref<Integration | null>(null)
const removeBusy = ref(false)

async function remove() {
  if (!removing.value) return
  removeBusy.value = true
  error.value = null
  try {
    await api.remove(removing.value.id)
    removing.value = null
    await load()
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not delete the integration'
  }
  finally {
    removeBusy.value = false
  }
}

const failedInstall = computed(() => {
  const reason = useRoute().query.error
  if (typeof reason !== 'string') return null
  return {
    missing_state: 'That install link was incomplete. Start again from this page.',
    expired_state: 'That install link had expired. Start again from this page.',
    awaiting_approval: 'GitHub is waiting for an organisation admin to approve the install.',
    missing_installation: 'GitHub did not say which installation was created. Try again.',
    no_app: 'No GitHub app is configured on this control server yet. Ask an admin.',
    github_unreachable: 'GitHub could not be reached. Try again.',
    save_failed: 'The installation could not be recorded. Try again.',
  }[reason] ?? 'The GitHub install did not complete.'
})
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-12 sm:px-6">
    <div class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <p class="eyebrow mb-2 text-primary-text">// integrations</p>
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">Integrations</h1>
        <p class="mt-2 max-w-2xl text-sm text-muted-foreground">
          Give a VM access to a private GitHub repository without putting a token on it. The VM
          clones from
          <code class="rounded bg-muted px-1 py-0.5 font-mono text-xs">github.int.&lt;domain&gt;</code>
          and the credential is added on the way out, so it never touches the machine.
        </p>
      </div>
      <Button v-if="items.length" class="shrink-0" @click="openAdd">
        <Plus class="size-4" aria-hidden="true" />
        New integration
      </Button>
    </div>

    <Alert v-if="failedInstall" variant="destructive" class="mt-6">
      <AlertTitle>GitHub install did not finish</AlertTitle>
      <AlertDescription>{{ failedInstall }}</AlertDescription>
    </Alert>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Something went wrong</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <div class="mt-6" aria-live="polite" :aria-busy="loading">
      <template v-if="loading">
        <div class="divide-y divide-border rounded-lg border border-border">
          <div v-for="n in 2" :key="n" class="space-y-2 p-4" aria-hidden="true">
            <Skeleton class="h-4 w-40" />
            <Skeleton class="h-3 w-full max-w-md" />
          </div>
        </div>
      </template>

      <div v-else-if="!items.length" class="rounded-lg border border-dashed border-border p-10 text-center">
        <GitBranch class="mx-auto size-8 text-muted-foreground" aria-hidden="true" />
        <p class="mt-3 text-sm font-medium">No integrations yet</p>
        <p class="mx-auto mt-1 max-w-md text-sm text-muted-foreground">
          Create one, connect a GitHub account, then attach it to the VMs that should be able to
          reach those repositories. It grants nothing until you attach it.
        </p>
        <Button class="mt-4" @click="openAdd">
          <Plus class="size-4" aria-hidden="true" />
          New integration
        </Button>
      </div>

      <div v-else class="divide-y divide-border rounded-lg border border-border">
        <div
          v-for="i in items"
          :key="i.id"
          class="flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:gap-4"
        >
          <div class="min-w-0 flex-1">
            <p class="flex flex-wrap items-center gap-2 text-sm font-medium">
              <NuxtLink :to="`/integrations/${i.id}`" class="hover:underline">{{ i.name }}</NuxtLink>
              <Badge v-if="!i.connected" variant="outline">Not connected</Badge>
              <Badge v-if="i.suspended" variant="destructive">
                <TriangleAlert class="size-3" aria-hidden="true" />
                Removed on GitHub
              </Badge>
              <Badge v-if="i.readonly" variant="secondary">Read-only</Badge>
            </p>
            <p class="mt-1 text-sm text-muted-foreground">
              <template v-if="i.connected">
                <span class="font-mono text-xs">{{ i.account }}</span>
                ·
                {{ i.all_repos ? 'every repository' : `${i.repo_count} ${i.repo_count === 1 ? 'repository' : 'repositories'}` }}
                ·
                <span :class="i.vm_count === 0 && 'text-foreground'">
                  {{ i.vm_count }} {{ i.vm_count === 1 ? 'VM' : 'VMs' }}
                </span>
              </template>
              <template v-else>
                Connect a GitHub account to finish setting this up.
              </template>
            </p>
            <p v-if="i.connected && i.vm_count === 0" class="mt-1 text-xs text-muted-foreground">
              Attached to no VMs, so it currently grants nothing.
            </p>
          </div>

          <div class="flex shrink-0 items-center gap-2">
            <Button variant="secondary" size="sm" as-child>
              <NuxtLink :to="`/integrations/${i.id}`">Configure</NuxtLink>
            </Button>
            <Button
              variant="ghost"
              size="sm"
              class="text-destructive hover:text-destructive"
              :aria-label="`Delete ${i.name}`"
              @click="removing = i"
            >
              <Trash2 class="size-4" aria-hidden="true" />
            </Button>
          </div>
        </div>
      </div>
    </div>

    <Dialog v-model:open="addOpen">
      <DialogContent>
        <form @submit.prevent="create">
          <DialogHeader>
            <DialogTitle>New integration</DialogTitle>
            <DialogDescription>
              Give it a name, then GitHub will ask which account and which repositories to grant.
              You choose which VMs may use it afterwards.
            </DialogDescription>
          </DialogHeader>

          <div class="mt-4 space-y-1.5">
            <Label for="integration-name">Name</Label>
            <Input
              id="integration-name"
              v-model="name"
              placeholder="work"
              spellcheck="false"
              autocomplete="off"
              class="font-mono text-sm"
            />
            <p class="text-xs text-muted-foreground">
              Lowercase letters, digits and single dashes, 3 to 52 characters.
            </p>
          </div>

          <DialogFooter class="mt-6">
            <DialogClose as-child>
              <Button type="button" variant="secondary">Cancel</Button>
            </DialogClose>
            <Button type="submit" :disabled="creating || !nameValid">
              {{ creating ? 'Opening GitHub…' : 'Continue to GitHub' }}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <Dialog :open="!!removing" @update:open="(v: boolean) => { if (!v) removing = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete {{ removing?.name }}?</DialogTitle>
          <DialogDescription>
            Every VM it is attached to loses access to those repositories on its next git command.
            The app stays installed on GitHub — remove it there too if you want to revoke it
            entirely.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="secondary" @click="removing = null">Cancel</Button>
          <Button variant="destructive" :disabled="removeBusy" @click="remove">
            {{ removeBusy ? 'Deleting…' : 'Delete' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
