<script setup lang="ts">
import { ArrowLeft } from '@lucide/vue'

const route = useRoute()

const { data: page } = await useAsyncData(`docs-${route.path}`, () =>
  queryCollection('docs').path(route.path).first(),
)

if (!page.value) {
  throw createError({ statusCode: 404, statusMessage: 'Page not found', fatal: true })
}

useHead({ title: `dummie — ${page.value.title}` })
useSeoMeta({
  description: page.value.description,
  ogTitle: `dummie — ${page.value.title}`,
  ogDescription: page.value.description,
  ogType: 'article',
})

const toc = computed(() => page.value?.body?.toc?.links ?? [])
</script>

<template>
  <article v-if="page">
    <header class="border-b border-border">
      <div class="mx-auto max-w-6xl px-4 py-10 sm:px-6 sm:py-12">
        <NuxtLink
          to="/docs"
          class="eyebrow inline-flex items-center gap-2 text-muted-foreground transition-colors hover:text-primary-text"
        >
          <ArrowLeft class="size-3.5" aria-hidden="true" />
          Docs
        </NuxtLink>
        <p v-if="page.section" class="eyebrow mt-6 text-primary-text">// {{ page.section }}</p>
        <h1 class="mt-3 max-w-3xl text-balance text-3xl font-semibold leading-[1.1] tracking-tight sm:text-4xl">
          {{ page.title }}
        </h1>
        <p v-if="page.description" class="mt-4 max-w-2xl leading-relaxed text-muted-foreground">
          {{ page.description }}
        </p>
      </div>
    </header>

    <div class="mx-auto max-w-6xl gap-10 px-4 py-12 sm:px-6 lg:grid lg:grid-cols-[13rem_minmax(0,1fr)_12rem]">
      <div class="hidden lg:block">
        <DocsNav />
      </div>

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

      <div class="mt-14 border-t border-border pt-10 lg:hidden">
        <DocsNav />
      </div>
    </div>
  </article>
</template>
