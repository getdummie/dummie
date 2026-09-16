<script setup lang="ts">
import { ArrowLeft } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
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
const host = ref('')
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

const quickKeys = [
  { label: 'Tab', data: '\t' },
  { label: 'Ctrl+C', data: '\x03' },
]

const pasteError = ref<string | null>(null)
let pasteErrorTimer: ReturnType<typeof setTimeout> | undefined

function showPasteError(msg: string) {
  pasteError.value = msg
  clearTimeout(pasteErrorTimer)
  pasteErrorTimer = setTimeout(() => (pasteError.value = null), 4000)
}

function sendKeys(data: string) {
  terminal.value?.send(data)
  terminal.value?.focus()
}

async function paste() {
  try {
    const text = await navigator.clipboard.readText()
    if (text) terminal.value?.send(text)
    terminal.value?.focus()
  }
  catch {
    showPasteError('The browser would not hand over the clipboard. Long-press the terminal to paste instead.')
  }
}

onBeforeUnmount(() => clearTimeout(pasteErrorTimer))
</script>

<template>
  <div class="flex h-dvh flex-col bg-background text-foreground">
    <header class="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-border px-4 py-3">
      <NuxtLink
        :to="`/vms/${id}`"
        class="inline-flex items-center gap-1.5 font-mono text-xs text-muted-foreground underline-offset-4 transition-colors hover:text-foreground hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
      >
        <ArrowLeft class="size-3.5" aria-hidden="true" />
        Back to VM
      </NuxtLink>

      <h1 class="font-mono text-sm font-semibold">
        {{ vm?.name || vm?.vm_id || 'console' }}
      </h1>

      <span v-if="host" class="font-mono text-xs text-muted-foreground">{{ host }}</span>

      <div class="ml-auto flex items-center gap-2">
        <Badge :variant="phase === 'open' ? 'default' : 'secondary'" class="font-mono text-xs">
          {{ statusLabel }}
        </Badge>
        <Button
          v-if="phase === 'open'"
          variant="outline"
          size="sm"
          class="font-mono text-xs"
          @click="terminal?.disconnect()"
        >
          Disconnect
        </Button>
        <Button
          v-else-if="phase === 'closed' || phase === 'error'"
          variant="outline"
          size="sm"
          class="font-mono text-xs"
          :disabled="!vm?.console_url"
          @click="terminal?.connect()"
        >
          Reconnect
        </Button>
      </div>
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
      @host="host = $event"
    />

    <div
      class="shrink-0 border-t border-border bg-background pb-[env(safe-area-inset-bottom)] sm:hidden"
    >
      <p v-if="pasteError" class="px-3 pt-2 font-mono text-xs text-muted-foreground">
        {{ pasteError }}
      </p>
      <div class="flex items-center gap-2 px-3 py-2">
        <Button
          v-for="key in quickKeys"
          :key="key.label"
          variant="outline"
          size="sm"
          class="flex-1 font-mono text-xs"
          :disabled="phase !== 'open'"
          @mousedown.prevent
          @click="sendKeys(key.data)"
        >
          {{ key.label }}
        </Button>
        <Button
          variant="outline"
          size="sm"
          class="flex-1 font-mono text-xs"
          :disabled="phase !== 'open'"
          @mousedown.prevent
          @click="paste()"
        >
          Paste
        </Button>
      </div>
    </div>
  </div>
</template>
