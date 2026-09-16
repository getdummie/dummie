<script setup lang="ts">
import { ArrowLeft } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import VmTerminal from '@/components/VmTerminal.vue'

definePageMeta({ middleware: ['auth'], layout: false })

interface VM {
  name: string
  vm_id: string
  status: string
  console_url: string
}

type Phase = 'loading' | 'connecting' | 'open' | 'closed' | 'error'

const route = useRoute()
const id = computed(() => String(route.params.id))

const vm = ref<VM | null>(null)
const phase = ref<Phase>('loading')
const message = ref<string | null>(null)
const terminal = useTemplateRef<InstanceType<typeof VmTerminal>>('terminal')

useHead(() => ({
  title: vm.value ? `dummie — console · ${vm.value.name || vm.value.vm_id}` : 'dummie — console',
}))

const statusLabel = computed(() => ({
  loading: 'loading',
  connecting: 'connecting',
  open: 'connected',
  closed: 'disconnected',
  error: 'error',
}[phase.value]))

const connection = computed(() => {
  switch (phase.value) {
    case 'open':
      return {
        dot: 'bg-emerald-500',
        label: 'Disconnect',
        button: 'border-destructive/40 text-destructive hover:bg-destructive/10 hover:text-destructive',
        action: () => terminal.value?.disconnect(),
      }
    case 'closed':
    case 'error':
      return {
        dot: 'bg-destructive',
        label: 'Reconnect',
        button: 'border-emerald-500/40 text-emerald-600 hover:bg-emerald-500/10 hover:text-emerald-600 dark:text-emerald-400 dark:hover:text-emerald-400',
        action: () => terminal.value?.connect(),
      }
    default:
      return { dot: 'bg-amber-500', label: statusLabel.value, button: '', action: null }
  }
})

const quickKeys = [
  { label: 'Tab', data: '\t' },
  { label: 'Ctrl+c', data: '\x03' },
  { label: 'Ctrl+r', data: '\x12' },
]

const pasteError = ref<string | null>(null)
let pasteErrorTimer: ReturnType<typeof setTimeout> | undefined

function showPasteError(msg: string) {
  pasteError.value = msg
  clearTimeout(pasteErrorTimer)
  pasteErrorTimer = setTimeout(() => (pasteError.value = null), 4000)
}

const modifiers = [
  { label: 'Ctrl', key: 'ctrlArmed' },
  { label: 'Shift', key: 'shiftArmed' },
] as const

function armed(key: 'ctrlArmed' | 'shiftArmed') {
  return terminal.value?.[key] ?? false
}

function toggleModifier(key: 'ctrlArmed' | 'shiftArmed') {
  const t = terminal.value
  if (!t) return
  t[key] = !t[key]
  t.focus()
}

const flashed = ref<string | null>(null)
let flashTimer: ReturnType<typeof setTimeout> | undefined

function flashClass(label: string) {
  // dark: variants too — the outline variant's own dark:bg-input/30 would
  // otherwise win over an unprefixed background.
  return flashed.value === label
    ? 'border-emerald-500 bg-emerald-500 text-white dark:border-emerald-500 dark:bg-emerald-500'
    : ''
}

function flash(label: string) {
  flashed.value = label
  clearTimeout(flashTimer)
  flashTimer = setTimeout(() => (flashed.value = null), 100)
}

function sendKeys(key: { label: string, data: string }) {
  terminal.value?.send(key.data)
  terminal.value?.focus()
  flash(key.label)
}

async function paste() {
  try {
    const text = await navigator.clipboard.readText()
    if (text) terminal.value?.send(text)
    terminal.value?.focus()
    flash('Paste')
  }
  catch {
    showPasteError('The browser would not hand over the clipboard. Long-press the terminal to paste instead.')
  }
}

onBeforeUnmount(() => {
  clearTimeout(pasteErrorTimer)
  clearTimeout(flashTimer)
})
</script>

<template>
  <!-- svh, not dvh: dvh tracks the browser chrome collapsing on scroll, which
       resizes the pty and makes tmux redraw. svh stays put. -->
  <div class="flex h-svh flex-col overscroll-none bg-background text-foreground">
    <header class="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-border px-4 py-3">
      <NuxtLink
        :to="`/vms/${id}`"
        class="inline-flex items-center gap-1.5 font-mono text-xs text-muted-foreground underline-offset-4 transition-colors hover:text-foreground hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring max-sm:-m-2 max-sm:p-2"
      >
        <ArrowLeft class="size-3.5" aria-hidden="true" />
        <span class="max-sm:sr-only">Back to VM</span>
      </NuxtLink>

      <div class="flex items-center gap-2">
        <span class="size-1.5 shrink-0 rounded-full" :class="connection.dot" aria-hidden="true" />
        <h1 class="font-mono text-sm font-semibold">
          {{ vm?.name || vm?.vm_id || 'console' }}
        </h1>
        <span class="sr-only">Console {{ statusLabel }}</span>
      </div>

      <Button
        variant="outline"
        size="sm"
        class="ml-auto font-mono text-xs"
        :class="connection.button"
        :disabled="!connection.action || !vm?.console_url"
        @click="connection.action?.()"
      >
        {{ connection.label }}
      </Button>
    </header>

    <Alert v-if="message" :variant="phase === 'error' ? 'destructive' : 'default'" class="rounded-none border-x-0">
      <AlertTitle>{{ phase === 'error' ? 'Console unavailable' : 'Disconnected' }}</AlertTitle>
      <AlertDescription>{{ message }}</AlertDescription>
    </Alert>

    <VmTerminal
      ref="terminal"
      :vm-id="id"
      @phase="phase = $event"
      @message="message = $event"
      @vm="vm = $event"
    />

    <div
      class="shrink-0 border-t border-border bg-background pb-[env(safe-area-inset-bottom)] sm:hidden"
    >
      <p v-if="pasteError" class="px-3 pt-2 font-mono text-xs text-muted-foreground">
        {{ pasteError }}
      </p>
      <div
        class="flex items-center gap-1.5 overflow-x-auto overscroll-x-contain px-3 py-2 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
      >
        <Button
          v-for="key in quickKeys"
          :key="key.label"
          variant="outline"
          size="sm"
          class="shrink-0 font-mono text-xs transition-colors"
          :class="flashClass(key.label)"
          :disabled="phase !== 'open'"
          @mousedown.prevent
          @click="sendKeys(key)"
        >
          {{ key.label }}
        </Button>
        <Button
          variant="outline"
          size="sm"
          class="shrink-0 font-mono text-xs transition-colors"
          :class="flashClass('Paste')"
          :disabled="phase !== 'open'"
          @mousedown.prevent
          @click="paste()"
        >
          Paste
        </Button>
        <Button
          v-for="mod in modifiers"
          :key="mod.key"
          :variant="armed(mod.key) ? 'default' : 'outline'"
          size="sm"
          class="shrink-0 font-mono text-xs"
          :disabled="phase !== 'open'"
          :aria-pressed="armed(mod.key)"
          @mousedown.prevent
          @click="toggleModifier(mod.key)"
        >
          {{ mod.label }}
        </Button>
      </div>
    </div>
  </div>
</template>
