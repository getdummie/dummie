<script setup lang="ts">
import { ArrowLeft, Check, ChevronsUpDown, Copy, LayoutGrid, Lock, Pencil, Plus, Trash2 } from '@lucide/vue'
import { useMediaQuery } from '@vueuse/core'
import { toast } from 'vue-sonner'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList, CommandSeparator } from '@/components/ui/command'
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import ThemeToggle from '@/components/ThemeToggle.vue'
import WorkspaceGrid from '@/components/workspace/WorkspaceGrid.vue'
import WorkspacePanel from '@/components/workspace/WorkspacePanel.vue'
import { agentWorkspaceKey, useAgentWorkspace, type AgentView } from '@/composables/useAgentWorkspace'
import { addPanel, panels, panelTypes, readingOrder, type GridItem, type PanelType, type WorkspaceLayout } from '@/lib/workspace'

definePageMeta({ middleware: ['auth'], layout: false })

const route = useRoute()
const id = computed(() => String(route.params.id))
const wide = useMediaQuery('(min-width: 1024px)')
const { authFetch } = useAuth()

const current = ref<WorkspaceLayout | null>(null)
const layouts = ref<WorkspaceLayout[]>([])
const loadError = ref<string | null>(null)
const items = computed(() => current.value?.layout.items ?? [])
const ordered = computed(() => readingOrder(items.value))
const missing = computed(() => panelTypes.filter(p => !items.value.some(i => i.panel === p)))
const mobileTab = ref<PanelType | null>(null)
const shownTab = computed(() => (items.value.some(i => i.panel === mobileTab.value) ? mobileTab.value : ordered.value[0]?.panel ?? null))

function reveal(view: AgentView) {
  if (!items.value.some(i => i.panel === view)) {
    toast(`The ${view} panel is not in this layout`, { action: { label: 'Add it', onClick: () => addPanelType(view) } })
    return
  }
  if (!wide.value) mobileTab.value = view
}

const ws = useAgentWorkspace(() => id.value, reveal)
provide(agentWorkspaceKey, reactive(ws))
const { phase, error, harnessInfo, install, title, statusLabel } = ws

useHead(() => ({ title: `dummie — workspace · ${title.value}` }))

async function readMessage(res: Response) {
  const b = await res.json().catch(() => null)
  return typeof b?.message === 'string' ? b.message : `HTTP ${res.status}`
}

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await authFetch(path, init && {
    ...init,
    headers: { 'Content-Type': 'application/json' },
  })
  if (!res.ok) throw new Error(await readMessage(res))
  return res.status === 204 ? (undefined as T) : res.json()
}

async function loadLayouts() {
  layouts.value = (await api<{ items: WorkspaceLayout[] }>('/workspace-layouts')).items
}

async function load() {
  loadError.value = null
  try {
    current.value = await api<WorkspaceLayout>(`/vms/${id.value}/workspace`)
    await loadLayouts()
  }
  catch (e) {
    loadError.value = e instanceof Error ? e.message : 'Could not load the workspace layout'
  }
}
onMounted(load)

let saveTimer: ReturnType<typeof setTimeout> | null = null

async function save() {
  saveTimer = null
  const l = current.value
  if (!l) return
  try {
    const saved = await api<WorkspaceLayout>(`/workspace-layouts/${l.id}`, {
      method: 'PUT',
      body: JSON.stringify({ name: l.name, layout: l.layout }),
    })
    layouts.value = layouts.value.map(x => (x.id === saved.id ? saved : x))
  }
  catch (e) {
    toast.error('Could not save the layout', { description: e instanceof Error ? e.message : undefined })
  }
}

async function flush() {
  if (!saveTimer) return
  clearTimeout(saveTimer)
  await save()
}

function setItems(next: GridItem[]) {
  if (!current.value || current.value.builtin) return
  current.value = { ...current.value, layout: { items: next } }
  if (saveTimer) clearTimeout(saveTimer)
  saveTimer = setTimeout(save, 500)
}

onBeforeUnmount(() => void flush())

