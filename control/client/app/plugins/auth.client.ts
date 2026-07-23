// Runs before route middleware on initial load. Awaiting init() means the
// silent refresh (via the httpOnly cookie) completes before any guard decides
// whether the user is authenticated.
export default defineNuxtPlugin(async () => {
  await useAuth().init()
})
