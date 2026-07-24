export default defineNuxtRouteMiddleware(async () => {
  const auth = useAuth()
  await auth.ready()

  // Anonymous → let them sign in. Logged-in but not an admin → hard 401.
  if (!auth.isAuthenticated.value) {
    return navigateTo('/signin')
  }
  if (auth.user.value?.user_type !== 'admin') {
    throw createError({ statusCode: 401, statusMessage: 'Unauthorized', fatal: true })
  }
})
