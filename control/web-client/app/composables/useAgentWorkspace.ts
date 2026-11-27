import { markRaw, type InjectionKey, type Reactive } from 'vue'
import { useLocalStorage, watchDebounced } from '@vueuse/core'
import type { AgentSession } from '@/composables/useAgentSocket'
import { emptyChat, type ChatState, type Draft, type Model } from '@/lib/agentChat'
import { mediaKind } from '@/lib/fileKind'
import { chatAdapter } from '@/lib/harness'

export type AgentView = 'chat' | 'files'

const installs: Record<string, string> = {
  pi: 'bun install -g @earendil-works/pi-coding-agent',
  claude: 'bun install -g @anthropic-ai/claude-code',
  codex: 'bun install -g @openai/codex',
  opencode: 'bun install -g opencode-ai',
  gemini: 'bun install -g @google/gemini-cli',
}

const modelKey = (m: { provider: string, id: string }) => `${m.provider}/${m.id}`

// The agent page's session state, shared by every panel that shows a part of it.
// reveal is asked to bring a view into sight, e.g. after a file is picked.
export function useAgentWorkspace(vmId: () => string, reveal: (view: AgentView) => void) {
  const route = useRoute()
  const router = useRouter()
  const sock = useAgentSocket(vmId)
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
  const openedFile = ref<{ cwd: string, path: string } | null>(null)
  const openedMedia = computed(() => openedFile.value && mediaKind(openedFile.value.path))
  // Files edited by hand since the last prompt, so the agent hears about them.
  const editedFiles = ref(new Set<string>())
  const cwdState = ref<'checking' | 'dir' | 'file' | 'missing' | null>(null)
  const creatingDir = ref(false)

  const install = computed(() => installs[harness.value])
  const busyHint = computed(() => harnessInfo.value?.steers
    ? 'a message sent now steers the running agent'
    : 'a message sent now waits for this turn to end')
  const title = computed(() => {
    const s = sessions.value.find(s => s.key === activeKey.value)
    return s?.name || s?.title || (activeKey.value ? 'session' : 'new session')
  })
  const renamable = computed(() => (hello.value?.harnesses ?? []).filter(h => h.renames).map(h => h.name))
  const statusLabel = computed(() => ({ connecting: 'connecting', open: 'connected', closed: 'reconnecting', error: 'error' }[phase.value]))

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

  function openFile(file: { cwd: string, path: string }) {
    openedFile.value = file
    reveal('files')
  }

  function openSession(s: AgentSession) {
    reveal('chat')
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
    reveal('chat')
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

  function renameSession(s: AgentSession, name: string) {
    return sock.rename(s.harness, s.id, name)
  }

  function onEdited(path: string) {
    editedFiles.value = new Set(editedFiles.value).add(path)
  }

  onMounted(() => sock.connect())
  onBeforeUnmount(() => sock.close())

  return {
    sock: markRaw(sock),
    phase,
    error,
    hello,
    sessions,
    diff,
    activeKey,
    activeCwd,
    harness,
    harnessInfo,
    chat,
    models,
    model,
    newCwd,
    sending,
    actionError,
    openedFile,
    openedMedia,
    cwdState,
    creatingDir,
    install,
    busyHint,
    title,
    renamable,
    statusLabel,
    pickHarness,
    openFile,
    openSession,
    startNew,
    send,
    stop,
    setModel,
    gitInit,
    createCwd,
    renameSession,
    onEdited,
  }
}

export type AgentWorkspace = Reactive<ReturnType<typeof useAgentWorkspace>>
export const agentWorkspaceKey: InjectionKey<AgentWorkspace> = Symbol('agent-workspace')

export function useInjectedAgentWorkspace(): AgentWorkspace {
  const ws = inject(agentWorkspaceKey)
  if (!ws) throw new Error('no agent workspace provided')
  return ws
}
