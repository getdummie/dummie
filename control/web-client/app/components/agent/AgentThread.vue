<script setup lang="ts">
import { ChevronRight, LoaderCircle, TriangleAlert } from '@lucide/vue'
import AgentMarkdown from '@/components/agent/AgentMarkdown.vue'
import { blocks, toolSummary, userImages, userText, type ChatState } from '@/lib/agentChat'

const props = defineProps<{ chat: ChatState }>()

const scroller = useTemplateRef<HTMLElement>('scroller')
const pinned = ref(true)

function onScroll() {
  const el = scroller.value
  if (!el) return
  pinned.value = el.scrollHeight - el.scrollTop - el.clientHeight < 80
  updateCurrent()
}

// The rail lists every prompt; the current one is the last that has scrolled
// to (or past) the top third of the view.
const turns = computed(() => props.chat.messages
  .map((m, i) => ({ i, m }))
  .filter(({ m }) => m.role === 'user')
  .map(({ i, m }) => ({ i, label: railLabel(userText(m)) })))
const current = ref(-1)

function railLabel(text: string) {
  const line = text.trim().split('\n')[0] || 'image'
  return line.length > 32 ? `${line.slice(0, 32)}…` : line
}

function turnEl(i: number) {
  return scroller.value?.querySelector<HTMLElement>(`[data-msg="${i}"]`) ?? null
}

function updateCurrent() {
  const el = scroller.value
  if (!el) return
  const mark = el.getBoundingClientRect().top + el.clientHeight / 3
  let at = turns.value[0]?.i ?? -1
  for (const t of turns.value) {
    const top = turnEl(t.i)?.getBoundingClientRect().top
    if (top !== undefined && top <= mark) at = t.i
  }
  current.value = at
}

function jump(i: number) {
  turnEl(i)?.scrollIntoView({ block: 'start', behavior: 'smooth' })
}

// Follow the stream only while the reader is at the bottom.
watch(() => props.chat, async () => {
  await nextTick()
  if (pinned.value) scroller.value?.scrollTo({ top: scroller.value.scrollHeight })
  updateCurrent()
}, { deep: true })

function resultText(id: string): string {
  const r = props.chat.results[id]
  if (!r) return ''
  return r.content.filter(b => b.type === 'text').map(b => (b as { text: string }).text).join('\n')
}

function argsJSON(b: { arguments?: Record<string, unknown>, partialArgs?: string }): string {
  return b.arguments ? JSON.stringify(b.arguments, null, 2) : (b.partialArgs ?? '')
}
</script>

