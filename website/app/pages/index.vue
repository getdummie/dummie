<script setup lang="ts">
import { ArrowRight } from '@lucide/vue'
import { Button } from '@/components/ui/button'

const { consoleUrl } = useRuntimeConfig().public

useHead({
  title: 'dummie — secure computers for everyone',
  meta: [
    {
      name: 'description',
      content:
        'dummie gives you your own computer in the cloud. Ready in seconds, sealed off from everything else, and yours to keep running for as long as you need it.',
    },
  ],
})
</script>

<template>
  <div>
    <section aria-labelledby="hero-heading" class="panel relative overflow-hidden border-b border-border">
      <div aria-hidden="true" class="pointer-events-none absolute inset-0 bg-grid opacity-60" />
      <div class="relative mx-auto w-full max-w-6xl px-4 py-16 sm:px-6 sm:py-20">
        <div class="grid items-center gap-12 lg:grid-cols-[1.05fr_0.95fr]">
          <div>
            <p class="eyebrow anim-rise mb-5 flex items-center gap-2 text-primary-text">
              <span aria-hidden="true" class="inline-block size-1.5 animate-pulse bg-primary" />
              Isolated by default
            </p>
            <h1
              id="hero-heading"
              class="anim-rise text-balance text-4xl font-semibold leading-[1.05] tracking-tight sm:text-5xl lg:text-6xl"
              style="animation-delay: 0.08s"
            >
              Secure computers
              <span class="block text-muted-foreground">for everyone</span>
            </h1>
            <div class="anim-rise mt-8 flex flex-col gap-3 sm:flex-row" style="animation-delay: 0.26s">
              <Button as-child size="lg" class="group font-mono text-sm">
                <a :href="`${consoleUrl}/signin`">
                  Boot a machine
                  <ArrowRight class="size-4 transition-transform duration-300 group-hover:translate-x-1" aria-hidden="true" />
                </a>
              </Button>
              <Button as-child size="lg" variant="outline" class="font-mono text-sm">
                <a href="#usecases">What it's for</a>
              </Button>
            </div>
          </div>

          <div class="anim-rise lg:pl-4" style="animation-delay: 0.34s">
            <MachineVisual />
          </div>
        </div>
      </div>
    </section>

    <UseCaseSections />

    <SecurityPanels />

    <section aria-labelledby="cta-heading" class="panel relative overflow-hidden">
      <div aria-hidden="true" class="pointer-events-none absolute inset-0 bg-grid opacity-40" />
      <div class="relative mx-auto w-full max-w-6xl px-4 py-16 text-center sm:px-6">
        <h2
          id="cta-heading"
          v-reveal
          class="mx-auto max-w-2xl text-balance text-3xl font-semibold tracking-tight sm:text-5xl"
        >
          Try now.
        </h2>
        <div v-reveal="80" class="mt-8 flex justify-center">
          <Button as-child size="lg" class="group font-mono text-sm">
            <a :href="`${consoleUrl}/signin`">
              Open the console
              <ArrowRight class="size-4 transition-transform duration-300 group-hover:translate-x-1" aria-hidden="true" />
            </a>
          </Button>
        </div>
      </div>
    </section>
  </div>
</template>
