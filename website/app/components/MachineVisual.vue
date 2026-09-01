<script setup lang="ts">
import {
  BatteryFull,
  ChevronLeft,
  ChevronRight,
  Code2,
  Folder,
  Globe,
  Lock,
  Terminal,
  Volume2,
  Wifi,
} from '@lucide/vue'

const dock = [Globe, Terminal, Folder, Code2]

const term = [
  { text: '$ bun dev', class: 'text-white/85' },
  { text: 'ready in 412 ms', class: 'text-white/40' },
  { text: '➜ local  :5173', class: 'text-primary' },
]
</script>

<template>
  <div
    role="img"
    aria-label="A Linux desktop running on dummie: a dock of apps, a browser window with a website open at my-app.dummie.app, and a terminal running a dev server."
    class="overflow-hidden rounded-lg border border-border bg-card p-2 shadow-2xl shadow-black/20 sm:p-3"
  >
    <div class="relative aspect-[16/10] overflow-hidden rounded-md bg-gradient-to-br from-primary/25 via-background to-secondary">
      <div class="flex items-center justify-between border-b border-border/50 bg-foreground/[0.07] px-2 py-1 font-mono text-[0.5rem] text-foreground/60">
        <span>Activities</span>
        <span class="font-semibold text-foreground/75">09:24</span>
        <span class="flex items-center gap-1">
          <Wifi class="size-2" />
          <Volume2 class="size-2" />
          <BatteryFull class="size-2.5" />
        </span>
      </div>

      <div class="absolute left-1 top-7 flex flex-col gap-1.5">
        <span
          v-for="(App, i) in dock"
          :key="i"
          class="anim-float flex size-6 items-center justify-center rounded-md border border-border bg-card/90 shadow-sm"
          :style="{ animationDelay: `${i * 0.35}s` }"
        >
          <component :is="App" class="size-3 text-primary-text" />
        </span>
      </div>

      <div class="absolute left-[13%] top-[13%] w-[60%] overflow-hidden rounded-md border border-border bg-background shadow-xl">
        <div class="flex items-center gap-1.5 bg-secondary px-2 pt-1.5">
          <span class="flex items-center gap-1 rounded-t-sm bg-background px-1.5 py-1 text-[0.5rem] text-foreground/70">
            <Globe class="size-2 text-primary-text" />
            My app
          </span>
        </div>
        <div class="flex items-center gap-1 border-b border-border bg-background px-2 py-1">
          <ChevronLeft class="size-2.5 text-muted-foreground" />
          <ChevronRight class="size-2.5 text-muted-foreground/40" />
          <span class="ml-0.5 flex flex-1 items-center gap-1 rounded-full bg-muted px-1.5 py-0.5 font-mono text-[0.5rem] text-muted-foreground">
            <Lock class="size-2 text-primary-text" />
            my-app.dummie.app
          </span>
        </div>

        <div class="p-2">
          <div class="flex items-center gap-1.5 pb-1.5">
            <span class="size-2 rounded-sm bg-primary" />
            <span class="text-[0.5rem] font-semibold">Acme</span>
            <span class="ml-auto flex gap-1.5 text-[0.5rem] text-muted-foreground">
              <span>Docs</span>
              <span>Pricing</span>
            </span>
          </div>

          <div class="anim-sweep relative overflow-hidden rounded bg-primary/15 p-2">
            <p class="text-[0.6875rem] font-semibold leading-tight tracking-tight">
              Ship it fast.
            </p>
            <p class="mt-0.5 text-[0.5rem] text-muted-foreground">
              Built this morning.
            </p>
            <span class="mt-1.5 inline-block rounded-sm bg-primary px-1.5 py-0.5 text-[0.5rem] font-medium text-primary-foreground">
              Get started
            </span>
          </div>

          <div class="mt-1.5 grid grid-cols-3 gap-1.5">
            <div v-for="(t, i) in ['Fast', 'Safe', 'Yours']" :key="t" class="rounded border border-border p-1">
              <span class="anim-bar-load mb-1 block h-3 rounded-sm bg-muted" :style="{ '--dm-w': '100%', animationDelay: `${i * 0.24}s` }" />
              <span class="text-[0.5rem] text-muted-foreground">{{ t }}</span>
            </div>
          </div>
        </div>
      </div>

      <div class="absolute bottom-[7%] right-[3%] w-[52%] overflow-hidden rounded-md border border-white/10 bg-[#0B1A1E] shadow-2xl">
        <div class="flex items-center gap-1.5 border-b border-white/10 px-2 py-1">
          <span class="size-1.5 rounded-full bg-white/20" />
          <span class="size-1.5 rounded-full bg-white/20" />
          <span class="size-1.5 rounded-full bg-primary/80" />
          <span class="ml-1 font-mono text-[0.5rem] text-white/40">bash</span>
        </div>
        <div class="space-y-1 p-2 font-mono text-[0.5625rem] leading-tight">
          <p
            v-for="(l, i) in term"
            :key="i"
            class="anim-type truncate"
            :class="l.class"
            :style="{ animationDelay: `${0.6 + i * 0.5}s` }"
          >
            {{ l.text }}
          </p>
          <p class="anim-type flex items-center gap-1 text-white/85" style="animation-delay: 2.1s">
            $ <span class="anim-blink inline-block h-2 w-1.5 bg-primary" />
          </p>
        </div>
      </div>
    </div>
  </div>
</template>
