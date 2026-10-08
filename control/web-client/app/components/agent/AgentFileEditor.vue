<script setup lang="ts">
import { File, FileDiff } from '@pierre/diffs'
import { Editor } from '@pierre/diffs/edit'
import { Check, Eye, EyeOff, LoaderCircle, Palette, Save, X } from '@lucide/vue'
import { useLocalStorage } from '@vueuse/core'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import AgentMarkdown from '@/components/agent/AgentMarkdown.vue'
import { AgentError, type AgentFile } from '@/composables/useAgentSocket'
import { previewKind } from '@/lib/fileKind'

// mode "file" edits the whole file; "diff" edits it inside its diff against HEAD.
const props = defineProps<{
  cwd: string
  path: string
  mode: 'file' | 'diff'
  readFile: (cwd: string, path: string) => Promise<AgentFile>
  writeFile: (cwd: string, path: string, content: string, hash: string, force?: boolean) => Promise<string>
}>()
const emit = defineEmits<{ close: [], saved: [path: string] }>()

const host = useTemplateRef<HTMLElement>('host')
const colorMode = useColorMode()
const themeType = computed(() => (colorMode.value === 'dark' ? 'dark' : 'light'))

// One pick per app theme, shared by every editor. Monokai has no light
// variant; snazzy-light keeps its saturated palette.
const themeChoices = {
  dark: ['monokai', 'one-dark-pro', 'dracula', 'github-dark', 'nord', 'tokyo-night', 'catppuccin-mocha', 'vitesse-dark', 'gruvbox-dark-medium', 'solarized-dark'],
  light: ['snazzy-light', 'one-light', 'github-light', 'vitesse-light', 'catppuccin-latte', 'solarized-light', 'gruvbox-light-medium', 'min-light'],
} as const
const editorTheme = useLocalStorage('dummie:editor-theme', { dark: 'monokai', light: 'snazzy-light' }, { mergeDefaults: true })

function pickTheme(name: string) {
  editorTheme.value = { ...editorTheme.value, [themeType.value]: name }
}

const loading = ref(true)
const saving = ref(false)
const dirty = ref(false)
const conflict = ref(false)
const confirmClose = ref(false)
const error = ref<string | null>(null)

const preview = computed(() => (props.mode === 'file' ? previewKind(props.path) : null))
const previewing = ref(false)
const text = ref('')
const svgUrl = ref<string | null>(null)

function togglePreview() {
  if (!previewing.value && editor) text.value = editor.getText()
  previewing.value = !previewing.value
}

watch([text, preview], ([t, p]) => {
  if (svgUrl.value) URL.revokeObjectURL(svgUrl.value)
  svgUrl.value = p === 'svg' && t ? URL.createObjectURL(new Blob([t], { type: 'image/svg+xml' })) : null
})

let hash = ''
let view: File | FileDiff | null = null
let editor: Editor<any> | null = null
let detach: (() => void) | null = null

function teardown() {
  detach?.()
  editor?.cleanUp('discard')
  view?.cleanUp()
  detach = editor = view = null
  host.value?.replaceChildren()
}

async function load() {
  teardown()
  loading.value = true
  error.value = null
  conflict.value = false
  try {
    const f = await props.readFile(props.cwd, props.path)
    hash = f.exists ? f.hash : ''
    text.value = f.content
    if (!host.value) return
    const name = props.path
    if (props.mode === 'diff') {
      const d = new FileDiff({ diffStyle: 'unified', overflow: 'scroll', theme: { ...editorTheme.value }, themeType: themeType.value, disableFileHeader: true })
      d.render({ oldFile: { name, contents: f.baseExists ? f.base : '' }, newFile: { name, contents: f.content }, containerWrapper: host.value })
      view = d
      editor = new Editor('file-diff', { onChange: () => { dirty.value = true } })
      detach = editor.edit(d)
    }
    else {
      const v = new File({ overflow: 'scroll', theme: { ...editorTheme.value }, themeType: themeType.value, disableFileHeader: true })
      v.render({ file: { name, contents: f.content }, containerWrapper: host.value })
      view = v
      editor = new Editor('file', { onChange: () => { dirty.value = true } })
      detach = editor.edit(v)
    }
    dirty.value = false
    editor.focus()
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not open the file'
  }
  finally {
    loading.value = false
  }
}

async function save(force = false) {
  if (!editor || saving.value) return
  saving.value = true
  error.value = null
  try {
    hash = await props.writeFile(props.cwd, props.path, editor.getText(), hash, force)
    dirty.value = false
    conflict.value = false
    emit('saved', props.path)
  }
  catch (e) {
    if (e instanceof AgentError && e.code === 'conflict') conflict.value = true
    else error.value = e instanceof Error ? e.message : 'Could not save'
  }
  finally {
    saving.value = false
  }
}

