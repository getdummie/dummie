<script setup lang="ts">
import { ArrowLeft, ArrowRight } from '@lucide/vue'
import { Button } from '@/components/ui/button'

const route = useRoute()

const { data: page } = await useAsyncData(`casestudy-${route.path}`, () =>
  queryCollection('casestudies').path(route.path).first(),
)

if (!page.value) {
  throw createError({ statusCode: 404, statusMessage: 'Case study not found', fatal: true })
}

useHead({ title: `dummie — ${page.value.title}` })
useSeoMeta({ description: page.value.description })

const published = computed(() =>
  page.value?.date
    ? new Intl.DateTimeFormat('en', { dateStyle: 'long' }).format(new Date(page.value.date))
    : '',
)

const toc = computed(() => page.value?.body?.toc?.links ?? [])
</script>

<template>
  <article v-if="page">
    <!-- Header -->
    <header class="relative overflow-hidden border-b border-border">
      <div aria-hidden="true" class="pointer-events-none absolute inset-0 bg-grid opacity-60" />
      <div class="relative mx-auto max-w-6xl px-4 py-16 sm:px-6 sm:py-20">
        <NuxtLink
          to="/casestudies"
          class="eyebrow inline-flex items-center gap-2 text-muted-foreground transition-colors hover:text-primary-text"
        >
          <ArrowLeft class="size-3.5" aria-hidden="true" />
          Case studies
        </NuxtLink>
        <p class="eyebrow mt-6 text-primary-text">// {{ page.number }}</p>
        <h1 class="mt-4 max-w-3xl text-balance text-4xl font-semibold leading-[1.08] tracking-tight sm:text-5xl">
          {{ page.title }}
        </h1>
        <p class="mt-6 max-w-2xl text-lg leading-relaxed text-muted-foreground">
          {{ page.description }}
        </p>
        <dl class="mt-8 flex flex-wrap items-center gap-x-6 gap-y-2 font-mono text-xs text-muted-foreground">
          <div class="flex gap-2">
            <dt class="sr-only">Published</dt>
            <dd>{{ published }}</dd>
          </div>
          <div class="flex gap-2">
            <dt class="sr-only">Reading time</dt>
            <dd>{{ page.readingTime }}</dd>
          </div>
          <div v-if="page.topics?.length" class="flex gap-2">
            <dt class="sr-only">Topics</dt>
            <dd>{{ page.topics.join(' · ') }}</dd>
          </div>
        </dl>
      </div>
    </header>

    <!-- Body -->
    <div class="mx-auto max-w-6xl gap-12 px-4 py-14 sm:px-6 lg:grid lg:grid-cols-[minmax(0,1fr)_14rem] lg:py-16">
      <div class="article-prose min-w-0">
        <ContentRenderer :value="page" />
      </div>

      <nav v-if="toc.length" aria-label="On this page" class="mt-12 lg:sticky lg:top-24 lg:mt-0 lg:self-start">
        <p class="eyebrow mb-4 text-muted-foreground/70">On this page</p>
        <ul class="space-y-2.5 border-l border-border">
          <li v-for="l in toc" :key="l.id">
            <a
              :href="`#${l.id}`"
              class="-ml-px block border-l border-transparent pl-4 text-sm text-muted-foreground transition-colors hover:border-primary hover:text-foreground"
            >
              {{ l.text }}
            </a>
          </li>
        </ul>
      </nav>
    </div>

    <!-- CTA -->
    <section aria-labelledby="cs-cta" class="border-t border-border">
      <div class="mx-auto flex max-w-6xl flex-col items-start justify-between gap-6 px-4 py-14 sm:flex-row sm:items-center sm:px-6">
        <div>
          <h2 id="cs-cta" class="text-2xl font-semibold tracking-tight">Run it yourself.</h2>
          <p class="mt-2 text-sm text-muted-foreground">
            Mint a token under Settings and point the script at your own host.
          </p>
        </div>
        <Button as-child size="lg" class="font-mono text-sm">
          <NuxtLink to="/dashboard">Open dashboard <ArrowRight class="size-4" aria-hidden="true" /></NuxtLink>
        </Button>
      </div>
    </section>
  </article>
</template>
