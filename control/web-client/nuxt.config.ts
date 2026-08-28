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

  ssr: false,

  runtimeConfig: {
    public: {
      apiBase: '',
      accessTokenMins: 5,
    },
  },

  vite: {
    plugins: [
      tailwindcss(),
    ],
    server: {
      allowedHosts: (process.env.DEV_ALLOWED_HOSTS ?? '')
        .split(',')
        .map(h => h.trim())
        .filter(Boolean),
    },
  },

  modules: ['shadcn-nuxt', '@nuxtjs/color-mode'],

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
