// https://nuxt.com/docs/api/configuration/nuxt-config
import tailwindcss from '@tailwindcss/vite'

export default defineNuxtConfig({
  compatibilityDate: '2025-07-15',
  devtools: { enabled: true },
  css: ['~/assets/css/tailwind.css'],

  app: {
    head: {
      // Without this the document ships no language, so screen readers fall
      // back to the user's default voice and mispronounce everything.
      htmlAttrs: { lang: 'en' },
      link: [
        // SVG first for browsers that support it; the .ico is the raster fallback.
        { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' },
        { rel: 'shortcut icon', type: 'image/x-icon', href: '/favicon.ico' },
      ],
    },
  },

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
    server: {
      // Echo proxies to this dev server without rewriting Host, so vite sees
      // whatever hostname the browser used and rejects anything it does not
      // know. Reaching the UI under the VMs' domain -- which is what makes a
      // guest same-site with this page, and so what makes the work view's
      // frames able to carry their session cookie -- means naming it here.
      // Comma separated; a leading dot covers every subdomain.
      allowedHosts: (process.env.DEV_ALLOWED_HOSTS ?? '')
        .split(',')
        .map(h => h.trim())
        .filter(Boolean),
    },
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
