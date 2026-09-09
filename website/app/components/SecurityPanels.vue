<script setup lang="ts">
import { Check, Cpu, Network, Shield, X } from '@lucide/vue'

const rules = [
  { allowed: true, dest: 'deb.debian.org', delay: '0s' },
  { allowed: true, dest: 'your own network', delay: '0.7s' },
  { allowed: false, dest: 'github.com', delay: '1.4s' },
  { allowed: false, dest: 'everything else', delay: '2.1s' },
]

const vms = [
  { label: 'yours', yours: true, offset: 0, bars: ['72%', '48%'] },
  { label: 'someone else', yours: false, offset: 0.5, bars: ['54%', '80%'] },
  { label: 'someone else', yours: false, offset: 1, bars: ['64%', '40%'] },
]

const panels = [
  {
    slug: 'security',
    kind: 'net',
    label: 'Network',
    icon: Network,
    title: 'Decide what gets in',
    body: 'A machine talks to nothing until you say so.',
    points: [
      'Allow a domain, a network, or nothing at all',
      'Every attempt is logged, allowed or blocked',
      'Change the rules while the machine keeps running',
    ],
  },
  {
    slug: 'isolation',
    kind: 'iso',
    label: 'Isolation',
    icon: Shield,
    title: 'Secure and Isolated',
    body: 'Your machine is a real VM with its own kernel.',
    points: [
      'Kernel and memory isolation, enforced by KVM',
      'No shared filesystem, no shared processes',
      'Deleting the machine deletes the disk with it',
    ],
  },
]
</script>

<template>
  <section
    v-for="(p, i) in panels"
    :id="p.slug"
    :key="p.slug"
    :aria-labelledby="`${p.slug}-heading`"
    class="panel border-b border-border"
  >
    <div class="mx-auto w-full max-w-6xl px-4 py-14 sm:px-6 lg:max-w-[84rem] lg:px-10">
      <div class="relative grid gap-10 lg:grid-cols-2 lg:items-start lg:gap-28 xl:gap-36">
        <span
          aria-hidden="true"
          class="pointer-events-none absolute inset-y-6 left-1/2 hidden w-px -translate-x-1/2 overflow-hidden bg-gradient-to-b from-transparent via-border to-transparent lg:block"
        >
          <span class="anim-line-glide absolute inset-x-0 h-1/3 bg-gradient-to-b from-transparent via-primary to-transparent" />
        </span>

        <div v-reveal :class="i % 2 === 1 ? 'lg:order-2' : ''">
          <div class="rounded-lg border border-border bg-background/70 p-5 shadow-sm sm:p-7">
            <template v-if="p.kind === 'net'">
              <ul class="divide-y divide-border">
                <li v-for="r in rules" :key="r.dest" class="flex items-center gap-3.5 py-5">
                  <component
                    :is="r.allowed ? Check : X"
                    aria-hidden="true"
                    class="size-4 shrink-0"
                    :class="r.allowed ? 'text-primary-text' : 'text-destructive'"
                  />
                  <span class="sr-only">{{ r.allowed ? 'Allowed' : 'Blocked' }}:</span>
                  <span
                    class="min-w-0 font-mono text-sm"
                    :class="r.allowed ? 'text-foreground' : 'text-muted-foreground line-through decoration-destructive/50'"
                  >
                    {{ r.dest }}
                  </span>

                  <span aria-hidden="true" class="relative ml-auto h-px w-20 shrink-0 bg-border sm:w-32">
                    <span
                      v-if="r.allowed"
                      class="anim-travel absolute inset-y-0 left-0 w-full"
                      :style="{ '--dm-dist': 'calc(100% - 0.5rem)', animationDelay: r.delay }"
                    >
                      <span class="absolute -top-1 left-0 size-2 rounded-full bg-primary" />
                    </span>
                    <template v-else>
                      <span class="absolute -top-2 right-0 h-4 w-0.5 bg-destructive" />
                      <span
                        class="anim-travel-block absolute -top-1 left-0 size-2 rounded-full bg-destructive"
                        :style="{ '--dm-stop': 'calc(100% - 0.75rem)', animationDelay: r.delay }"
                      />
                    </template>
                  </span>
                </li>
              </ul>
            </template>

            <template v-else>
              <div class="grid grid-cols-3 gap-2">
                <div
                  v-for="vm in vms"
                  :key="vm.label"
                  class="relative overflow-hidden rounded-t-md border-2 border-b-0 p-3"
                  :class="vm.yours ? 'border-primary bg-primary/5' : 'border-border bg-card'"
                >
                  <span class="eyebrow block text-[0.625rem]" :class="vm.yours ? 'text-primary-text' : 'text-muted-foreground/60'">
                    {{ vm.label }}
                  </span>
                  <div aria-hidden="true" class="mt-3.5 space-y-2">
                    <span
                      v-for="(w, i2) in vm.bars"
                      :key="i2"
                      class="anim-bar-load block h-2 rounded-full"
                      :class="vm.yours && i2 === 0 ? 'bg-primary/50' : 'bg-muted'"
                      :style="{ '--dm-w': w, animationDelay: `${vm.offset + i2 * 0.3}s` }"
                    />
                  </div>
                  <span class="mt-5 block rounded border border-dashed border-border px-2 py-1.5 text-center font-mono text-[0.625rem] text-muted-foreground">
                    own kernel
                  </span>

                  <span
                    v-if="vm.yours"
                    aria-hidden="true"
                    class="anim-travel-block absolute bottom-2.5 left-3 size-2 rounded-full bg-destructive"
                    :style="{ '--dm-stop': 'calc(100% - 2rem)' }"
                  />
                </div>
              </div>

              <div class="anim-wall-pulse flex items-center gap-3 border-2 border-primary bg-primary/10 px-5 py-4">
                <Cpu aria-hidden="true" class="size-4 shrink-0 text-primary-text" />
                <span class="eyebrow text-primary-text">kvm</span>
                <span class="ml-auto font-mono text-xs text-muted-foreground">isolation in hardware</span>
              </div>

              <div class="rounded-b-md border border-t-0 border-border bg-muted/40 px-5 py-4 font-mono text-sm text-muted-foreground">
                Physical server
              </div>
            </template>
          </div>
        </div>

        <div v-reveal="120" :class="i % 2 === 1 ? 'lg:order-1' : ''">
          <p class="eyebrow flex items-center gap-2.5 text-muted-foreground/60">
            <component :is="p.icon" aria-hidden="true" class="size-4 text-primary-text" />
            {{ p.label }}
          </p>
          <h3
            :id="`${p.slug}-heading`"
            class="mt-5 text-balance text-3xl font-semibold tracking-tight sm:text-4xl xl:text-5xl"
          >
            {{ p.title }}
          </h3>
          <p class="mt-6 max-w-xl text-base leading-relaxed text-muted-foreground sm:text-lg">
            {{ p.body }}
          </p>
          <ul class="mt-8 max-w-xl space-y-4 border-t border-border pt-8">
            <li
              v-for="pt in p.points"
              :key="pt"
              class="flex items-start gap-3.5 font-mono text-sm leading-relaxed text-muted-foreground"
            >
              <span aria-hidden="true" class="mt-2.5 h-px w-6 shrink-0 bg-primary" />
              {{ pt }}
            </li>
          </ul>
        </div>
      </div>
    </div>
  </section>
</template>
