<script setup lang="ts">
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { TableCell, TableRow } from '@/components/ui/table'
import { fmtCost, fmtTokens, monthOptions, type UsageByUser } from '@/lib/llm-usage'
import type { DataTableColumn } from '@/lib/table'

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · llm usage' })

const columns: DataTableColumn[] = [
  { key: 'user', label: 'User' },
  { key: 'requests', label: 'Requests', align: 'right' },
  { key: 'in', label: 'In', align: 'right' },
  { key: 'cache_read', label: 'Cache read', align: 'right' },
  { key: 'cache_write', label: 'Cache write', align: 'right' },
  { key: 'out', label: 'Out', align: 'right' },
  { key: 'cost', label: 'Cost', align: 'right' },
]

const { authFetch } = useAuth()
const months = monthOptions()
const month = ref(months[0]!.value)
const items = ref<UsageByUser[]>([])
const available = ref(true)
const loading = ref(true)
const error = ref<string | null>(null)

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await authFetch(`/admin/llm-usage?month=${month.value}`)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const body = await res.json()
    items.value = body.items ?? []
    available.value = !!body.available
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not load usage'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)
watch(month, load)
</script>

<template>
  <AdminShell>
    <div class="mt-8 flex flex-wrap items-end justify-between gap-4">
      <div>
        <p class="eyebrow mb-2 text-primary-text">// admin · llm usage</p>
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">LLM usage</h1>
        <p class="mt-2 max-w-2xl text-sm text-muted-foreground">
          Usage of the global keys, per user. Users' own keys are not tracked.
        </p>
      </div>
      <div class="space-y-1.5">
        <Label for="admin-usage-month">Month</Label>
        <NativeSelect id="admin-usage-month" v-model="month">
          <NativeSelectOption v-for="m in months" :key="m.value" :value="m.value">{{ m.label }}</NativeSelectOption>
        </NativeSelect>
      </div>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Something went wrong</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>
    <Alert v-else-if="!available && !loading" class="mt-6">
      <AlertTitle>Usage is not available</AlertTitle>
      <AlertDescription>Usage is recorded in ClickHouse, which this server cannot read right now.</AlertDescription>
    </Alert>

    <DataTable
      label="LLM usage by user"
      :columns="columns"
      :loading="loading"
      loading-label="Loading usage…"
      :empty="!items.length"
      class="mt-6"
    >
      <template #empty>No usage of the global keys this month.</template>
      <TableRow v-for="u in items" :key="u.user_id">
        <TableCell>
          <NuxtLink :to="`/admin/model/llm-usage/${u.user_id}?month=${month}`" class="font-mono hover:underline">
            {{ u.username || u.user_id }}
          </NuxtLink>
        </TableCell>
        <TableCell class="text-right tabular-nums">{{ u.requests.toLocaleString() }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtTokens(u.input_tokens) }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtTokens(u.cache_read_tokens) }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtTokens(u.cache_write_tokens) }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtTokens(u.output_tokens) }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtCost(u) }}</TableCell>
      </TableRow>
    </DataTable>
  </AdminShell>
</template>
