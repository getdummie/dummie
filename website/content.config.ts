import { defineCollection, defineContentConfig, z } from '@nuxt/content'

export default defineContentConfig({
  collections: {
    casestudies: defineCollection({
      type: 'page',
      source: 'casestudies/*.md',
      schema: z.object({
        number: z.string(),
        date: z.date(),
        readingTime: z.string(),
        topics: z.array(z.string()),
      }),
    }),
    docs: defineCollection({
      type: 'page',
      source: 'docs/**/*.md',
      schema: z.object({
        // Which sidebar group the page sits under, and where in it. Ordering
        // comes from the numeric filename prefix, which @nuxt/content strips
        // from the path, so `stem` sorts the way the directory reads.
        section: z.string(),
      }),
    }),
  },
})
