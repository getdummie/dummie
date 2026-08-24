<script setup lang="ts">
import { ArrowRight } from '@lucide/vue'

useHead({ title: 'dummie — case studies' })
useSeoMeta({
  description:
    'Worked examples of running untrusted and AI-generated code on dummie: the API calls, the script, and the things that bite.',
})

const { data: studies } = await useAsyncData('casestudies', () =>
  queryCollection('casestudies')
    .order('number', 'ASC')
    .select('path', 'title', 'description', 'number', 'date', 'readingTime', 'topics')
    .all(),
)
</script>

<template>
  <div>
    <section aria-labelledby="cs-heading" class="relative overflow-hidden border-b border-border">
      <div aria-hidden="true" class="pointer-events-none absolute inset-0 bg-grid opacity-60" />
      <div class="relative mx-auto max-w-6xl px-4 py-16 sm:px-6 sm:py-20">
        <p class="eyebrow mb-5 flex items-center gap-2 text-primary-text">
          <span aria-hidden="true" class="inline-block size-1.5 bg-primary" />
          Case studies
        </p>
        <h1 id="cs-heading" class="max-w-3xl text-balance text-4xl font-semibold leading-[1.05] tracking-tight sm:text-5xl">
          Things people actually build on a throwaway machine.
        </h1>
        <p class="mt-6 max-w-xl text-lg leading-relaxed text-muted-foreground">
          Each one is a complete run: the API calls in order, the script that
          drives them, and the failure modes that are not obvious until the
          second attempt.
        </p>
      </div>
    </section>

    <section aria-label="Case studies">
      <ul v-if="studies?.length" class="mx-auto max-w-6xl divide-y divide-border border-b border-border">
        <li v-for="s in studies" :key="s.path">
          <NuxtLink
            :to="s.path"
            class="group grid gap-4 px-4 py-8 transition-colors hover:bg-muted/40 sm:grid-cols-[4rem_1fr_auto] sm:items-baseline sm:gap-6 sm:px-6"
          >
            <span class="eyebrow text-muted-foreground/70">{{ s.number }}</span>

            <div>
              <h2 class="text-xl font-semibold tracking-tight transition-colors group-hover:text-primary-text sm:text-2xl">
                {{ s.title }}
              </h2>
              <p class="mt-2 max-w-2xl text-sm leading-relaxed text-muted-foreground">
                {{ s.description }}
              </p>
              <ul v-if="s.topics?.length" class="mt-4 flex flex-wrap gap-1.5">
                <li
                  v-for="t in s.topics"
                  :key="t"
                  class="border border-border px-2 py-0.5 font-mono text-[0.7rem] text-muted-foreground"
                >
                  {{ t }}
                </li>
              </ul>
            </div>

            <span class="flex items-center gap-2 font-mono text-xs text-muted-foreground sm:justify-end">
              {{ s.readingTime }}
              <ArrowRight
                class="size-4 transition-transform group-hover:translate-x-0.5 group-hover:text-primary-text"
                aria-hidden="true"
              />
            </span>
          </NuxtLink>
        </li>
      </ul>

      <p v-else class="mx-auto max-w-6xl border-b border-border px-4 py-16 font-mono text-sm text-muted-foreground sm:px-6">
        No case studies yet.
      </p>
    </section>
  </div>
</template>
