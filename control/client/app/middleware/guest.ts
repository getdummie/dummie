export default defineNuxtRouteMiddleware(async () => {
  const auth = useAuth()
  await auth.ready()
  if (auth.isAuthenticated.value) {
    return navigateTo('/dashboard')
  }
})
