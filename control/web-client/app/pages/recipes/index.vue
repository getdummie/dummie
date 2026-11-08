<script setup lang="ts">
import { Pencil, Plus, Rocket, ScrollText, Trash2 } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogScrollContent,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'
import OptionCombobox from '@/components/OptionCombobox.vue'
import TargetFormRow from '@/components/TargetFormRow.vue'
import type { Integration } from '@/composables/useIntegrations'
import { formToRecipeTarget, recipeSummary, recipeTargetToForm, type Recipe, type RecipeConfig } from '@/lib/recipes'
import { blankTarget, type TargetForm } from '@/lib/targets'

definePageMeta({ middleware: ['auth'] })
useHead({ title: 'dummie — recipes' })

interface Option { id: string, name: string }

const { authFetch } = useAuth()
const integrationsApi = useIntegrations()

const items = ref<Recipe[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

const osImages = ref<Option[]>([])
const kernels = ref<Option[]>([])
const integrations = ref<Integration[]>([])

const maxTargets = 32
const maxInitScriptBytes = 64 * 1024

async function readMessage(res: Response) {
  const b = await res.json().catch(() => null)
  return typeof b?.message === 'string' ? b.message : `HTTP ${res.status}`
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await authFetch('/recipes')
    if (!res.ok) throw new Error(await readMessage(res))
    items.value = (await res.json()).items ?? []
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not load your recipes'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

async function loadChoices() {
  const [img, ker] = await Promise.all([
    authFetch('/vms/osimages').catch(() => null),
    authFetch('/vms/kernels').catch(() => null),
  ])
  if (img?.ok) osImages.value = (await img.json()).items ?? []
  if (ker?.ok) kernels.value = (await ker.json()).items ?? []
  integrations.value = await integrationsApi.list().catch(() => [])
}

const formOpen = ref(false)
const editing = ref<Recipe | null>(null)
const saving = ref(false)
const formError = ref<string | null>(null)
const blank = {
  name: '',
  description: '',
  osimage_id: '',
  kernel_id: '',
  cpus: '',
  memory_mib: '',
  disk_size: '',
  default_port: '',
  public_ports: '',
  ttl_seconds: '',
  init_script: '',
}
const form = reactive({ ...blank })
const targets = ref<TargetForm[]>([])
const picked = ref<Record<string, string[]>>({})

async function openForm(r: Recipe | null) {
  editing.value = r
  formError.value = null
  Object.assign(form, blank)
  targets.value = []
  picked.value = {}
  if (r) {
    const c = r.config
    Object.assign(form, {
      name: r.name,
      description: r.description,
      osimage_id: c.osimage_id ?? '',
      kernel_id: c.kernel_id ?? '',
      cpus: c.cpus ? String(c.cpus) : '',
      memory_mib: c.memory_mib ? String(c.memory_mib) : '',
      disk_size: c.disk_size ?? '',
      default_port: c.default_port ? String(c.default_port) : '',
      public_ports: (c.public_ports ?? []).join(', '),
      ttl_seconds: c.ttl_seconds ? String(c.ttl_seconds) : '',
      init_script: c.init_script ?? '',
    })
    targets.value = (c.targets ?? []).map(recipeTargetToForm)
    picked.value = Object.fromEntries((c.integrations ?? []).map(i => [i.id, i.repos ?? []]))
  }
  formOpen.value = true
  await loadChoices()
}

function toggleIntegration(id: string, on: boolean) {
  const next = { ...picked.value }
  if (on) next[id] = next[id] ?? []
  else delete next[id]
  picked.value = next
}

function toggleRepo(id: string, repo: string, on: boolean) {
  const repos = new Set(picked.value[id] ?? [])
  if (on) repos.add(repo)
  else repos.delete(repo)
  picked.value = { ...picked.value, [id]: [...repos] }
}

const optionalInt = (v: string) => (v.trim() ? Number(v) : undefined)

function validate(): string | null {
  if (!form.name.trim()) return 'Give the recipe a name.'
  for (const [label, v, min] of [['vCPU', form.cpus, 1], ['Memory', form.memory_mib, 64], ['Default port', form.default_port, 1], ['TTL', form.ttl_seconds, 0]] as const) {
    if (v.trim() && (!Number.isInteger(Number(v)) || Number(v) < min)) return `${label} must be a whole number of at least ${min}.`
  }
  if (form.disk_size.trim() && !/^\d+[KkMmGgTt]?$/.test(form.disk_size.trim())) {
    return 'Disk size must be a number, optionally with a K, M, G or T suffix — e.g. 2G.'
  }
  if (form.public_ports.trim() && !/^\d+(\s*,\s*\d+)*$/.test(form.public_ports.trim())) {
    return 'Public ports must be a comma separated list of numbers.'
  }
  if (new TextEncoder().encode(form.init_script).length > maxInitScriptBytes) return 'The init script must be at most 64 KiB.'
  if (targets.value.some(t => !t.destination.trim())) return 'Every destination needs an address or a name.'
  return null
}

function toConfig(): RecipeConfig {
  const known = new Set(integrations.value.map(i => i.id))
  const kept = Object.entries(picked.value).filter(([id]) => !integrations.value.length || known.has(id))
  return {
    osimage_id: form.osimage_id || undefined,
    kernel_id: form.kernel_id || undefined,
    cpus: optionalInt(form.cpus),
    memory_mib: optionalInt(form.memory_mib),
    disk_size: form.disk_size.trim() || undefined,
    default_port: optionalInt(form.default_port),
    public_ports: form.public_ports.trim() ? form.public_ports.split(',').map(p => Number(p.trim())) : undefined,
    ttl_seconds: optionalInt(form.ttl_seconds),
    init_script: form.init_script.trim() ? form.init_script : undefined,
    targets: targets.value.length ? targets.value.map(formToRecipeTarget) : undefined,
    integrations: kept.length ? kept.map(([id, repos]) => ({ id, repos: repos.length ? repos : undefined })) : undefined,
  }
}

async function save() {
  formError.value = validate()
  if (formError.value) return
  saving.value = true
  try {
    const res = await authFetch(editing.value ? `/recipes/${editing.value.id}` : '/recipes', {
      method: editing.value ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: form.name.trim(), description: form.description.trim(), config: toConfig() }),
    })
    if (!res.ok) throw new Error(await readMessage(res))
    formOpen.value = false
    await load()
  }
  catch (e) {
    formError.value = e instanceof Error ? e.message : 'Could not save the recipe'
  }
  finally {
    saving.value = false
  }
}

