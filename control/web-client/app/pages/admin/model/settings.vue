<script setup lang="ts">
import { TriangleAlert } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · settings' })

type SettingKind = 'bool' | 'string' | 'secret'

interface SettingRow {
  key: string
  kind: SettingKind
  label: string
  description: string
  placeholder?: string
  warn: boolean
  value: string
  is_set: boolean
  updated_at: string
}

const drafts = ref<Record<string, string>>({})

const { authFetch } = useAuth()

const items = ref<SettingRow[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const saving = ref<Record<string, boolean>>({})
const saveError = ref<string | null>(null)
const savedKey = ref<string | null>(null)

async function readMessage(res: Response): Promise<string | null> {
  try {
    const b = await res.json()
    return typeof b?.message === 'string' ? b.message : null
  }
  catch {
    return null
  }
}

function ordinal(n: number) {
  if (n % 100 >= 11 && n % 100 <= 13) return `${n}th`
  return `${n}${['th', 'st', 'nd', 'rd'][n % 10] ?? 'th'}`
}

function fmtDate(s: string) {
  if (!s) return 'never'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  const date = `${ordinal(d.getDate())} ${d.toLocaleString(undefined, { month: 'long' })} ${d.getFullYear()}`
  const time = d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })
  return `${date}, ${time}`
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await authFetch('/admin/settings')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = await res.json()
    items.value = data.items ?? []
    drafts.value = Object.fromEntries(
      items.value.filter(s => s.kind !== 'bool').map(s => [s.key, s.kind === 'secret' ? '' : s.value]),
    )
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load settings'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

async function save(row: SettingRow, value: boolean | string) {
  const previous = row.value
  if (typeof value === 'boolean') row.value = String(value)
  saving.value = { ...saving.value, [row.key]: true }
  saveError.value = null
  savedKey.value = null
  try {
    const res = await authFetch(`/admin/settings/${row.key}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ value }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    const saved = await res.json()
    row.value = saved.value
    row.is_set = saved.is_set
    row.updated_at = saved.updated_at
    if (row.kind === 'secret') drafts.value = { ...drafts.value, [row.key]: '' }
    else if (row.kind === 'string') drafts.value = { ...drafts.value, [row.key]: saved.value }
    savedKey.value = row.key
  }
  catch (e) {
    row.value = previous
    saveError.value = e instanceof Error ? e.message : 'Could not save setting'
  }
  finally {
    const next = { ...saving.value }
    delete next[row.key]
    saving.value = next
  }
}

function isDirty(row: SettingRow) {
  const draft = drafts.value[row.key] ?? ''
  if (row.kind === 'secret') return draft !== ''
  return draft !== row.value
}
</script>

<template>
  <TooltipProvider :delay-duration="150">
    <AdminShell>

      <div class="mt-8">
        <p class="eyebrow mb-2 text-primary-text">// admin · settings</p>
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">Settings</h1>
        <p class="mt-2 max-w-2xl text-sm text-muted-foreground">
          Runtime settings for the control plane. Changes take effect on the next request — no restart.
        </p>
      </div>

      <Alert v-if="error" variant="destructive" class="mt-6">
        <AlertTitle>Could not load settings</AlertTitle>
        <AlertDescription>{{ error }}</AlertDescription>
      </Alert>

      <Alert v-if="saveError" variant="destructive" class="mt-6">
        <AlertTitle>Could not save</AlertTitle>
        <AlertDescription>{{ saveError }}</AlertDescription>
      </Alert>

      <div class="mt-6 divide-y divide-border rounded-lg border border-border" aria-live="polite" :aria-busy="loading">
        <p v-if="loading" class="sr-only">Loading settings…</p>

        <template v-if="loading">
          <div v-for="n in 2" :key="n" class="flex items-start gap-4 p-4" aria-hidden="true">
            <div class="min-w-0 flex-1 space-y-2">
              <Skeleton class="h-4 w-48" />
              <Skeleton class="h-3 w-full max-w-lg" />
            </div>
            <Skeleton class="h-5 w-9 shrink-0 rounded-full" />
          </div>
        </template>

        <p v-else-if="!items.length" class="p-6 text-center text-sm text-muted-foreground">
          No settings to configure.
        </p>

        <div
          v-for="s in items"
          v-else
          :key="s.key"
          class="flex flex-col gap-3 p-4 sm:flex-row sm:items-start sm:gap-4"
        >
          <div class="min-w-0 flex-1">
            <p :id="`setting-${s.key}-label`" class="flex flex-wrap items-center gap-2 text-sm font-medium">
              {{ s.label }}
              <Tooltip v-if="s.warn && s.value === 'true'">
                <TooltipTrigger as-child>
                  <button
                    type="button"
                    class="rounded-sm text-destructive focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                    :aria-label="`Why ${s.label} is flagged`"
                  >
                    <TriangleAlert class="size-4" aria-hidden="true" />
                  </button>
                </TooltipTrigger>
                <TooltipContent class="max-w-xs">
                  Enabled, and it weakens security — see the description below.
                </TooltipContent>
              </Tooltip>
            </p>
            <p :id="`setting-${s.key}-desc`" class="mt-1 max-w-2xl text-sm text-muted-foreground">{{ s.description }}</p>
            <p class="mt-1.5 font-mono text-xs text-muted-foreground">
              {{ s.key }} · changed {{ fmtDate(s.updated_at) }}
              <span v-if="s.kind === 'secret'">· {{ s.is_set ? 'set' : 'not set' }}</span>
            </p>
          </div>

          <Switch
            v-if="s.kind === 'bool'"
            :model-value="s.value === 'true'"
            :disabled="saving[s.key]"
            :aria-labelledby="`setting-${s.key}-label`"
            :aria-describedby="`setting-${s.key}-desc`"
            class="shrink-0 sm:mt-1"
            @update:model-value="(v: boolean) => save(s, v)"
          />

          <form
            v-else
            class="flex w-full shrink-0 gap-2 sm:w-80"
            @submit.prevent="save(s, drafts[s.key] ?? '')"
          >
            <Input
              :id="`setting-${s.key}-input`"
              v-model="drafts[s.key]"
              :type="s.kind === 'secret' ? 'password' : 'text'"
              :placeholder="s.kind === 'secret' && s.is_set ? '••••••••' : s.placeholder"
              :disabled="saving[s.key]"
              :autocomplete="s.kind === 'secret' ? 'new-password' : 'off'"
              spellcheck="false"
              :aria-labelledby="`setting-${s.key}-label`"
              :aria-describedby="`setting-${s.key}-desc`"
              class="min-w-0 flex-1 font-mono text-sm"
            />
            <Button type="submit" variant="secondary" :disabled="saving[s.key] || !isDirty(s)">
              Save
            </Button>
          </form>
        </div>
      </div>

      <AdminOIDCProviders />

      <AdminGitHubApp />

      <p role="status" aria-live="polite" class="sr-only">
        {{ savedKey ? `${savedKey} saved` : '' }}
      </p>
    </AdminShell>
  </TooltipProvider>
</template>
