// gemini over acp: session/update notifications in chunks, plus the turn
// markers dinit adds. History is the same notifications, replayed.
import { emptyChat, settle, type Block, type ChatAdapter, type ChatMessage, type ChatState, type ToolResult } from '@/lib/agentChat'

function assistant(s: ChatState): ChatMessage {
  const last = s.messages[s.messages.length - 1]
  if (last?.role === 'assistant' && last.stopReason === 'pending') return last
  const m: ChatMessage = { role: 'assistant', content: [], stopReason: 'pending' }
  s.messages.push(m)
  return s.messages[s.messages.length - 1]!
}

function chunk(content: any): Block | null {
  if (content?.type === 'text') return { type: 'text', text: content.text ?? '' }
  if (content?.type === 'image') return { type: 'image', data: content.data, mimeType: content.mimeType }
  return null
}

function append(blocks: Block[], b: Block) {
  const last = blocks[blocks.length - 1]
  if (last?.type === 'text' && b.type === 'text') last.text += b.text
  else if (last?.type === 'thinking' && b.type === 'thinking') last.thinking += b.thinking
  else blocks.push(b)
}

function toolContent(content: any[] | undefined): Block[] {
  const out: string[] = []
  for (const c of content ?? []) {
    if (c.type === 'content' && c.content?.type === 'text') out.push(c.content.text)
    else if (c.type === 'diff') out.push(`edited ${c.path}`)
  }
  return out.length ? [{ type: 'text', text: out.join('\n') }] : []
}

function setResult(s: ChatState, id: string, u: any) {
  const prev: ToolResult = s.results[id] ?? { content: [], isError: false, running: true }
  s.results[id] = {
    content: u.content ? toolContent(u.content) : prev.content,
    isError: u.status ? u.status === 'failed' : prev.isError,
    running: u.status ? u.status === 'pending' || u.status === 'in_progress' : prev.running,
  }
}

function applyUpdate(s: ChatState, u: any) {
  switch (u.sessionUpdate) {
    case 'user_message_chunk': {
      const b = chunk(u.content)
      if (!b) break
      const last = s.messages[s.messages.length - 1]
      if (last?.role === 'user') append(last.content as Block[], b)
      else s.messages.push({ role: 'user', content: [b] })
      break
    }
    case 'agent_message_chunk': {
      const b = chunk(u.content)
      if (b) append(assistant(s).content as Block[], b)
      break
    }
    case 'agent_thought_chunk':
      if (u.content?.type === 'text') append(assistant(s).content as Block[], { type: 'thinking', thinking: u.content.text ?? '' })
      break
    case 'tool_call':
      (assistant(s).content as Block[]).push({ type: 'toolCall', id: u.toolCallId, name: u.title || u.kind || 'tool', arguments: u.rawInput ?? {} })
      setResult(s, u.toolCallId, { status: u.status ?? 'pending', content: u.content })
      break
    case 'tool_call_update':
      setResult(s, u.toolCallId, u)
      break
  }
}

function applyEvent(s: ChatState, ev: any) {
  switch (ev.method) {
    case 'session/update':
      applyUpdate(s, ev.params?.update ?? {})
      break
    case 'dummie/turn_start':
      s.busy = true
      break
    case 'dummie/turn_end': {
      s.busy = false
      settle(s)
      const p = ev.params ?? {}
      if (p.error || p.stopReason === 'refusal') {
        s.messages.push({ role: 'assistant', content: [], stopReason: 'error', errorMessage: p.error ?? 'the model refused' })
      }
      else if (p.stopReason === 'cancelled') {
        s.messages.push({ role: 'assistant', content: [], stopReason: 'aborted' })
      }
      break
    }
  }
}

export const geminiChat: ChatAdapter = {
  fromHistory(events: any[], busy: boolean) {
    const s = emptyChat()
    for (const ev of events ?? []) applyEvent(s, ev)
    s.busy = busy
    if (!busy) settle(s)
    return s
  },
  applyEvent,
}
