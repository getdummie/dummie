<script setup lang="ts">
import { Monitor } from '@lucide/vue'

const description = 'How the control plane, the QEMU hosts, the proxies and the guest init fit together — and which process owns what.'

// Add, remove or reorder freely — the grid flows to however many there are.
// Wrap anything in backticks to render it as inline code.
const notes = [
  {
    title: 'Restarts do not drop sessions',
    body: '`dpipe` owns the file descriptor, not `dproxy`. Connection lifetime is deliberately decoupled from the lifetime of the process that accepted it, so shipping a new `dproxy` disconnects nobody.',
  },
  {
    title: 'A credential never reaches a guest',
    body: '`intproxy` identifies a VM by the source address of its connection, which `nftables` makes trustworthy, then asks `dclient` for a token scoped to that one repository. The GitHub App private key never leaves the control server.',
  },
  {
    title: 'Hosts reconcile, they are not driven',
    body: 'The control plane records what should exist. A host that goes silent simply stops being given work, and the VMs it was running are reaped by their TTL — there is no operator step in the middle.',
  },
]

function parts(body: string) {
  return body.split('`').map((text, i) => ({ text, code: i % 2 === 1 }))
}

useHead({ title: 'dummie — architecture' })
useSeoMeta({
  description,
  ogTitle: 'dummie — architecture',
  ogDescription: description,
})
</script>

<template>
  <div class="max-sm:pb-16">
    <section aria-labelledby="arch-heading" class="relative overflow-hidden border-b border-border">
      <div aria-hidden="true" class="pointer-events-none absolute inset-0 bg-grid opacity-60" />
      <div class="relative mx-auto w-full max-w-[92rem] px-4 py-10 sm:px-6 sm:py-12">
        <h1 id="arch-heading" class="eyebrow anim-rise flex items-center justify-center gap-2 text-primary-text">
          <span aria-hidden="true" class="inline-block size-1.5 animate-pulse bg-primary" />
          Architecture
        </h1>
        <div class="anim-rise mt-8" style="animation-delay: 0.08s">
          <ArchitectureDiagram />
        </div>
      </div>
    </section>

    <section aria-labelledby="notes-heading">
      <div class="mx-auto w-full max-w-7xl px-4 py-14 sm:px-6 sm:py-16">
        <h2 id="notes-heading" v-reveal class="text-2xl font-semibold tracking-tight sm:text-3xl">
          Things the picture does not say
        </h2>

        <div class="mt-8 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
          <article
            v-for="(note, i) in notes"
            :key="note.title"
            v-reveal="60 * (i + 1)"
            class="rounded-lg border border-border bg-card p-5"
          >
            <h3 class="font-mono text-sm font-semibold">{{ note.title }}</h3>
            <p class="mt-2.5 text-sm leading-relaxed text-muted-foreground">
              <template v-for="(part, j) in parts(note.body)" :key="j">
                <code v-if="part.code" class="font-mono text-foreground">{{ part.text }}</code>
                <template v-else>{{ part.text }}</template>
              </template>
            </p>
          </article>
        </div>
      </div>
    </section>

    <div
      class="fixed inset-x-0 bottom-0 z-40 bg-primary text-primary-foreground shadow-[0_-8px_24px_-12px_rgb(0_0_0/0.4)] pb-[env(safe-area-inset-bottom)] sm:hidden"
      role="note"
    >
      <p class="flex items-center justify-center gap-2.5 px-4 py-3 text-center font-mono text-sm font-semibold">
        <Monitor class="size-4 shrink-0" aria-hidden="true" />
        Best viewed on a desktop
      </p>
    </div>
  </div>
</template>
