<script setup lang="ts">
import { Check, Cpu, X } from '@lucide/vue'

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
</script>

<template>
  <div class="grid border-t border-l border-border lg:grid-cols-2">
    <section aria-labelledby="net-panel" class="border-b border-r border-border p-6 sm:p-8">
      <p class="eyebrow text-primary-text">Network</p>
      <h3 id="net-panel" class="mt-4 text-xl font-semibold tracking-tight sm:text-2xl">
        You decide what gets in and out
      </h3>

      <ul class="mt-7 divide-y divide-border border-y border-border">
        <li v-for="r in rules" :key="r.dest" class="flex items-center gap-3 py-3.5">
          <component
            :is="r.allowed ? Check : X"
            aria-hidden="true"
            class="size-3.5 shrink-0"
            :class="r.allowed ? 'text-primary-text' : 'text-destructive'"
          />
          <span class="sr-only">{{ r.allowed ? 'Allowed' : 'Blocked' }}:</span>
          <span
            class="min-w-0 font-mono text-[13px]"
            :class="r.allowed ? 'text-foreground' : 'text-muted-foreground line-through decoration-destructive/50'"
          >
            {{ r.dest }}
          </span>

          <span aria-hidden="true" class="relative ml-auto h-px w-16 shrink-0 bg-border sm:w-24">
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
    </section>

    <section aria-labelledby="iso-panel" class="border-b border-r border-border p-6 sm:p-8">
      <p class="eyebrow text-primary-text">Isolation</p>
      <h3 id="iso-panel" class="mt-4 text-xl font-semibold tracking-tight sm:text-2xl">
        Nothing is shared with anyone
      </h3>

      <div class="mt-7">
        <div class="grid grid-cols-3 gap-1.5">
          <div
            v-for="vm in vms"
            :key="vm.label"
            class="relative overflow-hidden rounded-t-md border-2 border-b-0 p-2.5"
            :class="vm.yours ? 'border-primary bg-primary/5' : 'border-border bg-card'"
          >
            <span class="eyebrow block text-[0.5625rem]" :class="vm.yours ? 'text-primary-text' : 'text-muted-foreground/60'">
              {{ vm.label }}
            </span>
            <div aria-hidden="true" class="mt-2.5 space-y-1.5">
              <span
                v-for="(w, i) in vm.bars"
                :key="i"
                class="anim-bar-load block h-1.5 rounded-full"
                :class="vm.yours && i === 0 ? 'bg-primary/50' : 'bg-muted'"
                :style="{ '--dm-w': w, animationDelay: `${vm.offset + i * 0.3}s` }"
              />
            </div>
            <span class="mt-3 block rounded border border-dashed border-border px-1.5 py-1 text-center font-mono text-[0.5625rem] text-muted-foreground">
              own kernel
            </span>

            <span
              v-if="vm.yours"
              aria-hidden="true"
              class="anim-travel-block absolute bottom-2 left-2.5 size-2 rounded-full bg-destructive"
              :style="{ '--dm-stop': 'calc(100% - 1.75rem)' }"
            />
          </div>
        </div>

        <div class="anim-wall-pulse flex items-center gap-2.5 border-2 border-primary bg-primary/10 px-4 py-3">
          <Cpu aria-hidden="true" class="size-4 shrink-0 text-primary-text" />
          <span class="eyebrow text-primary-text">kvm</span>
          <span class="ml-auto font-mono text-xs text-muted-foreground">isolation in hardware</span>
        </div>

        <div class="rounded-b-md border border-t-0 border-border bg-muted/40 px-4 py-3.5 font-mono text-[13px] text-muted-foreground">
          Physical server
        </div>
      </div>
    </section>
  </div>
</template>
