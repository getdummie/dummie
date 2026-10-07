<script setup lang="ts">
import { Check, CircleCheck, CircleDot, Folder, LoaderCircle, Pencil, Plus, X } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import AgentHarnessIcon from '@/components/agent/AgentHarnessIcon.vue'
import type { AgentSession } from '@/composables/useAgentSocket'

const props = defineProps<{
  sessions: AgentSession[]
  active: string | null
  home?: string
  // Harnesses that can rename a session, and how to do it.
  renamable: string[]
  rename: (s: AgentSession, name: string) => Promise<void>
}>()
const emit = defineEmits<{ open: [session: AgentSession], new: [] }>()

// Grouped the way paseo does: what is working, what is waiting on you, the rest.
const groups = computed(() => [
  { key: 'working', label: 'Working', icon: LoaderCircle, iconClass: 'animate-spin text-primary-text', items: props.sessions.filter(s => s.streaming) },
  { key: 'open', label: 'Open', icon: CircleDot, iconClass: 'text-primary-text', items: props.sessions.filter(s => s.live && !s.streaming) },
  { key: 'done', label: 'Done', icon: CircleCheck, iconClass: 'text-muted-foreground', items: props.sessions.filter(s => !s.live) },
].filter(g => g.items.length))

function label(s: AgentSession) {
  return s.name || s.title || 'new session'
}

const editing = ref<string | null>(null)
const draft = ref('')
const saving = ref(false)
const renameError = ref<string | null>(null)

async function startRename(s: AgentSession) {
  editing.value = s.key
  draft.value = s.name || s.title || ''
  renameError.value = null
  await nextTick()
  const el = document.getElementById(`rename-${s.key}`) as HTMLInputElement | null
  el?.focus()
  el?.select()
}

async function saveRename(s: AgentSession) {
  const name = draft.value.trim()
  if (!name || saving.value) return
  saving.value = true
  renameError.value = null
  try {
    await props.rename(s, name)
    editing.value = null
  }
  catch (e) {
    renameError.value = e instanceof Error ? e.message : 'Could not rename the session'
  }
  finally {
    saving.value = false
  }
}

function shortCwd(cwd: string) {
  return props.home && cwd.startsWith(props.home) ? `~${cwd.slice(props.home.length)}` : cwd
}

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto', style: 'narrow' })
function ago(ms: number) {
  const s = Math.round((ms - Date.now()) / 1000)
  if (s > -60) return 'now'
  if (s > -3600) return rtf.format(Math.round(s / 60), 'minute')
  if (s > -86400) return rtf.format(Math.round(s / 3600), 'hour')
  return rtf.format(Math.round(s / 86400), 'day')
}
</script>

<template>
  <nav class="flex size-full min-h-0 flex-col" aria-label="agent sessions">
    <header class="flex items-center gap-2 border-b border-border px-3 py-2">
      <h2 class="eyebrow text-muted-foreground">sessions</h2>
      <Button variant="outline" size="sm" class="ml-auto h-7 font-mono text-xs" @click="emit('new')">
        <Plus class="size-3.5" aria-hidden="true" />
        New
      </Button>
    </header>
    <p v-if="!sessions.length" class="p-3 font-mono text-xs text-muted-foreground">No sessions yet.</p>

    <div class="min-h-0 flex-1 overflow-y-auto px-1.5 pb-2">
      <section v-for="g in groups" :key="g.key" class="mt-3">
        <h3 class="flex items-center gap-2 px-2.5 pb-1.5 text-xs text-muted-foreground">
          <component :is="g.icon" class="size-3.5" :class="g.iconClass" aria-hidden="true" />
          {{ g.label }}
        </h3>
        <ul>
          <li v-for="s in g.items" :key="s.key" class="group relative">
            <form
              v-if="editing === s.key"
              class="rounded-md bg-muted px-2.5 py-2"
              @submit.prevent="saveRename(s)"
              @keydown.esc="editing = null"
            >
              <div class="flex items-center gap-1">
                <Input
                  :id="`rename-${s.key}`"
                  v-model="draft"
                  class="h-7 min-w-0 font-mono text-xs"
                  maxlength="120"
                  aria-label="session name"
                  :aria-invalid="!!renameError"
                  :disabled="saving"
                />
                <Button type="submit" variant="ghost" size="icon" class="size-7 shrink-0" :disabled="saving || !draft.trim()" aria-label="save name">
                  <Check class="size-3.5" aria-hidden="true" />
                </Button>
                <Button type="button" variant="ghost" size="icon" class="size-7 shrink-0" aria-label="cancel renaming" @click="editing = null">
                  <X class="size-3.5" aria-hidden="true" />
                </Button>
              </div>
              <p v-if="renameError" class="mt-1.5 font-mono text-[11px] text-destructive" role="alert">{{ renameError }}</p>
            </form>
            <button
              v-else
              type="button"
              class="flex w-full items-start gap-2.5 rounded-md px-2.5 py-2 text-left transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-ring"
              :class="s.key === active && 'bg-muted'"
              :aria-current="s.key === active ? 'page' : undefined"
              @click="emit('open', s)"
            >
              <span class="relative mt-[3px]">
                <AgentHarnessIcon :harness="s.harness" />
                <span
                  v-if="s.streaming"
                  class="absolute -bottom-0.5 -right-0.5 size-1.5 rounded-full bg-primary ring-2 ring-background"
                  aria-hidden="true"
                />
              </span>
              <span class="flex min-w-0 flex-1 flex-col gap-0.5">
                <span class="flex items-baseline gap-2">
                  <span class="truncate text-sm">{{ label(s) }}</span>
                  <span class="ml-auto shrink-0 font-mono text-[11px] text-muted-foreground">{{ ago(s.updated) }}</span>
                </span>
                <span class="flex items-center gap-1.5 pr-6 font-mono text-[11px] text-muted-foreground">
                  <span class="shrink-0">{{ s.harness }}</span>
                  <span aria-hidden="true">·</span>
                  <Folder class="size-3 shrink-0" aria-hidden="true" />
                  <span class="truncate">{{ shortCwd(s.cwd) }}</span>
                </span>
              </span>
            </button>
            <Button
              v-if="editing !== s.key && renamable.includes(s.harness)"
              variant="ghost"
              size="icon"
              class="absolute right-1.5 bottom-1 size-6 text-muted-foreground lg:opacity-0 lg:group-hover:opacity-100 lg:focus-visible:opacity-100"
              :aria-label="`rename ${label(s)}`"
              @click="startRename(s)"
            >
              <Pencil class="size-3" aria-hidden="true" />
            </Button>
          </li>
        </ul>
      </section>
    </div>
  </nav>
</template>
