<script setup lang="ts">
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { TableCell, TableRow } from '@/components/ui/table'
import { fmtCost, fmtTokens, monthOptions, type UsageReport, type UsageSource, type UsageTotals } from '@/lib/llm-usage'
import type { DataTableColumn } from '@/lib/table'

const props = defineProps<{ endpoint: string, month?: string, source?: UsageSource, selectableSource?: boolean }>()

const { authFetch } = useAuth()
const months = monthOptions()
const month = ref(months.some(m => m.value === props.month) ? props.month! : months[0]!.value)
const source = ref<UsageSource>(props.source ?? (props.selectableSource ? 'all' : 'global'))
type Breakdown = 'vm' | 'model' | 'day'
type BreakdownRow = UsageTotals & { key: string, label: string, title?: string }
const breakdown = ref<Breakdown>('vm')
const report = ref<UsageReport | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)
let activeLoad = 0

async function load() {
  const id = ++activeLoad
  loading.value = true
  error.value = null
  try {
    const params = new URLSearchParams({ month: month.value })
    if (props.selectableSource || props.source) params.set('source', source.value)
    const res = await authFetch(`${props.endpoint}?${params}`)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const body: UsageReport = await res.json()
    if ((props.selectableSource || props.source) && body.source !== source.value) {
      throw new Error('This server does not support the selected usage view yet')
    }
    if (id === activeLoad) {
      report.value = body
      if (!body.by_vm && breakdown.value === 'vm') breakdown.value = 'model'
    }
  }
  catch (e) {
    if (id === activeLoad) error.value = e instanceof Error ? e.message : 'Could not load usage'
  }
  finally {
    if (id === activeLoad) loading.value = false
  }
}
onMounted(load)
watch([month, source], load)

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
const hasUnpriced = computed(() => !!report.value?.totals.unpriced)
const modelPrefix = (provider: string, rowSource?: 'personal' | 'global') => `${provider}${rowSource === 'personal' ? '' : '@global'}`
const breakdownLabel = computed(() => ({ vm: 'VM', model: 'Model', day: 'Day' })[breakdown.value])
const breakdownColumns = computed(() => columns({ key: 'item', label: breakdownLabel.value }))
const breakdownRows = computed<BreakdownRow[]>(() => {
  if (!report.value) return []
  switch (breakdown.value) {
    case 'vm':
      return (report.value.by_vm ?? []).map(vm => ({
        ...vm,
        key: vm.vm_id,
        label: vm.vm_name || `VM ${vm.vm_id.slice(0, 8)}`,
        title: vm.vm_id,
      }))
    case 'model':
      return report.value.by_model.map(model => ({
        ...model,
        key: `${model.source ?? 'global'}/${model.provider}/${model.model}`,
        label: `${modelPrefix(model.provider, model.source)}/${model.model}`,
      }))
    case 'day':
      return report.value.by_day.map(day => ({ ...day, key: day.date, label: day.date }))
  }
})
const monthId = 'llm-usage-month'
const sourceId = 'llm-usage-source'
const breakdownId = 'llm-usage-breakdown'
</script>

<template>
  <div>
    <div class="flex flex-wrap items-end gap-3">
      <div v-if="selectableSource" class="space-y-1.5">
        <Label :for="sourceId">Integrations</Label>
        <NativeSelect :id="sourceId" v-model="source">
          <NativeSelectOption value="all">All integrations</NativeSelectOption>
          <NativeSelectOption value="personal">Personal integrations</NativeSelectOption>
          <NativeSelectOption value="global">Global integrations</NativeSelectOption>
        </NativeSelect>
      </div>
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

    <div class="mt-8 space-y-1.5">
      <Label :for="breakdownId">Break down by</Label>
      <NativeSelect :id="breakdownId" v-model="breakdown">
        <NativeSelectOption value="vm">VM</NativeSelectOption>
        <NativeSelectOption value="model">Model</NativeSelectOption>
        <NativeSelectOption value="day">Day</NativeSelectOption>
      </NativeSelect>
    </div>
    <DataTable
      :label="`Usage by ${breakdownLabel}`"
      :columns="breakdownColumns"
      :loading="loading"
      :loading-rows="2"
      :empty="!breakdownRows.length"
      class="mt-3"
    >
      <template #empty>No usage this month.</template>
      <TableRow v-for="row in breakdownRows" :key="row.key">
        <TableCell class="font-mono text-xs" :title="row.title">{{ row.label }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ row.requests.toLocaleString() }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtTokens(row.input_tokens) }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtTokens(row.cache_read_tokens) }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtTokens(row.cache_write_tokens) }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtTokens(row.output_tokens) }}</TableCell>
        <TableCell class="text-right tabular-nums">{{ fmtCost(row, true) }}</TableCell>
      </TableRow>
    </DataTable>

    <p class="mt-4 text-xs text-muted-foreground">
      Cost is at pay-as-you-go prices from LiteLLM's price list, even for a coding plan key, where
      the real cost is the subscription. Days and months are UTC; new usage can take a minute to appear.
      VM names reflect their current names; deleted VMs are shown by ID.
      <template v-if="hasUnpriced"> * Some models have no known price and are left out of the cost.</template>
    </p>
  </div>
</template>
