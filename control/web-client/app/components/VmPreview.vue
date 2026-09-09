<script setup lang="ts">
import { ExternalLink, RotateCw } from '@lucide/vue'
import { useElementSize } from '@vueuse/core'
import { Button } from '@/components/ui/button'

const props = defineProps<{ url: string, name: string, live: boolean }>()

// The guest is rendered at a desktop width and scaled down, so a card shows the
// page the way a visitor would see it rather than its mobile layout.
const frameWidth = 1280
const frameHeight = 800

const box = useTemplateRef<HTMLDivElement>('box')
const { width } = useElementSize(box)
const scale = computed(() => (width.value ? width.value / frameWidth : 0))

const host = computed(() => {
  try {
    return new URL(props.url).hostname
  }
  catch {
    return ''
  }
})

const reloads = ref(0)
</script>

<template>
  <div class="border-b border-border">
    <div class="flex items-center gap-2 border-b border-border bg-muted/50 px-2.5 py-1.5">
      <div class="flex shrink-0 gap-1" aria-hidden="true">
        <span v-for="n in 3" :key="n" class="size-2 rounded-full bg-border" />
      </div>
      <p class="min-w-0 flex-1 truncate rounded bg-background/70 px-2 py-0.5 font-mono text-[10px] text-muted-foreground">
        {{ host || 'no address' }}
      </p>
      <div class="flex shrink-0 items-center">
        <Button
          v-if="live && url"
          variant="ghost"
          size="icon-xs"
          class="relative z-20 text-muted-foreground"
          :aria-label="`Reload the preview of ${name}`"
          @click="reloads++"
        >
          <RotateCw aria-hidden="true" />
        </Button>
        <Button
          v-if="url"
          variant="ghost"
          size="icon-xs"
          as-child
          class="relative z-20 text-muted-foreground"
        >
          <a :href="url" target="_blank" rel="noopener" :title="url">
            <ExternalLink aria-hidden="true" />
            <span class="sr-only">Open {{ url }} (opens in a new tab)</span>
          </a>
        </Button>
      </div>
    </div>

    <div ref="box" class="relative aspect-[16/10] overflow-hidden bg-muted/30">
      <iframe
        v-if="live && url"
        :key="reloads"
        :src="url"
        :title="`${name} preview`"
        class="pointer-events-none absolute left-0 top-0 origin-top-left border-0 bg-white"
        :style="{
          width: `${frameWidth}px`,
          height: `${frameHeight}px`,
          transform: `scale(${scale})`,
        }"
        loading="lazy"
        referrerpolicy="no-referrer"
        sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
      />
      <p v-else class="absolute inset-0 grid place-items-center px-4 text-center font-mono text-xs text-muted-foreground">
        {{ url ? 'not running' : 'no address yet' }}
      </p>
    </div>
  </div>
</template>