<template>
  <div class="relative flex min-h-0 flex-1 flex-col">
    <div ref="scroller" class="min-h-0 flex-1 overflow-y-auto" @scroll.passive="onScroll">
      <ol class="mx-auto flex max-w-3xl flex-col gap-5 px-4 py-6">
        <li v-for="(m, i) in chat.messages" :key="i" :data-msg="i" class="scroll-mt-4">
          <div v-if="m.role === 'user'" class="ml-auto w-fit max-w-[85%] rounded-lg bg-muted px-3 py-2 text-sm">
            <div v-if="userImages(m).length" class="mb-2 flex flex-wrap gap-2">
              <img
                v-for="(img, j) in userImages(m)"
                :key="j"
                :src="`data:${img.mimeType};base64,${img.data}`"
                alt="attached image"
                class="max-h-40 rounded border border-border"
              >
            </div>
            <p class="whitespace-pre-wrap break-words">{{ userText(m) }}</p>
          </div>

          <div v-else-if="m.role === 'assistant'" class="flex flex-col gap-3 text-sm">
            <template v-for="(b, j) in blocks(m.content)" :key="j">
              <AgentMarkdown v-if="b.type === 'text'" :source="b.text" :streaming="m.stopReason === 'pending'" />

              <details v-else-if="b.type === 'thinking' && b.thinking" class="group text-muted-foreground">
                <summary class="flex cursor-pointer list-none items-center gap-1 font-mono text-xs">
                  <ChevronRight class="size-3.5 transition-transform group-open:rotate-90" aria-hidden="true" />
                  thinking
                </summary>
                <p class="mt-2 whitespace-pre-wrap border-l border-border pl-3 text-xs">{{ b.thinking }}</p>
              </details>

              <details v-else-if="b.type === 'toolCall'" class="group rounded-md border border-border">
                <summary class="flex cursor-pointer list-none items-center gap-2 px-3 py-2 font-mono text-xs">
                  <ChevronRight class="size-3.5 shrink-0 transition-transform group-open:rotate-90" aria-hidden="true" />
                  <span class="font-semibold">{{ b.name }}</span>
                  <span class="truncate text-muted-foreground">{{ toolSummary(b) }}</span>
                  <LoaderCircle
                    v-if="chat.results[b.id]?.running || (!chat.results[b.id] && m.stopReason === 'pending')"
                    class="ml-auto size-3.5 shrink-0 animate-spin text-muted-foreground"
                    aria-label="running"
                  />
                  <TriangleAlert
                    v-else-if="chat.results[b.id]?.isError"
                    class="ml-auto size-3.5 shrink-0 text-destructive"
                    aria-label="failed"
                  />
                </summary>
                <div class="border-t border-border">
                  <pre class="max-h-48 overflow-auto px-3 py-2 font-mono text-xs text-muted-foreground">{{ argsJSON(b) }}</pre>
                  <pre
                    v-if="resultText(b.id)"
                    class="max-h-80 overflow-auto border-t border-border px-3 py-2 font-mono text-xs"
                    :class="chat.results[b.id]?.isError && 'text-destructive'"
                  >{{ resultText(b.id) }}</pre>
                </div>
              </details>
            </template>

            <p v-if="m.stopReason === 'error' || m.stopReason === 'aborted'" class="font-mono text-xs text-destructive">
              {{ m.stopReason === 'aborted' ? 'stopped' : (m.errorMessage || 'the model returned an error') }}
            </p>
          </div>

          <div v-else-if="m.role === 'bashExecution'" class="rounded-md border border-border font-mono text-xs">
            <p class="border-b border-border px-3 py-2">$ {{ m.command }}</p>
            <pre class="max-h-80 overflow-auto px-3 py-2">{{ m.output }}</pre>
          </div>

          <div v-else-if="m.summary" class="border-l-2 border-border pl-3 text-xs text-muted-foreground">
            <p class="eyebrow mb-1">{{ m.role === 'compactionSummary' ? 'compacted' : 'summary' }}</p>
            <AgentMarkdown :source="m.summary" />
          </div>
        </li>

        <li v-if="chat.retry" class="font-mono text-xs text-muted-foreground" role="status">{{ chat.retry }}</li>
        <li v-if="chat.busy" class="flex items-center gap-2 font-mono text-xs text-muted-foreground" role="status">
          <LoaderCircle class="size-3.5 animate-spin" aria-hidden="true" />
          working
        </li>
      </ol>
    </div>

    <ol
      v-if="turns.length > 1"
      class="agent-rail absolute top-1/2 right-2 hidden max-h-[70%] -translate-y-1/2 flex-col overflow-y-auto md:flex"
      aria-label="Jump to a prompt"
    >
      <li v-for="t in turns" :key="t.i">
        <button
          type="button"
          class="flex w-full items-center justify-end gap-2 py-[3px] font-mono text-[11px] tracking-wide text-muted-foreground focus-visible:outline-none"
          :class="t.i === current && 'on text-foreground'"
          :aria-current="t.i === current ? 'true' : undefined"
          @click="jump(t.i)"
        >
          <span class="label">{{ t.label }}</span>
          <i class="tick" />
        </button>
      </li>
    </ol>
  </div>
</template>

<style>
.agent-rail .label { opacity: 0; transform: translateX(6px); transition: opacity .2s, transform .2s; white-space: nowrap; background: var(--background); padding: 0 .25rem; border-radius: 3px; pointer-events: none; }
.agent-rail .tick { width: 8px; height: 2px; background: var(--border); transition: width .2s, background .2s; flex-shrink: 0; }
.agent-rail:hover .label, .agent-rail:focus-within .label { opacity: 1; transform: none; }
.agent-rail button:hover .tick { width: 12px; background: var(--muted-foreground); }
.agent-rail .on .tick { width: 14px; background: var(--primary); }
.agent-rail button:hover { color: var(--foreground); }
</style>
