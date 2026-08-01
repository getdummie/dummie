import { useLocalStorage } from '@vueuse/core'

/**
 * Expanded/collapsed state of the app rail. Shared between the layout (which
 * offsets the page) and the sidebar itself, and remembered across reloads.
 */
export function useSidebar() {
  const expanded = useLocalStorage('dummie:sidebar-expanded', false)

  function toggle() {
    expanded.value = !expanded.value
  }

  return { expanded, toggle }
}
