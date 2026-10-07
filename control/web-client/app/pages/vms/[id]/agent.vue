<script setup lang="ts">
import { ArrowLeft, CircleCheck, FileDiff as FileDiffIcon, FolderPlus, PanelLeft } from '@lucide/vue'
import { useLocalStorage, useMediaQuery, watchDebounced } from '@vueuse/core'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from '@/components/ui/resizable'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import AgentChanges from '@/components/agent/AgentChanges.vue'
import AgentComposer from '@/components/agent/AgentComposer.vue'
import AgentFileEditor from '@/components/agent/AgentFileEditor.vue'
import AgentHarnessIcon from '@/components/agent/AgentHarnessIcon.vue'
import AgentSessions from '@/components/agent/AgentSessions.vue'
import AgentThread from '@/components/agent/AgentThread.vue'
import ThemeToggle from '@/components/ThemeToggle.vue'
import VmDestinations from '@/components/VmDestinations.vue'
import VmTerminal, { type Phase as ConsolePhase } from '@/components/VmTerminal.vue'
import VmTerminalKeypad from '@/components/VmTerminalKeypad.vue'
import type { AgentSession } from '@/composables/useAgentSocket'
import { emptyChat, type ChatState, type Draft, type Model } from '@/lib/agentChat'
import { chatAdapter } from '@/lib/harness'

definePageMeta({ middleware: ['auth'], layout: false })

const route = useRoute()
const router = useRouter()
const id = computed(() => String(route.params.id))
const wide = useMediaQuery('(min-width: 1024px)')

const sock = useAgentSocket(() => id.value)
const { phase, error, hello, sessions, diff } = sock

const activeKey = ref<string | null>(null)
const activeId = ref<string | null>(null)
const activeCwd = ref('')
const alive = ref(false)
// The active session's harness, or the one a new session will start in.
const harness = ref('pi')
const preferredHarness = useLocalStorage('dummie:agent-harness', 'pi')
const chat = ref<ChatState>(emptyChat())
const sessionModels = ref<Model[]>([])
const harnessInfo = computed(() => hello.value?.harnesses.find(h => h.name === harness.value))
// Until a session is open there is no agent to ask, so dinit's preset list stands in.
const models = computed(() => (activeKey.value && sessionModels.value.length ? sessionModels.value : harnessInfo.value?.models ?? []))
const model = ref('')
// The last model picked per harness, so a new session does not start on its default.
const preferredModels = useLocalStorage<Record<string, string>>('dummie:agent-models', {})
const newCwd = ref('')
const sending = ref(false)
const actionError = ref<string | null>(null)
const sessionsOpen = ref(false)
const changesOpen = ref(false)
const centerTab = ref<'chat' | 'console' | 'files' | 'network'>('chat')
// The file picked in the right panel's tree, edited in the center panel.
const openedFile = ref<{ cwd: string, path: string } | null>(null)

function onOpenFile(file: { cwd: string, path: string }) {
  openedFile.value = file
  centerTab.value = 'files'
  changesOpen.value = false
}
// The console mounts on first use and then stays, so switching tabs keeps
// its shell alive.
const consoleMounted = ref(false)
const consolePhase = ref<ConsolePhase>('loading')
const consoleMessage = ref<string | null>(null)
const terminal = useTemplateRef<InstanceType<typeof VmTerminal>>('terminal')

watch(centerTab, async (tab) => {
  if (tab !== 'console') return
  consoleMounted.value = true
  await nextTick()
  terminal.value?.focus()
})
// Files edited by hand since the last prompt, so the agent hears about them.
const editedFiles = ref(new Set<string>())
const cwdState = ref<'checking' | 'dir' | 'file' | 'missing' | null>(null)
const creatingDir = ref(false)

const installs: Record<string, string> = {
  pi: 'bun install -g @earendil-works/pi-coding-agent',
  claude: 'bun install -g @anthropic-ai/claude-code',
  codex: 'bun install -g @openai/codex',
  opencode: 'bun install -g opencode-ai',
  gemini: 'bun install -g @google/gemini-cli',
}
const busyHint = computed(() => harnessInfo.value?.steers
  ? 'a message sent now steers the running agent'
  : 'a message sent now waits for this turn to end')
const title = computed(() => {
  const s = sessions.value.find(s => s.key === activeKey.value)
  return s?.name || s?.title || (activeKey.value ? 'session' : 'new session')
})

useHead(() => ({ title: `dummie — agent · ${title.value}` }))

const modelKey = (m: { provider: string, id: string }) => `${m.provider}/${m.id}`

