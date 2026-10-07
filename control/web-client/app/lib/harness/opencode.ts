// opencode serve: a message is its info plus parts, and events update either.
// The raw messages are kept so a part can be patched and its message rebuilt.
import { markRaw } from 'vue'
import { dataUrlImage, emptyChat, settle, type Block, type ChatAdapter, type ChatMessage, type ChatState } from '@/lib/agentChat'

type Part = { id: string, messageID: string, type: string, [k: string]: any }
type Info = { id: string, role: string, [k: string]: any }
type Raw = { info: Info, parts: Part[] }
type Store = Record<string, Raw>

function store(s: ChatState): Store {
  if (!s.extra) s.extra = markRaw({})
  return s.extra as Store
}

function rebuild(s: ChatState, raw: Raw) {
  const { info, parts } = raw
  const content: Block[] = []
  for (const p of parts) {
    if (p.type === 'text' && p.text && !p.synthetic && !p.ignored) content.push({ type: 'text', text: p.text })
    else if (p.type === 'reasoning') content.push({ type: 'thinking', thinking: p.text ?? '' })
    else if (p.type === 'file' && typeof p.url === 'string' && p.mime?.startsWith('image/')) {
      const img = dataUrlImage(p.url)
      if (img) content.push(img)
    }
    else if (p.type === 'tool') {
      const st = p.state ?? {}
      content.push({ type: 'toolCall', id: p.callID, name: p.tool, arguments: st.input ?? {} })
      const out = st.status === 'error' ? st.error : st.output
      s.results[p.callID] = {
        content: out ? [{ type: 'text', text: String(out) }] : [],
        isError: st.status === 'error',
        running: st.status === 'pending' || st.status === 'running',
      }
    }
  }

  const m: ChatMessage = { id: info.id, role: info.role, content, model: info.modelID, provider: info.providerID }
  if (info.role === 'assistant') {
    const err = info.error
    m.stopReason = err ? (err.name === 'MessageAbortedError' ? 'aborted' : 'error') : info.time?.completed ? 'stop' : 'pending'
    if (err) m.errorMessage = err.data?.message ?? err.name
  }
  const at = s.refs[info.id]
  if (at !== undefined) s.messages[at] = m
  else s.refs[info.id] = s.messages.push(m) - 1
}

function rawFor(s: ChatState, messageID: string): Raw {
  const st = store(s)
  st[messageID] ??= { info: { id: messageID, role: 'assistant' }, parts: [] }
  return st[messageID]!
}

function applyEvent(s: ChatState, ev: any) {
  const p = ev.properties ?? {}
  switch (ev.type) {
    case 'message.updated': {
      const raw = rawFor(s, p.info.id)
      raw.info = p.info
      rebuild(s, raw)
      break
    }
    case 'message.part.updated': {
      const raw = rawFor(s, p.part.messageID)
      const i = raw.parts.findIndex(x => x.id === p.part.id)
      if (i >= 0) raw.parts[i] = p.part
      else raw.parts.push(p.part)
      rebuild(s, raw)
      break
    }
    case 'message.part.delta': {
      const raw = rawFor(s, p.messageID)
      const part = raw.parts.find(x => x.id === p.partID)
      if (part && p.field) part[p.field] = (part[p.field] ?? '') + (p.delta ?? '')
      rebuild(s, raw)
      break
    }
    case 'message.part.removed': {
      const raw = rawFor(s, p.messageID)
      raw.parts = raw.parts.filter(x => x.id !== p.partID)
      rebuild(s, raw)
      break
    }
    case 'session.status': {
      const type = p.status?.type
      s.busy = type !== 'idle'
      s.retry = type === 'retry' ? `retrying (${p.status.attempt ?? '?'}): ${p.status.message ?? ''}` : null
      if (!s.busy) settle(s)
      break
    }
    case 'session.idle':
      s.busy = false
      s.retry = null
      settle(s)
      break
    case 'session.error':
      if (p.error && p.error.name !== 'MessageAbortedError') {
        s.messages.push({ role: 'assistant', content: [], stopReason: 'error', errorMessage: p.error.data?.message ?? p.error.name })
      }
      break
  }
}

export const opencodeChat: ChatAdapter = {
  fromHistory(messages: Raw[], busy: boolean) {
    const s = emptyChat()
    for (const raw of messages ?? []) {
      store(s)[raw.info.id] = raw
      rebuild(s, raw)
    }
    s.busy = busy
    return s
  },
  applyEvent,
}
