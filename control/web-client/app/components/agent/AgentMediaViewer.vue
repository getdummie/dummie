<script setup lang="ts">
import { X } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import type { MediaKind } from '@/lib/fileKind'

const props = defineProps<{
  cwd: string
  path: string
  kind: MediaKind
  mime: string
  readRaw: (cwd: string, path: string, type: string, onProgress?: (fraction: number) => void) => Promise<Blob>
}>()
const emit = defineEmits<{ close: [] }>()

const url = ref<string | null>(null)
const progress = ref(0)
const error = ref<string | null>(null)

onMounted(async () => {
  try {
    const blob = await props.readRaw(props.cwd, props.path, props.mime, (f) => { progress.value = f })
    url.value = URL.createObjectURL(blob)
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not open the file'
  }
})
onBeforeUnmount(() => url.value && URL.revokeObjectURL(url.value))
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col">
    <div class="flex items-center gap-2 border-b border-border px-3 py-1.5 font-mono text-xs">
      <span class="min-w-0 truncate" :title="path">{{ path }}</span>
      <Button variant="ghost" size="sm" class="ml-auto h-6 shrink-0 px-2 font-mono text-xs" @click="emit('close')">
        <X class="size-3" aria-hidden="true" />
        Close
      </Button>
    </div>

    <p v-if="error" class="px-3 py-2 font-mono text-xs text-destructive" role="alert">{{ error }}</p>
    <p v-else-if="!url" class="p-3 font-mono text-xs text-muted-foreground">Opening… {{ Math.round(progress * 100) }}%</p>
    <div v-else class="flex min-h-0 flex-1 items-center justify-center overflow-auto p-4">
      <img v-if="kind === 'image'" :src="url" :alt="path" class="max-h-full max-w-full object-contain">
      <audio v-else-if="kind === 'audio'" :src="url" controls class="w-full max-w-xl" />
      <video v-else :src="url" controls class="max-h-full max-w-full" />
    </div>
  </div>
</template>
