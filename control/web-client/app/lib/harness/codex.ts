// codex app-server: a thread is turns of items, and each item becomes one
// chat message. Shapes follow codex's app-server v2 protocol.
import { dataUrlImage, emptyChat, settle, textBlocks, upsert, type Block, type ChatAdapter, type ChatMessage, type ChatState } from '@/lib/agentChat'

type Item = { id: string, type: string, [k: string]: any }

function setItem(s: ChatState, item: Item, live: boolean) {
  const m = toMessage(s, item, live)
  if (m) upsert(s, item.id, m)
}

function toMessage(s: ChatState, item: Item, live: boolean): ChatMessage | null {
  const pending = live ? 'pending' : 'stop'
  switch (item.type) {
    case 'userMessage': {
      const content: Block[] = []
      for (const c of item.content ?? []) {
        if (c.type === 'text') content.push({ type: 'text', text: c.text })
        const img = c.type === 'image' && typeof c.url === 'string' ? dataUrlImage(c.url) : null
        if (img) content.push(img)
      }
      return { id: item.id, role: 'user', content }
    }
    case 'agentMessage':
      return { id: item.id, role: 'assistant', content: textBlocks(item.text), stopReason: pending }
    case 'reasoning': {
      const text = (item.summary?.length ? item.summary : item.content ?? []).join('\n\n')
      return { id: item.id, role: 'assistant', content: [{ type: 'thinking', thinking: text }], stopReason: pending }
    }
  }
  const t = toolOf(item)
  if (!t) return null
  const running = item.status === 'inProgress'
  s.results[item.id] = { content: textBlocks(t.output), isError: !running && t.failed, running }
  return { id: item.id, role: 'assistant', content: [{ type: 'toolCall', id: item.id, name: t.name, arguments: t.args }], stopReason: 'stop' }
}

function toolOf(item: Item): { name: string, args: Record<string, unknown>, output: string, failed: boolean } | null {
  switch (item.type) {
    case 'commandExecution':
      return {
        name: 'bash',
        args: { command: Array.isArray(item.command) ? item.command.join(' ') : item.command },
        output: item.aggregatedOutput ?? '',
        failed: item.status === 'failed' || (item.exitCode ?? 0) !== 0,
      }
    case 'fileChange': {
      const changes = (item.changes ?? []) as { path: string, diff?: string }[]
      return {
        name: 'edit',
        args: { path: changes.map(c => c.path).join(', ') },
        output: changes.map(c => c.diff ?? '').join('\n'),
        failed: item.status === 'failed',
      }
    }
    case 'mcpToolCall':
      return {
        name: `${item.server}.${item.tool}`,
        args: item.arguments ?? {},
        output: item.error?.message ?? (item.result?.content ?? []).map((c: any) => c.text ?? '').join('\n'),
        failed: !!item.error,
      }
    case 'webSearch':
      return { name: 'web_search', args: { query: item.query }, output: '', failed: false }
  }
  return null
}

function appendText(s: ChatState, id: string, delta: string, kind: 'text' | 'thinking') {
  const at = s.refs[id]
  const m = at === undefined ? null : s.messages[at]
  const b = m && Array.isArray(m.content) ? m.content[0] : undefined
  if (b?.type === 'text' && kind === 'text') b.text += delta
  else if (b?.type === 'thinking' && kind === 'thinking') b.thinking += delta
}

function applyEvent(s: ChatState, ev: any) {
  const p = ev.params ?? {}
  switch (ev.method) {
    case 'turn/started':
      s.busy = true
      s.retry = null
      break
    case 'turn/completed': {
      s.busy = false
      s.retry = null
      settle(s)
      const t = p.turn ?? {}
      if (t.status === 'failed') s.messages.push({ role: 'assistant', content: [], stopReason: 'error', errorMessage: t.error?.message })
      if (t.status === 'interrupted') s.messages.push({ role: 'assistant', content: [], stopReason: 'aborted' })
      break
    }
    case 'item/started':
    case 'item/completed':
      if (p.item) setItem(s, p.item, ev.method === 'item/started')
      break
    case 'item/agentMessage/delta':
      appendText(s, p.itemId, p.delta ?? '', 'text')
      break
    case 'item/reasoning/summaryTextDelta':
    case 'item/reasoning/textDelta':
      appendText(s, p.itemId, p.delta ?? '', 'thinking')
      break
    case 'item/commandExecution/outputDelta': {
      const r = s.results[p.itemId]
      const b = r?.content[0]
      if (r && b?.type === 'text') b.text += p.delta ?? ''
      else if (r) r.content = textBlocks(p.delta)
      break
    }
    case 'error':
      if (p.willRetry) s.retry = `retrying: ${p.error?.message ?? ''}`
      else s.messages.push({ role: 'assistant', content: [], stopReason: 'error', errorMessage: p.error?.message })
      break
  }
}

export const codexChat: ChatAdapter = {
  fromHistory(turns: { items?: Item[] }[], streaming: boolean) {
    const s = emptyChat()
    for (const t of turns ?? []) for (const item of t.items ?? []) setItem(s, item, false)
    s.busy = streaming
    return s
  },
  applyEvent,
}
