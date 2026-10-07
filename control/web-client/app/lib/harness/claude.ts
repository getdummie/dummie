// claude -p in stream-json: anthropic message records plus raw stream events.
// History is the session jsonl, one assistant block per line.
import { emptyChat, settle, type Block, type ChatAdapter, type ChatMessage, type ChatState } from '@/lib/agentChat'

type ClaudeRecord = { type: string, [k: string]: any }

// Prompts claude itself wraps (slash commands and their output) are noise here.
const wrapped = /^<(command-|local-command-)/

function toBlocks(content: any[]): Block[] {
  const out: Block[] = []
  for (const c of content) {
    if (c.type === 'text' && c.text) out.push({ type: 'text', text: c.text })
    else if (c.type === 'thinking') out.push({ type: 'thinking', thinking: c.thinking ?? '' })
    else if (c.type === 'redacted_thinking') out.push({ type: 'thinking', thinking: '', redacted: true })
    else if (c.type === 'tool_use') out.push({ type: 'toolCall', id: c.id, name: c.name, arguments: c.input ?? {} })
    else if (c.type === 'image' && c.source?.type === 'base64') out.push({ type: 'image', data: c.source.data, mimeType: c.source.media_type })
  }
  return out
}

function resultBlocks(content: unknown): Block[] {
  if (typeof content === 'string') return content ? [{ type: 'text', text: content }] : []
  return Array.isArray(content) ? toBlocks(content) : []
}

function addRecord(s: ChatState, r: ClaudeRecord) {
  if (r.parent_tool_use_id || r.isSidechain || r.isMeta) return
  const msg = r.message ?? {}
  const content = typeof msg.content === 'string' ? [{ type: 'text', text: msg.content }] : msg.content ?? []

  if (r.type === 'assistant') {
    const at = msg.id ? s.refs[msg.id] : undefined
    // A message streamed as partials is already whole.
    if (msg.id && s.refs[`stream:${msg.id}`] !== undefined) return
    if (at !== undefined) {
      const m = s.messages[at]!
      m.content = [...(m.content as Block[]), ...toBlocks(content)]
      return
    }
    const m: ChatMessage = { id: msg.id, role: 'assistant', content: toBlocks(content), model: msg.model, stopReason: 'stop' }
    const i = s.messages.push(m) - 1
    if (msg.id) s.refs[msg.id] = i
    return
  }

  if (r.type === 'user') {
    const rest: any[] = []
    for (const c of content) {
      if (c.type === 'tool_result') {
        s.results[c.tool_use_id] = { content: resultBlocks(c.content), isError: !!c.is_error, running: false }
      }
      else if (!(c.type === 'text' && wrapped.test(c.text ?? ''))) {
        rest.push(c)
      }
    }
    if (rest.length) s.messages.push({ role: 'user', content: toBlocks(rest) })
  }
}

function streaming(s: ChatState): ChatMessage | null {
  const last = s.messages[s.messages.length - 1]
  return last?.role === 'assistant' && last.stopReason === 'pending' ? last : null
}

function applyStream(s: ChatState, e: any) {
  switch (e.type) {
    case 'message_start': {
      const id = e.message?.id
      const i = s.messages.push({ id, role: 'assistant', content: [], model: e.message?.model, stopReason: 'pending' }) - 1
      if (id) {
        s.refs[id] = i
        s.refs[`stream:${id}`] = i
      }
      break
    }
    case 'content_block_start': {
      const m = streaming(s)
      const b = e.content_block ?? {}
      if (!m) break
      const content = m.content as Block[]
      if (b.type === 'text') content[e.index] = { type: 'text', text: b.text ?? '' }
      else if (b.type === 'thinking') content[e.index] = { type: 'thinking', thinking: b.thinking ?? '' }
      else if (b.type === 'redacted_thinking') content[e.index] = { type: 'thinking', thinking: '', redacted: true }
      else if (b.type === 'tool_use') content[e.index] = { type: 'toolCall', id: b.id, name: b.name, partialArgs: '' }
      break
    }
    case 'content_block_delta': {
      const b = (streaming(s)?.content as Block[] | undefined)?.[e.index]
      const d = e.delta ?? {}
      if (b?.type === 'text' && d.type === 'text_delta') b.text += d.text
      else if (b?.type === 'thinking' && d.type === 'thinking_delta') b.thinking += d.thinking
      else if (b?.type === 'toolCall' && d.type === 'input_json_delta') b.partialArgs = (b.partialArgs ?? '') + d.partial_json
      break
    }
    case 'content_block_stop': {
      const b = (streaming(s)?.content as Block[] | undefined)?.[e.index]
      if (b?.type !== 'toolCall') break
      try {
        b.arguments = JSON.parse(b.partialArgs || '{}')
        b.partialArgs = undefined
      }
      catch {}
      s.results[b.id] = { content: [], isError: false, running: true }
      break
    }
    case 'message_stop': {
      const m = streaming(s)
      if (m) m.stopReason = 'stop'
      break
    }
  }
}

function applyEvent(s: ChatState, r: ClaudeRecord) {
  if (r.parent_tool_use_id) return
  switch (r.type) {
    case 'stream_event':
      s.busy = true
      applyStream(s, r.event ?? {})
      break
    case 'assistant':
      addRecord(s, r)
      break
    case 'user': {
      const before = s.messages.length
      addRecord(s, r)
      if (s.messages.length > before) s.busy = true
      break
    }
    case 'result':
      s.busy = false
      s.retry = null
      settle(s)
      if (r.is_error || (r.subtype && r.subtype !== 'success')) {
        const why = typeof r.result === 'string' && r.result ? r.result : (r.errors ?? []).join('\n') || r.subtype
        s.messages.push({ role: 'assistant', content: [], stopReason: 'error', errorMessage: why })
      }
      break
  }
}

export const claudeChat: ChatAdapter = {
  fromHistory(records: ClaudeRecord[], busy: boolean) {
    const s = emptyChat()
    for (const r of records ?? []) addRecord(s, r)
    s.busy = busy
    return s
  },
  applyEvent,
}
