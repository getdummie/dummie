<script setup lang="ts">
import { ArrowLeft, FileDiff as FileDiffIcon, PanelLeft } from '@lucide/vue'
import { useMediaQuery } from '@vueuse/core'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from '@/components/ui/resizable'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import AgentChanges from '@/components/agent/AgentChanges.vue'
import AgentChatPanel from '@/components/agent/AgentChatPanel.vue'
import AgentFilesPanel from '@/components/agent/AgentFilesPanel.vue'
import AgentSessions from '@/components/agent/AgentSessions.vue'
import AgentTransfer from '@/components/agent/AgentTransfer.vue'
import ThemeToggle from '@/components/ThemeToggle.vue'
import VmDestinations from '@/components/VmDestinations.vue'
import VmTerminal, { type Phase as ConsolePhase } from '@/components/VmTerminal.vue'
import VmTerminalKeypad from '@/components/VmTerminalKeypad.vue'
import { agentWorkspaceKey, useAgentWorkspace } from '@/composables/useAgentWorkspace'

definePageMeta({ middleware: ['auth'], layout: false })

const route = useRoute()
const id = computed(() => String(route.params.id))
const wide = useMediaQuery('(min-width: 1024px)')

const sessionsOpen = ref(false)
const changesOpen = ref(false)
const centerTab = ref<'chat' | 'console' | 'files' | 'transfer' | 'network'>('chat')

const ws = useAgentWorkspace(() => id.value, (view) => {
  centerTab.value = view
  if (view === 'chat') sessionsOpen.value = false
  else changesOpen.value = false
})
provide(agentWorkspaceKey, reactive(ws))
const { sock, phase, error, hello, sessions, diff, activeKey, activeCwd, newCwd, harnessInfo, install, title, renamable, statusLabel } = ws
const { openFile, openSession, startNew, gitInit, renameSession, onEdited } = ws

// The transfer tab mounts on first use and then stays, so its queue survives tab switches.
const transferMounted = ref(false)
// The console mounts on first use and then stays, so switching tabs keeps
// its shell alive.
const consoleMounted = ref(false)
const consolePhase = ref<ConsolePhase>('loading')
const consoleMessage = ref<string | null>(null)
const terminal = useTemplateRef<InstanceType<typeof VmTerminal>>('terminal')

watch(centerTab, async (tab) => {
  if (tab === 'transfer') transferMounted.value = true
  if (tab !== 'console') return
  consoleMounted.value = true
  await nextTick()
  terminal.value?.focus()
})

useHead(() => ({ title: `dummie — agent · ${title.value}` }))
</script>

