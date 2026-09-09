<script setup lang="ts">
import { Bot, Code2, FlaskConical, Link, Monitor, Sparkles } from '@lucide/vue'

const cases = [
  {
    id: '01',
    slug: 'usecase-web',
    anim: 'web',
    icon: Code2,
    title: 'Web development',
    body: 'VMs booting in seconds.',
    points: [
      'Root access, real ports, no container gymnastics',
      'Keep a machine per project instead of one tangled laptop',
      'Share your creations as a simple link',
      'Let your agent full access without worrying about getting compromised',
      'Clone private repos without having any keys on the VM',
    ],
  },
  {
    id: '02',
    slug: 'usecase-malware',
    anim: 'lab',
    icon: FlaskConical,
    title: 'Malware analysis',
    body: 'Run arbitrary apps, and watch what they reache for',
    points: [
      'Isolated at the hardware boundary, not by a sandbox flag',
      'Network traffic inspected and logged while you work',
      'Destroy it the second you\'re done — nothing persists',
    ],
  },
  {
    id: '03',
    slug: 'usecase-eval',
    anim: 'ai',
    icon: Bot,
    title: 'Sandboxed evaluation',
    body: 'Let your agent run the code it just wrote.',
    points: [
      'A clean machine per run, booted in seconds',
      'Drive it from your own harness over the API',
      'No blast radius beyond the box it ran in',
    ],
  },
  {
    id: '04',
    slug: 'usecase-demos',
    anim: 'share',
    icon: Link,
    title: 'Demos',
    body: 'Build live interactive demos directly on the cloud',
    points: [
      'Sharing presentations is as easy as sharing a link',
      'Works from a browser, accessible to everyone',
    ],
  },
  {
    id: '05',
    slug: 'usecase-desktops',
    anim: 'desktop',
    icon: Monitor,
    title: 'Desktops',
    body: 'A full Linux desktop in a browser tab.',
    points: [
      'Useful when the work needs a GUI',
      'Reach it from any device',
      'Suspend it and pick up exactly where you left off',
      'Files and installed software stay on the machine',
    ],
  },
  {
    id: '06',
    slug: 'usecase-anything',
    anim: 'any',
    icon: Sparkles,
    title: 'Anything else',
    body: 'It\'s a computer with an internet connection and nothing in your way.',
    points: [
      'Bring your own image, or start from ours',
      'Scale the CPU and memory to the job',
      'Boots in seconds',
    ],
  },
]
</script>

<template>
  <section
    v-for="(c, i) in cases"
    :id="i === 0 ? 'usecases' : c.slug"
    :key="c.id"
    :aria-labelledby="`${c.slug}-heading`"
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
          <UseCaseVisual :anim="c.anim" />
        </div>

        <div v-reveal="120" :class="i % 2 === 1 ? 'lg:order-1' : ''">
          <p class="eyebrow flex items-center gap-2.5 text-muted-foreground/60">
            <component :is="c.icon" aria-hidden="true" class="size-4 text-primary-text" />
            {{ c.id }}
          </p>
          <h3
            :id="`${c.slug}-heading`"
            class="mt-5 text-balance text-3xl font-semibold tracking-tight sm:text-4xl xl:text-5xl"
          >
            {{ c.title }}
          </h3>
          <p class="mt-6 max-w-xl text-base leading-relaxed text-muted-foreground sm:text-lg">
            {{ c.body }}
          </p>
          <ul class="mt-8 max-w-xl space-y-4 border-t border-border pt-8">
            <li
              v-for="p in c.points"
              :key="p"
              class="flex items-start gap-3.5 font-mono text-sm leading-relaxed text-muted-foreground"
            >
              <span aria-hidden="true" class="mt-2.5 h-px w-6 shrink-0 bg-primary" />
              {{ p }}
            </li>
          </ul>
        </div>
      </div>
    </div>
  </section>
</template>