function addPanelType(panel: PanelType) {
  if (current.value?.builtin) {
    askName('duplicate')
    return
  }
  const next = addPanel(items.value, panel)
  if (!next) {
    toast.error('There is no room for another panel')
    return
  }
  setItems(next)
  addOpen.value = false
}

const addOpen = ref(false)
const layoutOpen = ref(false)
const busy = ref(false)

async function pickLayout(l: WorkspaceLayout) {
  layoutOpen.value = false
  if (l.id === current.value?.id) return
  busy.value = true
  try {
    await flush()
    current.value = await api<WorkspaceLayout>(`/vms/${id.value}/workspace`, {
      method: 'PUT',
      body: JSON.stringify({ layout_id: l.id }),
    })
  }
  catch (e) {
    toast.error('Could not switch the layout', { description: e instanceof Error ? e.message : undefined })
  }
  finally {
    busy.value = false
  }
}

const nameDialog = ref<'new' | 'duplicate' | 'rename' | null>(null)
const nameValue = ref('')
const nameError = ref<string | null>(null)
const nameTitles = { new: 'New layout', duplicate: 'Duplicate layout', rename: 'Rename layout' }

function askName(mode: 'new' | 'duplicate' | 'rename') {
  layoutOpen.value = false
  nameError.value = null
  nameValue.value = mode === 'rename' ? current.value?.name ?? '' : mode === 'duplicate' ? `${current.value?.name} copy` : ''
  nameDialog.value = mode
}

async function submitName() {
  const mode = nameDialog.value
  const l = current.value
  if (!mode || !l) return
  busy.value = true
  nameError.value = null
  try {
    await flush()
    if (mode === 'rename') {
      current.value = await api<WorkspaceLayout>(`/workspace-layouts/${l.id}`, {
        method: 'PUT',
        body: JSON.stringify({ name: nameValue.value, layout: l.layout }),
      })
    }
    else {
      const created = await api<WorkspaceLayout>('/workspace-layouts', {
        method: 'POST',
        body: JSON.stringify({ name: nameValue.value, layout: mode === 'duplicate' ? l.layout : { items: [] } }),
      })
      current.value = await api<WorkspaceLayout>(`/vms/${id.value}/workspace`, {
        method: 'PUT',
        body: JSON.stringify({ layout_id: created.id }),
      })
    }
    await loadLayouts()
    nameDialog.value = null
  }
  catch (e) {
    nameError.value = e instanceof Error ? e.message : 'Could not save the layout'
  }
  finally {
    busy.value = false
  }
}

const deleteOpen = ref(false)

