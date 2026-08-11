import { safeRedirect } from '@/lib/redirect'

export default defineNuxtRouteMiddleware(async (to) => {
  const auth = useAuth()
  await auth.ready()
  if (auth.isAuthenticated.value) {
    // An already-signed-in visitor who lands here with a ?redirect (a back
    // button during the proxy hand-off, say) wants that, not the dashboard.
    const back = safeRedirect(to.query.redirect)
    if (back) return navigateTo(back, { external: true })
    return navigateTo('/dashboard')
  }
})
