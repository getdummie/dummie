<script setup lang="ts">
import { Button } from '@/components/ui/button'
import VmTerminal, { type Phase } from '@/components/VmTerminal.vue'
import VmTerminalKeypad from '@/components/VmTerminalKeypad.vue'

defineProps<{ vmId: string }>()
const ws = useInjectedAgentWorkspace()

const phase = ref<Phase>('loading')
const message = ref<string | null>(null)
const terminal = useTemplateRef<InstanceType<typeof VmTerminal>>('terminal')
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col">
    <div class="flex items-center gap-2 border-b border-border px-3 py-1.5">
      <span
        class="size-1.5 rounded-full"
        :class="phase === 'open' ? 'bg-emerald-500' : phase === 'closed' || phase === 'error' ? 'bg-destructive' : 'bg-amber-500'"
        aria-hidden="true"
      />
      <span class="font-mono text-xs text-muted-foreground">{{ phase }}</span>
      <Button v-if="phase === 'open'" variant="outline" size="sm" class="ml-auto h-6 font-mono text-xs" @click="terminal?.disconnect()">
        Disconnect
      </Button>
      <Button
        v-else-if="phase === 'closed' || phase === 'error'"
        variant="outline"
        size="sm"
        class="ml-auto h-6 font-mono text-xs"
        @click="terminal?.connect()"
      >
        Reconnect
      </Button>
    </div>
    <p
      v-if="message"
      class="border-b border-border bg-muted/40 px-3 py-2 font-mono text-xs"
      :class="phase === 'error' ? 'text-destructive' : 'text-muted-foreground'"
      role="status"
    >
      {{ message }}
    </p>
    <VmTerminal
      ref="terminal"
      :vm-id="vmId"
      :cwd="ws.activeCwd || ws.newCwd.trim() || undefined"
      @phase="phase = $event"
      @message="message = $event"
    />
    <VmTerminalKeypad
      class="shrink-0 border-t border-border pb-[env(safe-area-inset-bottom)] sm:hidden"
      :terminal="terminal"
      :disabled="phase !== 'open'"
    />
  </div>
</template>
