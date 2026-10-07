<script setup lang="ts">
import { ArrowDown, ArrowLeft, ArrowRight, ArrowUp } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import type VmTerminal from '@/components/VmTerminal.vue'

const props = defineProps<{
  terminal: InstanceType<typeof VmTerminal> | null | undefined
  disabled: boolean
}>()

const rowOneKeys = [
  { label: 'Tab', data: '\t' },
  { label: 'Enter', data: '\r' },
]

const rowTwoKeys = [
  { label: 'Esc', data: '\x1b' },
  { label: 'Ctrl+c', data: '\x03' },
  { label: 'Ctrl+r', data: '\x12' },
]

const arrowKeys = {
  up: { label: 'Up', data: '\x1b[A', icon: ArrowUp },
  left: { label: 'Left', data: '\x1b[D', icon: ArrowLeft },
  down: { label: 'Down', data: '\x1b[B', icon: ArrowDown },
  right: { label: 'Right', data: '\x1b[C', icon: ArrowRight },
}

const pasteError = ref<string | null>(null)
let pasteErrorTimer: ReturnType<typeof setTimeout> | undefined

function showPasteError(msg: string) {
  pasteError.value = msg
  clearTimeout(pasteErrorTimer)
  pasteErrorTimer = setTimeout(() => (pasteError.value = null), 4000)
}

const modifiers = [
  { label: 'Ctrl', key: 'ctrlArmed' },
  { label: 'Shift', key: 'shiftArmed' },
] as const

function armed(key: 'ctrlArmed' | 'shiftArmed') {
  return props.terminal?.[key] ?? false
}

function toggleModifier(key: 'ctrlArmed' | 'shiftArmed') {
  const t = props.terminal
  if (!t) return
  t[key] = !t[key]
  t.focus()
}

const flashed = ref<string | null>(null)
let flashTimer: ReturnType<typeof setTimeout> | undefined

function flashClass(label: string) {
  // dark: variants too — the outline variant's own dark:bg-input/30 would
  // otherwise win over an unprefixed background.
  return flashed.value === label
    ? 'border-emerald-500 bg-emerald-500 text-white dark:border-emerald-500 dark:bg-emerald-500'
    : ''
}

function flash(label: string) {
  flashed.value = label
  clearTimeout(flashTimer)
  flashTimer = setTimeout(() => (flashed.value = null), 100)
}

function sendKeys(key: { label: string, data: string }) {
  props.terminal?.send(key.data)
  props.terminal?.focus()
  flash(key.label)
}

async function paste() {
  try {
    const text = await navigator.clipboard.readText()
    if (text) props.terminal?.send(text)
    props.terminal?.focus()
    flash('Paste')
  }
  catch {
    showPasteError('The browser would not hand over the clipboard. Long-press the terminal to paste instead.')
  }
}

onBeforeUnmount(() => {
  clearTimeout(pasteErrorTimer)
  clearTimeout(flashTimer)
})
</script>

<template>
  <div>
    <p v-if="pasteError" class="px-3 pt-2 font-mono text-xs text-muted-foreground">
      {{ pasteError }}
    </p>
    <div class="flex items-start gap-2 px-3 py-2">
      <div class="grid min-w-0 flex-1 grid-cols-4 gap-1.5">
        <Button
          v-for="key in rowOneKeys"
          :key="key.label"
          variant="outline"
          size="sm"
          class="px-1 font-mono text-[11px] transition-colors"
          :class="flashClass(key.label)"
          :disabled="disabled"
          @mousedown.prevent
          @click="sendKeys(key)"
        >
          {{ key.label }}
        </Button>
        <Button
          v-for="mod in modifiers"
          :key="mod.key"
          :variant="armed(mod.key) ? 'default' : 'outline'"
          size="sm"
          class="px-1 font-mono text-[11px]"
          :disabled="disabled"
          :aria-pressed="armed(mod.key)"
          @mousedown.prevent
          @click="toggleModifier(mod.key)"
        >
          {{ mod.label }}
        </Button>
        <Button
          v-for="key in rowTwoKeys"
          :key="key.label"
          variant="outline"
          size="sm"
          class="px-1 font-mono text-[11px] transition-colors"
          :class="flashClass(key.label)"
          :disabled="disabled"
          @mousedown.prevent
          @click="sendKeys(key)"
        >
          {{ key.label }}
        </Button>
        <Button
          variant="outline"
          size="sm"
          class="px-1 font-mono text-[11px] transition-colors"
          :class="flashClass('Paste')"
          :disabled="disabled"
          @mousedown.prevent
          @click="paste()"
        >
          Paste
        </Button>
      </div>

      <div class="grid shrink-0 grid-cols-3 grid-rows-2 gap-1.5">
        <Button
          variant="outline"
          size="icon-sm"
          class="col-start-2 row-start-1 transition-colors"
          :class="flashClass(arrowKeys.up.label)"
          :disabled="disabled"
          aria-label="Up"
          @mousedown.prevent
          @click="sendKeys(arrowKeys.up)"
        >
          <ArrowUp class="size-3.5" />
        </Button>
        <Button
          v-for="key in [arrowKeys.left, arrowKeys.down, arrowKeys.right]"
          :key="key.label"
          variant="outline"
          size="icon-sm"
          class="row-start-2 transition-colors"
          :class="flashClass(key.label)"
          :disabled="disabled"
          :aria-label="key.label"
          @mousedown.prevent
          @click="sendKeys(key)"
        >
          <component :is="key.icon" class="size-3.5" />
        </Button>
      </div>
    </div>
  </div>
</template>
