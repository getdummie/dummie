<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import type { DataTableColumn } from '@/lib/table'
import { cn } from '@/lib/utils'

const props = withDefaults(
  defineProps<{
    // Names the scroll region and the table for screen readers.
    label: string
    columns: DataTableColumn[]
    loading?: boolean
    loadingRows?: number
    // Sentence read out while rows are loading, e.g. "Loading users…".
    loadingLabel?: string
    empty?: boolean
    // Off when the table already sits inside a bordered card, so the two
    // borders do not stack.
    frame?: boolean
    class?: HTMLAttributes['class']
  }>(),
  { loadingRows: 5, frame: true },
)

// The cell padding shadcn ships (px-2) is narrower than the p-4 / sm:p-6 the
// surrounding cards use, so the first column sits a few pixels inside the card
// title above it and the whole table reads as misaligned. Edge cells are pushed
// out to match the card, inner ones get room to breathe.
const edges = [
  '[&_th]:px-3 [&_td]:px-3',
  '[&_th:first-child]:pl-4 [&_td:first-child]:pl-4 sm:[&_th:first-child]:pl-6 sm:[&_td:first-child]:pl-6',
  '[&_th:last-child]:pr-4 [&_td:last-child]:pr-4 sm:[&_th:last-child]:pr-6 sm:[&_td:last-child]:pr-6',
].join(' ')

const rhythm = '[&_th]:h-auto [&_th]:py-3 [&_td]:py-3'

// Mono, uppercase and muted, matching the `eyebrow` labels used elsewhere, so
// a header row is never mistaken for a data row.
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
            <TableCell v-for="col in props.columns" :key="col.key"><Skeleton class="h-4 w-full" /></TableCell>
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
