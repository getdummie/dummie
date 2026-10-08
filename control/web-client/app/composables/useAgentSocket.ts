import { ref, shallowRef } from 'vue'

export type AgentPhase = 'connecting' | 'open' | 'closed' | 'error'

export interface AgentModel {
  provider: string
  id: string
  name?: string
}

export interface AgentHarness {
  name: string
  available: boolean
  error?: string
  // steers says a prompt sent mid-turn reaches the running turn; for the
  // rest it waits for the turn to end.
  steers: boolean
  renames: boolean
  models: AgentModel[]
}

export interface AgentHello {
  version: string
  home: string
  cwd: string
  harnesses: AgentHarness[]
}

export interface AgentSession {
  key: string
  id: string
  cwd: string
  name?: string
  title?: string
  created: string
  updated: number
  harness: string
  live: boolean
  streaming: boolean
}

export interface AgentDiff {
  cwd: string
  repo: boolean
  branch?: string
  patch: string
  truncated?: boolean
  tree: string[]
  error?: string
}

export interface AgentFile {
  content: string
  hash: string
  exists: boolean
  base: string
  baseExists: boolean
}

// AgentError keeps the daemon's code, e.g. "conflict" on a stale save.
export class AgentError extends Error {
  constructor(message: string, readonly code?: string) {
    super(message)
  }
}

type Msg = Record<string, any>
type Pending = { resolve: (m: Msg) => void, reject: (e: Error) => void }

const replyTypes = new Set(['opened', 'models', 'uploaded', 'upload_ack', 'ok', 'sessions', 'stat', 'file', 'written', 'write_ack', 'raw'])
const uploadChunk = 192 * 1024
// Characters, not bytes: JSON escaping can grow them, and dpipe takes 1 MiB.
const writeChunk = 128 * 1024
const maxBackoff = 15_000

