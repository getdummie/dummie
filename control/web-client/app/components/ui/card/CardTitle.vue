<script setup lang="ts">
import type { HTMLAttributes } from "vue"
import { cn } from "@/lib/utils"

// `as` lets a page pick the heading level that keeps its document outline
// intact — a card is not always three levels deep. Defaults to h3 so existing
// usage is unchanged.
const props = withDefaults(
  defineProps<{
    class?: HTMLAttributes["class"]
    as?: string
  }>(),
  { as: "h3" },
)
</script>

<template>
  <component
    :is="props.as"
    data-slot="card-title"
    :class="cn('leading-none font-semibold', props.class)"
  >
    <slot />
  </component>
</template>
