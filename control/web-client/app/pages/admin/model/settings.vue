<script setup lang="ts">
import { TriangleAlert } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · settings' })

interface SettingRow {
  key: string
  label: string
  description: string
  warn: boolean
  value: boolean
  updated_at: string
}

const { authFetch } = useAuth()

const items = ref<SettingRow[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
// Keyed by setting key so two toggles saving at once cannot disable each other.
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

function fmtDate(s: string) {
  if (!s) return 'never'
  const d = new Date(s)
  return Number.isNaN(d.getTime()) ? s : d.toLocaleString()
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await authFetch('/admin/settings')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = await res.json()
    items.value = data.items ?? []
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load settings'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

async function save(row: SettingRow, value: boolean) {
  const previous = row.value
  // Optimistic: the switch has already moved under the pointer, so reverting on
  // failure reads better than snapping back and forth on every success.
  row.value = value
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
    row.updated_at = saved.updated_at
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
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-12 sm:px-6">
    <AdminNav />

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
          <!-- Not a <label>: the switch is a button, which `for` cannot target.
               The Switch points at these two with aria-labelledby/describedby. -->
          <p :id="`setting-${s.key}-label`" class="flex flex-wrap items-center gap-2 text-sm font-medium">
            {{ s.label }}
            <TriangleAlert
              v-if="s.warn && s.value"
              class="size-4 text-destructive"
              aria-label="Weakens security while enabled"
            />
          </p>
          <p :id="`setting-${s.key}-desc`" class="mt-1 max-w-2xl text-sm text-muted-foreground">{{ s.description }}</p>
          <p class="mt-1.5 font-mono text-xs text-muted-foreground">
            {{ s.key }} · changed {{ fmtDate(s.updated_at) }}
          </p>
        </div>

        <Switch
          :model-value="s.value"
          :disabled="saving[s.key]"
          :aria-labelledby="`setting-${s.key}-label`"
          :aria-describedby="`setting-${s.key}-desc`"
          class="shrink-0 sm:mt-1"
          @update:model-value="(v: boolean) => save(s, v)"
        />
      </div>
    </div>

    <!-- The switch's own state change isn't announced as a save confirmation,
         so the outcome gets its own live region. (WCAG 4.1.3) -->
    <p role="status" aria-live="polite" class="sr-only">
      {{ savedKey ? `${savedKey} saved` : '' }}
    </p>
  </div>
</template>
