<script setup lang="ts">
// One tile per harness; codex, claude and opencode get their marks here when
// they are supported. Unknown harnesses fall back to their initial.
defineProps<{ harness: string }>()
</script>

<template>
  <span
    class="inline-grid size-4 shrink-0 place-items-center rounded-[4px] bg-foreground text-background"
    :title="harness"
    aria-hidden="true"
  >
    <svg v-if="harness === 'pi'" viewBox="0 0 24 24" class="size-3" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round">
      <path d="M5 7.5h14M9.5 7.5v10M14.5 7.5v8.5c0 1 .6 1.5 1.5 1.5h1" />
    </svg>
    <span v-else class="font-mono text-[9px] font-semibold uppercase">{{ harness.slice(0, 1) }}</span>
  </span>
</template>
