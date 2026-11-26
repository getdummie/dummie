<script setup lang="ts">
import { Folder } from '@lucide/vue'
import { watchDebounced } from '@vueuse/core'
import { ListboxFilter } from 'reka-ui'
import { Command, CommandGroup, CommandItem, CommandList } from '@/components/ui/command'
import { Input } from '@/components/ui/input'
import type { AgentDirEntry } from '@/composables/useAgentSocket'

const props = defineProps<{
  id: string
  placeholder?: string
  list: (path: string) => Promise<{ path: string, entries: AgentDirEntry[] }>
}>()
const model = defineModel<string>({ required: true })

const focused = ref(false)
const dirs = ref<string[]>([])
let listed: string | null = null

const split = computed(() => {
  const i = model.value.lastIndexOf('/')
  return i < 0 ? null : { parent: model.value.slice(0, i + 1), prefix: model.value.slice(i + 1) }
})

const options = computed(() => {
  if (!split.value) return []
  const { parent, prefix } = split.value
  const p = prefix.toLowerCase()
  return dirs.value
    .filter(n => n !== prefix && n.toLowerCase().startsWith(p) && (p.startsWith('.') || !n.startsWith('.')))
    .slice(0, 100)
    .map(n => parent + n)
})

async function load() {
  const parent = split.value?.parent ?? null
  if (parent === listed) return
  listed = parent
  if (!parent) {
    dirs.value = []
    return
  }
  try {
    const res = await props.list(parent)
    if (listed === parent) dirs.value = res.entries.filter(e => e.dir).map(e => e.name).sort()
  }
  catch {
    if (listed === parent) {
      dirs.value = []
      listed = null
    }
  }
}

watchDebounced(() => split.value?.parent, load, { debounce: 150 })

function onFocus() {
  focused.value = true
  void load()
}

function pick(path: string) {
  model.value = path
}
</script>

<template>
  <Command class="h-auto overflow-visible bg-transparent">
    <div class="relative">
      <ListboxFilter v-model="model" as-child>
        <Input
          :id="id"
          v-model="model"
          class="font-mono text-sm"
          spellcheck="false"
          autocomplete="off"
          role="combobox"
          :aria-expanded="focused && options.length > 0"
          :placeholder="placeholder"
          @focus="onFocus"
          @blur="focused = false"
          @keydown.esc="focused = false"
          @input="focused = true"
        />
      </ListboxFilter>
      <div
        v-show="focused && options.length"
        class="absolute inset-x-0 top-full z-50 mt-1 rounded-md border bg-popover shadow-md"
        @mousedown.prevent
      >
        <CommandList class="max-h-64">
          <CommandGroup>
            <CommandItem v-for="o in options" :key="o" :value="o" class="gap-2 font-mono text-xs" @select="pick(o)">
              <Folder class="size-3.5" aria-hidden="true" />
              <span class="truncate">{{ o }}</span>
            </CommandItem>
          </CommandGroup>
        </CommandList>
      </div>
    </div>
  </Command>
</template>
