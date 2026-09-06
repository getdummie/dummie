<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import type { DataTableColumn } from '@/lib/table'
import { cn } from '@/lib/utils'

const props = withDefaults(
  defineProps<{
    label: string
    columns: DataTableColumn[]
    loading?: boolean
    loadingRows?: number
    loadingLabel?: string
    empty?: boolean
    frame?: boolean
    class?: HTMLAttributes['class']
  }>(),
  { loadingRows: 5, frame: true },
)

const edges = [
  '[&_th]:px-3 [&_td]:px-3',
  '[&_th:first-child]:pl-4 [&_td:first-child]:pl-4 sm:[&_th:first-child]:pl-6 sm:[&_td:first-child]:pl-6',
  '[&_th:last-child]:pr-4 [&_td:last-child]:pr-4 sm:[&_th:last-child]:pr-6 sm:[&_td:last-child]:pr-6',
].join(' ')

const rhythm = '[&_th]:h-auto [&_th]:py-3 [&_td]:py-3'

const headings = '[&_th]:font-mono [&_th]:text-xs [&_th]:font-normal [&_th]:tracking-wider [&_th]:uppercase [&_th]:text-muted-foreground'
</script>

<template>
  <div
    :class="cn('overflow-hidden', props.frame && 'rounded-lg border border-border', props.class)"
    aria-live="polite"
    :aria-busy="props.loading"
  >
    <p v-if="props.loading && props.loadingLabel" class="sr-only">{{ props.loadingLabel }}</p>
    <Table :label="props.label" :class="cn(edges, rhythm, headings)">
      <TableHeader :class="cn('bg-muted/40', !props.frame && 'border-t border-border')">
        <TableRow class="hover:bg-transparent">
          <TableHead
            v-for="col in props.columns"
            :key="col.key"
            :class="cn(col.align === 'right' && 'text-right', col.class)"
          >
            {{ col.label }}
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <template v-if="props.loading">
          <TableRow v-for="n in props.loadingRows" :key="n" aria-hidden="true">
            <TableCell v-for="col in props.columns" :key="col.key" :class="col.class"><Skeleton class="h-4 w-full" /></TableCell>
          </TableRow>
        </template>
        <TableEmpty v-else-if="props.empty" :colspan="props.columns.length">
          <slot name="empty" />
        </TableEmpty>
        <slot v-else />
      </TableBody>
    </Table>
  </div>
</template>
