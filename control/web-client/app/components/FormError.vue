<script setup lang="ts">
import { AlertCircle } from '@lucide/vue'

// A form error that assistive tech actually notices.
//
// Three things make this work, and all three are easy to get wrong:
//   1. The `role="alert"` wrapper is always rendered, even when there's no
//      error. A live region inserted at the same moment as its content is
//      routinely missed — the region has to be there first.
//   2. The message carries an icon and an "Error:" prefix, so the failure is
//      not signalled by red text alone. (WCAG 1.4.1)
//   3. It exposes a stable `id` for the offending field's `aria-describedby`,
//      so tabbing back into the input re-reads the reason. (WCAG 3.3.1)
defineProps<{
  id: string
  message?: string | null
}>()
</script>

<template>
  <div role="alert" aria-live="assertive">
    <p v-if="message" :id="id" class="flex items-start gap-2 text-sm text-destructive">
      <AlertCircle class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
      <span><span class="font-semibold">Error:</span> {{ message }}</span>
    </p>
  </div>
</template>
