<script setup lang="ts">
import { RotateCw } from '@lucide/vue'
import { useElementSize, useLocalStorage } from '@vueuse/core'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import type { ViewportPreset } from '@/lib/viewports'

// A fixed-size iframe rather than one that fills the pane: the point of this is
// to see the guest at a real device width, so the frame keeps its dimensions and
// is scaled down to fit whatever room the pane has.
const props = defineProps<{
  src: string
  label: string
  presets: ViewportPreset[]
  // Dimensions are remembered per pane, not per VM: a device size is a property
  // of what the user is testing against, not of the guest.
  storageKey: string
}>()

// The reload is the page's to serve, not this component's: src carries a token
// that only lives minutes, so reloading means minting a new one.
const emit = defineEmits<{ reload: [] }>()

const first = props.presets[0]!
const width = useLocalStorage(`${props.storageKey}:w`, first.width)
const height = useLocalStorage(`${props.storageKey}:h`, first.height)

// Bumped to force the iframe to remount, which is what reloads it: assigning the
// same src again does nothing, and once the guest has navigated inside the frame
// its current url is not src any more.
const reloads = ref(0)

const frame = useTemplateRef<HTMLDivElement>('frame')
const { width: paneWidth, height: paneHeight } = useElementSize(frame)

// Never scales up: a 390px page blown up to fill a wide pane would be a
// misleading picture of what the guest renders.
const scale = computed(() => {
  if (!paneWidth.value || !paneHeight.value) return 1
  return Math.min(1, paneWidth.value / width.value, paneHeight.value / height.value)
})

// undefined rather than a "custom" value: Select treats no value as a
// placeholder, which is exactly what a hand-typed size is -- none of the
// presets describes it.
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

// Clamped rather than trusted: these come back from localStorage and from a
// number input, both of which can hand over something that would collapse the
// frame or hang the layout.
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

        <!-- Spinners off: at this size their arrows sit on top of the last digit,
             and a dimension is typed or picked from the presets rather than
             stepped one pixel at a time. -->
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

    <!-- Three boxes: the outer one is what the scale is measured against, the
         middle one takes up the scaled size so scrollbars match what is drawn
         (a transform does not change layout size), and the inner one is the
         frame at its true dimensions. -->
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
          <!-- No sandbox: the frame has to send this origin's session cookie for
               the control server to authorise the request at all, and a sandbox
               that keeps allow-same-origin to allow that is one the document can
               use to drop its own sandboxing anyway. -->
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
