import { safeRedirect } from '@/lib/redirect'

export default defineNuxtRouteMiddleware(async (to) => {
  const auth = useAuth()
  await auth.ready()
  if (auth.isAuthenticated.value) {
    const back = safeRedirect(to.query.redirect)
    if (back) return navigateTo(back, { external: true })
    return navigateTo('/dashboard')
  }
})