export function useAgentSocket(vmId: () => string) {
  const { authFetch } = useAuth()

  const phase = ref<AgentPhase>('connecting')
  const error = ref<string | null>(null)
  const hello = shallowRef<AgentHello | null>(null)
  const sessions = shallowRef<AgentSession[]>([])
  const diff = shallowRef<AgentDiff | null>(null)

  const listeners = new Set<(m: Msg) => void>()
  const pending = new Map<string, Pending>()
  let ws: WebSocket | null = null
  let seq = 0
  let backoff = 1000
  let closedByUs = false
  let retry: ReturnType<typeof setTimeout> | null = null
  let watching = ''

  async function connect() {
    closedByUs = false
    phase.value = 'connecting'
    let creds: { url: string, token: string }
    try {
      const res = await authFetch(`/vms/${vmId()}/agent-token`, { method: 'POST' })
      if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
      creds = await res.json()
    }
    catch (e) {
      fail(e instanceof Error ? e.message : 'Could not get permission to open the agent')
      return
    }

    const url = new URL(creds.url)
    url.searchParams.set('token', creds.token)
    const sock = new WebSocket(url.toString())
    ws = sock

    sock.onopen = () => {
      backoff = 1000
      phase.value = 'open'
      error.value = null
      send({ t: 'hello' })
      if (watching) send({ t: 'watch', cwd: watching })
    }
    sock.onmessage = ev => typeof ev.data === 'string' && receive(JSON.parse(ev.data))
    sock.onclose = (ev) => {
      if (ws !== sock) return
      ws = null
      for (const p of pending.values()) p.reject(new Error('the connection to the vm closed'))
      pending.clear()
      if (closedByUs || phase.value === 'error') return
      phase.value = 'closed'
      if (!error.value && ev.reason) error.value = ev.reason
      retry = setTimeout(connect, backoff)
      backoff = Math.min(backoff * 2, maxBackoff)
    }
  }

  function fail(msg: string) {
    phase.value = 'error'
    error.value = msg
  }

  function receive(m: Msg) {
    if (m.req && pending.has(m.req) && (replyTypes.has(m.t) || m.t === 'error')) {
      const p = pending.get(m.req)!
      pending.delete(m.req)
      if (m.t === 'error') p.reject(new AgentError(m.message || 'the agent refused the request', m.code))
      else p.resolve(m)
    }
    switch (m.t) {
      case 'hello':
        hello.value = m as unknown as AgentHello
        send({ t: 'sessions' })
        break
      case 'sessions':
        sessions.value = m.sessions ?? []
        break
      case 'diff':
        if (m.cwd === watching) diff.value = m as unknown as AgentDiff
        break
      case 'error':
        // Unsupported or unstartable agents are fatal; per-request errors are not.
        if (!m.req && m.code) fail(m.message)
        break
    }
    for (const l of listeners) l(m)
  }

  function send(m: Msg) {
    if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(m))
  }

  function request(m: Msg): Promise<Msg> {
    return new Promise((resolve, reject) => {
      if (ws?.readyState !== WebSocket.OPEN) {
        reject(new Error('not connected to the vm'))
        return
      }
      const req = `r${++seq}`
      pending.set(req, { resolve, reject })
      ws.send(JSON.stringify({ ...m, req }))
    })
  }

  async function prompt(key: string, text: string, attachments: { path: string, mime: string }[]) {
    await request({ t: 'prompt', key, text, attachments })
  }

  async function abort(key: string) {
    await request({ t: 'abort', key })
  }

  async function models(key: string): Promise<{ models: AgentModel[], current: AgentModel | null }> {
    const m = await request({ t: 'models', key })
    return { models: m.models ?? [], current: m.current ?? null }
  }

  async function rename(harness: string, id: string, name: string) {
    await request({ t: 'rename', harness, id, name })
  }

  async function setModel(key: string, model: AgentModel) {
    await request({ t: 'set_model', key, model: { provider: model.provider, id: model.id } })
  }

  async function upload(file: File, onProgress?: (fraction: number) => void): Promise<string> {
    const req = `u${++seq}`
    for (let off = 0; off < file.size || off === 0; off += uploadChunk) {
      const chunk = new Uint8Array(await file.slice(off, off + uploadChunk).arrayBuffer())
      const last = off + uploadChunk >= file.size
      const reply = await new Promise<Msg>((resolve, reject) => {
        if (ws?.readyState !== WebSocket.OPEN) return reject(new Error('not connected to the vm'))
        pending.set(req, { resolve, reject })
        ws.send(JSON.stringify({ t: 'upload', req, name: file.name, data: toBase64(chunk), last }))
      })
      onProgress?.(Math.min(1, (off + chunk.length) / Math.max(file.size, 1)))
      if (last) return reply.path as string
    }
    throw new Error('upload did not finish')
  }

  async function readFile(cwd: string, path: string): Promise<AgentFile> {
    return (await request({ t: 'read_file', cwd, path })).file as AgentFile
  }

  async function readRaw(cwd: string, path: string, type: string, onProgress?: (fraction: number) => void): Promise<Blob> {
    const parts: BlobPart[] = []
    for (let offset = 0; ;) {
      const m = await request({ t: 'read_raw', cwd, path, offset })
      const chunk = fromBase64(m.data as string)
      parts.push(chunk)
      offset += chunk.length
      onProgress?.(Math.min(1, offset / Math.max(m.size as number, 1)))
      if (m.last || chunk.length === 0) return new Blob(parts, { type })
    }
  }

  // writeFile saves only if the file still has `hash`, unless forced; it
  // resolves to the new hash.
  async function writeFile(cwd: string, path: string, content: string, hash: string, force = false): Promise<string> {
    const req = `w${++seq}`
    let off = 0
    for (;;) {
      let end = Math.min(off + writeChunk, content.length)
      // A chunk must not end inside a surrogate pair, or the half is lost.
      const code = content.charCodeAt(end - 1)
      if (end < content.length && code >= 0xD800 && code <= 0xDBFF) end--
      const last = end >= content.length
      const data = content.slice(off, end)
      const reply = await new Promise<Msg>((resolve, reject) => {
        if (ws?.readyState !== WebSocket.OPEN) return reject(new Error('not connected to the vm'))
        pending.set(req, { resolve, reject })
        ws.send(JSON.stringify({ t: 'write_file', req, cwd, path, hash, force, data, last }))
      })
      if (last) return reply.hash as string
      off = end
    }
  }

  function watch(cwd: string) {
    if (cwd === watching) return
    watching = cwd
    diff.value = null
    send({ t: 'watch', cwd })
  }

  function on(fn: (m: Msg) => void) {
    listeners.add(fn)
    return () => listeners.delete(fn)
  }

  function close() {
    closedByUs = true
    if (retry) clearTimeout(retry)
    ws?.close(1000, 'closed by the user')
    ws = null
  }

  return { phase, error, hello, sessions, diff, connect, close, request, prompt, abort, models, setModel, rename, upload, readFile, readRaw, writeFile, watch, on }
}

function fromBase64(s: string): Uint8Array<ArrayBuffer> {
  const bin = atob(s)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

function toBase64(bytes: Uint8Array): string {
  let s = ''
  for (let i = 0; i < bytes.length; i += 0x8000) {
    s += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
  }
  return btoa(s)
}

async function readMessage(res: Response): Promise<string | null> {
  try {
    const b = await res.json()
    return typeof b?.message === 'string' ? b.message : null
  }
  catch {
    return null
  }
}
