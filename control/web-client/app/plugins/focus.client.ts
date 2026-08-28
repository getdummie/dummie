export default defineNuxtPlugin((nuxtApp) => {
  let pending = false

  const router = useRouter()

  router.afterEach((to, from) => {
    if (to.path === from.path) return
    pending = true
  })

  nuxtApp.hook('page:finish', () => {
    if (!pending) return
    pending = false

    const main = document.getElementById('main-content')
    if (!main) return

    main.focus({ preventScroll: true })
    window.scrollTo({ top: 0, behavior: 'auto' })
  })
})
