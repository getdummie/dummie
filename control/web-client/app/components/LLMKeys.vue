<script setup lang="ts">
import { Trash2 } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'

interface LLMKey {
  provider: string
  label: string
  plans: { id: string, label: string }[]
  plan: string
  key_set: boolean
  updated_at?: string
}

const props = defineProps<{ admin?: boolean }>()

const { authFetch } = useAuth()
const base = computed(() => (props.admin ? '/admin/llm-keys' : '/me/llm-keys'))

const items = ref<LLMKey[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const drafts = reactive<Record<string, { plan: string, key: string }>>({})
const saving = ref<string | null>(null)
const saved = ref<string | null>(null)
const removing = ref<LLMKey | null>(null)
const removeBusy = ref(false)

async function readMessage(res: Response): Promise<string | null> {
  try {
    const b = await res.json()
    return typeof b?.message === 'string' ? b.message : null
  }
  catch {
    return null
  }
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await authFetch(base.value)
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    items.value = (await res.json()).items ?? []
    for (const k of items.value) drafts[k.provider] = { plan: k.plan, key: '' }
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not load the LLM keys'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

function dirty(k: LLMKey) {
  const d = drafts[k.provider]
  return !!d && (d.key.trim() !== '' || (k.key_set && d.plan !== k.plan))
}

async function save(k: LLMKey) {
  const d = drafts[k.provider]
  if (!d) return
  saving.value = k.provider
  error.value = null
  try {
    const res = await authFetch(`${base.value}/${k.provider}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ plan: d.plan, api_key: d.key.trim() }),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    await load()
    saved.value = k.provider
    setTimeout(() => { saved.value = null }, 2000)
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not save the key'
  }
  finally {
    saving.value = null
  }
}

async function remove() {
  if (!removing.value) return
  removeBusy.value = true
  error.value = null
  try {
    const res = await authFetch(`${base.value}/${removing.value.provider}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    removing.value = null
    await load()
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not remove the key'
  }
  finally {
    removeBusy.value = false
  }
}

const prefix = (p: string) => (props.admin ? `${p}@global` : p)
</script>

<template>
  <section>
    <p class="eyebrow mb-2 text-primary-text">{{ admin ? '// admin · integrations' : '// integrations · llm' }}</p>
    <h2 class="text-xl font-semibold tracking-tight">{{ admin ? 'Global LLM keys' : 'LLM keys' }}</h2>
    <p class="mt-2 max-w-2xl text-sm text-muted-foreground">
      <template v-if="admin">
        Usable from every user's VMs, as models prefixed with
        <code class="rounded bg-muted px-1 py-0.5 font-mono text-xs">&lt;provider&gt;@global/</code>.
      </template>
      <template v-else>
        Usable from all of your VMs, and only yours, as models prefixed with
        <code class="rounded bg-muted px-1 py-0.5 font-mono text-xs">&lt;provider&gt;/</code>.
      </template>
      VMs call
      <code class="rounded bg-muted px-1 py-0.5 font-mono text-xs">https://llm.int.&lt;tld&gt;/v1</code>
      with any placeholder key; the real one stays encrypted on this server.
    </p>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Something went wrong</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <div class="mt-6 divide-y divide-border rounded-lg border border-border" :aria-busy="loading">
      <div v-if="loading" class="space-y-2 p-4" aria-hidden="true">
        <Skeleton class="h-4 w-32" />
        <Skeleton class="h-9 w-full max-w-md" />
      </div>

      <form
        v-for="k in items"
        v-else
        :key="k.provider"
        class="space-y-4 p-4 sm:p-6"
        @submit.prevent="save(k)"
      >
        <div class="flex flex-wrap items-center justify-between gap-2">
          <p class="text-sm font-medium">{{ k.label }}</p>
          <Badge :variant="k.key_set ? 'default' : 'outline'">
            {{ k.key_set ? 'Configured' : 'Not configured' }}
          </Badge>
        </div>

        <div v-if="drafts[k.provider]" class="grid gap-4 sm:grid-cols-[12rem_1fr]">
          <div class="space-y-1.5">
            <Label :for="`llm-plan-${k.provider}`">Plan</Label>
            <NativeSelect :id="`llm-plan-${k.provider}`" v-model="drafts[k.provider]!.plan">
              <NativeSelectOption v-for="p in k.plans" :key="p.id" :value="p.id">{{ p.label }}</NativeSelectOption>
            </NativeSelect>
          </div>
          <div class="space-y-1.5">
            <Label :for="`llm-key-${k.provider}`">API key</Label>
            <Input
              :id="`llm-key-${k.provider}`"
              v-model="drafts[k.provider]!.key"
              type="password"
              autocomplete="off"
              spellcheck="false"
              :placeholder="k.key_set ? '••••••••' : 'Paste your API key'"
              class="font-mono text-sm"
            />
            <p class="text-xs text-muted-foreground">
              {{ k.key_set ? 'Stored and never sent back. Leave empty to keep it.' : 'Checked against the provider before it is saved.' }}
            </p>
          </div>
        </div>

        <p v-if="k.key_set" class="text-xs text-muted-foreground">
          Models appear as
          <code class="rounded bg-muted px-1 py-0.5 font-mono">{{ prefix(k.provider) }}/&lt;model&gt;</code>
          in <code class="rounded bg-muted px-1 py-0.5 font-mono">/v1/models</code>.
        </p>

        <div class="flex flex-wrap items-center gap-2">
          <Button type="submit" :disabled="saving === k.provider || !dirty(k)">
            {{ saving === k.provider ? 'Checking…' : 'Save' }}
          </Button>
          <span v-if="saved === k.provider" class="text-sm text-muted-foreground">Saved</span>
          <Button
            v-if="k.key_set"
            type="button"
            variant="ghost"
            class="ml-auto text-destructive hover:text-destructive"
            @click="removing = k"
          >
            <Trash2 class="size-4" aria-hidden="true" />
            Remove
          </Button>
        </div>
      </form>
    </div>

    <Dialog :open="!!removing" @update:open="(v: boolean) => { if (!v) removing = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Remove the {{ removing?.label }} key?</DialogTitle>
          <DialogDescription>
            {{ admin ? 'Every user' : 'Your VMs' }} lose access to
            <span class="font-mono">{{ removing ? prefix(removing.provider) : '' }}/…</span>
            models within a minute.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <DialogClose as-child>
            <Button variant="secondary">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" :disabled="removeBusy" @click="remove">
            {{ removeBusy ? 'Removing…' : 'Remove' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </section>
</template>
