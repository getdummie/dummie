<script setup lang="ts">
import { ArrowLeft, Maximize } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import VmDesktop, { type Phase } from '@/components/VmDesktop.vue'

definePageMeta({ middleware: ['auth'], layout: false })

const route = useRoute()
const id = computed(() => String(route.params.id))

const vm = ref<{ name: string, vm_id: string } | null>(null)
const phase = ref<Phase>('loading')
const message = ref<string | null>(null)
const desktop = useTemplateRef<InstanceType<typeof VmDesktop>>('desktop')

useHead(() => ({
  title: vm.value ? `dummie — desktop · ${vm.value.name || vm.value.vm_id}` : 'dummie — desktop',
}))

const connection = computed(() => {
  switch (phase.value) {
    case 'open':
      return {
        dot: 'bg-emerald-500',
        label: 'Disconnect',
        button: 'border-destructive/40 text-destructive hover:bg-destructive/10 hover:text-destructive',
        action: () => desktop.value?.disconnect(),
      }
    case 'closed':
    case 'error':
      return {
        dot: 'bg-destructive',
        label: 'Reconnect',
        button: 'border-emerald-500/40 text-emerald-600 hover:bg-emerald-500/10 hover:text-emerald-600 dark:text-emerald-400 dark:hover:text-emerald-400',
        action: () => desktop.value?.connect(),
      }
    default:
      return { dot: 'bg-amber-500', label: phase.value, button: '', action: null }
  }
})
</script>

<template>
  <div class="flex h-svh flex-col overscroll-none bg-background text-foreground">
    <header class="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
      <NuxtLink
        :to="`/vms/${id}`"
        class="inline-flex shrink-0 items-center gap-1.5 font-mono text-xs text-muted-foreground underline-offset-4 transition-colors hover:text-foreground hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring max-sm:-m-2 max-sm:p-2"
      >
        <ArrowLeft class="size-3.5" aria-hidden="true" />
        <span class="max-sm:sr-only">Back to VM</span>
      </NuxtLink>

      <div class="flex min-w-0 flex-1 items-center gap-2">
        <span class="size-1.5 shrink-0 rounded-full" :class="connection.dot" aria-hidden="true" />
        <h1 class="min-w-0 truncate font-mono text-sm font-semibold">
          {{ vm?.name || vm?.vm_id || 'desktop' }}
        </h1>
        <span class="sr-only">Desktop {{ phase }}</span>
      </div>

      <div class="flex items-center gap-1">
        <template v-if="phase === 'open'">
          <Button
            variant="outline"
            size="sm"
            class="font-mono text-xs max-sm:hidden"
            title="Send the Super key to the desktop"
            @click="desktop?.superKey()"
          >
            Super
          </Button>
          <Button variant="outline" size="sm" class="font-mono text-xs max-sm:hidden" @click="desktop?.ctrlAltDel()">
            Ctrl+Alt+Del
          </Button>
          <Button
            variant="outline"
            size="sm"
            class="font-mono text-xs"
            title="Fullscreen; in Chromium, system keys like Super and Alt+Tab go to the desktop. Hold Esc to exit."
            @click="desktop?.fullscreen()"
          >
            <Maximize class="size-3.5" aria-hidden="true" />
            <span class="max-sm:sr-only">Fullscreen</span>
          </Button>
        </template>
        <Button
          variant="outline"
          size="sm"
          class="font-mono text-xs"
          :class="connection.button"
          :disabled="!connection.action"
          @click="connection.action?.()"
        >
          {{ connection.label }}
        </Button>
      </div>
    </header>

    <Alert v-if="message" :variant="phase === 'error' ? 'destructive' : 'default'" class="rounded-none border-x-0">
      <AlertTitle>{{ phase === 'error' ? 'Desktop unavailable' : 'Disconnected' }}</AlertTitle>
      <AlertDescription>{{ message }}</AlertDescription>
    </Alert>

    <VmDesktop
      ref="desktop"
      :vm-id="id"
      @phase="phase = $event"
      @message="message = $event"
      @vm="vm = $event"
    />
  </div>
</template>
