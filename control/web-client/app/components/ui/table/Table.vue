<script setup lang="ts">
import type { HTMLAttributes } from "vue"
import { cn } from "@/lib/utils"

// `label` names the scroll container and makes it a keyboard-reachable region.
// Without a tab stop, a keyboard-only user cannot scroll a table that overflows
// horizontally unless it happens to contain a focusable cell. (WCAG 2.1.1)
const props = defineProps<{
  class?: HTMLAttributes["class"]
  label?: string
}>()
</script>

<template>
  <div
    data-slot="table-container"
    class="relative w-full overflow-auto"
    :role="props.label ? 'region' : undefined"
    :aria-label="props.label"
    :tabindex="props.label ? 0 : undefined"
  >
    <table data-slot="table" :class="cn('w-full caption-bottom text-sm', props.class)">
      <slot />
    </table>
  </div>
</template>
