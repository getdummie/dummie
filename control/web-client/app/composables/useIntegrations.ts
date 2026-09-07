export interface Integration {
  id: string
  name: string
  kind: string
  account: string
  connected: boolean
  suspended: boolean
  all_repos: boolean
  all_reposable: boolean
  readonly: boolean
  repo_count: number
  vm_count: number
  repos?: string[]
  created_at: string
}

export interface IntegrationVM {
  id: string
  name: string
  ip: string
}

export interface OwnedVM {
  id: string
  name: string
  status: string
  url: string
}

// integrationHost is what a guest clones from. The fleet domain is the VM's own
// hostname minus its name, the same derivation the VM page uses.
export function integrationHost(vm: OwnedVM | null | undefined): string {
  if (!vm?.url) return ''
  let host: string
  try {
    host = new URL(vm.url).hostname
  }
  catch {
    return ''
  }
  const tld = vm.name && host.startsWith(`${vm.name}.`) ? host.slice(vm.name.length + 1) : host
  return tld ? `github.int.${tld}` : ''
}

export function useIntegrations() {
  const { authFetch } = useAuth()

  async function message(res: Response): Promise<string | null> {
    try {
      const b = await res.json()
      return typeof b?.message === 'string' ? b.message : null
    }
    catch {
      return null
    }
  }

  async function request<T>(path: string, init?: RequestInit): Promise<T> {
    const res = await authFetch(path, init)
    if (!res.ok) throw new Error((await message(res)) || `HTTP ${res.status}`)
    if (res.status === 204) return undefined as T
    return await res.json() as T
  }

  function json(method: string, body: unknown): RequestInit {
    return { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }
  }

  return {
    list: () => request<{ items: Integration[] }>('/integrations').then(d => d.items ?? []),
    get: (id: string) => request<Integration>(`/integrations/${id}`),
    create: (name: string) => request<Integration>('/integrations', json('POST', { name })),
    update: (id: string, patch: { all_repos: boolean, readonly: boolean }) =>
      request<void>(`/integrations/${id}`, json('PATCH', patch)),
    remove: (id: string) => request<void>(`/integrations/${id}`, { method: 'DELETE' }),

    installURL: (id: string) =>
      request<{ url: string }>(`/integrations/${id}/github/install`).then(d => d.url),
    availableRepos: (id: string) =>
      request<{ items: string[] }>(`/integrations/${id}/available-repos`).then(d => d.items ?? []),
    setRepos: (id: string, repos: string[]) =>
      request<void>(`/integrations/${id}/repos`, json('PUT', { repos })),

    attachedVMs: (id: string) =>
      request<{ items: IntegrationVM[] }>(`/integrations/${id}/vms`).then(d => d.items ?? []),
    attach: (id: string, vmID: string) =>
      request<void>(`/integrations/${id}/vms/${vmID}`, { method: 'POST' }),
    detach: (id: string, vmID: string) =>
      request<void>(`/integrations/${id}/vms/${vmID}`, { method: 'DELETE' }),

    ownedVMs: () =>
      request<{ items: OwnedVM[] }>('/vms?limit=200').then(d => d.items ?? []),
  }
}
