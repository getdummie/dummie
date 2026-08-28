<script setup lang="ts">
import { Check, Copy } from '@lucide/vue'
import { useClipboard } from '@vueuse/core'

const props = defineProps<{
  code?: string
  language?: string
  filename?: string
  meta?: string
  class?: string
  highlights?: number[]
}>()

const { copy, copied, isSupported } = useClipboard({ copiedDuring: 1600 })
</script>

<template>
  <div class="code-block border border-border bg-card">
    <div
      class="sticky top-14 z-20 flex items-center gap-3 border-b border-border bg-card/95 px-3 py-1.5 backdrop-blur"
    >
      <span class="eyebrow !text-[0.65rem] text-muted-foreground/80">{{ props.language || 'text' }}</span>
      <span v-if="props.filename" class="truncate font-mono text-xs text-muted-foreground">
        {{ props.filename }}
      </span>
      <button
        v-if="isSupported"
        type="button"
        class="ml-auto flex shrink-0 items-center gap-1.5 border border-transparent px-1.5 py-0.5 font-mono text-xs text-muted-foreground transition-colors hover:border-border hover:text-primary-text"
        :aria-label="copied ? 'Code copied' : 'Copy code'"
        @click="copy(props.code ?? '')"
      >
        <component :is="copied ? Check : Copy" class="size-3.5" aria-hidden="true" />
        <span aria-hidden="true">{{ copied ? 'copied' : 'copy' }}</span>
      </button>
    </div>

    <pre :class="props.class" tabindex="0"><slot /></pre>
  </div>
</template>
