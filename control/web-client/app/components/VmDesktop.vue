<script setup lang="ts">
import type { UserInteraction } from '@devolutions/iron-remote-desktop'

const props = defineProps<{ vmId: string }>()

export type Phase = 'loading' | 'ready' | 'connecting' | 'open' | 'closed' | 'error'

interface VM {
  name: string
  vm_id: string
  status: string
  desktop_url: string
}

interface DesktopToken {
  url: string
  token: string
  username: string
  password: string
}

interface IronError {
  kind?: () => number
  backtrace?: () => string
  rdcleanpathDetails?: () => { httpStatusCode?: number } | undefined
}

const emit = defineEmits<{
  phase: [phase: Phase]
  message: [message: string | null]
  vm: [vm: VM]
}>()

const { authFetch } = useAuth()

const phase = ref<Phase>('loading')
const message = ref<string | null>(null)
watch(phase, p => emit('phase', p))
watch(message, m => emit('message', m))

const root = useTemplateRef<HTMLDivElement>('root')
const screen = useTemplateRef<HTMLDivElement>('screen')
let ui: UserInteraction | null = null
let rdpModule: typeof import('@devolutions/iron-remote-desktop-rdp') | null = null
let vm: VM | null = null

async function readMessage(res: Response): Promise<string | null> {
  try {
    const b = await res.json()
    return typeof b?.message === 'string' ? b.message : null
  }
  catch {
    return null
  }
}

function describe(e: unknown): string {
  const err = e as IronError
  const status = err?.rdcleanpathDetails?.()?.httpStatusCode
  const detail = typeof err?.backtrace === 'function' ? err.backtrace() : String(e)
  return status ? `${detail} (proxy HTTP ${status})` : detail
}

// Audio arrives as 16-bit PCM over RDPSND. Chunks are queued back to back and
// the queue restarts at a small delay when it underruns or drifts too far ahead.
let actx: AudioContext | null = null
let playAt = 0

function audioContext() {
  actx ??= new AudioContext({ latencyHint: 'playback' })
  if (actx.state === 'suspended') void actx.resume()
  return actx
}

function onAudio(m: { type: string, data: Uint8Array, channels: number, sampleRate: number }) {
  if (m.type === 'close') {
    playAt = 0
    return
  }
  if (m.type !== 'wave') return
  const ctx = audioContext()
  const pcm = new DataView(m.data.buffer, m.data.byteOffset, m.data.byteLength)
  const frames = Math.floor(m.data.byteLength / 2 / m.channels)
  if (!frames) return
  const buf = ctx.createBuffer(m.channels, frames, m.sampleRate)
  for (let c = 0; c < m.channels; c++) {
    const out = buf.getChannelData(c)
    for (let i = 0; i < frames; i++) out[i] = pcm.getInt16((i * m.channels + c) * 2, true) / 32768
  }
  const src = ctx.createBufferSource()
  src.buffer = buf
  src.connect(ctx.destination)
  const now = ctx.currentTime
  if (playAt < now + 0.01 || playAt > now + 0.5) playAt = now + 0.1
  src.start(playAt)
  playAt += buf.duration
}

// IronRDP draws every frame with putImageData. On a GPU-backed canvas some
// Chromium drivers drop those frames, so its canvas is kept in software.
function keepRendererInSoftware() {
  const proto = HTMLCanvasElement.prototype as HTMLCanvasElement & { dummiePatched?: boolean }
  if (proto.dummiePatched) return
  proto.dummiePatched = true
  const original = proto.getContext
  proto.getContext = function (this: HTMLCanvasElement, type: string, options?: unknown) {
    const host = (this.getRootNode() as ShadowRoot).host
    if (type === '2d' && host?.tagName === 'IRON-REMOTE-DESKTOP') {
      options = { ...(options as object), willReadFrequently: true }
    }
    return original.call(this, type, options)
  } as typeof proto.getContext
}

// Mirrors IronRDP's own check: without clipboard-read permission support it
// drops to a text-only mode where every transfer needs a user gesture.
const clipboardFallback = ref(false)

async function detectClipboardFallback() {
  try {
    await navigator.permissions.query({ name: 'clipboard-read' as PermissionName })
    clipboardFallback.value = !navigator.clipboard?.read
  }
  catch {
    clipboardFallback.value = true
  }
}

async function pasteIntoDesktop() {
  try {
    await ui?.sendClipboardData()
  }
  catch (e) {
    message.value = `Could not read your clipboard: ${e instanceof Error ? e.message : e}`
  }
}

function resumeAudio() {
  if (actx?.state === 'suspended') void actx.resume()
}

