<script setup lang="ts">
import { Boxes, GitBranch, Network, ShieldCheck, Timer, TerminalSquare } from '@lucide/vue'

const features = [
  {
    id: '01',
    icon: ShieldCheck,
    title: 'Hardware isolation',
    body: 'Every run gets its own microVM. Untrusted and AI-generated code never touches your host or its neighbors.',
  },
  {
    id: '02',
    icon: Timer,
    title: 'Sub-second boot',
    body: 'Cold starts under 150ms. Spin up thousands of sandboxes in parallel and tear them down just as fast.',
  },
  {
    id: '03',
    icon: GitBranch,
    title: 'Fork & snapshot',
    body: 'Freeze a running sandbox and restore it in under 200ms. Branch execution state like you branch code.',
  },
  {
    id: '04',
    icon: Network,
    title: 'Per-run networking',
    body: 'Egress-only, allow-listed, or fully offline. Network policy is scoped to a single run, not the fleet.',
  },
  {
    id: '05',
    icon: TerminalSquare,
    title: 'Full filesystem',
    body: 'A real POSIX filesystem with quotas. Mount your workspace read-write and stream files back out.',
  },
  {
    id: '06',
    icon: Boxes,
    title: 'Any runtime',
    body: 'Bring your own image. Python, Node, Go, or a bespoke toolchain — if it runs in a container, it runs here.',
  },
]
</script>

<template>
  <ul class="grid grid-cols-1 border-l border-t border-border sm:grid-cols-2 lg:grid-cols-3">
    <li
      v-for="f in features"
      :key="f.id"
      class="group relative border-b border-r border-border p-6 transition-colors hover:bg-secondary/40 sm:p-8"
    >
      <div class="mb-5 flex items-center justify-between" aria-hidden="true">
        <component :is="f.icon" class="size-5 text-primary-text" />
        <span class="eyebrow text-muted-foreground/60">{{ f.id }}</span>
      </div>
      <h3 class="mb-2 text-lg font-semibold tracking-tight">
        {{ f.title }}
      </h3>
      <p class="text-sm leading-relaxed text-muted-foreground">
        {{ f.body }}
      </p>
    </li>
  </ul>
</template>
