import { piChat, type ChatAdapter } from '@/lib/agentChat'
import { claudeChat } from '@/lib/harness/claude'
import { codexChat } from '@/lib/harness/codex'
import { geminiChat } from '@/lib/harness/gemini'
import { opencodeChat } from '@/lib/harness/opencode'

const adapters: Record<string, ChatAdapter> = {
  pi: piChat,
  claude: claudeChat,
  codex: codexChat,
  gemini: geminiChat,
  opencode: opencodeChat,
}

export function chatAdapter(harness: string): ChatAdapter {
  return adapters[harness] ?? piChat
}
