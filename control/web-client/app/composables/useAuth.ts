import { computed, ref } from 'vue'

export interface AuthUser {
  id: string
  username: string
  email: string
  first_name: string
  last_name: string
  user_type: string
}

interface SessionResponse {
  access_token: string
  user: AuthUser
}

export interface SignupPayload {
  username: string
  email: string
  password: string
  first_name: string
  last_name: string
}

// Module-scope singletons: the access token lives ONLY in memory (never
// persisted by JS). The httpOnly refresh cookie — set by the server — is what
// survives reloads and lets us silently re-establish the session.
const user = ref<AuthUser | null>(null)
const accessToken = ref<string | null>(null)
let refreshTimer: ReturnType<typeof setTimeout> | null = null
let readyPromise: Promise<void> | null = null

export function useAuth() {
  const config = useRuntimeConfig()
  const base = config.public.apiBase as string
  const accessMins = Number(config.public.accessTokenMins) || 5

  const isAuthenticated = computed(() => !!user.value)

  function api(path: string, body?: unknown) {
    return fetch(`${base}/api/v1${path}`, {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  }

  function clearTimer() {
    if (refreshTimer) {
      clearTimeout(refreshTimer)
      refreshTimer = null
    }
  }

  // Proactively refresh ~60s before the access token expires (min 30s), so the
  // session never lapses while the tab is open.
  function scheduleRefresh() {
    clearTimer()
    const ms = Math.max(accessMins * 60 - 60, 30) * 1000
    refreshTimer = setTimeout(() => {
      void refresh()
    }, ms)
  }

  function setSession(data: SessionResponse) {
    accessToken.value = data.access_token
    user.value = data.user
    scheduleRefresh()
  }

  function clearSession() {
    clearTimer()
    accessToken.value = null
    user.value = null
  }

  async function signin(identifier: string, password: string) {
    const res = await api('/signin', { identifier, password })
    if (!res.ok) throw new Error((await errorMessage(res)) || 'Invalid credentials')
    setSession(await res.json())
  }

  async function signup(payload: SignupPayload) {
    const res = await api('/signup', payload)
    if (!res.ok) throw new Error((await errorMessage(res)) || 'Could not create account')
    setSession(await res.json())
  }

  async function refresh(): Promise<boolean> {
    try {
      const res = await api('/token_refresh')
      if (!res.ok) {
        clearSession()
        return false
      }
      setSession(await res.json())
      return true
    }
    catch {
      clearSession()
      return false
    }
  }

  // authFetch is for authenticated (e.g. admin) endpoints. Auth rides the
  // httpOnly access-token cookie (credentials: 'include'); on a 401 we refresh
  // once and retry so an expired access cookie is handled transparently.
  async function authFetch(path: string, init: RequestInit = {}): Promise<Response> {
    const url = `${base}/api/v1${path}`
    const opts: RequestInit = { credentials: 'include', ...init }
    let res = await fetch(url, opts)
    if (res.status === 401 && await refresh()) {
      res = await fetch(url, opts)
    }
    return res
  }

  async function signout() {
    try {
      await api('/signout')
    }
    catch {
      // best-effort; clear local state regardless
    }
    clearSession()
    await navigateTo('/signin')
  }

  // init runs once (guarded by readyPromise): attempt a silent refresh so a
  // reload restores the session from the httpOnly cookie before guards run.
  function init(): Promise<void> {
    if (!readyPromise) {
      readyPromise = refresh().then(() => undefined)
    }
    return readyPromise
  }

  return {
    user,
    accessToken,
    isAuthenticated,
    ready: init,
    init,
    signin,
    signup,
    refresh,
    signout,
    authFetch,
  }
}

async function errorMessage(res: Response): Promise<string | null> {
  try {
    const body = await res.json()
    return typeof body?.message === 'string' ? body.message : null
  }
  catch {
    return null
  }
}
