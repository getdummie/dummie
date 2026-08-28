<script setup lang="ts">
import { RotateCw } from '@lucide/vue'
import { useElementSize, useLocalStorage } from '@vueuse/core'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import type { ViewportPreset } from '@/lib/viewports'

const props = defineProps<{
  src: string
  label: string
  presets: ViewportPreset[]
  storageKey: string
}>()

const emit = defineEmits<{ reload: [] }>()

const first = props.presets[0]!
const width = useLocalStorage(`${props.storageKey}:w`, first.width)
const height = useLocalStorage(`${props.storageKey}:h`, first.height)

const reloads = ref(0)

const frame = useTemplateRef<HTMLDivElement>('frame')
const { width: paneWidth, height: paneHeight } = useElementSize(frame)

const scale = computed(() => {
  if (!paneWidth.value || !paneHeight.value) return 1
  return Math.min(1, paneWidth.value / width.value, paneHeight.value / height.value)
})

const activePreset = computed(() => {
  const match = props.presets.find(p => p.width === width.value && p.height === height.value)
  return match?.label
})

function applyPreset(label: unknown) {
  const preset = props.presets.find(p => p.label === label)
  if (!preset) return
  width.value = preset.width
  height.value = preset.height
}

const dimensionInputClass = 'h-7 w-14 px-2 text-center text-xs [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none'

function setDimension(target: 'w' | 'h', raw: string | number) {
  const n = Math.round(Number(raw))
  if (!Number.isFinite(n)) return
  const clamped = Math.min(Math.max(n, 120), 4000)
  if (target === 'w') width.value = clamped
  else height.value = clamped
}
</script>

<template>
  <section class="flex min-h-0 min-w-0 flex-1 flex-col bg-muted/30">
    <header class="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2">
      <h2 class="eyebrow text-muted-foreground">{{ label }}</h2>

      <div class="ml-auto flex flex-wrap items-center gap-2">
        <Select :model-value="activePreset" @update:model-value="applyPreset">
          <SelectTrigger
            size="sm"
            :aria-label="`${label} device preset`"
            class="h-7 gap-1.5 px-2 font-mono text-xs"
          >
            <SelectValue placeholder="custom" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem
              v-for="p in presets"
              :key="p.label"
              :value="p.label"
              class="font-mono text-xs"
            >
              {{ p.label }}
            </SelectItem>
          </SelectContent>
        </Select>

        <div class="flex items-center gap-1 font-mono text-xs text-muted-foreground">
          <Input
            :model-value="width"
            type="number"
            inputmode="numeric"
            :aria-label="`${label} width in pixels`"
            :class="dimensionInputClass"
            @change="setDimension('w', ($event.target as HTMLInputElement).value)"
          />
          <span aria-hidden="true">×</span>
          <Input
            :model-value="height"
            type="number"
            inputmode="numeric"
            :aria-label="`${label} height in pixels`"
            :class="dimensionInputClass"
            @change="setDimension('h', ($event.target as HTMLInputElement).value)"
          />
        </div>

        <span class="font-mono text-xs text-muted-foreground tabular-nums">
          {{ Math.round(scale * 100) }}%
        </span>

        <Button
          variant="ghost"
          size="icon"
          class="size-7"
          :title="`Reload ${label}`"
          @click="reloads++; emit('reload')"
        >
          <RotateCw class="size-3.5" aria-hidden="true" />
          <span class="sr-only">Reload {{ label }}</span>
        </Button>
      </div>
    </header>

    <div ref="frame" class="min-h-0 flex-1 overflow-auto p-3">
      <div
        class="mx-auto"
        :style="{ width: `${Math.round(width * scale)}px`, height: `${Math.round(height * scale)}px` }"
      >
        <div
          class="overflow-hidden rounded-md border border-border bg-background shadow-sm"
          :style="{
            width: `${width}px`,
            height: `${height}px`,
            transform: `scale(${scale})`,
            transformOrigin: 'top left',
          }"
        >
          <iframe
            :key="reloads"
            :src="src"
            :title="`${label} preview`"
            class="size-full border-0"
          />
        </div>
      </div>
    </div>
  </section>
</template>
