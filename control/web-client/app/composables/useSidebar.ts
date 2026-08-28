import { useLocalStorage } from '@vueuse/core'

export function useSidebar() {
  const expanded = useLocalStorage('dummie:sidebar-expanded', false)

  function toggle() {
    expanded.value = !expanded.value
  }

  return { expanded, toggle }
}
