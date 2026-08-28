<script setup lang="ts">
import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'

const props = defineProps<{
  vmId: string
  fontSize?: number
}>()

export type Phase = 'loading' | 'connecting' | 'open' | 'closed' | 'error'

interface VM {
  name: string
  vm_id: string
  status: string
  console_url: string
}

interface ConsoleToken {
  url: string
  token: string
  expires_in: number
}

const emit = defineEmits<{
  phase: [phase: Phase]
  message: [message: string | null]
  vm: [vm: VM]
  host: [host: string]
}>()

const { authFetch } = useAuth()
const colorMode = useColorMode()

const themes = {
  dark: {
    background: '#09090b',
    foreground: '#e4e4e7',
    cursor: '#e4e4e7',
    selectionBackground: '#3f3f46',
  },
  light: {
    background: '#ffffff',
    foreground: '#18181b',
    cursor: '#18181b',
    cursorAccent: '#ffffff',
    selectionBackground: '#bfdbfe',
    black: '#18181b',
    red: '#b91c1c',
    green: '#15803d',
    yellow: '#a16207',
    blue: '#1d4ed8',
    magenta: '#a21caf',
    cyan: '#0e7490',
    white: '#71717a',
    brightBlack: '#52525b',
    brightRed: '#dc2626',
    brightGreen: '#16a34a',
    brightYellow: '#ca8a04',
    brightBlue: '#2563eb',
    brightMagenta: '#c026d3',
    brightCyan: '#0891b2',
    brightWhite: '#27272a',
  },
} as const

const theme = computed(() => colorMode.value === 'dark' ? themes.dark : themes.light)

const phase = ref<Phase>('loading')
const message = ref<string | null>(null)

watch(phase, p => emit('phase', p))
watch(message, m => emit('message', m))

const termEl = useTemplateRef<HTMLDivElement>('termEl')
let term: Terminal | null = null
let fit: FitAddon | null = null
let ws: WebSocket | null = null
let resizeObserver: ResizeObserver | null = null

watch(theme, (t) => {
  if (term) term.options.theme = { ...t }
})

const encoder = new TextEncoder()

async function readMessage(res: Response): Promise<string | null> {
  try {
    const b = await res.json()
    return typeof b?.message === 'string' ? b.message : null
  }
  catch {
    return null
  }
}

function sendResize() {
  if (!term || ws?.readyState !== WebSocket.OPEN) return
  ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
}

function fitAndResize() {
  try {
    fit?.fit()
  }
  catch {
    return
  }
  sendResize()
}

async function connect() {
  if (!term) return
  phase.value = 'connecting'
  message.value = null

  let creds: ConsoleToken
  try {
    const res = await authFetch(`/vms/${props.vmId}/console-token`, { method: 'POST' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    creds = await res.json()
  }
  catch (e) {
    phase.value = 'error'
    message.value = e instanceof Error ? e.message : 'Could not get permission to open a console'
    return
  }

  const url = new URL(creds.url)
  url.searchParams.set('token', creds.token)
  emit('host', url.host)

  const sock = new WebSocket(url.toString())
  sock.binaryType = 'arraybuffer'
  ws = sock

  sock.onopen = () => {
    phase.value = 'open'
    message.value = null
    fitAndResize()
    term?.focus()
  }
  sock.onmessage = (ev) => {
    if (typeof ev.data === 'string') return
    term?.write(new Uint8Array(ev.data as ArrayBuffer))
  }
  sock.onerror = () => {
    message.value = 'The connection failed. The VM may be stopped, or the console may not be reachable from here.'
  }
  sock.onclose = (ev) => {
    if (ws === sock) ws = null
    phase.value = 'closed'
    if (!message.value) {
      message.value = ev.reason || 'The session ended.'
    }
  }
}

function disconnect() {
  const sock = ws
  ws = null
  sock?.close(1000, 'closed by the user')
}

onMounted(async () => {
  let vm: VM
  try {
    const res = await authFetch(`/vms/${props.vmId}`)
    if (res.status === 404) throw new Error('This VM does not exist, or is not yours.')
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    vm = await res.json()
  }
  catch (e) {
    phase.value = 'error'
    message.value = e instanceof Error ? e.message : 'Failed to load this VM'
    return
  }
  emit('vm', vm)

  if (!vm.console_url) {
    phase.value = 'error'
    message.value = 'The host this VM runs on has no domain, so it has no console.'
    return
  }

  term = new Terminal({
    convertEol: false,
    cursorBlink: true,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
    fontSize: props.fontSize ?? 13,
    theme: { ...theme.value },
  })
  fit = new FitAddon()
  term.loadAddon(fit)
  term.open(termEl.value!)

  term.onData((data) => {
    if (ws?.readyState === WebSocket.OPEN) ws.send(encoder.encode(data))
  })
  term.onResize(() => sendResize())

  resizeObserver = new ResizeObserver(() => fitAndResize())
  resizeObserver.observe(termEl.value!)

  await connect()
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  resizeObserver = null
  ws?.close(1000, 'page closed')
  ws = null
  term?.dispose()
  term = null
})

defineExpose({ phase, message, connect, disconnect })
</script>

<template>
  <div
    ref="termEl"
    class="min-h-0 flex-1 overflow-hidden bg-white p-2 dark:bg-[#09090b]"
    role="application"
    aria-label="Terminal"
  />
</template>