async function connect() {
  if (!ui || !rdpModule || !vm || phase.value === 'connecting' || phase.value === 'open') return
  phase.value = 'connecting'
  message.value = null
  audioContext()

  let creds: DesktopToken
  try {
    const res = await authFetch(`/vms/${props.vmId}/desktop-token`, { method: 'POST' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    creds = await res.json()
  }
  catch (e) {
    phase.value = 'error'
    message.value = e instanceof Error ? e.message : 'Could not get permission to open the desktop'
    return
  }

  const url = new URL(creds.url)
  url.searchParams.set('token', creds.token)
  const box = screen.value!.getBoundingClientRect()

  try {
    const config = ui.configBuilder()
      .withUsername(creds.username)
      .withPassword(creds.password)
      .withDestination(`${vm.name}:3389`)
      .withProxyAddress(url.toString())
      .withServerDomain('')
      .withAuthToken(creds.token)
      .withDesktopSize({ width: Math.max(640, Math.round(box.width)), height: Math.max(480, Math.round(box.height)) })
      .withExtension(rdpModule.displayControl(true))
      .withExtension(rdpModule.audioPlayback(onAudio as (m: unknown) => void))
      .build()
    const session = await ui.connect(config)
    ui.setVisibility(true)
    phase.value = 'open'
    const end = await session.run()
    phase.value = 'closed'
    message.value = end.reason() || 'The session ended.'
  }
  catch (e) {
    phase.value = 'error'
    message.value = describe(e)
  }
  finally {
    ui?.setVisibility(phase.value === 'open')
  }
}

function disconnect() {
  ui?.shutdown()
}

function ctrlAltDel() {
  ui?.ctrlAltDel()
}

function superKey() {
  ui?.metaKey()
}

// The Keyboard Lock API only exists in Chromium and only works in fullscreen;
// it sends Super, Alt+Tab and friends to the guest instead of the local desktop.
const canLockKeyboard = import.meta.client && !!(navigator as Navigator & { keyboard?: { lock?: unknown } }).keyboard?.lock

async function fullscreen() {
  try {
    await root.value!.requestFullscreen({ navigationUI: 'hide' })
    if (canLockKeyboard) await (navigator as Navigator & { keyboard: { lock: () => Promise<void> } }).keyboard.lock()
  }
  catch (e) {
    message.value = `Fullscreen failed: ${e instanceof Error ? e.message : e}`
  }
}

function onFullscreenChange() {
  if (!document.fullscreenElement && canLockKeyboard) {
    (navigator as Navigator & { keyboard: { unlock: () => void } }).keyboard.unlock()
  }
}

onMounted(async () => {
  document.addEventListener('fullscreenchange', onFullscreenChange)
  document.addEventListener('pointerdown', resumeAudio)

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
  emit('vm', vm!)

  if (!vm!.desktop_url) {
    phase.value = 'error'
    message.value = 'The host this VM runs on has no domain, so it has no desktop.'
    return
  }

  try {
    const [, rdp] = await Promise.all([
      import('@devolutions/iron-remote-desktop'),
      import('@devolutions/iron-remote-desktop-rdp'),
    ])
    rdpModule = rdp
    keepRendererInSoftware()
    await rdp.init('INFO')
    // The element reads `module` as it mounts, so it is built here rather than
    // in the template.
    const el = document.createElement('iron-remote-desktop') as HTMLElement & { module: unknown }
    el.module = rdp.Backend
    el.setAttribute('scale', 'fit')
    el.setAttribute('flexcenter', 'true')
    const ready = new Promise<UserInteraction>(resolve =>
      el.addEventListener('ready', e => resolve((e as CustomEvent).detail.irgUserInteraction), { once: true }))
    screen.value!.append(el)
    ui = await ready
    ui.setEnableClipboard(true)
    await detectClipboardFallback()
    // Only fires in the fallback mode. A copy in the guest follows a key press or
    // click in this tab, which is what lets the page write the clipboard here.
    ui.onClipboardRemoteUpdateCallback(() => {
      ui?.saveRemoteClipboardData().catch(() => {})
    })
  }
  catch (e) {
    phase.value = 'error'
    message.value = `Could not load the remote desktop client: ${e instanceof Error ? e.message : e}`
    return
  }

  phase.value = 'ready'
  await connect()
})

onBeforeUnmount(() => {
  document.removeEventListener('fullscreenchange', onFullscreenChange)
  document.removeEventListener('pointerdown', resumeAudio)
  ui?.shutdown()
  ui = null
  void actx?.close()
  actx = null
})

defineExpose({ phase, message, connect, disconnect, ctrlAltDel, superKey, fullscreen, clipboardFallback, pasteIntoDesktop })
</script>

<template>
  <div ref="root" class="flex min-h-0 flex-1 flex-col bg-black">
    <div
      ref="screen"
      class="min-h-0 flex-1 overflow-hidden"
      role="application"
      aria-label="Remote desktop"
    />
  </div>
</template>
