<script setup lang="ts">
import { ArrowLeft, ExternalLink, GitBranch, RefreshCw, TriangleAlert } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import type { Integration, IntegrationVM, OwnedVM } from '@/composables/useIntegrations'
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

// Attaching happens on a VM's own page, so this is a read-only view of who has
// been given access.
const vms = ref<OwnedVM[]>([])
const attachedVMs = ref<IntegrationVM[]>([])
const attached = computed(() => new Set(attachedVMs.value.map(v => v.id)))

// An attachment reaches exactly the repositories picked for it on the VM's own
// page, and nothing when none are.
function vmRepos(vm: IntegrationVM): string[] {
  return vm.repos ?? []
}

useHead(() => ({ title: `dummie — ${item.value?.name ?? 'integration'}` }))

async function load() {
  loading.value = true
  error.value = null
  try {
    const [i, a, v] = await Promise.all([api.get(id.value), api.attachedVMs(id.value), api.ownedVMs()])
    item.value = i
    attachedVMs.value = a
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

const managing = ref(false)

// Which repositories github granted is settled on github, not here, so this
// only works out where that installation lives and sends the browser there.
async function manageOnGitHub() {
  managing.value = true
  error.value = null
  try {
    window.location.href = await api.manageURL(id.value)
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not open the installation on GitHub'
    managing.value = false
  }
}

// The usage snippet needs a fleet domain, which comes from a VM's own hostname.
// An attached VM is the accurate source; otherwise any of the user's will do.
const usage = computed(() => {
  const attachedVM = vms.value.find(v => attached.value.has(v.id))
  const host = integrationHost(attachedVM ?? vms.value[0])
  const repo = available.value[0]
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
            <h2 class="text-lg font-semibold tracking-tight">Repositories</h2>
            <div class="flex shrink-0 flex-wrap items-center gap-2">
              <Button variant="ghost" size="sm" :disabled="availableLoading" @click="loadAvailable">
                <RefreshCw class="size-3.5" :class="availableLoading && 'animate-spin'" aria-hidden="true" />
                Refresh
              </Button>
              <Button variant="secondary" size="sm" :disabled="managing" @click="manageOnGitHub">
                <ExternalLink class="size-3.5" aria-hidden="true" />
                {{ managing ? 'Opening GitHub…' : 'Change access on GitHub' }}
              </Button>
            </div>
          </div>

          <Alert v-if="availableError" variant="destructive" class="mt-4">
            <AlertTitle>Could not reach GitHub</AlertTitle>
            <AlertDescription>{{ availableError }}</AlertDescription>
          </Alert>

          <div v-if="availableLoading" class="mt-4 space-y-2" aria-hidden="true">
            <Skeleton v-for="n in 3" :key="n" class="h-6 w-64" />
          </div>

          <p v-else-if="!available.length" class="mt-4 text-sm text-muted-foreground">
            GitHub granted this installation no repositories. Use
            <span class="font-medium">Change access on GitHub</span> above, then refresh.
          </p>

          <ul v-else class="mt-4 space-y-1.5">
            <li v-for="repo in available" :key="repo" class="font-mono text-sm">{{ repo }}</li>
          </ul>
        </section>

        <section class="mt-6 rounded-lg border border-border p-4 sm:p-6">
          <h2 class="text-lg font-semibold tracking-tight">Attached VMs</h2>
          <p class="mt-1 max-w-2xl text-sm text-muted-foreground">
            To grant or revoke access, open a VM and use its own integrations section.
          </p>

          <ul v-if="attachedVMs.length" class="mt-4 divide-y divide-border">
            <li
              v-for="vm in attachedVMs"
              :key="vm.id"
              class="flex flex-col gap-1.5 py-2.5 sm:flex-row sm:items-start sm:justify-between sm:gap-4"
            >
              <div class="min-w-0">
                <NuxtLink :to="`/vms/${vm.id}`" class="truncate text-sm font-medium hover:underline">
                  {{ vm.name }}
                </NuxtLink>
                <p class="font-mono text-xs text-muted-foreground">{{ vm.ip }}</p>
              </div>
              <p v-if="!vmRepos(vm).length" class="shrink-0 text-xs text-muted-foreground">
                no repositories
              </p>
              <ul v-else class="flex flex-wrap gap-x-3 gap-y-1 sm:max-w-[60%] sm:justify-end">
                <li v-for="repo in vmRepos(vm)" :key="repo" class="font-mono text-xs text-muted-foreground">
                  {{ repo }}
                </li>
              </ul>
            </li>
          </ul>

          <div v-else class="mt-4 flex items-start gap-2 rounded-md bg-muted/40 p-3 text-sm">
            <TriangleAlert class="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <p class="text-muted-foreground">
              Attached to no VMs, so this integration currently grants nothing.
              <template v-if="vms.length">
                Open a
                <NuxtLink to="/vms" class="underline underline-offset-2">VM</NuxtLink>
                to give it access.
              </template>
              <template v-else>
                You have no VMs yet —
                <NuxtLink to="/vms" class="underline underline-offset-2">create one</NuxtLink>.
              </template>
            </p>
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
