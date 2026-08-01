// In an SPA the browser never reloads, so focus stays wherever the user left it
// — usually on the link they just activated, which no longer exists. Moving it
// to <main> after each navigation restores the behaviour a real page load gives
// for free: the next Tab continues from the top of the new page's content.
//
// `NuxtRouteAnnouncer` (app.vue) handles the *announcement* half of this; this
// plugin handles the *focus* half. (WCAG 2.4.3)
export default defineNuxtPlugin((nuxtApp) => {
  let pending = false

  const router = useRouter()

  router.afterEach((to, from) => {
    // Ignore in-page hash changes and query-only updates: those aren't new
    // pages, and stealing focus mid-interaction is worse than leaving it alone.
    if (to.path === from.path) return
    pending = true
  })

  // `page:finish` fires once the incoming page component has mounted, so the
  // <main> we focus is the one holding the new content.
  nuxtApp.hook('page:finish', () => {
    if (!pending) return
    pending = false

    const main = document.getElementById('main-content')
    if (!main) return

    main.focus({ preventScroll: true })
    window.scrollTo({ top: 0, behavior: 'auto' })
  })
})
