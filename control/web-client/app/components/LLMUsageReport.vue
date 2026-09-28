<script setup lang="ts">
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { TableCell, TableRow } from '@/components/ui/table'
import { fmtCost, fmtTokens, monthOptions, type UsageReport } from '@/lib/llm-usage'
import type { DataTableColumn } from '@/lib/table'

const props = defineProps<{ endpoint: string, month?: string, source?: 'global' | 'personal' }>()

const { authFetch } = useAuth()
const months = monthOptions()
const month = ref(months.some(m => m.value === props.month) ? props.month! : months[0]!.value)
const report = ref<UsageReport | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)

async function load() {
  loading.value = true
  error.value = null
  try {
    const params = new URLSearchParams({ month: month.value })
    if (props.source) params.set('source', props.source)
    const res = await authFetch(`${props.endpoint}?${params}`)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const body: UsageReport = await res.json()
    if (props.source && body.source !== props.source) {
      throw new Error('This server does not support personal usage yet')
    }
    report.value = body
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

const stats = computed(() => {
  const t = report.value?.totals
  if (!t) return []
  return [
    { label: 'Requests', value: t.requests.toLocaleString() },
    { label: 'Tokens in', value: fmtTokens(t.input_tokens) },
    { label: 'Cache read', value: fmtTokens(t.cache_read_tokens) },
    { label: 'Cache write', value: fmtTokens(t.cache_write_tokens) },
    { label: 'Tokens out', value: fmtTokens(t.output_tokens) },
    { label: 'Cost', value: fmtCost(t) },
  ]
})

const columns = (first: DataTableColumn): DataTableColumn[] => [
  first,
  { key: 'requests', label: 'Requests', align: 'right' },
  { key: 'in', label: 'In', align: 'right' },
  { key: 'cache_read', label: 'Cache read', align: 'right' },
  { key: 'cache_write', label: 'Cache write', align: 'right' },
  { key: 'out', label: 'Out', align: 'right' },
  { key: 'cost', label: 'Cost', align: 'right' },
]
const modelColumns = columns({ key: 'model', label: 'Model' })
const dayColumns = columns({ key: 'date', label: 'Day' })

const hasUnpriced = computed(() => !!report.value?.totals.unpriced)
const modelPrefix = (provider: string) => `${provider}${props.source === 'personal' ? '' : '@global'}`
const monthId = computed(() => props.source === 'personal' ? 'personal-usage-month' : 'global-usage-month')
</script>

<template>
  <div>
    <div class="flex flex-wrap items-end gap-3">
      <div class="space-y-1.5">
        <Label :for="monthId">Month</Label>
        <NativeSelect :id="monthId" v-model="month">
          <NativeSelectOption v-for="m in months" :key="m.value" :value="m.value">{{ m.label }}</NativeSelectOption>
        </NativeSelect>
      </div>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Something went wrong</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <Alert v-else-if="report && !report.available && !loading" class="mt-6">
      <AlertTitle>Usage is not available</AlertTitle>
      <AlertDescription>Usage is recorded in ClickHouse, which this server cannot read right now.</AlertDescription>
    </Alert>

    <div class="mt-6 grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6" :aria-busy="loading">
      <template v-if="loading">
        <Skeleton v-for="n in 6" :key="n" class="h-16 w-full" />
      </template>
      <div v-for="s in stats" v-else :key="s.label" class="rounded-lg border border-border p-3">
        <p class="font-mono text-xs uppercase tracking-wider text-muted-foreground">{{ s.label }}</p>
        <p class="mt-1 text-lg font-semibold tabular-nums">{{ s.value }}</p>
      </div>
    </div>

    <h3 class="mt-8 text-sm font-medium">By model</h3>
    <DataTable
      label="Usage by model"
      :columns="modelColumns"
      :loading="loading"
      :loading-rows="2"
      :empty="!report?.by_model.length"
      class="mt-3"
    >
      <template #empty>No usage this month.</template>
      <TableRow v-for="r in report?.by_model" :key="`${r.provider}/${r.model}`">
        <TableCell class="font-mono text-xs">{{ modelPrefix(r.provider) }}/{{ r.model }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ r.requests.toLocaleString() }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtTokens(r.input_tokens) }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtTokens(r.cache_read_tokens) }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtTokens(r.cache_write_tokens) }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtTokens(r.output_tokens) }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtCost(r) }}</TableCell>
      </TableRow>
    </DataTable>

    <h3 class="mt-8 text-sm font-medium">By day</h3>
    <DataTable
      label="Usage by day"
      :columns="dayColumns"
      :loading="loading"
      :loading-rows="3"
      :empty="!report?.by_day.length"
      class="mt-3"
    >
      <template #empty>No usage this month.</template>
      <TableRow v-for="d in report?.by_day" :key="d.date">
        <TableCell class="font-mono text-xs">{{ d.date }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ d.requests.toLocaleString() }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtTokens(d.input_tokens) }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtTokens(d.cache_read_tokens) }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtTokens(d.cache_write_tokens) }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtTokens(d.output_tokens) }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtCost(d) }}</TableCell>
      </TableRow>
    </DataTable>

    <p class="mt-4 text-xs text-muted-foreground">
      Cost is at pay-as-you-go prices from LiteLLM's price list, even for a coding plan key, where
      the real cost is the subscription. Days and months are UTC; new usage can take a minute to appear.
      <template v-if="hasUnpriced"> * Some models have no known price and are left out of the cost.</template>
    </p>
  </div>
</template>
