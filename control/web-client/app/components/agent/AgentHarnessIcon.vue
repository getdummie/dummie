<script setup lang="ts">
// One tile per harness, with a plain glyph rather than each tool's logo.
// Unknown harnesses fall back to their initial.
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
    <svg v-else-if="harness === 'claude'" viewBox="0 0 24 24" class="size-3" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round">
      <path d="M12 4v16M4 12h16M6.3 6.3l11.4 11.4M17.7 6.3 6.3 17.7" />
    </svg>
    <svg v-else-if="harness === 'codex'" viewBox="0 0 24 24" class="size-3" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round">
      <path d="m5 7 5 5-5 5M13 17h6" />
    </svg>
    <svg v-else-if="harness === 'opencode'" viewBox="0 0 24 24" class="size-3" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linejoin="round">
      <rect x="6" y="5" width="12" height="14" rx="1" />
    </svg>
    <svg v-else-if="harness === 'gemini'" viewBox="0 0 24 24" class="size-3" fill="currentColor">
      <path d="M12 2c.6 5.4 4.6 9.4 10 10-5.4.6-9.4 4.6-10 10-.6-5.4-4.6-9.4-10-10 5.4-.6 9.4-4.6 10-10Z" />
    </svg>
    <span v-else class="font-mono text-[9px] font-semibold uppercase">{{ harness.slice(0, 1) }}</span>
  </span>
</template>
