<script setup lang="ts">
import type { NuxtError } from '#app'
import { ArrowLeft } from '@lucide/vue'
import { Button } from '@/components/ui/button'

const props = defineProps<{ error: NuxtError }>()

useHead({ title: () => `dummie — ${props.error.statusCode || 'error'}` })

const message = computed(() => {
  if (props.error.statusCode === 404) return 'This page could not be found.'
  return props.error.statusMessage || 'Something went wrong.'
})
</script>

<template>
  <main
    id="main-content"
    tabindex="-1"
    class="flex min-h-svh flex-col items-center justify-center bg-background px-4 text-center text-foreground focus-visible:outline-none"
  >
    <div aria-hidden="true" class="pointer-events-none absolute inset-0 bg-grid opacity-40" />
    <div class="relative">
      <p class="eyebrow mb-4 text-primary-text">// Error</p>
      <!-- The status code alone is a meaningless heading; the sr-only half
           carries the actual meaning for anyone navigating by heading. -->
      <h1 class="font-mono text-7xl font-bold tracking-tight sm:text-8xl">
        {{ error.statusCode || 500 }}
        <span class="sr-only">— {{ message }}</span>
      </h1>
      <p class="mt-4 max-w-sm text-muted-foreground">
        {{ message }}
      </p>
      <div class="mt-8 flex justify-center">
        <Button class="font-mono text-sm" @click="clearError({ redirect: '/' })">
          <ArrowLeft class="size-4" aria-hidden="true" />
          Back to home
        </Button>
      </div>
    </div>
  </main>
</template>
