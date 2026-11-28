<script setup lang="ts">
import { Maximize } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import VmDesktop, { type Phase } from '@/components/VmDesktop.vue'

defineProps<{ vmId: string }>()

const phase = ref<Phase>('loading')
const message = ref<string | null>(null)
const desktop = useTemplateRef<InstanceType<typeof VmDesktop>>('desktop')
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
      <div class="ml-auto flex items-center gap-1">
        <template v-if="phase === 'open'">
          <Button variant="outline" size="sm" class="h-6 font-mono text-xs" @click="desktop?.superKey()">
            Super
          </Button>
          <Button variant="outline" size="sm" class="h-6 font-mono text-xs" @click="desktop?.ctrlAltDel()">
            Ctrl+Alt+Del
          </Button>
          <Button variant="outline" size="icon-xs" aria-label="Fullscreen" @click="desktop?.fullscreen()">
            <Maximize aria-hidden="true" />
          </Button>
          <Button variant="outline" size="sm" class="h-6 font-mono text-xs" @click="desktop?.disconnect()">
            Disconnect
          </Button>
        </template>
        <Button
          v-else-if="phase === 'closed' || phase === 'error'"
          variant="outline"
          size="sm"
          class="h-6 font-mono text-xs"
          @click="desktop?.connect()"
        >
          Reconnect
        </Button>
      </div>
    </div>
    <p
      v-if="message"
      class="border-b border-border bg-muted/40 px-3 py-2 font-mono text-xs"
      :class="phase === 'error' ? 'text-destructive' : 'text-muted-foreground'"
      role="status"
    >
      {{ message }}
    </p>
    <VmDesktop
      ref="desktop"
      :vm-id="vmId"
      @phase="phase = $event"
      @message="message = $event"
    />
  </div>
</template>
