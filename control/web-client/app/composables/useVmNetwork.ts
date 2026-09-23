import type { Ref } from 'vue'
import type { DeniedAttempt, TargetRecord } from '@/lib/targets'
import { deniedKey, tempAllowPayload } from '@/lib/targets'

const deniedWindowSeconds = 3600
const deniedRefreshMs = 15_000

export function useVmNetwork(vmId: Ref<string>) {
  const { authFetch } = useAuth()

  const targets = ref<TargetRecord[]>([])
  const denied = ref<DeniedAttempt[]>([])
  const deniedAvailable = ref(true)
  const loaded = ref(false)
  let timer: ReturnType<typeof setInterval> | undefined

  async function loadTargets() {
    if (document.hidden) return
    const res = await authFetch(`/vms/${vmId.value}/targets`)
    if (res.ok) targets.value = (await res.json()).items ?? []
  }

  async function loadDenied() {
    if (document.hidden) return
    try {
      const res = await authFetch(`/vms/${vmId.value}/denied?seconds=${deniedWindowSeconds}`)
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const data = await res.json()
      denied.value = data.items ?? []
      deniedAvailable.value = data.available ?? false
    }
    catch {
      deniedAvailable.value = false
    }
  }

  const allowing = ref<string | null>(null)
  const allowError = ref<string | null>(null)

  async function allow(d: DeniedAttempt) {
    allowing.value = deniedKey(d)
    allowError.value = null
    try {
      const res = await authFetch(`/vms/${vmId.value}/targets`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(tempAllowPayload(d)),
      })
      if (!res.ok && res.status !== 409) throw new Error(`HTTP ${res.status}`)
      await loadTargets()
    }
    catch {
      allowError.value = `Could not allow ${d.domain || d.address}`
    }
    finally {
      allowing.value = null
    }
  }

  const refresh = () => Promise.allSettled([loadTargets(), loadDenied()])
  const refreshing = ref(false)

  function startTimer() {
    clearInterval(timer)
    timer = setInterval(refresh, deniedRefreshMs)
  }

  async function refreshNow() {
    if (refreshing.value) return
    refreshing.value = true
    await Promise.all([refresh(), new Promise(r => setTimeout(r, 400))])
    refreshing.value = false
    startTimer()
  }

  onMounted(async () => {
    await refresh()
    loaded.value = true
    startTimer()
  })

  onBeforeUnmount(() => clearInterval(timer))

  return { targets, denied, deniedAvailable, loaded, allowing, allowError, allow, refreshing, refreshNow }
}
