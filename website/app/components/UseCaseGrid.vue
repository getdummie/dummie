<script setup lang="ts">
import {
  Bot,
  Bug,
  Code2,
  Cpu,
  Database,
  FlaskConical,
  Gamepad2,
  Globe,
  Link,
  Monitor,
  MousePointer2,
  Server,
  Sparkles,
} from '@lucide/vue'

const cases = [
  { id: '01', anim: 'web', icon: Code2, title: 'Web development', body: 'Build on a machine that isn\'t your laptop.' },
  { id: '02', anim: 'lab', icon: FlaskConical, title: 'Malware analysis', body: 'Open something risky, then delete the machine.' },
  { id: '03', anim: 'ai', icon: Bot, title: 'Sandboxed evaluation', body: 'Run AI generated code.' },
  { id: '04', anim: 'share', icon: Link, title: 'Demos', body: 'Share what you made as a link.' },
  { id: '05', anim: 'desktop', icon: Monitor, title: 'Desktops', body: 'A full desktop, right in your browser.' },
  { id: '06', anim: 'any', icon: Sparkles, title: 'Anything else', body: 'It\'s a computer. Use it however you like.' },
]

const chart = ['32%', '48%', '70%', '92%']
const matrix = Array.from({ length: 36 }, (_, i) => ({
  delay: `${((i * 7) % 13) * 0.19}s`,
  lit: i % 5 === 0,
}))
const cycleIcons = [Database, Globe, Server, Cpu, Gamepad2]
</script>

