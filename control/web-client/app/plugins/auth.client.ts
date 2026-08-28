export default defineNuxtPlugin(async () => {
  await useAuth().init()
})
