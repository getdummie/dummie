// https://nuxt.com/docs/api/configuration/nuxt-config
import tailwindcss from '@tailwindcss/vite'

export default defineNuxtConfig({
  compatibilityDate: '2025-07-15',
  devtools: { enabled: true },
  css: ['~/assets/css/tailwind.css'],

  // Static SPA: no server-rendered pages. `nuxt generate` emits a static SPA.
  ssr: false,

  runtimeConfig: {
    public: {
      // Relative by default: the app is served through the Echo proxy, so
      // `/api/v1/*` resolves on the same origin.
      apiBase: '',
      // Access-token lifetime (minutes); the client refreshes shortly before this.
      // Keep in sync with the server's ACCESS_TOKEN_EXPIRY_MINS.
      accessTokenMins: 5,
    },
  },

  vite: {
    plugins: [
      tailwindcss(),
    ],
  },

  modules: ['shadcn-nuxt', '@nuxtjs/color-mode'],

  colorMode: {
    // Toggle the `.dark` class the CSS expects (no `-mode` suffix), default to
    // following the OS ("system"), fall back to dark when it can't be detected.
    classSuffix: '',
    preference: 'system',
    fallback: 'dark',
  },

  shadcn: {
    /**
     * Prefix for all the imported component.
     * @default "Ui"
     */
    prefix: '',
    /**
     * Directory that the component lives in.
     * Will respect the Nuxt aliases.
     * @link https://nuxt.com/docs/api/nuxt-config#alias
     * @default "@/components/ui"
     */
    componentDir: '@/components/ui'
  }
})
