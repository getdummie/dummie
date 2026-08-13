<script setup lang="ts">
import { ArrowLeft } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import VmTerminal from '@/components/VmTerminal.vue'

// Its own route rather than a child of /vms/[id], which would turn that page
// into a layout: a terminal wants the whole viewport, and it is opened in a new
// tab from the VM page anyway.
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

      <!-- The hostname the shell is actually behind, so it is visible even
           though the page itself is served from the control server. -->
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
  </div>
</template>