<template>
  <ul class="grid grid-cols-1 border-l border-t border-border sm:grid-cols-2 lg:grid-cols-3">
    <li
      v-for="c in cases"
      :key="c.id"
      class="group border-b border-r border-border p-5 transition-colors hover:bg-secondary/40 sm:p-6"
    >
      <div
        aria-hidden="true"
        class="relative aspect-[16/9] w-full overflow-hidden rounded-md border border-border bg-background/70 transition-transform duration-500 group-hover:scale-[1.02]"
      >
        <template v-if="c.anim === 'web'">
          <div class="flex h-full flex-col">
            <div class="flex items-center gap-1.5 border-b border-border px-2.5 py-2">
              <span class="size-1.5 rounded-full bg-border" />
              <span class="size-1.5 rounded-full bg-border" />
              <span class="size-1.5 rounded-full bg-primary/60" />
              <span class="ml-1.5 h-2 flex-1 rounded-full bg-muted" />
            </div>
            <div class="flex flex-1 flex-col gap-2 p-2.5">
              <div class="anim-sweep relative flex h-[38%] flex-col justify-center gap-1.5 overflow-hidden rounded bg-primary/15 px-2.5">
                <span class="anim-bar-load block h-1.5 rounded-full bg-primary/60" :style="{ '--dm-w': '68%' }" />
                <span class="anim-bar-load block h-1 rounded-full bg-primary/30" :style="{ '--dm-w': '44%', animationDelay: '0.2s' }" />
              </div>
              <div class="grid flex-1 grid-cols-3 gap-1.5">
                <div v-for="i in 3" :key="i" class="flex flex-col gap-1 rounded border border-border p-1.5">
                  <span class="block h-1/2 rounded-sm bg-muted" />
                  <span class="anim-bar-load block h-1 rounded-full bg-muted" :style="{ '--dm-w': '80%', animationDelay: `${i * 0.22}s` }" />
                  <span class="anim-bar-load block h-1 rounded-full bg-muted" :style="{ '--dm-w': '55%', animationDelay: `${i * 0.3}s` }" />
                </div>
              </div>
              <span class="block h-2.5 rounded bg-muted" />
            </div>
          </div>
        </template>

        <template v-else-if="c.anim === 'lab'">
          <div class="relative flex h-full items-center justify-center p-4">
            <span class="absolute left-2 top-2 size-3 border-l-2 border-t-2 border-primary/70" />
            <span class="absolute right-2 top-2 size-3 border-r-2 border-t-2 border-primary/70" />
            <span class="absolute bottom-2 left-2 size-3 border-b-2 border-l-2 border-primary/70" />
            <span class="absolute bottom-2 right-2 size-3 border-b-2 border-r-2 border-primary/70" />

            <span class="anim-scan absolute inset-3" :style="{ '--dm-h': '100%' }">
              <span class="absolute inset-x-0 top-0 h-px bg-primary/80" />
            </span>

            <span class="flex aspect-square h-[76%] items-center justify-center rounded-full border border-dashed border-destructive/50">
              <span class="anim-jiggle">
                <Bug class="size-7 text-destructive sm:size-8" />
              </span>
            </span>
          </div>
        </template>

        <template v-else-if="c.anim === 'ai'">
          <div class="h-full p-2.5">
            <div class="flex h-full flex-col gap-1.5 rounded border border-dashed border-primary/50 bg-primary/[0.04] p-2.5">
              <span class="eyebrow text-[0.5rem] text-primary-text/70">sandbox</span>
              <span
                v-for="(w, i) in ['62%', '84%', '48%', '72%']"
                :key="i"
                class="anim-bar-load block h-1.5 rounded-full"
                :class="i === 1 ? 'bg-primary/40' : 'bg-muted'"
                :style="{ '--dm-w': w, animationDelay: `${i * 0.35}s` }"
              />
              <span class="flex items-center gap-1.5">
                <span class="block h-1.5 w-[30%] rounded-full bg-muted" />
                <span class="anim-blink block h-3 w-1.5 bg-primary" />
              </span>
              <span class="anim-chip mt-auto inline-flex w-fit items-center gap-1 rounded-full border border-primary bg-primary/15 px-2 py-0.5 font-mono text-[0.5rem] text-primary-text">
                exit 0
              </span>
            </div>
          </div>
        </template>

        <template v-else-if="c.anim === 'share'">
          <div class="flex h-full flex-col p-2.5">
            <div class="relative flex flex-1 flex-col rounded border border-border bg-card p-2.5">
              <span class="block h-1.5 w-[55%] rounded-full bg-foreground/30" />
              <span class="mt-1 block h-1 w-[35%] rounded-full bg-muted" />

              <span class="anim-chip absolute right-2 top-2 rounded-sm border border-primary bg-primary/15 px-1.5 py-0.5 font-mono text-[0.5rem] font-semibold text-primary-text">
                $120M
              </span>

              <div class="relative mt-auto flex h-[55%] items-end gap-1.5">
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
            <div class="mt-2 flex justify-center gap-1">
              <span class="size-1 rounded-full bg-primary" />
              <span class="size-1 rounded-full bg-border" />
              <span class="size-1 rounded-full bg-border" />
            </div>
          </div>
        </template>

        <template v-else-if="c.anim === 'desktop'">
          <div class="relative h-full bg-gradient-to-br from-primary/20 via-background to-muted">
            <div class="flex items-center justify-center gap-1 border-b border-border/60 bg-foreground/[0.06] px-2 py-1">
              <span class="h-1 w-4 rounded-full bg-foreground/25" />
            </div>

            <div class="absolute left-1.5 top-6 flex flex-col gap-1.5">
              <span
                v-for="i in 3"
                :key="i"
                class="anim-float block size-4 rounded-md border border-border bg-card"
                :style="{ animationDelay: `${i * 0.4}s` }"
              />
            </div>

            <div class="absolute bottom-4 left-8 right-3 top-9 overflow-hidden rounded border border-border bg-card shadow-lg">
              <div class="flex items-center gap-1 border-b border-border px-1.5 py-1">
                <span class="size-1 rounded-full bg-border" />
                <span class="size-1 rounded-full bg-border" />
                <span class="ml-1 h-1 w-6 rounded-full bg-muted" />
              </div>
              <div class="space-y-1.5 p-2">
                <span
                  v-for="(w, i) in ['70%', '45%', '82%']"
                  :key="i"
                  class="anim-bar-load block h-1.5 rounded-full bg-muted"
                  :style="{ '--dm-w': w, animationDelay: `${i * 0.3}s` }"
                />
              </div>
            </div>

            <span class="anim-cursor absolute inset-0">
              <MousePointer2 class="size-4 fill-primary text-primary drop-shadow" />
            </span>
            <span class="anim-click absolute left-[58%] top-[34%] -ml-2 -mt-2 size-5 rounded-full border-2 border-primary" />
          </div>
        </template>

        <template v-else>
          <div class="relative h-full p-2.5">
            <div class="grid h-full grid-cols-6 grid-rows-6 gap-1">
              <span
                v-for="(m, i) in matrix"
                :key="i"
                class="anim-pulse-dot rounded-sm"
                :class="m.lit ? 'bg-primary/70' : 'bg-muted'"
                :style="{ animationDelay: m.delay }"
              />
            </div>
            <span class="absolute inset-0 flex items-center justify-center">
              <span class="flex size-11 items-center justify-center rounded-full border border-border bg-background shadow-sm sm:size-12">
                <component
                  :is="ico"
                  v-for="(ico, i) in cycleIcons"
                  :key="i"
                  class="anim-cycle absolute size-5 text-primary-text"
                  :style="{ animationDelay: `${i * 1.08}s` }"
                />
              </span>
            </span>
          </div>
        </template>
      </div>

      <div class="mt-4 flex items-center justify-between">
        <component :is="c.icon" aria-hidden="true" class="size-5 text-primary-text" />
        <span class="eyebrow text-muted-foreground/50" aria-hidden="true">{{ c.id }}</span>
      </div>
      <h3 class="mt-2.5 text-base font-semibold tracking-tight sm:text-lg">
        {{ c.title }}
      </h3>
      <p class="mt-1.5 text-sm leading-relaxed text-muted-foreground">
        {{ c.body }}
      </p>
    </li>
  </ul>
</template>
