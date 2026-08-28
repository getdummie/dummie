<script setup lang="ts">
const { data: pages } = await useAsyncData('docs-nav', () =>
  queryCollection('docs')
    .order('stem', 'ASC')
    .select('path', 'title', 'section', 'stem')
    .all(),
)

const groups = computed(() => {
  const out: { section: string, pages: { path: string, title: string }[] }[] = []
  for (const p of pages.value ?? []) {
    const section = p.section || 'Docs'
    let group = out.find(g => g.section === section)
    if (!group) {
      group = { section, pages: [] }
      out.push(group)
    }
    group.pages.push({ path: p.path, title: p.title })
  }
  return out
})
</script>

<template>
  <nav aria-label="Documentation" class="lg:sticky lg:top-24 lg:self-start">
    <ul class="space-y-7">
      <li v-for="g in groups" :key="g.section">
        <p class="eyebrow mb-3 text-muted-foreground/70">{{ g.section }}</p>
        <ul class="space-y-1.5 border-l border-border">
          <li v-for="p in g.pages" :key="p.path">
            <NuxtLink
              :to="p.path"
              class="-ml-px block border-l border-transparent pl-4 text-sm text-muted-foreground transition-colors hover:border-primary hover:text-foreground"
              active-class="!border-primary font-medium !text-foreground"
            >
              {{ p.title }}
            </NuxtLink>
          </li>
        </ul>
      </li>
    </ul>
  </nav>
</template>
