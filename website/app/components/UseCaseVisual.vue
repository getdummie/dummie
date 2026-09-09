<script setup lang="ts">
import { Bug, Cpu, Database, Gamepad2, Globe, MousePointer2, Server } from '@lucide/vue'

defineProps<{ anim: string }>()

const chart = ['32%', '48%', '70%', '92%']
const matrix = Array.from({ length: 36 }, (_, i) => ({
  delay: `${((i * 7) % 13) * 0.19}s`,
  lit: i % 5 === 0,
}))
const cycleIcons = [Database, Globe, Server, Cpu, Gamepad2]
</script>

<template>
  <div
    aria-hidden="true"
    class="relative aspect-[16/10] w-full overflow-hidden rounded-lg border border-border bg-background/70 shadow-sm lg:aspect-[4/3]"
  >
    <template v-if="anim === 'web'">
      <div class="flex h-full flex-col">
        <div class="flex items-center gap-1.5 border-b border-border px-3 py-2.5">
          <span class="size-2 rounded-full bg-border" />
          <span class="size-2 rounded-full bg-border" />
          <span class="size-2 rounded-full bg-primary/60" />
          <span class="ml-2 h-2.5 flex-1 rounded-full bg-muted" />
        </div>
        <div class="flex flex-1 flex-col gap-3 p-4">
          <div class="anim-sweep relative flex h-[38%] flex-col justify-center gap-2 overflow-hidden rounded bg-primary/15 px-4">
            <span class="anim-bar-load block h-2 rounded-full bg-primary/60" :style="{ '--dm-w': '68%' }" />
            <span class="anim-bar-load block h-1.5 rounded-full bg-primary/30" :style="{ '--dm-w': '44%', animationDelay: '0.2s' }" />
          </div>
          <div class="grid flex-1 grid-cols-3 gap-3">
            <div v-for="i in 3" :key="i" class="flex flex-col gap-1.5 rounded border border-border p-2">
              <span class="block h-1/2 rounded-sm bg-muted" />
              <span class="anim-bar-load block h-1.5 rounded-full bg-muted" :style="{ '--dm-w': '80%', animationDelay: `${i * 0.22}s` }" />
              <span class="anim-bar-load block h-1.5 rounded-full bg-muted" :style="{ '--dm-w': '55%', animationDelay: `${i * 0.3}s` }" />
            </div>
          </div>
          <span class="block h-3 rounded bg-muted" />
        </div>
      </div>
    </template>

    <template v-else-if="anim === 'lab'">
      <div class="relative flex h-full items-center justify-center p-6">
        <span class="absolute left-3 top-3 size-4 border-l-2 border-t-2 border-primary/70" />
        <span class="absolute right-3 top-3 size-4 border-r-2 border-t-2 border-primary/70" />
        <span class="absolute bottom-3 left-3 size-4 border-b-2 border-l-2 border-primary/70" />
        <span class="absolute bottom-3 right-3 size-4 border-b-2 border-r-2 border-primary/70" />

        <span class="anim-scan absolute inset-4" :style="{ '--dm-h': '100%' }">
          <span class="absolute inset-x-0 top-0 h-px bg-primary/80" />
        </span>

        <span class="flex aspect-square h-[76%] items-center justify-center rounded-full border border-dashed border-destructive/50">
          <span class="anim-jiggle">
            <Bug class="size-10 text-destructive" />
          </span>
        </span>
      </div>
    </template>

    <template v-else-if="anim === 'ai'">
      <div class="h-full p-4">
        <div class="flex h-full flex-col gap-2.5 rounded border border-dashed border-primary/50 bg-primary/[0.04] p-4">
          <span class="eyebrow text-[0.625rem] text-primary-text/70">sandbox</span>
          <span
            v-for="(w, i) in ['62%', '84%', '48%', '72%']"
            :key="i"
            class="anim-bar-load block h-2 rounded-full"
            :class="i === 1 ? 'bg-primary/40' : 'bg-muted'"
            :style="{ '--dm-w': w, animationDelay: `${i * 0.35}s` }"
          />
          <span class="flex items-center gap-2">
            <span class="block h-2 w-[30%] rounded-full bg-muted" />
            <span class="anim-blink block h-4 w-2 bg-primary" />
          </span>
          <span class="anim-chip mt-auto inline-flex w-fit items-center gap-1 rounded-full border border-primary bg-primary/15 px-2.5 py-0.5 font-mono text-[0.625rem] text-primary-text">
            exit 0
          </span>
        </div>
      </div>
    </template>

    <template v-else-if="anim === 'share'">
      <div class="flex h-full flex-col p-4">
        <div class="relative flex flex-1 flex-col rounded border border-border bg-card p-4">
          <span class="block h-2 w-[55%] rounded-full bg-foreground/30" />
          <span class="mt-1.5 block h-1.5 w-[35%] rounded-full bg-muted" />

          <span class="anim-chip absolute right-3 top-3 rounded-sm border border-primary bg-primary/15 px-2 py-0.5 font-mono text-[0.625rem] font-semibold text-primary-text">
            $120M
          </span>

          <div class="relative mt-auto flex h-[55%] items-end gap-2">
            <span
              v-for="(h, i) in chart"
              :key="i"
              class="anim-grow flex-1 rounded-sm"
              :class="i === chart.length - 1 ? 'bg-primary' : 'bg-primary/30'"
              :style="{ '--dm-h': h, animationDelay: `${i * 0.18}s` }"
            />
            <svg class="pointer-events-none absolute inset-0 size-full" viewBox="0 0 100 60" preserveAspectRatio="none" fill="none">
              <path class="anim-draw" d="M4 52 L34 40 L64 22 L94 5" stroke="var(--primary-text)" stroke-width="2" vector-effect="non-scaling-stroke" />
            </svg>
          </div>
        </div>
        <div class="mt-3 flex justify-center gap-1.5">
          <span class="size-1.5 rounded-full bg-primary" />
          <span class="size-1.5 rounded-full bg-border" />
          <span class="size-1.5 rounded-full bg-border" />
        </div>
      </div>
    </template>

    <template v-else-if="anim === 'desktop'">
      <div class="relative h-full bg-gradient-to-br from-primary/20 via-background to-muted">
        <div class="flex items-center justify-center gap-1 border-b border-border/60 bg-foreground/[0.06] px-3 py-1.5">
          <span class="h-1.5 w-6 rounded-full bg-foreground/25" />
        </div>

        <div class="absolute left-3 top-10 flex flex-col gap-2">
          <span
            v-for="i in 3"
            :key="i"
            class="anim-float block size-6 rounded-md border border-border bg-card"
            :style="{ animationDelay: `${i * 0.4}s` }"
          />
        </div>

        <div class="absolute bottom-6 left-14 right-5 top-14 overflow-hidden rounded border border-border bg-card shadow-lg">
          <div class="flex items-center gap-1.5 border-b border-border px-2.5 py-1.5">
            <span class="size-1.5 rounded-full bg-border" />
            <span class="size-1.5 rounded-full bg-border" />
            <span class="ml-1 h-1.5 w-10 rounded-full bg-muted" />
          </div>
          <div class="space-y-2.5 p-3">
            <span
              v-for="(w, i) in ['70%', '45%', '82%']"
              :key="i"
              class="anim-bar-load block h-2 rounded-full bg-muted"
              :style="{ '--dm-w': w, animationDelay: `${i * 0.3}s` }"
            />
          </div>
        </div>

        <span class="anim-cursor absolute inset-0">
          <MousePointer2 class="size-5 fill-primary text-primary drop-shadow" />
        </span>
        <span class="anim-click absolute left-[58%] top-[34%] -ml-3 -mt-3 size-7 rounded-full border-2 border-primary" />
      </div>
    </template>

    <template v-else>
      <div class="relative h-full p-4">
        <div class="grid h-full grid-cols-6 grid-rows-6 gap-2">
          <span
            v-for="(m, i) in matrix"
            :key="i"
            class="anim-pulse-dot rounded-sm"
            :class="m.lit ? 'bg-primary/70' : 'bg-muted'"
            :style="{ animationDelay: m.delay }"
          />
        </div>
        <span class="absolute inset-0 flex items-center justify-center">
          <span class="flex size-16 items-center justify-center rounded-full border border-border bg-background shadow-sm">
            <component
              :is="ico"
              v-for="(ico, i) in cycleIcons"
              :key="i"
              class="anim-cycle absolute size-7 text-primary-text"
              :style="{ animationDelay: `${i * 1.08}s` }"
            />
          </span>
        </span>
      </div>
    </template>
  </div>
</template>
