// The chat every harness is turned into. pi's own shapes are the model, and
// pi's reducer is here; the other harnesses' are in lib/harness/.

export type Block =
  | { type: 'text', text: string }
  | { type: 'thinking', thinking: string, redacted?: boolean }
  | { type: 'toolCall', id: string, name: string, arguments?: Record<string, unknown>, partialArgs?: string }
  | { type: 'image', data: string, mimeType: string }

export interface ChatMessage {
  id?: string
  role: string
  content?: string | Block[]
  toolCallId?: string
  toolName?: string
  isError?: boolean
  stopReason?: string
  errorMessage?: string
  model?: string
  provider?: string
  command?: string
  output?: string
  summary?: string
  timestamp?: number
}

export interface ToolResult {
  content: Block[]
  isError: boolean
  running: boolean
}

export interface Model {
  id: string
  provider: string
  name?: string
}

export interface Draft {
  text: string
  files: File[]
}

export interface ChatState {
  messages: ChatMessage[]
  results: Record<string, ToolResult>
  busy: boolean
  retry: string | null
  // refs maps a harness's own message or item id to its index in messages.
  refs: Record<string, number>
  extra?: unknown
}

export interface ChatAdapter {
  fromHistory: (history: any, streaming: boolean) => ChatState
  applyEvent: (s: ChatState, ev: any) => void
}

export function emptyChat(): ChatState {
  return { messages: [], results: {}, busy: false, retry: null, refs: {} }
}

// settle ends whatever was still streaming once a turn is over.
export function settle(s: ChatState) {
  for (const m of s.messages) if (m.stopReason === 'pending') m.stopReason = 'stop'
  for (const r of Object.values(s.results)) r.running = false
}

// upsert puts m at the index kept for id, or appends it.
export function upsert(s: ChatState, id: string, m: ChatMessage): ChatMessage {
  const at = s.refs[id]
  if (at !== undefined) s.messages[at] = m
  else s.refs[id] = s.messages.push(m) - 1
  return m
}

export function dataUrlImage(url: string): Extract<Block, { type: 'image' }> | null {
  const m = /^data:([^;,]+);base64,(.*)$/s.exec(url)
  return m ? { type: 'image', mimeType: m[1]!, data: m[2]! } : null
}

export function textBlocks(text: string | undefined): Block[] {
  return text ? [{ type: 'text', text }] : []
}

export const piChat: ChatAdapter = {
  fromHistory(messages: ChatMessage[], streaming: boolean) {
    const s = emptyChat()
    s.busy = streaming
    for (const m of messages ?? []) addMessage(s, m)
    return s
  },
  applyEvent,
}

// Tool results render under their call, so they are kept apart from the
// visible message list.
function addMessage(s: ChatState, m: ChatMessage) {
  if (m.role === 'system') return
  if (m.role === 'toolResult' && m.toolCallId) {
    s.results[m.toolCallId] = { content: blocks(m.content), isError: !!m.isError, running: false }
    return
  }
  s.messages.push(m)
}

export function blocks(content: ChatMessage['content']): Block[] {
  if (typeof content === 'string') return content ? [{ type: 'text', text: content }] : []
  return content ?? []
}

function streamingAssistant(s: ChatState): ChatMessage | null {
  const last = s.messages[s.messages.length - 1]
  return last?.role === 'assistant' && last.stopReason === 'pending' ? last : null
}

function applyEvent(s: ChatState, ev: any) {
  switch (ev.type) {
    case 'agent_start':
      s.busy = true
      break
    case 'agent_settled':
      s.busy = false
      s.retry = null
      break
    case 'message_start':
      if (ev.message?.role === 'assistant') {
        s.messages.push({ ...ev.message, content: [], stopReason: 'pending' })
      }
      break
    case 'message_update':
      applyDelta(s, ev.assistantMessageEvent)
      break
    case 'message_end': {
      const m = ev.message as ChatMessage
      if (m?.role === 'assistant' && streamingAssistant(s)) {
        s.messages[s.messages.length - 1] = m
      }
      else if (m) {
        addMessage(s, m)
      }
      break
    }
    case 'tool_execution_start':
      s.results[ev.toolCallId] = { content: [], isError: false, running: true }
      break
    case 'tool_execution_update':
      s.results[ev.toolCallId] = {
        content: blocks(ev.partialResult?.content),
        isError: false,
        running: true,
      }
      break
    case 'tool_execution_end':
      s.results[ev.toolCallId] = {
        content: blocks(ev.result?.content),
        isError: !!ev.isError,
        running: false,
      }
      break
    case 'auto_retry_start':
      s.retry = `retrying (${ev.attempt}/${ev.maxAttempts}): ${ev.errorMessage ?? ''}`
      break
    case 'auto_retry_end':
      s.retry = ev.success ? null : `gave up: ${ev.finalError ?? ''}`
      break
  }
}

function applyDelta(s: ChatState, d: any) {
  const m = streamingAssistant(s)
  if (!m || !d) return
  const content = m.content as Block[]
  const i = d.contentIndex as number
  switch (d.type) {
    case 'text_start':
      content[i] = { type: 'text', text: '' }
      break
    case 'text_delta':
      if (content[i]?.type === 'text') (content[i] as { text: string }).text += d.delta
      else content[i] = { type: 'text', text: d.delta }
      break
    case 'text_end':
      content[i] = { type: 'text', text: d.content }
      break
    case 'thinking_start':
      content[i] = { type: 'thinking', thinking: '' }
      break
    case 'thinking_delta':
      if (content[i]?.type === 'thinking') (content[i] as { thinking: string }).thinking += d.delta
      else content[i] = { type: 'thinking', thinking: d.delta }
      break
    case 'thinking_end':
      content[i] = { type: 'thinking', thinking: d.content }
      break
    case 'toolcall_start':
      content[i] = { type: 'toolCall', id: d.id, name: d.toolName, partialArgs: '' }
      break
    case 'toolcall_delta': {
      const b = content[i]
      if (b?.type === 'toolCall') b.partialArgs = (b.partialArgs ?? '') + d.delta
      break
    }
    case 'toolcall_end':
      content[i] = d.toolCall
      break
  }
}

export function userText(m: ChatMessage): string {
  return blocks(m.content).filter(b => b.type === 'text').map(b => (b as { text: string }).text).join('\n')
}

export function userImages(m: ChatMessage) {
  return blocks(m.content).filter(b => b.type === 'image') as { type: 'image', data: string, mimeType: string }[]
}

// toolSummary is the one line a collapsed tool call shows.
export function toolSummary(b: Extract<Block, { type: 'toolCall' }>): string {
  const a = b.arguments ?? {}
  const pick = a.command ?? a.path ?? a.file_path ?? a.pattern ?? a.query
  if (typeof pick === 'string') return pick
  if (b.partialArgs) return b.partialArgs.slice(0, 120)
  const json = JSON.stringify(a)
  return json === '{}' ? '' : json.slice(0, 120)
}
