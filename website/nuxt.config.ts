import tailwindcss from '@tailwindcss/vite'

export default defineNuxtConfig({
  compatibilityDate: '2025-07-15',
  devtools: { enabled: true },
  css: ['~/assets/css/tailwind.css'],

  app: {
    head: {
      htmlAttrs: { lang: 'en' },
      link: [
        { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' },
        { rel: 'shortcut icon', type: 'image/x-icon', href: '/favicon.ico' },
      ],
    },
  },

  nitro: {
    prerender: {
      crawlLinks: true,
      routes: ['/', '/404.html'],
    },
  },

  runtimeConfig: {
    public: {
      consoleUrl: (process.env.CONSOLE_URL || 'http://localhost:1323').replace(/\/+$/, ''),
    },
  },

  modules: ['shadcn-nuxt', '@nuxtjs/color-mode', '@nuxt/content'],

  vite: {
    plugins: [
      tailwindcss(),
    ],
  },

  content: {
    experimental: { sqliteConnector: 'bun' },
    build: {
      markdown: {
        highlight: {
          theme: { default: 'github-light', dark: 'github-dark' },
          langs: ['python', 'sh', 'json', 'yaml', 'ts', 'vue', 'md', 'go', 'nix'],
        },
      },
    },
  },

  colorMode: {
    classSuffix: '',
    preference: 'system',
    fallback: 'dark',
  },

  shadcn: {
    prefix: '',
    componentDir: '@/components/ui'
  }
})
