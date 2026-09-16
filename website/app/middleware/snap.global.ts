// Toggled here rather than with useHead: unhead removes the class after the
// incoming page is in the DOM, and in that gap mandatory snapping grabs the
// footer — the only snap target on a page without .panel sections.
export default defineNuxtRouteMiddleware((to) => {
  if (import.meta.client) {
    document.documentElement.classList.toggle('snap-panels', to.path === '/')
  }
})
