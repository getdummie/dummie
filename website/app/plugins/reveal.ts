export default defineNuxtPlugin((nuxtApp) => {
  nuxtApp.vueApp.directive('reveal', {
    getSSRProps: () => ({}),
    mounted(el: HTMLElement, binding) {
      if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return

      el.classList.add('reveal')
      if (binding.value) el.style.transitionDelay = `${binding.value}ms`

      const io = new IntersectionObserver(
        (entries) => {
          for (const entry of entries) {
            if (!entry.isIntersecting) continue
            el.classList.add('reveal-in')
            io.disconnect()
          }
        },
        { threshold: 0.12, rootMargin: '0px 0px -6% 0px' },
      )
      io.observe(el)
    },
  })
})
