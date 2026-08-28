<script setup lang="ts">
import { AlertCircle } from '@lucide/vue'

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
