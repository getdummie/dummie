<script setup lang="ts">
import type { HTMLAttributes } from "vue"
import { cn } from "@/lib/utils"

// Without `scope`, a screen reader has to guess which cells a header governs.
// "col" is right for the overwhelmingly common case of a header row.
const props = withDefaults(
  defineProps<{
    class?: HTMLAttributes["class"]
    scope?: "col" | "row" | "colgroup" | "rowgroup"
  }>(),
  { scope: "col" },
)
</script>

<template>
  <th
    data-slot="table-head"
    :scope="props.scope"
    :class="cn('text-foreground h-10 px-2 text-left align-middle font-medium whitespace-nowrap [&:has([role=checkbox])]:pr-0 *:[[role=checkbox]]:translate-y-0.5', props.class)"
  >
    <slot />
  </th>
</template>