<template>
  <div class="flex h-dvh flex-col bg-background text-foreground">
    <header class="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-center gap-3 border-b border-border px-4 py-3">
      <div class="flex min-w-0 items-center gap-3">
        <Button v-if="!wide" variant="ghost" size="icon" class="size-8 shrink-0" aria-label="sessions" @click="sessionsOpen = true">
          <PanelLeft class="size-4" aria-hidden="true" />
        </Button>
        <NuxtLink
          :to="`/vms/${id}`"
          class="inline-flex shrink-0 items-center gap-1.5 font-mono text-xs text-muted-foreground underline-offset-4 transition-colors hover:text-foreground hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
        >
          <ArrowLeft class="size-3.5" aria-hidden="true" />
          <span class="hidden sm:inline">Back to VM</span>
        </NuxtLink>
        <h1 class="min-w-0 truncate font-mono text-sm font-semibold">{{ title }}</h1>
      </div>

      <NuxtLink to="/dashboard" class="flex shrink-0 items-center gap-2" aria-label="dummie dashboard">
        <img src="/logo.svg" alt="" aria-hidden="true" class="size-6">
        <span class="hidden font-mono text-sm font-semibold tracking-tight sm:inline"><span class="text-primary-text">dumm</span>ie<span class="text-primary-text">/</span></span>
      </NuxtLink>

      <div class="flex items-center justify-end gap-3">
        <Badge :variant="phase === 'open' ? 'default' : 'secondary'" class="font-mono text-xs">{{ statusLabel }}</Badge>
        <Button v-if="!wide" variant="ghost" size="icon" class="size-8" aria-label="changes" @click="changesOpen = true">
          <FileDiffIcon class="size-4" aria-hidden="true" />
        </Button>
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

    <component
      :is="wide ? ResizablePanelGroup : 'div'"
      v-bind="wide ? { direction: 'horizontal', autoSaveId: 'dummie:agent-columns' } : {}"
      class="flex min-h-0 flex-1"
    >
      <template v-if="wide">
        <ResizablePanel :default-size="18" :min-size="12">
          <AgentSessions :sessions="sessions" :active="activeKey" :home="hello?.home" :renamable="renamable" :rename="renameSession" @open="openSession" @new="startNew" />
        </ResizablePanel>
        <ResizableHandle />
      </template>

      <component :is="wide ? ResizablePanel : 'div'" v-bind="wide ? { defaultSize: 50, minSize: 30 } : {}" class="flex min-w-0 flex-1 flex-col">
        <section class="flex size-full min-h-0 flex-col" aria-label="chat">
          <header class="flex items-center gap-2 border-b border-border px-3 py-2">
            <Tabs v-model="centerTab">
              <TabsList class="h-7">
                <TabsTrigger value="chat" class="h-6 font-mono text-xs">chat</TabsTrigger>
                <TabsTrigger value="console" class="h-6 font-mono text-xs">console</TabsTrigger>
                <TabsTrigger value="files" class="h-6 font-mono text-xs">files</TabsTrigger>
                <TabsTrigger value="transfer" class="h-6 font-mono text-xs">transfer</TabsTrigger>
                <TabsTrigger value="network" class="h-6 font-mono text-xs">network</TabsTrigger>
              </TabsList>
            </Tabs>
            <div v-if="centerTab === 'console'" class="ml-auto flex items-center gap-2">
              <span
                class="size-1.5 rounded-full"
                :class="consolePhase === 'open' ? 'bg-emerald-500' : consolePhase === 'closed' || consolePhase === 'error' ? 'bg-destructive' : 'bg-amber-500'"
                aria-hidden="true"
              />
              <Button
                v-if="consolePhase === 'open'"
                variant="outline"
                size="sm"
                class="h-7 font-mono text-xs"
                @click="terminal?.disconnect()"
              >
                Disconnect
              </Button>
              <Button
                v-else-if="consolePhase === 'closed' || consolePhase === 'error'"
                variant="outline"
                size="sm"
                class="h-7 font-mono text-xs"
                @click="terminal?.connect()"
              >
                Reconnect
              </Button>
              <span v-else class="font-mono text-xs text-muted-foreground">{{ consolePhase }}</span>
            </div>
          </header>

          <AgentChatPanel v-show="centerTab === 'chat'" />

          <AgentFilesPanel v-show="centerTab === 'files'" hint="Pick a file in the right panel's files tab to open it here." />

          <div v-if="transferMounted" v-show="centerTab === 'transfer'" class="flex min-h-0 flex-1 flex-col">
            <AgentTransfer
              :initial-path="activeCwd || hello?.cwd || hello?.home || ''"
              :connected="phase === 'open'"
              :active="centerTab === 'transfer'"
              :list="sock.list"
              :upload="sock.upload"
              :read-raw="sock.readRaw"
            />
          </div>

          <div v-if="centerTab === 'network'" class="min-h-0 flex-1 overflow-y-auto p-4">
            <VmDestinations :vm-id="id" />
          </div>

          <div v-if="consoleMounted" v-show="centerTab === 'console'" class="flex min-h-0 flex-1 flex-col">
            <p
              v-if="consoleMessage"
              class="border-b border-border bg-muted/40 px-3 py-2 font-mono text-xs"
              :class="consolePhase === 'error' ? 'text-destructive' : 'text-muted-foreground'"
              role="status"
            >
              {{ consoleMessage }}
            </p>
            <VmTerminal
              ref="terminal"
              :vm-id="id"
              :cwd="activeCwd || newCwd.trim() || undefined"
              @phase="consolePhase = $event"
              @message="consoleMessage = $event"
            />
            <VmTerminalKeypad
              class="shrink-0 border-t border-border pb-[env(safe-area-inset-bottom)] sm:hidden"
              :terminal="terminal"
              :disabled="consolePhase !== 'open'"
            />
          </div>
        </section>
      </component>

      <template v-if="wide">
        <ResizableHandle />
        <ResizablePanel :default-size="32" :min-size="18">
          <AgentChanges :diff="diff" :read-file="sock.readFile" :write-file="sock.writeFile" @git-init="gitInit" @edited="onEdited" @open-file="openFile" />
        </ResizablePanel>
      </template>
    </component>

    <Sheet v-if="!wide" v-model:open="sessionsOpen">
      <SheetContent side="left" class="w-[85%] gap-0 p-0">
        <SheetHeader class="sr-only">
          <SheetTitle>Sessions</SheetTitle>
          <SheetDescription>Agent sessions in this VM</SheetDescription>
        </SheetHeader>
        <AgentSessions :sessions="sessions" :active="activeKey" :home="hello?.home" :renamable="renamable" :rename="renameSession" @open="openSession" @new="startNew" />
      </SheetContent>
    </Sheet>
    <Sheet v-if="!wide" v-model:open="changesOpen">
      <SheetContent side="right" class="w-full gap-0 p-0 sm:max-w-full">
        <SheetHeader class="sr-only">
          <SheetTitle>Changes</SheetTitle>
          <SheetDescription>Uncommitted changes in the session's directory</SheetDescription>
        </SheetHeader>
        <AgentChanges in-sheet :diff="diff" :read-file="sock.readFile" :write-file="sock.writeFile" @git-init="gitInit" @edited="onEdited" @open-file="openFile" />
      </SheetContent>
    </Sheet>
  </div>
</template>
