<script setup lang="ts">
import { GripVertical, X } from '@lucide/vue'
import { useElementSize } from '@vueuse/core'
import { Button } from '@/components/ui/button'
import { fits, GRID_COLS, GRID_ROWS, panels, type GridItem, type PanelType } from '@/lib/workspace'

const props = defineProps<{ items: GridItem[], readonly?: boolean }>()
const emit = defineEmits<{ 'update:items': [items: GridItem[]] }>()

const GAP = 6
const gridEl = useTemplateRef<HTMLElement>('grid')
const draft = ref<GridItem[] | null>(null)
const shown = computed(() => draft.value ?? props.items)

// One dot per cell corner, centred in the gaps between cells.
const { width, height } = useElementSize(gridEl)
const dots = computed(() => {
  const cw = (width.value + GAP) / GRID_COLS
  const ch = (height.value + GAP) / GRID_ROWS
  const tone = draft.value ? 70 : 40
  return {
    backgroundImage: `radial-gradient(circle, color-mix(in oklab, var(--muted-foreground) ${tone}%, transparent) 1.25px, transparent 1.5px)`,
    backgroundSize: `${cw}px ${ch}px`,
    backgroundPosition: `${cw / 2 - GAP / 2}px ${ch / 2 - GAP / 2}px`,
  }
})

let drag: { panel: PanelType, mode: 'move' | 'resize', x: number, y: number, orig: GridItem } | null = null

const clamp = (n: number, lo: number, hi: number) => Math.min(Math.max(n, lo), hi)
const area = (i: GridItem) => ({ gridColumn: `${i.x + 1} / span ${i.w}`, gridRow: `${i.y + 1} / span ${i.h}` })

function cell() {
  const r = gridEl.value!.getBoundingClientRect()
  return { w: (r.width + GAP) / GRID_COLS, h: (r.height + GAP) / GRID_ROWS, left: r.left, top: r.top }
}

function replace(items: GridItem[], ...next: GridItem[]) {
  return items.map(i => next.find(n => n.panel === i.panel) ?? i)
}

function start(e: PointerEvent, item: GridItem, mode: 'move' | 'resize') {
  if (props.readonly || e.button !== 0 || (mode === 'move' && (e.target as HTMLElement).closest('button'))) return
  e.preventDefault()
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
  drag = { panel: item.panel, mode, x: e.clientX, y: e.clientY, orig: { ...item } }
  draft.value = props.items
}

function move(e: PointerEvent) {
  if (!drag) return
  const c = cell()
  const dx = Math.round((e.clientX - drag.x) / c.w)
  const dy = Math.round((e.clientY - drag.y) / c.h)
  const o = drag.orig
  const base = props.items
  if (drag.mode === 'resize') {
    const next = { ...o, w: clamp(o.w + dx, 1, GRID_COLS - o.x), h: clamp(o.h + dy, 1, GRID_ROWS - o.y) }
    if (fits(next, base, o.panel)) draft.value = replace(base, next)
    return
  }
  const next = { ...o, x: clamp(o.x + dx, 0, GRID_COLS - o.w), y: clamp(o.y + dy, 0, GRID_ROWS - o.h) }
  if (fits(next, base, o.panel)) {
    draft.value = replace(base, next)
    return
  }
  // Dropping onto another panel swaps the two.
  const px = Math.floor((e.clientX - c.left) / c.w)
  const py = Math.floor((e.clientY - c.top) / c.h)
  const target = base.find(i => i.panel !== o.panel && px >= i.x && px < i.x + i.w && py >= i.y && py < i.y + i.h)
  if (target) {
    draft.value = replace(base,
      { ...o, x: target.x, y: target.y, w: target.w, h: target.h },
      { ...target, x: o.x, y: o.y, w: o.w, h: o.h })
  }
}

function end() {
  if (!drag) return
  drag = null
  const next = draft.value
  draft.value = null
  if (next && next.some((n, i) => JSON.stringify(n) !== JSON.stringify(props.items[i]))) emit('update:items', next)
}

function nudge(e: KeyboardEvent, item: GridItem) {
  if (props.readonly) return
  const d = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] }[e.key]
  if (!d) return
  e.preventDefault()
  const next = e.shiftKey
    ? { ...item, w: item.w + d[0]!, h: item.h + d[1]! }
    : { ...item, x: item.x + d[0]!, y: item.y + d[1]! }
  if (fits(next, props.items, item.panel)) emit('update:items', replace(props.items, next))
}

function remove(panel: PanelType) {
  emit('update:items', props.items.filter(i => i.panel !== panel))
}
</script>

<template>
  <div
    ref="grid"
    class="grid size-full min-h-0"
    :class="draft && 'select-none'"
    :style="{
      ...dots,
      gap: `${GAP}px`,
      gridTemplateColumns: `repeat(${GRID_COLS}, minmax(0, 1fr))`,
      gridTemplateRows: `repeat(${GRID_ROWS}, minmax(0, 1fr))`,
    }"
  >
    <section
      v-for="item in shown"
      :key="item.panel"
      :style="area(item)"
      class="relative flex min-h-0 min-w-0 flex-col overflow-hidden rounded-md border border-border bg-background"
      :class="draft && 'ring-1 ring-ring/40'"
      :aria-label="panels[item.panel].label"
    >
      <header
        class="flex h-7 shrink-0 touch-none items-center gap-1.5 border-b border-border bg-muted/40 pl-1.5 pr-0.5 font-mono text-xs text-muted-foreground focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring"
        :class="!readonly && 'cursor-grab active:cursor-grabbing'"
        :tabindex="readonly ? undefined : 0"
        :aria-label="readonly ? undefined : `${panels[item.panel].label} panel: drag to move, arrow keys move, shift and arrow keys resize`"
        @pointerdown="start($event, item, 'move')"
        @pointermove="move"
        @pointerup="end"
        @pointercancel="end"
        @keydown="nudge($event, item)"
      >
        <GripVertical class="size-3.5 shrink-0" aria-hidden="true" />
        <component :is="panels[item.panel].icon" class="size-3.5 shrink-0" aria-hidden="true" />
        <span class="truncate">{{ panels[item.panel].label }}</span>
        <Button
          v-if="!readonly"
          variant="ghost"
          size="icon"
          class="ml-auto size-6"
          :aria-label="`remove the ${panels[item.panel].label} panel`"
          @click="remove(item.panel)"
        >
          <X class="size-3.5" aria-hidden="true" />
        </Button>
      </header>
      <div class="flex min-h-0 flex-1 flex-col" :class="draft && 'pointer-events-none'">
        <slot name="panel" :item="item" />
      </div>
      <div
        v-if="!readonly"
        class="absolute right-0 bottom-0 z-10 size-3 cursor-se-resize touch-none border-r-2 border-b-2 border-muted-foreground/40 hover:border-foreground"
        aria-hidden="true"
        @pointerdown="start($event, item, 'resize')"
        @pointermove="move"
        @pointerup="end"
        @pointercancel="end"
      />
    </section>
    <div v-if="!shown.length" class="col-span-full row-span-full grid place-items-center">
      <slot name="empty" />
    </div>
  </div>
</template>
