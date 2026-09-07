<script setup lang="ts">
import { ArrowLeft, ExternalLink, GitBranch, RefreshCw, TriangleAlert } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import type { Integration, OwnedVM } from '@/composables/useIntegrations'
import { integrationHost } from '@/composables/useIntegrations'

definePageMeta({ middleware: ['auth'] })

const route = useRoute()
const id = computed(() => String(route.params.id))

const api = useIntegrations()

const item = ref<Integration | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)

const available = ref<string[]>([])
const availableError = ref<string | null>(null)
const availableLoading = ref(false)

const selected = ref<Set<string>>(new Set())
const savingRepos = ref(false)

const vms = ref<OwnedVM[]>([])
const attached = ref<Set<string>>(new Set())
const vmBusy = ref<Record<string, boolean>>({})

useHead(() => ({ title: `dummie — ${item.value?.name ?? 'integration'}` }))

async function load() {
  loading.value = true
  error.value = null
  try {
    const [i, a, v] = await Promise.all([api.get(id.value), api.attachedVMs(id.value), api.ownedVMs()])
    item.value = i
    selected.value = new Set(i.repos ?? [])
    attached.value = new Set(a.map(x => x.id))
    vms.value = v
    if (i.connected) await loadAvailable()
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not load the integration'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

async function loadAvailable() {
  availableLoading.value = true
  availableError.value = null
  try {
    available.value = await api.availableRepos(id.value)
  }
  catch (e) {
    availableError.value = e instanceof Error ? e.message : 'Could not ask GitHub for the repositories'
  }
  finally {
    availableLoading.value = false
  }
}

async function connect() {
  error.value = null
  try {
    window.location.href = await api.installURL(id.value)
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not start the GitHub install'
  }
}

function toggleRepo(repo: string, on: boolean) {
  const next = new Set(selected.value)
  if (on) next.add(repo)
  else next.delete(repo)
  selected.value = next
}

const reposDirty = computed(() => {
  const was = new Set(item.value?.repos ?? [])
  if (was.size !== selected.value.size) return true
  for (const r of selected.value) if (!was.has(r)) return true
  return false
})

async function saveRepos() {
  savingRepos.value = true
  error.value = null
  try {
    await api.setRepos(id.value, [...selected.value])
    if (item.value) item.value.repos = [...selected.value]
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not save the repositories'
  }
  finally {
    savingRepos.value = false
  }
}

async function setFlags(patch: { all_repos?: boolean, readonly?: boolean }) {
  if (!item.value) return
  const before = { all_repos: item.value.all_repos, readonly: item.value.readonly }
  const next = { ...before, ...patch }
  item.value.all_repos = next.all_repos
  item.value.readonly = next.readonly
  error.value = null
  try {
    await api.update(id.value, next)
  }
  catch (e) {
    item.value.all_repos = before.all_repos
    item.value.readonly = before.readonly
    error.value = e instanceof Error ? e.message : 'Could not save'
  }
}

async function toggleVM(vm: OwnedVM, on: boolean) {
  vmBusy.value = { ...vmBusy.value, [vm.id]: true }
  error.value = null
  const next = new Set(attached.value)
  try {
    if (on) {
      await api.attach(id.value, vm.id)
      next.add(vm.id)
    }
    else {
      await api.detach(id.value, vm.id)
      next.delete(vm.id)
    }
    attached.value = next
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not change the attachment'
  }
  finally {
    const b = { ...vmBusy.value }
    delete b[vm.id]
    vmBusy.value = b
  }
}

// The usage snippet needs a fleet domain, which comes from a VM's own hostname.
// An attached VM is the accurate source; otherwise any of the user's will do.
const usage = computed(() => {
  const attachedVM = vms.value.find(v => attached.value.has(v.id))
  const host = integrationHost(attachedVM ?? vms.value[0])
  const repo = [...selected.value][0]
  return host ? { host, repo } : null
})
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-12 sm:px-6">
    <Button variant="ghost" size="sm" as-child class="-ml-2">
      <NuxtLink to="/integrations">
        <ArrowLeft class="size-4" aria-hidden="true" />
        Integrations
      </NuxtLink>
    </Button>

    <div v-if="loading" class="mt-6 space-y-4" aria-hidden="true">
      <Skeleton class="h-8 w-56" />
      <Skeleton class="h-32 w-full" />
      <Skeleton class="h-48 w-full" />
    </div>

    <template v-else-if="item">
      <div class="mt-4">
        <p class="eyebrow mb-2 text-primary-text">// integrations</p>
        <h1 class="flex flex-wrap items-center gap-2 text-2xl font-semibold tracking-tight sm:text-3xl">
          {{ item.name }}
          <Badge v-if="item.readonly" variant="secondary">Read-only</Badge>
        </h1>
        <p v-if="item.connected" class="mt-2 text-sm text-muted-foreground">
          Connected to <span class="font-mono">{{ item.account }}</span> on GitHub.
        </p>
      </div>

      <Alert v-if="error" variant="destructive" class="mt-6">
        <AlertTitle>Something went wrong</AlertTitle>
        <AlertDescription>{{ error }}</AlertDescription>
      </Alert>

      <Alert v-if="item.suspended" variant="destructive" class="mt-6">
        <AlertTitle>This installation is gone on GitHub</AlertTitle>
        <AlertDescription>
          It was removed or suspended there, so no new tokens can be minted and clones will be
          refused. Reconnect it to restore access.
          <Button variant="secondary" size="sm" class="mt-3" @click="connect">
            <GitBranch class="size-4" aria-hidden="true" />
            Reconnect
          </Button>
        </AlertDescription>
      </Alert>

      <!-- Not connected: nothing else on this page can do anything yet. -->
      <div v-if="!item.connected" class="mt-6 rounded-lg border border-dashed border-border p-10 text-center">
        <GitBranch class="mx-auto size-8 text-muted-foreground" aria-hidden="true" />
        <p class="mt-3 text-sm font-medium">Connect a GitHub account</p>
        <p class="mx-auto mt-1 max-w-md text-sm text-muted-foreground">
          GitHub will ask which account or organisation to install into, and which repositories to
          grant. That choice is the outer bound — this integration can only ever narrow it.
        </p>
        <Button class="mt-4" @click="connect">
          <GitBranch class="size-4" aria-hidden="true" />
          Continue to GitHub
        </Button>
        <p class="mx-auto mt-4 max-w-md text-xs text-muted-foreground">
          If GitHub leaves you on its own settings page instead of returning here, the app has no
          Setup URL configured — ask an admin to add it. If it is already installed on the account
          you want, uninstall it there first: GitHub only runs the setup flow on a fresh install.
        </p>
      </div>

      <template v-else>
        <section class="mt-8 rounded-lg border border-border p-4 sm:p-6">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h2 class="text-lg font-semibold tracking-tight">Repositories</h2>
              <p class="mt-1 max-w-2xl text-sm text-muted-foreground">
                Which of the repositories GitHub granted this integration covers. To change what is
                on offer here, change the installation on GitHub.
              </p>
            </div>
            <Button variant="ghost" size="sm" :disabled="availableLoading" @click="loadAvailable">
              <RefreshCw class="size-3.5" :class="availableLoading && 'animate-spin'" aria-hidden="true" />
              Refresh
            </Button>
          </div>

          <div v-if="item.all_reposable" class="mt-4 flex items-start gap-3 rounded-md bg-muted/40 p-3">
            <Switch
              id="all-repos"
              :model-value="item.all_repos"
              class="mt-0.5"
              @update:model-value="(v: boolean) => setFlags({ all_repos: v })"
            />
            <div>
              <Label for="all-repos" class="text-sm font-medium">Every repository</Label>
              <p class="mt-0.5 text-xs text-muted-foreground">
                Follows the installation, including repositories added to it later.
              </p>
            </div>
          </div>

          <Alert v-if="availableError" variant="destructive" class="mt-4">
            <AlertTitle>Could not reach GitHub</AlertTitle>
            <AlertDescription>{{ availableError }}</AlertDescription>
          </Alert>

          <template v-if="!item.all_repos">
            <div v-if="availableLoading" class="mt-4 space-y-2" aria-hidden="true">
              <Skeleton v-for="n in 3" :key="n" class="h-6 w-64" />
            </div>

            <p v-else-if="!available.length" class="mt-4 text-sm text-muted-foreground">
              GitHub granted this installation no repositories.
              <a
                href="https://github.com/settings/installations"
                target="_blank"
                rel="noreferrer"
                class="inline-flex items-center gap-1 underline underline-offset-2"
              >
                Change what it can see
                <ExternalLink class="size-3" aria-hidden="true" />
              </a>
            </p>

            <template v-else>
              <ul class="mt-4 space-y-2">
                <li v-for="repo in available" :key="repo" class="flex items-center gap-2.5">
                  <Checkbox
                    :id="`repo-${repo}`"
                    :model-value="selected.has(repo)"
                    @update:model-value="(v: boolean) => toggleRepo(repo, v)"
                  />
                  <Label :for="`repo-${repo}`" class="font-mono text-sm font-normal">{{ repo }}</Label>
                </li>
              </ul>
              <div class="mt-4 flex items-center gap-2">
                <Button :disabled="savingRepos || !reposDirty" @click="saveRepos">
                  {{ savingRepos ? 'Saving…' : 'Save repositories' }}
                </Button>
                <p v-if="!selected.size" class="text-xs text-muted-foreground">
                  With none selected, this integration grants nothing.
                </p>
              </div>
            </template>
          </template>
        </section>

        <section class="mt-6 rounded-lg border border-border p-4 sm:p-6">
          <h2 class="text-lg font-semibold tracking-tight">Attached VMs</h2>
          <p class="mt-1 max-w-2xl text-sm text-muted-foreground">
            Only these VMs can reach the repositories above. Changes take effect on the next git
            command — nothing restarts.
          </p>

          <p v-if="!vms.length" class="mt-4 text-sm text-muted-foreground">
            You have no VMs yet. <NuxtLink to="/vms" class="underline underline-offset-2">Create one</NuxtLink>.
          </p>

          <ul v-else class="mt-4 divide-y divide-border">
            <li v-for="vm in vms" :key="vm.id" class="flex items-center justify-between gap-4 py-2.5">
              <div class="min-w-0">
                <p class="truncate text-sm font-medium">{{ vm.name }}</p>
                <p class="font-mono text-xs text-muted-foreground">{{ vm.status }}</p>
              </div>
              <Switch
                :model-value="attached.has(vm.id)"
                :disabled="vmBusy[vm.id]"
                :aria-label="`Attach ${item.name} to ${vm.name}`"
                @update:model-value="(v: boolean) => toggleVM(vm, v)"
              />
            </li>
          </ul>

          <div v-if="!attached.size" class="mt-4 flex items-start gap-2 rounded-md bg-muted/40 p-3 text-sm">
            <TriangleAlert class="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <p class="text-muted-foreground">
              Attached to no VMs, so this integration currently grants nothing.
            </p>
          </div>
        </section>

        <section class="mt-6 rounded-lg border border-border p-4 sm:p-6">
          <h2 class="text-lg font-semibold tracking-tight">Access</h2>

          <div class="mt-4 flex items-start gap-3">
            <Switch
              id="readonly"
              :model-value="item.readonly"
              class="mt-0.5"
              @update:model-value="(v: boolean) => setFlags({ readonly: v })"
            />
            <div>
              <Label for="readonly" class="text-sm font-medium">Read-only</Label>
              <p class="mt-0.5 max-w-xl text-xs text-muted-foreground">
                Clones and reads work; <span class="font-mono">git push</span> and writes through the
                API are refused with a message saying so.
              </p>
            </div>
          </div>
        </section>

        <section v-if="usage" class="mt-6 rounded-lg border border-border p-4 sm:p-6">
          <h2 class="text-lg font-semibold tracking-tight">Using it</h2>
          <div class="mt-4">
            <IntegrationUsage :host="usage.host" :repo="usage.repo" />
          </div>
        </section>
      </template>
    </template>

    <Alert v-else variant="destructive" class="mt-6">
      <AlertTitle>Not found</AlertTitle>
      <AlertDescription>{{ error || 'No such integration.' }}</AlertDescription>
    </Alert>
  </div>
</template>
