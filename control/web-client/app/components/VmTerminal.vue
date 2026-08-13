<script setup lang="ts">
import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'

// Extracted from the console page so the workspace view can put a live shell in a
// pane without a second copy of the websocket protocol. The component owns the
// connection and reports its phase; whoever mounts it draws the chrome.
const props = defineProps<{
  vmId: string
  fontSize?: number
}>()

export type Phase = 'loading' | 'connecting' | 'open' | 'closed' | 'error'

interface VM {
  name: string
  vm_id: string
  status: string
  // The websocket endpoint for this VM's terminal. "" when the host it runs on
  // has no domain, which is the same condition that leaves `url` empty.
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

const phase = ref<Phase>('loading')
const message = ref<string | null>(null)

watch(phase, p => emit('phase', p))
watch(message, m => emit('message', m))

const termEl = useTemplateRef<HTMLDivElement>('termEl')
let term: Terminal | null = null
let fit: FitAddon | null = null
let ws: WebSocket | null = null
let resizeObserver: ResizeObserver | null = null

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

/** Sends the current geometry so the guest's pty matches what is on screen. */
function sendResize() {
  if (!term || ws?.readyState !== WebSocket.OPEN) return
  ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
}

function fitAndResize() {
  try {
    fit?.fit()
  }
  catch {
    // fit throws while the element is detached or has no size yet; the next
    // observation covers it.
    return
  }
  sendResize()
}

// The token is minted per connection and expires in minutes, so it is fetched
// at connect time rather than held: a tab left open overnight gets a fresh one
// when the user reconnects instead of failing the handshake with a dead
// credential.
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

  // The token rides the query string because a websocket handshake has no way
  // to carry a header the browser did not put there itself. It is single-use in
  // practice and short-lived, and the proxy never logs the query.
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
    if (typeof ev.data === 'string') return // control frames are ours to send, not to read
    term?.write(new Uint8Array(ev.data as ArrayBuffer))
  }
  sock.onerror = () => {
    // A failed handshake surfaces here with no detail the browser will share,
    // and onclose follows; say what is actionable rather than guessing.
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
    // The guest owns the palette; these are only what it draws on.
    theme: { background: '#09090b', foreground: '#e4e4e7', cursor: '#e4e4e7' },
  })
  fit = new FitAddon()
  term.loadAddon(fit)
  term.open(termEl.value!)

  term.onData((data) => {
    if (ws?.readyState === WebSocket.OPEN) ws.send(encoder.encode(data))
  })
  term.onResize(() => sendResize())

  // ResizeObserver rather than a window listener: the terminal also changes
  // size when the banner above it appears or goes away, and in the workspace view
  // whenever its pane is dragged.
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
  <!-- aria-label rather than a visible one: the terminal is its own live
       region and xterm manages the screen-reader text inside it. -->
  <div
    ref="termEl"
    class="min-h-0 flex-1 overflow-hidden bg-[#09090b] p-2"
    role="application"
    aria-label="Terminal"
  />
</template>