sock.on((m) => {
  if (m.t === 'event' && m.key === activeKey.value) chatAdapter(harness.value).applyEvent(chat.value, m.ev)
  if (m.t === 'exit' && m.key === activeKey.value) {
    alive.value = false
    chat.value.busy = false
    if (m.error) actionError.value = `The agent stopped: ${m.error}`
  }
  if (m.t === 'hello') {
    newCwd.value ||= m.cwd
    if (activeId.value) {
      void open({ harness: harness.value, id: activeId.value })
      return
    }
    watchCwd(m.cwd)
    const sid = typeof route.query.id === 'string' ? route.query.id : null
    pickHarness(typeof route.query.harness === 'string' ? route.query.harness : preferredHarness.value)
    if (sid) void open({ harness: harness.value, id: sid })
  }
})

function pickHarness(name: string) {
  if (activeKey.value) return
  harness.value = name
  preferredHarness.value = name
  model.value = preferredModels.value[name] ?? ''
}

function watchCwd(cwd: string) {
  if (cwd) sock.watch(cwd)
}

function openSession(s: AgentSession) {
  sessionsOpen.value = false
  void open({ harness: s.harness, id: s.id })
}

async function open(target: { harness: string, id: string }) {
  actionError.value = null
  try {
    loadOpened(await sock.request({ t: 'open', ...target }))
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not open the session'
  }
}

function loadOpened(res: Record<string, any>) {
  activeKey.value = res.key
  activeId.value = res.id
  harness.value = res.harness
  activeCwd.value = res.cwd
  alive.value = true
  chat.value = chatAdapter(res.harness).fromHistory(res.history, !!res.streaming)
  model.value = res.model ? modelKey(res.model) : ''
  watchCwd(res.cwd)
  void router.replace({ query: { harness: res.harness, id: res.id } })
  void loadModels()
}

async function loadModels() {
  if (!activeKey.value) return
  try {
    const res = await sock.models(activeKey.value)
    sessionModels.value = res.models
    if (res.current) model.value = modelKey(res.current)
  }
  catch {
    sessionModels.value = []
  }
}

function startNew() {
  activeKey.value = null
  activeId.value = null
  alive.value = false
  chat.value = emptyChat()
  sessionModels.value = []
  pickHarness(preferredHarness.value)
  sessionsOpen.value = false
  centerTab.value = 'chat'
  newCwd.value = hello.value?.cwd ?? ''
  watchCwd(newCwd.value)
  void router.replace({ query: {} })
}

async function ensureSession(): Promise<string> {
  if (activeKey.value && alive.value) return activeKey.value
  const picked = models.value.find(m => modelKey(m) === model.value)
  const res = activeId.value
    ? await sock.request({ t: 'open', harness: harness.value, id: activeId.value })
    : await sock.request({ t: 'open', harness: harness.value, cwd: newCwd.value.trim(), model: picked && { provider: picked.provider, id: picked.id } })
  loadOpened(res)
  return res.key
}

async function send(draft: Draft) {
  sending.value = true
  actionError.value = null
  try {
    const key = await ensureSession()
    const attachments = await Promise.all(draft.files.map(async f => ({
      path: await sock.upload(f),
      mime: f.type || 'application/octet-stream',
    })))
    let text = draft.text
    if (editedFiles.value.size) {
      text += `\n\n(I edited these files by hand since your last turn; re-read them before changing them: ${[...editedFiles.value].join(', ')})`
    }
    await sock.prompt(key, text, attachments)
    editedFiles.value = new Set()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not send the message'
  }
  finally {
    sending.value = false
  }
}

async function stop() {
  if (!activeKey.value) return
  try {
    await sock.abort(activeKey.value)
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not stop the agent'
  }
}

async function setModel(key: string) {
  const m = models.value.find(m => modelKey(m) === key)
  if (!m) return
  const remember = () => {
    model.value = key
    preferredModels.value = { ...preferredModels.value, [harness.value]: key }
  }
  if (!activeKey.value || !alive.value) {
    remember()
    return
  }
  try {
    await sock.setModel(activeKey.value, m)
    remember()
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not switch the model'
  }
}

async function gitInit() {
  try {
    await sock.request({ t: 'git_init', cwd: activeCwd.value || newCwd.value })
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'git init failed'
  }
}

watch(newCwd, (cwd) => {
  if (!activeKey.value) watchCwd(cwd.trim())
})

async function checkCwd() {
  const path = newCwd.value.trim()
  if (activeKey.value || !path || phase.value !== 'open') {
    cwdState.value = null
    return
  }
  cwdState.value = 'checking'
  try {
    const st = await sock.request({ t: 'stat', path })
    cwdState.value = st.dir ? 'dir' : st.exists ? 'file' : 'missing'
  }
  catch {
    cwdState.value = null
  }
}

watchDebounced([newCwd, phase, activeKey], checkCwd, { debounce: 300, immediate: true })

