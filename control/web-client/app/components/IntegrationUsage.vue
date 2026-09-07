<script setup lang="ts">
import { Check, Copy } from '@lucide/vue'
import { Button } from '@/components/ui/button'

const props = defineProps<{
  host: string
  repo?: string
  scheme?: string
}>()

const scheme = computed(() => props.scheme || 'https')
const repo = computed(() => props.repo || '<owner>/<repo>')

const cloneCmd = computed(() => `git clone ${scheme.value}://${props.host}/${repo.value}.git`)
const ghCmd = computed(() => `export GH_HOST=${props.host}\nexport GH_ENTERPRISE_TOKEN=unused`)

const copied = ref<string | null>(null)

async function copy(key: string, value: string) {
  try {
    await navigator.clipboard.writeText(value)
    copied.value = key
    setTimeout(() => {
      if (copied.value === key) copied.value = null
    }, 2000)
  }
  catch {
  }
}
</script>

<template>
  <div class="space-y-3">
    <div>
      <div class="flex items-center justify-between gap-2">
        <p class="text-xs text-muted-foreground">Clone from inside the VM</p>
        <Button variant="ghost" size="sm" aria-label="Copy the clone command" @click="copy('clone', cloneCmd)">
          <component :is="copied === 'clone' ? Check : Copy" class="size-3.5" aria-hidden="true" />
          {{ copied === 'clone' ? 'Copied' : 'Copy' }}
        </Button>
      </div>
      <pre class="mt-1 overflow-x-auto rounded-md bg-muted px-3 py-2 font-mono text-xs">{{ cloneCmd }}</pre>
    </div>

    <div>
      <div class="flex items-center justify-between gap-2">
        <p class="text-xs text-muted-foreground">For the <span class="font-mono">gh</span> CLI</p>
        <Button variant="ghost" size="sm" aria-label="Copy the gh environment" @click="copy('gh', ghCmd)">
          <component :is="copied === 'gh' ? Check : Copy" class="size-3.5" aria-hidden="true" />
          {{ copied === 'gh' ? 'Copied' : 'Copy' }}
        </Button>
      </div>
      <pre class="mt-1 overflow-x-auto rounded-md bg-muted px-3 py-2 font-mono text-xs">{{ ghCmd }}</pre>
    </div>

    <p class="text-xs text-muted-foreground">
      Nothing to install or configure in the guest — the URL is the only difference from cloning
      GitHub directly, and no credential is ever stored on the VM.
    </p>
  </div>
</template>
