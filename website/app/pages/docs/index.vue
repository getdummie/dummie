<script setup lang="ts">
import { ArrowRight, BookOpen } from '@lucide/vue'

const { consoleUrl } = useRuntimeConfig().public

const description = 'Self-host dummie and see how the control plane, hosts and microVMs fit together.'

useHead({ title: 'dummie — docs' })
useSeoMeta({
  description,
  ogTitle: 'dummie — docs',
  ogDescription: description,
})

const { data: pages } = await useAsyncData('docs-index', () =>
  queryCollection('docs')
    .order('stem', 'ASC')
    .select('path', 'title', 'description', 'section')
    .all(),
)

const groups = computed(() => {
  const out: { section: string, pages: { path: string, title: string, description?: string }[] }[] = []
  for (const p of pages.value ?? []) {
    const section = p.section || 'Docs'
    let group = out.find(g => g.section === section)
    if (!group) {
      group = { section, pages: [] }
      out.push(group)
    }
    group.pages.push({ path: p.path, title: p.title, description: p.description })
  }
  return out
})
</script>

<template>
  <div>
    <section aria-labelledby="docs-heading" class="relative overflow-hidden border-b border-border">
      <div aria-hidden="true" class="pointer-events-none absolute inset-0 bg-grid opacity-60" />
      <div class="relative mx-auto max-w-6xl px-4 py-16 sm:px-6 sm:py-20">
        <p class="eyebrow mb-5 flex items-center gap-2 text-primary-text">
          <span aria-hidden="true" class="inline-block size-1.5 bg-primary" />
          Documentation
        </p>
        <h1 id="docs-heading" class="max-w-3xl text-balance text-4xl font-semibold leading-[1.05] tracking-tight sm:text-5xl">
          Run it yourself, on your own hardware.
        </h1>
        <p class="mt-6 max-w-xl text-lg leading-relaxed text-muted-foreground">
          dummie is open source and self-hostable end to end. These pages cover
          standing it up, how the pieces fit together, and the concepts the API
          is built around.
        </p>
        <div class="mt-8">
          <a
            :href="`${consoleUrl}/docs`"
            class="inline-flex items-center gap-2 font-mono text-sm text-primary-text underline-offset-4 hover:underline"
          >
            <BookOpen class="size-4" aria-hidden="true" />
            API reference
            <ArrowRight class="size-4" aria-hidden="true" />
          </a>
        </div>
      </div>
    </section>

    <section aria-label="Documentation pages">
      <div v-if="groups.length" class="mx-auto max-w-6xl px-4 py-14 sm:px-6">
        <div v-for="g in groups" :key="g.section" class="mb-12 last:mb-0">
          <h2 class="eyebrow mb-5 text-muted-foreground/70">{{ g.section }}</h2>
          <ul class="grid gap-px border border-border bg-border sm:grid-cols-2">
            <li v-for="p in g.pages" :key="p.path">
              <NuxtLink :to="p.path" class="group block h-full bg-background p-6 transition-colors hover:bg-muted/40">
                <h3 class="text-lg font-semibold tracking-tight transition-colors group-hover:text-primary-text">
                  {{ p.title }}
                </h3>
                <p v-if="p.description" class="mt-2 text-sm leading-relaxed text-muted-foreground">
                  {{ p.description }}
                </p>
              </NuxtLink>
            </li>
          </ul>
        </div>
      </div>

      <p v-else class="mx-auto max-w-6xl px-4 py-16 font-mono text-sm text-muted-foreground sm:px-6">
        No documentation yet.
      </p>
    </section>
  </div>
</template>