async function createCwd() {
  creatingDir.value = true
  try {
    const res = await sock.request({ t: 'mkdir', path: newCwd.value.trim() })
    newCwd.value = res.path
    await checkCwd()
    watchCwd(res.path)
  }
  catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Could not create the directory'
  }
  finally {
    creatingDir.value = false
  }
}

const renamable = computed(() => (hello.value?.harnesses ?? []).filter(h => h.renames).map(h => h.name))

function renameSession(s: AgentSession, name: string) {
  return sock.rename(s.harness, s.id, name)
}

function onEdited(path: string) {
  editedFiles.value = new Set(editedFiles.value).add(path)
}

const statusLabel = computed(() => ({ connecting: 'connecting', open: 'connected', closed: 'reconnecting', error: 'error' }[phase.value]))

onMounted(() => sock.connect())
onBeforeUnmount(() => sock.close())
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
        <code class="font-mono text-xs">{{ installs[harnessInfo.name] }}</code>
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

          <div v-show="centerTab === 'chat'" class="flex min-h-0 flex-1 flex-col">
            <div v-if="!activeKey" class="mx-auto mt-10 w-full max-w-3xl px-4">
              <p id="agent-harness" class="eyebrow mb-2 text-muted-foreground">agent</p>
              <Tabs :model-value="harness" class="mb-6" @update:model-value="pickHarness(String($event))">
                <TabsList class="h-auto flex-wrap justify-start" aria-labelledby="agent-harness">
                  <TabsTrigger
                    v-for="h in hello?.harnesses ?? []"
                    :key="h.name"
                    :value="h.name"
                    class="h-7 flex-none gap-1.5 font-mono text-xs"
                    :class="!h.available && 'opacity-60'"
                  >
                    <AgentHarnessIcon :harness="h.name" />
                    {{ h.name }}
                  </TabsTrigger>
                </TabsList>
              </Tabs>
              <label for="agent-cwd" class="eyebrow mb-2 block text-muted-foreground">working directory</label>
              <Input id="agent-cwd" v-model="newCwd" class="font-mono text-sm" spellcheck="false" placeholder="~/app" />
              <div class="mt-2 flex min-h-7 flex-wrap items-center gap-2 font-mono text-xs" role="status">
                <span v-if="cwdState === 'dir'" class="inline-flex items-center gap-1 text-muted-foreground">
                  <CircleCheck class="size-3.5 text-primary-text" aria-hidden="true" />
                  exists
                </span>
                <span v-else-if="cwdState === 'file'" class="text-destructive">that is a file, not a directory</span>
                <template v-else-if="cwdState === 'missing'">
                  <span class="text-muted-foreground">does not exist yet</span>
                  <Button variant="outline" size="sm" class="h-7 font-mono text-xs" :disabled="creatingDir" @click="createCwd">
                    <FolderPlus class="size-3.5" aria-hidden="true" />
                    Create it
                  </Button>
                </template>
              </div>
              <p class="mt-1 font-mono text-xs text-muted-foreground">
                A new {{ harness }} session starts here when you send the first message. The diff pane shows this directory's uncommitted changes.
              </p>
            </div>

            <AgentThread v-if="activeKey" :chat="chat" />
            <div v-else class="flex-1" />

            <p v-if="actionError" class="mx-auto w-full max-w-3xl px-4 pb-2 font-mono text-xs text-destructive" role="alert">
              {{ actionError }}
            </p>
            <AgentComposer
              :busy="chat.busy"
              :busy-hint="busyHint"
              :sending="sending"
              :disabled="phase !== 'open' || !harnessInfo?.available"
              :models="models"
              :model="model"
              @send="send"
              @stop="stop"
              @model="setModel"
            />
          </div>

          <div v-show="centerTab === 'files'" class="flex min-h-0 flex-1 flex-col">
            <AgentFileEditor
              v-if="openedFile"
              :key="`${openedFile.cwd}/${openedFile.path}`"
              :cwd="openedFile.cwd"
              :path="openedFile.path"
              mode="file"
              :read-file="sock.readFile"
              :write-file="sock.writeFile"
              @close="openedFile = null"
              @saved="onEdited"
            />
            <p v-else class="p-4 font-mono text-xs text-muted-foreground">
              Pick a file in the right panel's files tab to open it here.
            </p>
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
          <AgentChanges :diff="diff" :read-file="sock.readFile" :write-file="sock.writeFile" @git-init="gitInit" @edited="onEdited" @open-file="onOpenFile" />
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
        <AgentChanges in-sheet :diff="diff" :read-file="sock.readFile" :write-file="sock.writeFile" @git-init="gitInit" @edited="onEdited" @open-file="onOpenFile" />
      </SheetContent>
    </Sheet>
  </div>
</template>
