<script setup lang="ts">
import { ChevronDown } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import type { Integration } from '@/composables/useIntegrations'

const props = defineProps<{ vmId: string, vmName: string, host: string }>()

// What this VM can reach through one integration: exactly the repositories
// picked for it.
interface VMIntegration {
  id: string
  name: string
  readonly: boolean
  all_repos: boolean
  repos?: string[]
}

const api = useIntegrations()
const { authFetch } = useAuth()

const integrations = ref<Integration[]>([])
const attached = ref<Map<string, VMIntegration>>(new Map())
const loading = ref(true)
const error = ref<string | null>(null)
const busy = ref<Record<string, boolean>>({})

const open = ref<string | null>(null)
const choices = ref<Record<string, string[]>>({})
const choicesLoading = ref(false)
const selected = ref<Set<string>>(new Set())
const saving = ref(false)

async function loadAttached() {
  const res = await authFetch(`/vms/${props.vmId}/integrations`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  const items = (await res.json()).items ?? []
  attached.value = new Map((items as VMIntegration[]).map(i => [i.id, i]))
}

// availableRepos is a live call to github, so it is fetched once per
// integration and kept. inflight keeps a prefetch and an open from both asking.
const inflight = new Map<string, Promise<void>>()

function loadChoices(id: string): Promise<void> {
  if (choices.value[id]) return Promise.resolve()
  const pending = inflight.get(id)
  if (pending) return pending

  const p = api.availableRepos(id)
    .then((repos) => {
      choices.value = { ...choices.value, [id]: repos }
    })
    .finally(() => inflight.delete(id))
  inflight.set(id, p)
  return p
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const [list] = await Promise.all([api.list(), loadAttached()])
    integrations.value = list
    // Warm the github call while the user is still reading, so opening the
    // editor does not wait on it. A failure here is not worth surfacing: the
    // open path reports it and retries.
    for (const i of list.filter(x => x.connected)) loadChoices(i.id).catch(() => {})
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not load the integrations'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

function reachable(i: Integration) {
  return attached.value.get(i.id)?.repos ?? []
}

async function toggle(i: Integration, on: boolean) {
  busy.value = { ...busy.value, [i.id]: true }
  error.value = null
  try {
    if (on) await api.attach(i.id, props.vmId)
    else await api.detach(i.id, props.vmId)
    if (!on && open.value === i.id) open.value = null
    await loadAttached()
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : `Could not ${on ? 'attach' : 'detach'} ${i.name}`
  }
  finally {
    const b = { ...busy.value }
    delete b[i.id]
    busy.value = b
  }
}

// An attachment reaches exactly what it names here, so a VM with nothing picked
// reaches nothing: attaching is not itself a grant.
async function toggleScope(i: Integration) {
  if (open.value === i.id) {
    open.value = null
    return
  }
  open.value = i.id
  error.value = null
  // This VM's own scope, already loaded with the list.
  selected.value = new Set(attached.value.get(i.id)?.repos ?? [])

  choicesLoading.value = !choices.value[i.id]
  try {
    await loadChoices(i.id)
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not read the repositories'
    open.value = null
  }
  finally {
    choicesLoading.value = false
  }
}

function pick(repo: string, on: boolean) {
  const next = new Set(selected.value)
  if (on) next.add(repo)
  else next.delete(repo)
  selected.value = next
}

async function saveScope(i: Integration) {
  saving.value = true
  error.value = null
  try {
    await api.setVMRepos(i.id, props.vmId, [...selected.value])
    open.value = null
    await loadAttached()
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not save the repositories'
  }
  finally {
    saving.value = false
  }
}
</script>

<template>
  <section aria-labelledby="integrations-heading" class="mt-4 rounded-lg border border-border p-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h2 id="integrations-heading" class="text-sm font-semibold">Integrations</h2>
      <Button variant="outline" size="sm" as-child class="font-mono text-xs">
        <NuxtLink to="/integrations">Manage</NuxtLink>
      </Button>
    </div>
    <p class="mt-0.5 max-w-2xl text-xs text-muted-foreground">
      Clone private github repositories without requiring ssh keys on the VM.
    </p>

    <FormError v-if="error" id="vm-integrations-error" :message="error" class="mt-3" />

    <div v-if="loading" class="mt-3 space-y-2" aria-busy="true">
      <Skeleton v-for="n in 2" :key="n" class="h-8 w-full" aria-hidden="true" />
    </div>

    <p v-else-if="!integrations.length" class="mt-3 text-xs text-muted-foreground">
      You have no integrations yet.
      <NuxtLink to="/integrations" class="underline underline-offset-2">Create one</NuxtLink>
      to clone a private repository from this VM.
    </p>

    <template v-else>
      <ul class="mt-3 divide-y divide-border">
        <li v-for="i in integrations" :key="i.id" class="py-2.5">
          <div class="flex items-center justify-between gap-3">
            <div class="min-w-0">
              <NuxtLink :to="`/integrations/${i.id}`" class="text-sm font-medium underline-offset-4 hover:underline">
                {{ i.name }}
              </NuxtLink>
              <span v-if="i.readonly" class="ml-2 font-mono text-xs text-muted-foreground">read-only</span>
              <p v-if="!i.connected" class="mt-0.5 text-xs text-muted-foreground">
                No GitHub account connected yet, so it grants nothing.
              </p>
              <p v-else-if="!attached.has(i.id)" class="mt-0.5 text-xs text-muted-foreground">
                Not attached.
              </p>
              <p v-else-if="!reachable(i).length" class="mt-0.5 text-xs text-muted-foreground">
                No repositories picked yet, so it grants nothing.
              </p>
              <ul v-else class="mt-1 flex flex-wrap gap-1">
                <li
                  v-for="repo in reachable(i)"
                  :key="repo"
                  class="rounded-md bg-muted px-1.5 py-0.5 font-mono text-xs text-muted-foreground"
                >
                  {{ repo }}
                </li>
              </ul>
            </div>
            <div class="flex shrink-0 items-center gap-2">
              <Button
                v-if="attached.has(i.id)"
                variant="ghost"
                size="sm"
                class="font-mono text-xs"
                :aria-expanded="open === i.id"
                @click="toggleScope(i)"
              >
                Repositories
                <ChevronDown class="size-3.5" :class="open === i.id && 'rotate-180'" aria-hidden="true" />
              </Button>
              <Switch
                :model-value="attached.has(i.id)"
                :disabled="busy[i.id] || !i.connected"
                :aria-label="`Give ${vmName} access to ${i.name}`"
                @update:model-value="(v: boolean) => toggle(i, v)"
              />
            </div>
          </div>

          <div v-if="open === i.id" class="mt-3 rounded-md bg-muted/40 p-3">
            <p class="text-xs text-muted-foreground">
              Pick repositories to grant access to this VM
            </p>
            <div v-if="choicesLoading" class="mt-2.5 space-y-2" aria-busy="true">
              <Skeleton v-for="n in 3" :key="n" class="h-4 w-48" aria-hidden="true" />
            </div>
            <p v-else-if="!choices[i.id]?.length" class="mt-2.5 text-xs text-muted-foreground">
              This integration covers no repositories yet. Select some on its own page first.
            </p>
            <template v-else>
              <ul class="mt-2.5 space-y-2">
                <li v-for="repo in choices[i.id]" :key="repo" class="flex items-center gap-2.5">
                  <Checkbox
                    :id="`vm-scope-${i.id}-${repo}`"
                    :model-value="selected.has(repo)"
                    @update:model-value="(v: boolean) => pick(repo, v)"
                  />
                  <Label :for="`vm-scope-${i.id}-${repo}`" class="font-mono text-xs font-normal">
                    {{ repo }}
                  </Label>
                </li>
              </ul>
              <Button size="sm" class="mt-3 font-mono text-xs" :disabled="saving" @click="saveScope(i)">
                {{ saving ? 'Saving…' : 'Save' }}
              </Button>
            </template>
          </div>
        </li>
      </ul>

      <div v-if="host && attached.size" class="mt-4">
        <IntegrationUsage :host="host" />
      </div>
    </template>
  </section>
</template>
