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
        section: z.string(),
      }),
    }),
  },
})
