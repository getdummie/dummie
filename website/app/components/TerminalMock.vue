<script setup lang="ts">
// Static, decorative CLI transcript in the Oxide "monodraw" spirit.
const lines = [
  { kind: 'cmd', text: 'dummie run ./agent.py --net=egress-only' },
  { kind: 'muted', text: 'resolving image  python:3.13-slim' },
  { kind: 'ok', text: 'sandbox sbx_7f3a booted in 142ms' },
  { kind: 'muted', text: 'mount /workspace  ro=false  quota=2GiB' },
  { kind: 'out', text: '> executing agent.py (pid 1) in isolated vm' },
  { kind: 'out', text: '> tool call: read_file("data.csv")  -> 4.2KB' },
  { kind: 'ok', text: 'run finished  exit=0  wall=1.83s  peak_mem=61MB' },
  { kind: 'muted', text: 'snapshot saved  snap_a19c  (restore in <200ms)' },
] as const
</script>

<template>
  <!-- This is a picture of a terminal, not a terminal. Exposing it as one image
       with a summary beats making a screen-reader user wade through eight lines
       of fake shell output to reach the rest of the hero. (WCAG 1.1.1) -->
  <div
    role="img"
    aria-label="Screenshot of a terminal: running an agent script in a dummie sandbox, which boots in 142 milliseconds, executes in an isolated VM, exits cleanly, and saves a snapshot."
    class="overflow-hidden rounded-lg border border-border bg-card shadow-2xl shadow-black/20"
  >
    <!-- title bar -->
    <div class="flex items-center gap-2 border-b border-border bg-secondary/40 px-4 py-2.5">
      <div class="flex gap-1.5">
        <span class="size-2.5 rounded-full bg-border" />
        <span class="size-2.5 rounded-full bg-border" />
        <span class="size-2.5 rounded-full bg-primary/70" />
      </div>
      <span class="ml-2 font-mono text-xs text-muted-foreground">~/dummie — sandbox</span>
    </div>
    <!-- body -->
    <div class="space-y-1.5 p-4 font-mono text-[13px] leading-relaxed sm:p-5">
      <div v-for="(l, i) in lines" :key="i" class="flex gap-2">
        <span class="select-none text-primary-text" :class="l.kind === 'cmd' ? 'opacity-100' : 'opacity-0'">$</span>
        <span
          class="min-w-0 break-all"
          :class="{
            'text-foreground': l.kind === 'cmd',
            'text-muted-foreground': l.kind === 'muted' || l.kind === 'out',
            'text-primary-text': l.kind === 'ok',
          }"
        >
          <span v-if="l.kind === 'ok'" class="mr-1">[OK]</span>{{ l.text }}
        </span>
      </div>
      <div class="flex gap-2 pt-1">
        <span class="select-none text-primary-text">$</span>
        <span class="inline-block h-4 w-2 animate-pulse bg-primary/80" />
      </div>
    </div>
  </div>
</template>