function close() {
  if (dirty.value && !confirmClose.value) {
    confirmClose.value = true
    return
  }
  emit('close')
}

function onKeydown(e: KeyboardEvent) {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
    e.preventDefault()
    void save()
  }
}

watch(themeType, t => view?.setThemeType(t))
watch(editorTheme, (theme) => {
  if (!view) return
  view.setOptions({ ...view.options, theme: { ...theme } } as typeof view.options)
  view.rerender()
}, { deep: true })
watch(() => [props.cwd, props.path, props.mode], load)
watch(dirty, () => { confirmClose.value = false })
onMounted(load)
onBeforeUnmount(() => {
  teardown()
  if (svgUrl.value) URL.revokeObjectURL(svgUrl.value)
})
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col" @keydown="onKeydown">
    <div class="flex items-center gap-2 border-b border-border px-3 py-1.5 font-mono text-xs">
      <span class="min-w-0 truncate" :title="path">{{ path }}</span>
      <span v-if="dirty" class="size-1.5 shrink-0 rounded-full bg-primary" aria-label="unsaved changes" />
      <div class="ml-auto flex shrink-0 items-center gap-1">
        <DropdownMenu>
          <DropdownMenuTrigger as-child>
            <Button variant="ghost" size="sm" class="h-6 px-2 font-mono text-xs" aria-label="editor theme">
              <Palette class="size-3" aria-hidden="true" />
              <span class="hidden sm:inline">{{ editorTheme[themeType] }}</span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" class="max-h-80 w-56 overflow-y-auto font-mono">
            <DropdownMenuLabel class="text-[11px] font-normal text-muted-foreground">{{ themeType }} themes</DropdownMenuLabel>
            <DropdownMenuItem
              v-for="name in themeChoices[themeType]"
              :key="name"
              class="text-xs"
              @select="pickTheme(name)"
            >
              {{ name }}
              <Check v-if="editorTheme[themeType] === name" class="ml-auto size-3.5" aria-hidden="true" />
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <Button
          v-if="preview"
          variant="ghost"
          size="sm"
          class="h-6 px-2 font-mono text-xs"
          :aria-pressed="previewing"
          :disabled="loading"
          @click="togglePreview"
        >
          <EyeOff v-if="previewing" class="size-3" aria-hidden="true" />
          <Eye v-else class="size-3" aria-hidden="true" />
          <span class="hidden sm:inline">{{ previewing ? 'Edit' : 'Preview' }}</span>
        </Button>
        <Button size="sm" class="h-6 px-2 font-mono text-xs" :disabled="!dirty || saving || loading" @click="save()">
          <LoaderCircle v-if="saving" class="size-3 animate-spin" aria-hidden="true" />
          <Save v-else class="size-3" aria-hidden="true" />
          Save
        </Button>
        <Button variant="ghost" size="sm" class="h-6 px-2 font-mono text-xs" @click="close">
          <X v-if="!confirmClose" class="size-3" aria-hidden="true" />
          {{ confirmClose ? 'Discard changes?' : 'Close' }}
        </Button>
      </div>
    </div>

    <div v-if="conflict" class="flex flex-wrap items-center gap-2 border-b border-border bg-muted/50 px-3 py-2 font-mono text-xs" role="alert">
      <span class="text-destructive">The file changed on disk since you opened it.</span>
      <Button variant="outline" size="sm" class="h-6 px-2 font-mono text-xs" @click="load">Reload theirs</Button>
      <Button variant="outline" size="sm" class="h-6 px-2 font-mono text-xs" @click="save(true)">Overwrite with mine</Button>
    </div>
    <p v-if="error" class="border-b border-border px-3 py-2 font-mono text-xs text-destructive" role="alert">{{ error }}</p>
    <p v-if="loading" class="p-3 font-mono text-xs text-muted-foreground">Opening…</p>

    <div v-show="!previewing" ref="host" class="min-h-0 flex-1 overflow-auto text-xs" />
    <!-- An empty sandbox runs no scripts and gives the page an opaque origin. -->
    <iframe v-if="preview === 'html' && previewing" :srcdoc="text" sandbox="" :title="path" class="min-h-0 w-full flex-1 bg-white" />
    <div v-else-if="preview && previewing" class="min-h-0 flex-1 overflow-auto p-4" aria-label="preview">
      <AgentMarkdown v-if="preview === 'markdown'" :source="text" class="mx-auto max-w-3xl text-sm" />
      <img v-else-if="svgUrl" :src="svgUrl" :alt="path" class="mx-auto max-w-full">
    </div>
  </div>
</template>
