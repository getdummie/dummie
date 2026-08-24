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

  // The opposite of the console, which is a static SPA: everything here is
  // marketing and prose, so it is prerendered to real HTML at build time and
  // served as files. crawlLinks walks from `/` so every case study and doc page
  // gets its own document without being listed here.
  nitro: {
    prerender: {
      crawlLinks: true,
      routes: ['/', '/404.html'],
    },
  },

  runtimeConfig: {
    public: {
      // Where this deployment's control plane lives. Every link into the console
      // is built from this, so it ends up baked into the prerendered HTML --
      // which is the point: they are real hyperlinks, not redirects, so hovering
      // one shows where it actually goes.
      //
      // Read from CONSOLE_URL rather than the NUXT_PUBLIC_ form so that one
      // variable name works everywhere: .env here, the build, and the container.
      // The production image bakes a sentinel instead and substitutes it at
      // container start (see docker-entrypoint.d/10-console-url.sh), so one
      // built image still serves any deployment without a rebuild.
      //
      // Trailing slash stripped: `.../` would build `https://host//signin`.
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
    // Nuxt runs under bun here, so `bun:sqlite` is the connector that exists;
    // the default one is a native node module this image cannot build.
    experimental: { sqliteConnector: 'bun' },
    build: {
      markdown: {
        // Both themes are emitted at once (the dark one as `--shiki-dark` CSS
        // vars) so code blocks follow the site's toggle without a re-highlight.
        highlight: {
          theme: { default: 'github-light', dark: 'github-dark' },
          // Only the languages named here get bundled; python is not in the
          // default set, so its blocks would render unhighlighted.
          langs: ['python', 'sh', 'json', 'yaml', 'ts', 'vue', 'md', 'go', 'nix'],
        },
      },
    },
  },

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
