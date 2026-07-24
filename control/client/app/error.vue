<script setup lang="ts">
import type { NuxtError } from '#app'
import { ArrowLeft } from '@lucide/vue'
import { Button } from '@/components/ui/button'

const props = defineProps<{ error: NuxtError }>()

useHead({ title: () => `dummie — ${props.error.statusCode || 'error'}` })

const message = computed(() => {
  if (props.error.statusCode === 401) return 'You do not have access to this area.'
  if (props.error.statusCode === 404) return 'This page could not be found.'
  return props.error.statusMessage || 'Something went wrong.'
})
</script>

<template>
  <div class="flex min-h-svh flex-col items-center justify-center bg-background px-4 text-center text-foreground">
    <div class="pointer-events-none absolute inset-0 bg-grid opacity-40" />
    <div class="relative">
      <p class="eyebrow mb-4 text-primary">// Error</p>
      <h1 class="font-mono text-7xl font-bold tracking-tight sm:text-8xl">
        {{ error.statusCode || 500 }}
      </h1>
      <p class="mt-4 max-w-sm text-muted-foreground">
        {{ message }}
      </p>
      <div class="mt-8 flex justify-center">
        <Button class="font-mono text-sm" @click="clearError({ redirect: '/' })">
          <ArrowLeft class="size-4" />
          Back to home
        </Button>
      </div>
    </div>
  </div>
</template>
