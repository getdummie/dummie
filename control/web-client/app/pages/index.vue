<script setup lang="ts">
import { ArrowRight } from '@lucide/vue'
import { Button } from '@/components/ui/button'

// Signed-in visitors go straight to the app shell.
definePageMeta({ middleware: 'guest' })

useHead({
  title: 'dummie — secure sandbox runtime',
  meta: [
    {
      name: 'description',
      content:
        'dummie boots secure, isolated cloud sandboxes in milliseconds for running untrusted and AI-generated code.',
    },
  ],
})

const stats = [
  { value: '142ms', label: 'cold boot' },
  { value: '10k+', label: 'concurrent sandboxes' },
  { value: '<200ms', label: 'snapshot restore' },
  { value: '99.99%', label: 'run isolation' },
]
</script>

<template>
  <div>
    <!-- Hero -->
    <section class="relative overflow-hidden border-b border-border">
      <div class="pointer-events-none absolute inset-0 bg-grid opacity-60" />
      <div class="relative mx-auto max-w-6xl px-4 py-20 sm:px-6 sm:py-28 lg:py-32">
        <div class="grid items-center gap-12 lg:grid-cols-[1.05fr_0.95fr]">
          <div>
            <p class="eyebrow mb-5 flex items-center gap-2 text-primary">
              <span class="inline-block size-1.5 bg-primary" />
              Secure sandbox runtime
            </p>
            <h1 class="text-balance text-4xl font-semibold leading-[1.05] tracking-tight sm:text-5xl lg:text-6xl">
              Run untrusted code like you own the box.
            </h1>
            <p class="mt-6 max-w-xl text-lg leading-relaxed text-muted-foreground">
              dummie boots hardware-isolated microVMs in milliseconds. Give your
              agents a real machine to work on — files, network, a full shell —
              without ever risking your own.
            </p>
            <div id="start" class="mt-8 flex flex-col gap-3 sm:flex-row">
              <Button as-child size="lg" class="font-mono text-sm">
                <a href="/health">Start building <ArrowRight class="size-4" /></a>
              </Button>
              <Button as-child size="lg" variant="outline" class="font-mono text-sm">
                <a href="/#runtime">Read the docs</a>
              </Button>
            </div>
            <p class="eyebrow mt-8 text-muted-foreground/70">
              $ curl -fsSL dummie.sh | sh
            </p>
          </div>

          <div id="runtime" class="lg:pl-4">
            <TerminalMock />
          </div>
        </div>
      </div>
    </section>

    <!-- Stats -->
    <section class="border-b border-border">
      <div class="mx-auto grid max-w-6xl grid-cols-2 divide-x divide-border border-x border-border sm:grid-cols-4">
        <div v-for="s in stats" :key="s.label" class="px-4 py-8 sm:px-6">
          <div class="font-mono text-2xl font-semibold tracking-tight text-foreground sm:text-3xl">
            {{ s.value }}
          </div>
          <div class="eyebrow mt-2 text-muted-foreground">
            {{ s.label }}
          </div>
        </div>
      </div>
    </section>

    <!-- Features -->
    <section id="product" class="border-b border-border">
      <div class="mx-auto max-w-6xl px-4 pt-16 sm:px-6 sm:pt-20">
        <p class="eyebrow mb-4 text-primary">// Built for agents</p>
        <h2 class="max-w-2xl text-balance text-3xl font-semibold tracking-tight sm:text-4xl">
          A production runtime for code you didn't write.
        </h2>
        <p class="mt-4 max-w-xl text-muted-foreground">
          Isolation, speed, and control as primitives — not add-ons.
        </p>
      </div>
      <div class="mx-auto mt-12 max-w-6xl px-4 sm:px-6">
        <FeatureGrid />
      </div>
      <div class="h-16 sm:h-20" />
    </section>

    <!-- Closing CTA -->
    <section class="relative overflow-hidden">
      <div class="pointer-events-none absolute inset-0 bg-grid opacity-40" />
      <div class="relative mx-auto max-w-6xl px-4 py-20 text-center sm:px-6 sm:py-28">
        <p class="eyebrow mb-5 text-primary">// Ship it</p>
        <h2 class="mx-auto max-w-2xl text-balance text-3xl font-semibold tracking-tight sm:text-5xl">
          Your first sandbox is one command away.
        </h2>
        <div class="mt-8 flex justify-center">
          <Button as-child size="lg" class="font-mono text-sm">
            <a href="/health">Check system health <ArrowRight class="size-4" /></a>
          </Button>
        </div>
      </div>
    </section>
  </div>
</template>
