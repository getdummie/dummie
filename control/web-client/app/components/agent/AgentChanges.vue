<script setup lang="ts">
import { FileDiff, parsePatchFiles, type FileDiffMetadata } from '@pierre/diffs'
import { FileTree, type GitStatusEntry } from '@pierre/trees'
import { ChevronRight, GitBranch, Pencil } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import AgentFileEditor from '@/components/agent/AgentFileEditor.vue'
import type { AgentDiff, AgentFile } from '@/composables/useAgentSocket'

const props = defineProps<{
  diff: AgentDiff | null
  readFile: (cwd: string, path: string) => Promise<AgentFile>
  writeFile: (cwd: string, path: string, content: string, hash: string, force?: boolean) => Promise<string>
  // In a sheet, whose close button sits over the header's right end.
  inSheet?: boolean
}>()
// A file picked in the tree opens in the center panel, not here.
const emit = defineEmits<{ gitInit: [], edited: [path: string], openFile: [file: { cwd: string, path: string }] }>()

const editingDiff = ref<string | null>(null)

function editInDiff(path: string) {
  if (folded.value.has(path)) toggle(path)
  editingDiff.value = path
}

const maxRendered = 100
// Like a pull request, big or deleted files start folded.
const foldOver = 400

const tab = ref<'changes' | 'files'>('changes')
const colorMode = useColorMode()
const themeType = computed(() => (colorMode.value === 'dark' ? 'dark' : 'light'))

const files = computed<FileDiffMetadata[]>(() => {
  if (!props.diff?.patch) return []
  try {
    return parsePatchFiles(props.diff.patch).flatMap(p => p.files)
  }
  catch {
    return []
  }
})
const shown = computed(() => files.value.slice(0, maxRendered))

interface Stat { add: number, del: number }

const stats = computed(() => {
  const byPath = new Map<string, Stat>()
  for (const f of files.value) {
    const s = { add: 0, del: 0 }
    for (const h of f.hunks) {
      s.add += h.additionLines
      s.del += h.deletionLines
    }
    byPath.set(f.name, s)
    const parts = f.name.split('/')
    for (let i = 1; i < parts.length; i++) {
      const dir = parts.slice(0, i).join('/')
      const d = byPath.get(dir) ?? { add: 0, del: 0 }
      d.add += s.add
      d.del += s.del
      byPath.set(dir, d)
    }
  }
  return byPath
})
const total = computed(() => files.value.reduce((t, f) => {
  const s = stats.value.get(f.name)!
  return { add: t.add + s.add, del: t.del + s.del }
}, { add: 0, del: 0 }))

const statusFor: Record<string, GitStatusEntry['status']> = {
  'new': 'added',
  'deleted': 'deleted',
  'rename-pure': 'renamed',
  'rename-changed': 'renamed',
  'change': 'modified',
}

const statusMark: Record<string, { letter: string, cls: string }> = {
  added: { letter: 'A', cls: 'text-[var(--diff-add)]' },
  deleted: { letter: 'D', cls: 'text-[var(--diff-del)]' },
  renamed: { letter: 'R', cls: 'text-[var(--diff-ren)]' },
  modified: { letter: 'M', cls: 'text-[var(--diff-mod)]' },
}

function statusOf(f: FileDiffMetadata) {
  return statusMark[statusFor[f.type] ?? 'modified']!
}

function splitPath(p: string) {
  const i = p.lastIndexOf('/')
  return i < 0 ? { dir: '', base: p } : { dir: p.slice(0, i + 1), base: p.slice(i + 1) }
}

// Folded state survives refreshes by path; a file seen for the first time
// folds only if it is big or deleted.
const folded = ref(new Set<string>())
const seen = new Set<string>()

function toggle(path: string) {
  const next = new Set(folded.value)
  if (!next.delete(path)) next.add(path)
  folded.value = next
}

const bodies = new Map<string, HTMLElement>()
let rendered: FileDiff[] = []

function renderDiffs() {
  for (const d of rendered) d.cleanUp()
  rendered = []
  const next = new Set(folded.value)
  for (const f of shown.value) {
    if (seen.has(f.name)) continue
    seen.add(f.name)
    const s = stats.value.get(f.name)!
    if (f.type === 'deleted' || s.add + s.del > foldOver) next.add(f.name)
  }
  folded.value = next
  for (const fileDiff of shown.value) {
    const el = bodies.get(fileDiff.name)
    if (!el) continue
    el.replaceChildren()
    const d = new FileDiff({ diffStyle: 'unified', overflow: 'wrap', themeType: themeType.value, disableFileHeader: true })
    d.render({ fileDiff, containerWrapper: el })
    rendered.push(d)
  }
}

