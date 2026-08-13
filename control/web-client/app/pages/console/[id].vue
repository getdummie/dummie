<script setup lang="ts">
import { ArrowLeft } from '@lucide/vue'
import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import '@xterm/xterm/css/xterm.css'

// Its own route rather than a child of /vms/[id], which would turn that page
// into a layout: a terminal wants the whole viewport, and it is opened in a new
// tab from the VM page anyway.
definePageMeta({ middleware: ['auth'], layout: false })

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

type Phase = 'loading' | 'connecting' | 'open' | 'closed' | 'error'

const route = useRoute()
const { authFetch } = useAuth()
const id = computed(() => String(route.params.id))

const vm = ref<VM | null>(null)
const phase = ref<Phase>('loading')
const message = ref<string | null>(null)
const host = ref('')

useHead(() => ({
  title: vm.value ? `dummie — console · ${vm.value.name || vm.value.vm_id}` : 'dummie — console',
}))

const statusLabel = computed(() => ({
  loading: 'loading',
  connecting: 'connecting',
  open: 'connected',
  closed: 'disconnected',
  error: 'error',
}[phase.value]))

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
    const res = await authFetch(`/vms/${id.value}/console-token`, { method: 'POST' })
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
  host.value = url.host

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
  try {
    const res = await authFetch(`/vms/${id.value}`)
    if (res.status === 404) throw new Error('This VM does not exist, or is not yours.')
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    vm.value = await res.json()
  }
  catch (e) {
    phase.value = 'error'
    message.value = e instanceof Error ? e.message : 'Failed to load this VM'
    return
  }

  if (!vm.value?.console_url) {
    phase.value = 'error'
    message.value = 'The host this VM runs on has no domain, so it has no console.'
    return
  }

  term = new Terminal({
    convertEol: false,
    cursorBlink: true,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
    fontSize: 13,
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
  // size when the banner above it appears or goes away.
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
        {{ vm?.name || vm?.vm_id || 'console' }}
      </h1>

      <!-- The hostname the shell is actually behind, so it is visible even
           though the page itself is served from the control server. -->
      <span v-if="host" class="font-mono text-xs text-muted-foreground">{{ host }}</span>

      <div class="ml-auto flex items-center gap-2">
        <Badge :variant="phase === 'open' ? 'default' : 'secondary'" class="font-mono text-xs">
          {{ statusLabel }}
        </Badge>
        <Button
          v-if="phase === 'open'"
          variant="outline"
          size="sm"
          class="font-mono text-xs"
          @click="disconnect"
        >
          Disconnect
        </Button>
        <Button
          v-else-if="phase === 'closed' || phase === 'error'"
          variant="outline"
          size="sm"
          class="font-mono text-xs"
          :disabled="!vm?.console_url"
          @click="connect"
        >
          Reconnect
        </Button>
      </div>
    </header>

    <Alert v-if="message" :variant="phase === 'error' ? 'destructive' : 'default'" class="rounded-none border-x-0">
      <AlertTitle>{{ phase === 'error' ? 'Console unavailable' : 'Disconnected' }}</AlertTitle>
      <AlertDescription>{{ message }}</AlertDescription>
    </Alert>

    <!-- aria-label rather than a visible one: the terminal is its own live
         region and xterm manages the screen-reader text inside it. -->
    <div
      ref="termEl"
      class="min-h-0 flex-1 overflow-hidden bg-[#09090b] p-2"
      role="application"
      aria-label="Terminal"
    />
  </div>
</template>
