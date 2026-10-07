<script setup lang="ts">
import { Check, ChevronDown, FileText, Paperclip, SendHorizontal, Square, X } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '@/components/ui/command'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Textarea } from '@/components/ui/textarea'
import type { Draft, Model } from '@/lib/agentChat'

const props = defineProps<{ busy: boolean, busyHint: string, disabled?: boolean, sending?: boolean, models: Model[], model: string }>()
const emit = defineEmits<{ send: [draft: Draft], stop: [], model: [key: string] }>()

const modelKey = (m: Model) => `${m.provider}/${m.id}`

const byProvider = computed(() => {
  const groups = new Map<string, Model[]>()
  for (const m of props.models) groups.set(m.provider, [...(groups.get(m.provider) ?? []), m])
  return [...groups]
})
const current = computed(() => props.models.find(m => modelKey(m) === props.model))
const pickerOpen = ref(false)

function pick(m: Model) {
  pickerOpen.value = false
  emit('model', modelKey(m))
}

const text = ref('')
const files = ref<{ file: File, url: string | null }[]>([])
const dragging = ref(false)
const picker = useTemplateRef<HTMLInputElement>('picker')

function add(list: FileList | File[] | null | undefined) {
  for (const file of list ?? []) {
    files.value.push({ file, url: file.type.startsWith('image/') ? URL.createObjectURL(file) : null })
  }
}

function remove(i: number) {
  const [f] = files.value.splice(i, 1)
  if (f?.url) URL.revokeObjectURL(f.url)
}

function onPaste(e: ClipboardEvent) {
  const pasted = [...(e.clipboardData?.files ?? [])]
  if (pasted.length) {
    e.preventDefault()
    add(pasted)
  }
}

function onDrop(e: DragEvent) {
  dragging.value = false
  add(e.dataTransfer?.files)
}

const canSend = computed(() => !props.disabled && !props.sending && (text.value.trim() !== '' || files.value.length > 0))

function send() {
  if (!canSend.value) return
  emit('send', { text: text.value, files: files.value.map(f => f.file) })
  text.value = ''
  for (const f of files.value) if (f.url) URL.revokeObjectURL(f.url)
  files.value = []
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
    e.preventDefault()
    send()
  }
}

onBeforeUnmount(() => {
  for (const f of files.value) if (f.url) URL.revokeObjectURL(f.url)
})
</script>

<template>
  <form
    class="mx-auto w-full max-w-3xl px-4 pb-4"
    @submit.prevent="send"
    @dragover.prevent="dragging = true"
    @dragleave.prevent="dragging = false"
    @drop.prevent="onDrop"
  >
    <div
      class="rounded-lg border bg-background transition-colors focus-within:border-ring"
      :class="dragging ? 'border-ring bg-accent/40' : 'border-border'"
    >
      <ul v-if="files.length" class="flex flex-wrap gap-2 px-3 pt-3">
        <li v-for="(f, i) in files" :key="i" class="relative">
          <img v-if="f.url" :src="f.url" :alt="f.file.name" class="size-16 rounded border border-border object-cover">
          <span v-else class="flex h-16 max-w-40 items-center gap-1.5 rounded border border-border px-2 font-mono text-xs">
            <FileText class="size-4 shrink-0" aria-hidden="true" />
            <span class="truncate">{{ f.file.name }}</span>
          </span>
          <button
            type="button"
            class="absolute -right-1.5 -top-1.5 rounded-full border border-border bg-background p-0.5"
            :aria-label="`remove ${f.file.name}`"
            @click="remove(i)"
          >
            <X class="size-3" aria-hidden="true" />
          </button>
        </li>
      </ul>

      <Textarea
        v-model="text"
        rows="2"
        :disabled="disabled"
        placeholder="Ask the agent… (Enter to send, Shift+Enter for a new line)"
        class="max-h-60 min-h-12 resize-none border-0 bg-transparent shadow-none focus-visible:ring-0 dark:bg-transparent"
        aria-label="message"
        @keydown="onKeydown"
        @paste="onPaste"
      />

      <div class="flex items-center gap-2 px-2 pb-2">
        <Popover v-if="models.length" v-model:open="pickerOpen">
          <PopoverTrigger as-child>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              role="combobox"
              :aria-expanded="pickerOpen"
              class="h-8 max-w-56 px-2 font-mono text-xs text-muted-foreground hover:text-foreground"
              aria-label="model"
            >
              <span class="truncate">{{ current ? (current.name || current.id) : 'pick a model' }}</span>
              <ChevronDown class="size-3.5 shrink-0 opacity-60" aria-hidden="true" />
            </Button>
          </PopoverTrigger>
          <PopoverContent align="start" side="top" class="w-72 p-0">
            <Command>
              <CommandInput placeholder="Search models…" class="font-mono text-xs" />
              <CommandList class="max-h-80">
                <CommandEmpty class="font-mono text-xs">No model found.</CommandEmpty>
                <template v-for="([provider, list], gi) in byProvider" :key="provider">
                  <CommandSeparator v-if="gi > 0" />
                  <CommandGroup :heading="provider" class="font-mono">
                    <CommandItem
                      v-for="m in list"
                      :key="modelKey(m)"
                      :value="modelKey(m)"
                      class="text-xs"
                      @select="pick(m)"
                    >
                      <span class="truncate">{{ m.name || m.id }}</span>
                      <Check v-if="modelKey(m) === model" class="ml-auto size-3.5" aria-hidden="true" />
                    </CommandItem>
                  </CommandGroup>
                </template>
              </CommandList>
            </Command>
          </PopoverContent>
        </Popover>
        <span v-if="busy" class="hidden truncate font-mono text-xs text-muted-foreground sm:inline">{{ busyHint }}</span>
        <div class="ml-auto flex items-center gap-1">
          <input ref="picker" type="file" multiple class="hidden" @change="add(($event.target as HTMLInputElement).files); ($event.target as HTMLInputElement).value = ''">
          <Button type="button" variant="ghost" size="icon" class="size-8" :disabled="disabled" aria-label="attach files" @click="picker?.click()">
            <Paperclip class="size-4" aria-hidden="true" />
          </Button>
          <Button v-if="busy" type="button" variant="outline" size="sm" class="h-8 font-mono text-xs" @click="emit('stop')">
            <Square class="size-3.5" aria-hidden="true" />
            Stop
          </Button>
          <Button type="submit" size="sm" class="h-8 font-mono text-xs" :disabled="!canSend">
            <SendHorizontal class="size-3.5" aria-hidden="true" />
            Send
          </Button>
        </div>
      </div>
    </div>
  </form>
</template>