const treeHost = useTemplateRef<HTMLElement>('treeHost')
let tree: FileTree | null = null

function gitStatus(): GitStatusEntry[] {
  return files.value.map(f => ({ path: f.name, status: statusFor[f.type] ?? 'modified' }))
}

function statFor(path: string) {
  return stats.value.get(path.replace(/\/$/, ''))
}

// Open every folder on the way to a change, the way paseo lands on a tree.
function expandChanged() {
  if (!tree) return
  for (const path of stats.value.keys()) {
    if (files.value.some(f => f.name === path)) continue
    const item = tree.getItem(path) ?? tree.getItem(`${path}/`)
    if (item?.isDirectory() && !item.isExpanded()) item.expand()
  }
}

function renderTree() {
  const paths = props.diff?.tree ?? []
  if (!treeHost.value) return
  if (!tree) {
    tree = new FileTree({
      paths,
      flattenEmptyDirectories: true,
      initialExpansion: 'closed',
      search: true,
      gitStatus: gitStatus(),
      renderRowDecoration: ({ item }) => {
        const s = statFor(item.path)
        if (!s) return null
        return {
          text: `+${s.add} -${s.del}`,
          parts: [
            { text: `+${s.add}`, color: 'var(--diff-add)' },
            { text: ' ' },
            { text: `-${s.del}`, color: 'var(--diff-del)' },
          ],
        }
      },
      onSelectionChange: (selected) => {
        const path = selected[0] ? String(selected[0]) : null
        if (path && props.diff && !tree?.getItem(path)?.isDirectory()) emit('openFile', { cwd: props.diff.cwd, path })
      },
    })
    tree.render({ containerWrapper: treeHost.value })
  }
  else {
    tree.resetPaths(paths)
    tree.setGitStatus(gitStatus())
  }
  expandChanged()
}

watch(files, renderDiffs, { flush: 'post' })
watch(() => props.diff?.tree, renderTree, { flush: 'post' })
watch(themeType, t => rendered.forEach(d => d.setThemeType(t)))

onMounted(() => {
  renderDiffs()
  renderTree()
})

onBeforeUnmount(() => {
  for (const d of rendered) d.cleanUp()
  tree?.cleanUp()
})
</script>

