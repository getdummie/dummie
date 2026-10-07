<script setup lang="ts">
import { ArrowLeft, Network } from '@lucide/vue'
import ThemeToggle from '@/components/ThemeToggle.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import VmNetworkPanel from '@/components/VmNetworkPanel.vue'
import VmTerminal from '@/components/VmTerminal.vue'
import VmTerminalKeypad from '@/components/VmTerminalKeypad.vue'
import { deniedCovered } from '@/lib/targets'

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
const { preference: themePreference, isDark, setPreference } = useConsoleTheme()
const network = reactive(useVmNetwork(id))
const networkOpen = ref(false)
const networkProps = computed(() => ({
  targets: network.targets,
  denied: network.denied,
  deniedAvailable: network.deniedAvailable,
  loaded: network.loaded,
  allowing: network.allowing,
  allowError: network.allowError,
  refreshing: network.refreshing,
}))
const deniedCount = computed(() => network.denied.filter(d => !deniedCovered(d, network.targets)).length)

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

// The keypad is fixed to the viewport, so the terminal needs a matching
// reservation below it — its height changes with the paste error line.
const keypad = useTemplateRef<HTMLElement>('keypad')
const keypadHeight = ref(0)
let keypadObserver: ResizeObserver | null = null

onMounted(() => {
  if (!keypad.value) return
  keypadObserver = new ResizeObserver(([entry]) => {
    keypadHeight.value = entry?.target.getBoundingClientRect().height ?? 0
  })
  keypadObserver.observe(keypad.value)
})

onBeforeUnmount(() => {
  keypadObserver?.disconnect()
  keypadObserver = null
})
</script>

<template>
  <!-- svh, not dvh: dvh tracks the browser chrome collapsing on scroll, which
       resizes the pty and makes tmux redraw. svh stays put. -->
  <div class="flex h-svh flex-col overscroll-none bg-background text-foreground">
    <header class="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-center gap-3 border-b border-border px-4 py-3">
      <div class="flex min-w-0 items-center gap-4">
        <NuxtLink
          :to="`/vms/${id}`"
          class="inline-flex shrink-0 items-center gap-1.5 font-mono text-xs text-muted-foreground underline-offset-4 transition-colors hover:text-foreground hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring max-sm:-m-2 max-sm:p-2"
        >
          <ArrowLeft class="size-3.5" aria-hidden="true" />
          <span class="max-sm:sr-only">Back to VM</span>
        </NuxtLink>

        <div class="flex min-w-0 items-center gap-2">
          <span class="size-1.5 shrink-0 rounded-full" :class="connection.dot" aria-hidden="true" />
          <h1 class="min-w-0 truncate font-mono text-sm font-semibold">
            {{ vm?.name || vm?.vm_id || 'console' }}
          </h1>
          <span class="sr-only">Console {{ statusLabel }}</span>
        </div>
      </div>

      <NuxtLink to="/dashboard" class="flex shrink-0 items-center gap-2" aria-label="dummie dashboard">
        <img src="/logo.svg" alt="" aria-hidden="true" class="size-6">
        <span class="hidden font-mono text-sm font-semibold tracking-tight sm:inline">dummie<span class="text-primary-text">/</span></span>
      </NuxtLink>

      <div class="flex items-center justify-end gap-1">
        <Button
          variant="outline"
          size="sm"
          class="font-mono text-xs sm:hidden"
          aria-label="Show network activity"
          @click="networkOpen = true"
        >
          <Network class="size-3.5" aria-hidden="true" />
          <span v-if="deniedCount" class="tabular-nums text-destructive">{{ deniedCount }}</span>
        </Button>
        <Button
          variant="outline"
          size="sm"
          class="font-mono text-xs"
          :class="connection.button"
          :disabled="!connection.action || !vm?.console_url"
          @click="connection.action?.()"
        >
          {{ connection.label }}
        </Button>

        <ThemeToggle
          :preference="themePreference"
          :is-dark="isDark"
          @update:preference="setPreference"
        />
      </div>
    </header>

    <div class="flex min-h-0 flex-1">
      <div class="flex min-w-0 flex-1 flex-col">
        <Alert v-if="message" :variant="phase === 'error' ? 'destructive' : 'default'" class="rounded-none border-x-0">
          <AlertTitle>{{ phase === 'error' ? 'Console unavailable' : 'Disconnected' }}</AlertTitle>
          <AlertDescription>{{ message }}</AlertDescription>
        </Alert>

        <VmTerminal
          ref="terminal"
          :vm-id="id"
          :dark="isDark"
          @phase="phase = $event"
          @message="message = $event"
          @vm="vm = $event"
        />
      </div>

      <aside aria-label="Network activity" class="hidden w-72 shrink-0 flex-col border-l border-border sm:flex">
        <VmNetworkPanel v-bind="networkProps" @allow="network.allow" @refresh="network.refreshNow" />
      </aside>
    </div>

    <Sheet v-model:open="networkOpen">
      <SheetContent side="right" class="w-[85%] gap-0 p-0">
        <SheetHeader class="shrink-0 border-b border-border px-4 py-3">
          <SheetTitle class="font-mono text-sm">Network</SheetTitle>
          <SheetDescription class="sr-only">Denied and allowed destinations for this VM</SheetDescription>
        </SheetHeader>
        <VmNetworkPanel v-bind="networkProps" @allow="network.allow" @refresh="network.refreshNow" />
      </SheetContent>
    </Sheet>

    <div class="shrink-0 sm:hidden" :style="{ height: `${keypadHeight}px` }" aria-hidden="true" />

    <div
      ref="keypad"
      class="fixed inset-x-0 bottom-0 z-50 border-t border-border bg-background pb-[env(safe-area-inset-bottom)] sm:hidden"
    >
      <VmTerminalKeypad :terminal="terminal" :disabled="phase !== 'open'" />
    </div>
  </div>
</template>