const deleting = ref<Recipe | null>(null)
const deleteError = ref<string | null>(null)

async function confirmDelete() {
  if (!deleting.value) return
  deleteError.value = null
  try {
    const res = await authFetch(`/recipes/${deleting.value.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error(await readMessage(res))
    deleting.value = null
    await load()
  }
  catch (e) {
    deleteError.value = e instanceof Error ? e.message : 'Could not delete the recipe'
  }
}
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-12 sm:px-6">
    <div class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <p class="eyebrow mb-2 text-primary-text">// recipes</p>
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">Recipes</h1>
        <p class="mt-2 max-w-2xl text-sm text-muted-foreground">
          Saved VM setups. Launching one opens the new-VM dialog filled in from it, so you only pick
          a host and a name.
        </p>
      </div>
      <Button v-if="items.length" class="shrink-0" @click="openForm(null)">
        <Plus class="size-4" aria-hidden="true" />
        New recipe
      </Button>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Something went wrong</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <div class="mt-6" aria-live="polite" :aria-busy="loading">
      <div v-if="loading" class="divide-y divide-border rounded-lg border border-border">
        <div v-for="n in 2" :key="n" class="space-y-2 p-4" aria-hidden="true">
          <Skeleton class="h-4 w-40" />
          <Skeleton class="h-3 w-full max-w-md" />
        </div>
      </div>

      <div v-else-if="!items.length" class="rounded-lg border border-dashed border-border p-10 text-center">
        <ScrollText class="mx-auto size-8 text-muted-foreground" aria-hidden="true" />
        <p class="mt-3 text-sm font-medium">No recipes yet</p>
        <p class="mx-auto mt-1 max-w-md text-sm text-muted-foreground">
          Save an OS image, sizes, ports, allowed destinations, integrations and an init script once,
          then launch VMs from it. You can also save an existing VM as a recipe from its page.
        </p>
        <Button class="mt-4" @click="openForm(null)">
          <Plus class="size-4" aria-hidden="true" />
          New recipe
        </Button>
      </div>

      <ul v-else class="divide-y divide-border rounded-lg border border-border">
        <li v-for="r in items" :key="r.id" class="flex flex-wrap items-start gap-3 p-4">
          <div class="min-w-0 flex-1">
            <p class="truncate font-mono text-sm font-medium">{{ r.name }}</p>
            <p v-if="r.description" class="mt-0.5 text-sm text-muted-foreground">{{ r.description }}</p>
            <div class="mt-2 flex flex-wrap gap-1.5">
              <Badge v-for="s in recipeSummary(r.config)" :key="s" variant="secondary" class="font-mono text-xs font-normal">{{ s }}</Badge>
              <span v-if="!recipeSummary(r.config).length" class="font-mono text-xs text-muted-foreground">all defaults</span>
            </div>
          </div>
          <div class="flex shrink-0 items-center gap-1">
            <Button as-child size="sm" class="font-mono text-xs">
              <NuxtLink :to="{ path: '/vms', query: { recipe: r.id } }">
                <Rocket class="size-3.5" aria-hidden="true" />
                Launch
              </NuxtLink>
            </Button>
            <Button variant="ghost" size="icon" class="size-8" :aria-label="`Edit ${r.name}`" @click="openForm(r)">
              <Pencil class="size-4" aria-hidden="true" />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              class="size-8 text-destructive hover:text-destructive"
              :aria-label="`Delete ${r.name}`"
              @click="deleting = r; deleteError = null"
            >
              <Trash2 class="size-4" aria-hidden="true" />
            </Button>
          </div>
        </li>
      </ul>
    </div>

    <Dialog v-model:open="formOpen">
      <DialogScrollContent class="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{{ editing ? 'Edit recipe' : 'New recipe' }}</DialogTitle>
          <DialogDescription>
            Only the name is required. Anything left empty uses the new-VM dialog's default when you launch.
          </DialogDescription>
        </DialogHeader>

        <form class="space-y-4" :aria-busy="saving" @submit.prevent="save">
          <div class="grid gap-4 sm:grid-cols-2">
            <div class="space-y-2">
              <Label for="r-name">Name</Label>
              <Input id="r-name" v-model="form.name" maxlength="64" placeholder="python sandbox" autocomplete="off" />
            </div>
            <div class="space-y-2">
              <Label for="r-desc">Description</Label>
              <Input id="r-desc" v-model="form.description" maxlength="500" placeholder="what it is for" />
            </div>
          </div>

          <div class="grid gap-4 sm:grid-cols-2">
            <div class="space-y-2">
              <Label for="r-osimage">OS image</Label>
              <OptionCombobox
                id="r-osimage"
                v-model="form.osimage_id"
                :options="osImages"
                default-label="Default (pick at launch)"
                search-placeholder="Search OS images…"
              />
            </div>
            <div class="space-y-2">
              <Label for="r-kernel">Kernel</Label>
              <OptionCombobox
                id="r-kernel"
                v-model="form.kernel_id"
                :options="kernels"
                default-label="Default (newest)"
                search-placeholder="Search kernels…"
              />
            </div>
          </div>

          <div class="grid gap-4 sm:grid-cols-3">
            <div class="space-y-2">
              <Label for="r-cpus">vCPU</Label>
              <Input id="r-cpus" v-model="form.cpus" type="number" min="1" inputmode="numeric" placeholder="1" />
            </div>
            <div class="space-y-2">
              <Label for="r-mem">Memory (MiB)</Label>
              <Input id="r-mem" v-model="form.memory_mib" type="number" min="64" inputmode="numeric" placeholder="512" />
            </div>
            <div class="space-y-2">
              <Label for="r-disk">Disk size</Label>
              <Input id="r-disk" v-model="form.disk_size" placeholder="5G" />
            </div>
          </div>

          <div class="grid gap-4 sm:grid-cols-3">
            <div class="space-y-2">
              <Label for="r-port">Default port</Label>
              <Input id="r-port" v-model="form.default_port" type="number" min="1" inputmode="numeric" placeholder="8000" />
            </div>
            <div class="space-y-2">
              <Label for="r-public">Public ports</Label>
              <Input id="r-public" v-model="form.public_ports" placeholder="3000, 5173" />
            </div>
            <div class="space-y-2">
              <Label for="r-ttl">TTL (seconds)</Label>
              <Input id="r-ttl" v-model="form.ttl_seconds" type="number" min="0" inputmode="numeric" placeholder="0 — no limit" />
            </div>
          </div>

          <div class="space-y-2">
            <Label for="r-init">Init script</Label>
            <Textarea
              id="r-init"
              v-model="form.init_script"
              rows="5"
              class="font-mono text-xs"
              spellcheck="false"
              placeholder="#!/bin/sh&#10;apt-get update && apt-get install -y htop"
            />
            <p class="text-xs text-muted-foreground">Runs once as root on the VM's first boot.</p>
          </div>

          <div class="space-y-3 border-t border-border pt-4">
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div>
                <p class="text-sm font-medium">Allowed destinations</p>
                <p class="mt-1 text-xs text-muted-foreground">Added to the VM's allowlist when it is created.</p>
              </div>
              <Button
                type="button"
                variant="outline"
                size="sm"
                class="font-mono text-xs"
                :disabled="targets.length >= maxTargets"
                @click="targets = [...targets, blankTarget()]"
              >
                <Plus class="size-4" aria-hidden="true" />
                Add
              </Button>
            </div>
            <TargetFormRow
              v-for="(t, i) in targets"
              :key="i"
              v-model="targets[i]!"
              :index="i"
              @remove="targets = targets.filter((_, n) => n !== i)"
            />
          </div>

          <div class="space-y-3 border-t border-border pt-4">
            <div>
              <p class="text-sm font-medium">Integrations</p>
              <p class="mt-1 text-xs text-muted-foreground">
                Attached to the VM when it is created. Leave every repository unticked to give it all
                the repositories the integration covers.
              </p>
            </div>
            <p v-if="!integrations.length" class="text-xs text-muted-foreground">
              You have no integrations yet. Create one under <NuxtLink to="/integrations" class="underline underline-offset-4">Integrations</NuxtLink>.
            </p>
            <div v-for="i in integrations" :key="i.id" class="space-y-2 rounded-lg border border-border p-3">
              <div class="flex items-center gap-2">
                <Checkbox
                  :id="`r-int-${i.id}`"
                  :model-value="i.id in picked"
                  @update:model-value="(v) => toggleIntegration(i.id, v === true)"
                />
                <Label :for="`r-int-${i.id}`" class="font-mono text-xs">{{ i.name }}</Label>
                <span class="font-mono text-xs text-muted-foreground">{{ i.kind }}</span>
              </div>
              <div v-if="i.id in picked && i.kind === 'github' && i.repos?.length" class="flex flex-wrap gap-x-4 gap-y-1.5 pl-6">
                <div v-for="repo in i.repos" :key="repo" class="flex items-center gap-1.5">
                  <Checkbox
                    :id="`r-repo-${i.id}-${repo}`"
                    :model-value="picked[i.id]?.includes(repo)"
                    @update:model-value="(v) => toggleRepo(i.id, repo, v === true)"
                  />
                  <Label :for="`r-repo-${i.id}-${repo}`" class="font-mono text-xs font-normal">{{ repo }}</Label>
                </div>
              </div>
            </div>
            <p
              v-for="id in Object.keys(picked).filter(id => integrations.length && !integrations.some(i => i.id === id))"
              :key="id"
              class="text-xs text-destructive"
            >
              An integration in this recipe no longer exists; it is dropped when you save.
            </p>
          </div>

          <FormError id="recipe-error" :message="formError" />

          <DialogFooter>
            <DialogClose as-child>
              <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
            </DialogClose>
            <Button type="submit" class="font-mono text-xs" :disabled="saving">
              {{ saving ? 'Saving…' : 'Save recipe' }}
            </Button>
          </DialogFooter>
        </form>
      </DialogScrollContent>
    </Dialog>

    <Dialog :open="!!deleting" @update:open="(v: boolean) => !v && (deleting = null)">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete {{ deleting?.name }}?</DialogTitle>
          <DialogDescription>VMs already launched from it are not affected.</DialogDescription>
        </DialogHeader>
        <FormError id="recipe-delete-error" :message="deleteError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" @click="confirmDelete">Delete</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