<template>
  <section class="agent-changes flex size-full min-h-0 flex-col" aria-label="changes">
    <header class="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2" :class="inSheet && 'pr-12'">
      <Tabs v-model="tab">
        <TabsList class="h-7">
          <TabsTrigger value="changes" class="h-6 font-mono text-xs">changes · {{ files.length }}</TabsTrigger>
          <TabsTrigger value="files" class="h-6 font-mono text-xs">files</TabsTrigger>
        </TabsList>
      </Tabs>
      <span v-if="files.length" class="font-mono text-xs">
        <span class="text-[var(--diff-add)]">+{{ total.add }}</span>
        <span class="text-[var(--diff-del)]"> -{{ total.del }}</span>
      </span>
      <span v-if="diff?.branch" class="ml-auto inline-flex items-center gap-1 truncate font-mono text-xs text-muted-foreground">
        <GitBranch class="size-3.5" aria-hidden="true" />
        {{ diff.branch }}
      </span>
    </header>

    <div v-if="!diff" class="p-4 font-mono text-xs text-muted-foreground">Reading the working tree…</div>
    <div v-else-if="!diff.repo" class="flex flex-col items-start gap-3 p-4 font-mono text-xs text-muted-foreground">
      <p>{{ diff.error || `${diff.cwd} is not a git repository, so there is nothing to diff against.` }}</p>
      <Button v-if="!diff.error" variant="outline" size="sm" class="h-7 font-mono text-xs" @click="emit('gitInit')">
        Initialize git here
      </Button>
    </div>

    <template v-else>
      <p v-if="diff.error" class="border-b border-border px-3 py-2 font-mono text-xs text-destructive">{{ diff.error }}</p>
      <p v-if="diff.truncated || files.length > maxRendered" class="border-b border-border px-3 py-2 font-mono text-xs text-muted-foreground">
        Too many changes to show them all here.
      </p>
      <p v-if="tab === 'changes' && !files.length" class="p-4 font-mono text-xs text-muted-foreground">No uncommitted changes.</p>
    </template>

    <div v-show="diff?.repo && tab === 'changes'" class="min-h-0 flex-1 overflow-y-auto">
      <section v-for="f in shown" :key="f.name" class="border-b border-border">
        <div class="group sticky top-0 z-10 flex items-center border-b border-border bg-background font-mono text-xs hover:bg-muted">
          <button
            type="button"
            class="flex min-w-0 flex-1 items-center gap-2 py-1.5 pl-3 text-left"
            :aria-expanded="!folded.has(f.name)"
            @click="toggle(f.name)"
          >
            <ChevronRight class="size-3.5 shrink-0 transition-transform" :class="!folded.has(f.name) && 'rotate-90'" aria-hidden="true" />
            <span class="w-3 shrink-0 font-semibold" :class="statusOf(f).cls" aria-hidden="true">{{ statusOf(f).letter }}</span>
            <span class="min-w-0 truncate" :title="f.name">
              <span class="text-muted-foreground">{{ splitPath(f.name).dir }}</span>{{ splitPath(f.name).base }}
            </span>
            <span class="ml-auto shrink-0">
              <span class="text-[var(--diff-add)]">+{{ stats.get(f.name)?.add }}</span>
              <span class="text-[var(--diff-del)]"> -{{ stats.get(f.name)?.del }}</span>
            </span>
          </button>
          <Button
            v-if="f.type !== 'deleted' && editingDiff !== f.name"
            variant="ghost"
            size="icon"
            class="mx-1 size-6 shrink-0 opacity-60 group-hover:opacity-100"
            :aria-label="`edit ${f.name}`"
            @click="editInDiff(f.name)"
          >
            <Pencil class="size-3" aria-hidden="true" />
          </Button>
          <span v-else class="w-2" />
        </div>
        <AgentFileEditor
          v-if="editingDiff === f.name && diff"
          class="max-h-[70vh]"
          :cwd="diff.cwd"
          :path="f.name"
          mode="diff"
          :read-file="readFile"
          :write-file="writeFile"
          @close="editingDiff = null"
          @saved="emit('edited', $event)"
        />
        <div v-show="!folded.has(f.name) && editingDiff !== f.name" :ref="el => el ? bodies.set(f.name, el as HTMLElement) : bodies.delete(f.name)" class="text-xs" />
      </section>
    </div>
    <div v-show="diff?.repo && tab === 'files'" ref="treeHost" class="agent-tree min-h-0 flex-1" />
  </section>
</template>

<style>
.agent-changes {
  --diff-add: oklch(0.6 0.15 150);
  --diff-del: oklch(0.6 0.2 25);
  --diff-mod: oklch(0.7 0.15 75);
  --diff-ren: oklch(0.6 0.13 250);
}
.dark .agent-changes {
  --diff-add: oklch(0.75 0.15 150);
  --diff-del: oklch(0.7 0.17 25);
  --diff-mod: oklch(0.8 0.14 80);
  --diff-ren: oklch(0.72 0.12 250);
}
/* The tree follows the OS through light-dark() unless told the app's theme. */
.agent-tree file-tree-container {
  color-scheme: light;
  height: 100%;
  --trees-bg-override: var(--background);
  --trees-fg-override: var(--foreground);
  --trees-fg-muted-override: var(--muted-foreground);
  --trees-bg-muted-override: var(--muted);
  --trees-selected-bg-override: var(--muted);
  --trees-selected-fg-override: var(--foreground);
  --trees-selected-focused-border-color-override: var(--ring);
  --trees-border-color-override: var(--border);
  --trees-accent-override: var(--primary);
  --trees-focus-ring-color-override: var(--ring);
  --trees-search-bg-override: var(--background);
  --trees-search-fg-override: var(--foreground);
  --trees-input-bg-override: var(--background);
  --trees-indent-guide-bg-override: var(--border);
  --trees-font-family-override: var(--font-sans);
  --trees-font-size-override: 13px;
  --trees-git-added-color-override: var(--diff-add);
  --trees-git-untracked-color-override: var(--diff-add);
  --trees-git-deleted-color-override: var(--diff-del);
  --trees-git-modified-color-override: var(--diff-mod);
  --trees-git-renamed-color-override: var(--diff-ren);
  --trees-status-added-override: var(--diff-add);
  --trees-status-untracked-override: var(--diff-add);
  --trees-status-deleted-override: var(--diff-del);
  --trees-status-modified-override: var(--diff-mod);
  --trees-status-renamed-override: var(--diff-ren);
}
.dark .agent-tree file-tree-container { color-scheme: dark; }
</style>
