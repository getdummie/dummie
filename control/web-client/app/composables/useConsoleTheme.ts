export type ThemePreference = 'light' | 'dark' | 'system'

const STORAGE_KEY = 'console-color-mode'

function isPreference(value: unknown): value is ThemePreference {
  return value === 'light' || value === 'dark' || value === 'system'
}

// The console carries its own light/dark choice — people often keep the app in
// light mode but want the terminal dark. It overrides the app-wide class while
// the page is mounted and hands it back on the way out.
export function useConsoleTheme() {
  const colorMode = useColorMode()
  const preference = ref<ThemePreference>('system')
  const systemDark = ref(false)

  const isDark = computed(() =>
    preference.value === 'system' ? systemDark.value : preference.value === 'dark',
  )

  function setPreference(value: ThemePreference) {
    preference.value = value
    if (import.meta.client) localStorage.setItem(STORAGE_KEY, value)
  }

  function apply(dark: boolean) {
    const el = document.documentElement
    el.classList.toggle('dark', dark)
    el.classList.toggle('light', !dark)
  }

  if (import.meta.client) {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (isPreference(stored)) preference.value = stored

    const media = window.matchMedia('(prefers-color-scheme: dark)')
    systemDark.value = media.matches
    const onMediaChange = (e: MediaQueryListEvent) => (systemDark.value = e.matches)
    media.addEventListener('change', onMediaChange)

    watchEffect(() => apply(isDark.value))
    // color-mode reapplies its own class when the app preference resolves
    // differently; take it back.
    watch(() => colorMode.value, () => apply(isDark.value))

    onBeforeUnmount(() => {
      media.removeEventListener('change', onMediaChange)
      apply(colorMode.value === 'dark')
    })
  }

  return { preference, isDark, setPreference }
}