async function deleteLayout() {
  const l = current.value
  if (!l) return
  busy.value = true
  try {
    if (saveTimer) clearTimeout(saveTimer)
    saveTimer = null
    await api(`/workspace-layouts/${l.id}`, { method: 'DELETE' })
    deleteOpen.value = false
    await load()
  }
  catch (e) {
    toast.error('Could not delete the layout', { description: e instanceof Error ? e.message : undefined })
  }
  finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="flex h-dvh flex-col bg-background text-foreground">
    <header class="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-border px-4 py-2">
      <NuxtLink
        :to="`/vms/${id}`"
        class="inline-flex shrink-0 items-center gap-1.5 font-mono text-xs text-muted-foreground underline-offset-4 transition-colors hover:text-foreground hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
      >
        <ArrowLeft class="size-3.5" aria-hidden="true" />
        <span class="hidden sm:inline">Back to VM</span>
      </NuxtLink>
      <NuxtLink to="/dashboard" class="shrink-0" aria-label="dummie dashboard">
        <img src="/logo.svg" alt="" aria-hidden="true" class="size-6">
      </NuxtLink>
      <h1 class="min-w-0 flex-1 truncate font-mono text-sm font-semibold">{{ title }}</h1>

      <div class="flex items-center gap-2">
        <Popover v-model:open="layoutOpen">
          <PopoverTrigger as-child>
            <Button variant="outline" size="sm" class="h-7 max-w-48 font-mono text-xs" :disabled="!current || busy" aria-label="layout">
              <LayoutGrid class="size-3.5 shrink-0" aria-hidden="true" />
              <span class="truncate">{{ current?.name ?? 'layout' }}</span>
              <ChevronsUpDown class="size-3.5 shrink-0 opacity-50" aria-hidden="true" />
            </Button>
          </PopoverTrigger>
          <PopoverContent align="end" class="w-64 p-0">
            <Command>
              <CommandInput placeholder="Switch layout…" />
              <CommandList>
                <CommandEmpty>Nothing matches that.</CommandEmpty>
                <CommandGroup heading="Layout for this VM">
                  <CommandItem v-for="l in layouts" :key="l.id" :value="`${l.name} ${l.id}`" class="gap-2" @select="pickLayout(l)">
                    <Check class="size-4 shrink-0" :class="l.id === current?.id ? 'opacity-100' : 'opacity-0'" aria-hidden="true" />
                    <span class="truncate font-mono text-xs">{{ l.name }}</span>
                    <Lock v-if="l.builtin" class="ml-auto size-3.5 text-muted-foreground" aria-label="read-only" />
                  </CommandItem>
                </CommandGroup>
                <CommandSeparator />
                <CommandGroup heading="Actions">
                  <CommandItem value="action new layout" class="gap-2 font-mono text-xs" @select="askName('new')">
                    <Plus class="size-4" aria-hidden="true" />
                    New empty layout
                  </CommandItem>
                  <CommandItem value="action duplicate layout" class="gap-2 font-mono text-xs" @select="askName('duplicate')">
                    <Copy class="size-4" aria-hidden="true" />
                    Duplicate this layout
                  </CommandItem>
                  <CommandItem v-if="!current?.builtin" value="action rename layout" class="gap-2 font-mono text-xs" @select="askName('rename')">
                    <Pencil class="size-4" aria-hidden="true" />
                    Rename this layout
                  </CommandItem>
                  <CommandItem
                    v-if="!current?.builtin"
                    value="action delete layout"
                    class="gap-2 font-mono text-xs text-destructive"
                    @select="layoutOpen = false; deleteOpen = true"
                  >
                    <Trash2 class="size-4" aria-hidden="true" />
                    Delete this layout
                  </CommandItem>
                </CommandGroup>
              </CommandList>
            </Command>
          </PopoverContent>
        </Popover>

        <Popover v-if="wide && !current?.builtin" v-model:open="addOpen">
          <PopoverTrigger as-child>
            <Button variant="outline" size="sm" class="h-7 font-mono text-xs" :disabled="!current || !missing.length">
              <Plus class="size-3.5" aria-hidden="true" />
              Panel
            </Button>
          </PopoverTrigger>
          <PopoverContent align="end" class="w-52 p-0">
            <Command>
              <CommandList>
                <CommandGroup heading="Add a panel">
                  <CommandItem v-for="p in missing" :key="p" :value="p" class="gap-2 font-mono text-xs" @select="addPanelType(p)">
                    <component :is="panels[p].icon" class="size-4" aria-hidden="true" />
                    {{ panels[p].label }}
                  </CommandItem>
                </CommandGroup>
              </CommandList>
            </Command>
          </PopoverContent>
        </Popover>

        <Badge :variant="phase === 'open' ? 'default' : 'secondary'" class="font-mono text-xs">{{ statusLabel }}</Badge>
        <ThemeToggle />
      </div>
    </header>

    <Alert v-if="phase === 'error'" variant="destructive" class="rounded-none border-x-0">
      <AlertTitle>The agent is not available</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>
    <Alert v-else-if="harnessInfo && !harnessInfo.available" class="rounded-none border-x-0">
      <AlertTitle>{{ harnessInfo.name }} is not installed in this VM</AlertTitle>
      <AlertDescription>
        Install it from the console, then reload this page:
        <code class="font-mono text-xs">{{ install }}</code>
      </AlertDescription>
    </Alert>
    <Alert v-if="loadError" variant="destructive" class="rounded-none border-x-0">
      <AlertTitle>Could not load the workspace layout</AlertTitle>
      <AlertDescription>{{ loadError }}</AlertDescription>
    </Alert>

    <div v-if="current?.builtin && wide" class="flex flex-wrap items-center gap-2 border-b border-border px-4 py-1.5 font-mono text-xs text-muted-foreground">
      <Lock class="size-3.5" aria-hidden="true" />
      The default layout is shared by everyone and cannot be changed.
      <Button variant="outline" size="sm" class="h-6 font-mono text-xs" @click="askName('duplicate')">
        <Copy class="size-3.5" aria-hidden="true" />
        Duplicate to customize
      </Button>
    </div>

    <div v-if="!current && !loadError" class="grid flex-1 grid-cols-12 gap-1.5 p-1.5">
      <Skeleton class="col-span-2" />
      <Skeleton class="col-span-6" />
      <Skeleton class="col-span-4" />
    </div>

    <main v-else-if="current && wide" class="min-h-0 flex-1 p-1.5">
      <WorkspaceGrid :items="items" :readonly="current.builtin" @update:items="setItems">
        <template #panel="{ item }">
          <WorkspacePanel :panel="item.panel" :vm-id="id" :active="true" />
        </template>
        <template #empty>
          <div class="text-center font-mono text-xs text-muted-foreground">
            <p>This layout has no panels yet.</p>
            <Button variant="outline" size="sm" class="mt-3 h-7 font-mono text-xs" @click="addOpen = true">
              <Plus class="size-3.5" aria-hidden="true" />
              Add a panel
            </Button>
          </div>
        </template>
      </WorkspaceGrid>
    </main>

    <main v-else-if="current" class="flex min-h-0 flex-1 flex-col">
      <Tabs v-if="ordered.length" :model-value="shownTab ?? undefined" class="border-b border-border px-3 py-2" @update:model-value="mobileTab = $event as PanelType">
        <TabsList class="h-auto w-full flex-wrap justify-start">
          <TabsTrigger v-for="i in ordered" :key="i.panel" :value="i.panel" class="h-7 flex-none gap-1.5 font-mono text-xs">
            <component :is="panels[i.panel].icon" class="size-3.5" aria-hidden="true" />
            {{ panels[i.panel].label }}
          </TabsTrigger>
        </TabsList>
      </Tabs>
      <p v-else class="p-4 font-mono text-xs text-muted-foreground">
        This layout has no panels yet. Add them from a wider screen.
      </p>
      <div v-for="i in ordered" v-show="i.panel === shownTab" :key="i.panel" class="flex min-h-0 flex-1 flex-col">
        <WorkspacePanel :panel="i.panel" :vm-id="id" :active="i.panel === shownTab" />
      </div>
    </main>

    <Dialog :open="!!nameDialog" @update:open="!$event && (nameDialog = null)">
      <DialogContent class="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>{{ nameDialog && nameTitles[nameDialog] }}</DialogTitle>
          <DialogDescription>
            {{ nameDialog === 'rename' ? 'Every VM using this layout shows the new name.' : 'The new layout opens for this VM.' }}
          </DialogDescription>
        </DialogHeader>
        <form class="grid gap-2" @submit.prevent="submitName">
          <Label for="layout-name">Name</Label>
          <Input id="layout-name" v-model="nameValue" maxlength="64" required autocomplete="off" />
          <p v-if="nameError" class="font-mono text-xs text-destructive" role="alert">{{ nameError }}</p>
          <DialogFooter class="mt-2">
            <DialogClose as-child>
              <Button type="button" variant="outline">Cancel</Button>
            </DialogClose>
            <Button type="submit" :disabled="busy || !nameValue.trim()">Save</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <Dialog v-model:open="deleteOpen">
      <DialogContent class="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>Delete {{ current?.name }}?</DialogTitle>
          <DialogDescription>VMs using this layout go back to your default layout.</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <DialogClose as-child>
            <Button variant="outline">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" :disabled="busy" @click="deleteLayout">Delete</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
