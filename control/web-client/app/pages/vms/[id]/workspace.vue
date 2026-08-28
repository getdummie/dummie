<script setup lang="ts">
import { ArrowLeft } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from '@/components/ui/resizable'
import { desktopPresets, mobilePresets } from '@/lib/viewports'
import VmTerminal from '@/components/VmTerminal.vue'
import VmViewport from '@/components/VmViewport.vue'

definePageMeta({ middleware: ['auth'], layout: false })

interface VM {
  id: string
  name: string
  vm_id: string
  status: string
  url: string
  console_url: string
}

type Phase = 'loading' | 'connecting' | 'open' | 'closed' | 'error'

const route = useRoute()
const { authFetch } = useAuth()
const id = computed(() => String(route.params.id))

const vm = ref<VM | null>(null)
const loadError = ref<string | null>(null)

const desktopURL = ref('')
const mobileURL = ref('')
const frameError = ref<string | null>(null)

async function openGuest(into: Ref<string>) {
  if (!vm.value?.url) {
    frameError.value = 'This VM has no web address: the host it runs on has no domain, so there is nothing to show.'
    return
  }
  try {
    const res = await authFetch(`/vms/${id.value}/web-session`, { method: 'POST' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    into.value = (await res.json()).url
    frameError.value = null
  }
  catch (e) {
    frameError.value = e instanceof Error ? e.message : 'Could not open a session on this VM'
  }
}

const phase = ref<Phase>('loading')
const consoleMessage = ref<string | null>(null)
const terminal = useTemplateRef<InstanceType<typeof VmTerminal>>('terminal')

useHead(() => ({
  title: vm.value ? `dummie — workspace · ${vm.value.name || vm.value.vm_id}` : 'dummie — workspace',
}))

const statusLabel = computed(() => ({
  loading: 'loading',
  connecting: 'connecting',
  open: 'connected',
  closed: 'disconnected',
  error: 'error',
}[phase.value]))

async function readMessage(res: Response): Promise<string | null> {
  try {
    const b = await res.json()
    return typeof b?.message === 'string' ? b.message : null
  }
  catch {
    return null
  }
}

onMounted(async () => {
  try {
    const res = await authFetch(`/vms/${id.value}`)
    if (res.status === 404) throw new Error('This VM does not exist, or is not yours.')
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    vm.value = await res.json()
  }
  catch (e) {
    loadError.value = e instanceof Error ? e.message : 'Failed to load this VM'
    return
  }

  await Promise.all([openGuest(desktopURL), openGuest(mobileURL)])
})
</script>

<template>
  <div class="flex h-dvh flex-col bg-background text-foreground">
    <header class="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-border px-4 py-3">
      <NuxtLink
        :to="`/vms/${id}`"
        class="inline-flex items-center gap-1.5 font-mono text-xs text-muted-foreground underline-offset-4 transition-colors hover:text-foreground hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
      >
        <ArrowLeft class="size-3.5" aria-hidden="true" />
        Back to VM
      </NuxtLink>

      <h1 class="font-mono text-sm font-semibold">
        {{ vm?.name || vm?.vm_id || 'workspace' }}
      </h1>

      <div class="ml-auto flex items-center gap-2">
        <Badge v-if="vm" variant="secondary" class="font-mono text-xs">{{ vm.status }}</Badge>
      </div>
    </header>

    <Alert v-if="loadError" variant="destructive" class="rounded-none border-x-0">
      <AlertTitle>Could not open this VM</AlertTitle>
      <AlertDescription>{{ loadError }}</AlertDescription>
    </Alert>

    <ResizablePanelGroup
      v-if="!loadError"
      direction="vertical"
      auto-save-id="dummie:work-rows"
      class="min-h-0 flex-1"
    >
      <ResizablePanel :default-size="65" :min-size="20">
        <ResizablePanelGroup direction="horizontal" auto-save-id="dummie:work-columns" class="size-full">
          <ResizablePanel :default-size="62" :min-size="20">
            <VmViewport
              v-if="desktopURL"
              :src="desktopURL"
              label="desktop"
              :presets="desktopPresets"
              storage-key="dummie:work-desktop"
              class="size-full"
              @reload="openGuest(desktopURL)"
            />
            <p v-else class="grid size-full place-items-center p-4 text-center font-mono text-xs text-muted-foreground">
              {{ frameError ?? 'Opening a session…' }}
            </p>
          </ResizablePanel>

          <ResizableHandle with-handle />

          <ResizablePanel :default-size="38" :min-size="15">
            <VmViewport
              v-if="mobileURL"
              :src="mobileURL"
              label="mobile"
              :presets="mobilePresets"
              storage-key="dummie:work-mobile"
              class="size-full"
              @reload="openGuest(mobileURL)"
            />
            <p v-else class="grid size-full place-items-center p-4 text-center font-mono text-xs text-muted-foreground">
              {{ frameError ?? 'Opening a session…' }}
            </p>
          </ResizablePanel>
        </ResizablePanelGroup>
      </ResizablePanel>

      <ResizableHandle with-handle />

      <ResizablePanel :default-size="35" :min-size="10">
        <section class="flex size-full min-h-0 flex-col">
          <header class="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2">
            <h2 class="eyebrow text-muted-foreground">console</h2>
            <div class="ml-auto flex items-center gap-2">
              <Badge :variant="phase === 'open' ? 'default' : 'secondary'" class="font-mono text-xs">
                {{ statusLabel }}
              </Badge>
              <Button
                v-if="phase === 'open'"
                variant="outline"
                size="sm"
                class="h-7 font-mono text-xs"
                @click="terminal?.disconnect()"
              >
                Disconnect
              </Button>
              <Button
                v-else-if="phase === 'closed' || phase === 'error'"
                variant="outline"
                size="sm"
                class="h-7 font-mono text-xs"
                @click="terminal?.connect()"
              >
                Reconnect
              </Button>
            </div>
          </header>

          <p
            v-if="consoleMessage"
            class="border-b border-border bg-muted/40 px-3 py-2 font-mono text-xs"
            :class="phase === 'error' ? 'text-destructive' : 'text-muted-foreground'"
            role="status"
          >
            {{ consoleMessage }}
          </p>

          <VmTerminal
            ref="terminal"
            :vm-id="id"
            @phase="phase = $event"
            @message="consoleMessage = $event"
          />
        </section>
      </ResizablePanel>
    </ResizablePanelGroup>
  </div>
</template>
