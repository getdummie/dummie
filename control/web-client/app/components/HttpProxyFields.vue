<script lang="ts">
import type { HTTPMethod, HTTPProxyBody } from '@/composables/useIntegrations'

// stored marks a header whose value lives on the server; left blank, it is kept.
export interface HeaderRow { name: string, value: string, stored: boolean }

export interface HTTPProxyForm {
  target_url: string
  methods: HTTPMethod[]
  verify_tls: boolean
  headers: HeaderRow[]
}

export function emptyProxyForm(): HTTPProxyForm {
  return { target_url: '', methods: ['GET', 'HEAD'], verify_tls: true, headers: [] }
}

export function proxyFormBody(f: HTTPProxyForm): HTTPProxyBody {
  return {
    target_url: f.target_url.trim(),
    methods: f.methods,
    verify_tls: f.verify_tls,
    headers: f.headers
      .filter(h => h.name.trim())
      .map(h => (h.stored && !h.value ? { name: h.name.trim() } : { name: h.name.trim(), value: h.value })),
  }
}

export function proxyFormValid(f: HTTPProxyForm): boolean {
  if (!/^https?:\/\/[^/?#\s]+/i.test(f.target_url.trim()) || !f.methods.length) return false
  return f.headers.every(h => !h.name.trim() || h.stored || h.value)
}
</script>

<script setup lang="ts">
import { Plus, X } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { HTTP_METHODS } from '@/composables/useIntegrations'

const props = defineProps<{ idPrefix: string }>()
const form = defineModel<HTTPProxyForm>({ required: true })

const insecureOverHTTP = computed(() => /^http:\/\//i.test(form.value.target_url.trim()) && form.value.headers.length > 0)

function setMethod(m: HTTPMethod, on: boolean) {
  const next = new Set(form.value.methods)
  if (on) next.add(m)
  else next.delete(m)
  form.value = { ...form.value, methods: HTTP_METHODS.filter(x => next.has(x)) }
}

function addHeader() {
  form.value = { ...form.value, headers: [...form.value.headers, { name: '', value: '', stored: false }] }
}

function removeHeader(i: number) {
  form.value = { ...form.value, headers: form.value.headers.filter((_, j) => j !== i) }
}

const id = (s: string) => `${props.idPrefix}-${s}`
</script>

<template>
  <div class="space-y-4">
    <div class="space-y-1.5">
      <Label :for="id('target')">Target URL</Label>
      <Input
        :id="id('target')"
        v-model="form.target_url"
        placeholder="https://api.example.com/v1"
        spellcheck="false"
        autocomplete="off"
        class="font-mono text-sm"
      />
      <p class="text-xs text-muted-foreground">
        Requests are forwarded here with their path and query appended.
      </p>
    </div>

    <fieldset class="space-y-2">
      <legend class="text-sm font-medium">Allowed methods</legend>
      <div class="flex flex-wrap gap-x-4 gap-y-2">
        <div v-for="m in HTTP_METHODS" :key="m" class="flex items-center gap-2">
          <Checkbox
            :id="id(`method-${m}`)"
            :model-value="form.methods.includes(m)"
            @update:model-value="(v: boolean | 'indeterminate') => setMethod(m, v === true)"
          />
          <Label :for="id(`method-${m}`)" class="font-mono text-xs font-normal">{{ m }}</Label>
        </div>
      </div>
    </fieldset>

    <div class="flex items-center justify-between gap-3">
      <div>
        <Label :for="id('verify')" class="font-normal">Verify the target's TLS certificate</Label>
        <p v-if="!form.verify_tls" class="mt-0.5 text-xs text-destructive">
          Anyone on the path can impersonate the target and read these headers.
        </p>
      </div>
      <Switch :id="id('verify')" v-model="form.verify_tls" />
    </div>

    <fieldset class="space-y-2">
      <legend class="text-sm font-medium">Headers</legend>
      <p class="text-xs text-muted-foreground">
        Added to every request and kept on the control server; VMs never see them. Values cannot
        be read back — leave a saved one blank to keep it.
      </p>
      <div
        v-for="(h, i) in form.headers"
        :key="i"
        class="flex flex-col gap-2 sm:flex-row sm:items-center"
      >
        <Input
          v-model="h.name"
          :aria-label="`Header ${i + 1} name`"
          placeholder="Authorization"
          spellcheck="false"
          autocomplete="off"
          class="font-mono text-sm sm:w-48"
        />
        <div class="flex min-w-0 flex-1 items-center gap-2">
          <Input
            v-model="h.value"
            :aria-label="`Header ${i + 1} value`"
            :placeholder="h.stored ? '•••••• saved' : 'value'"
            spellcheck="false"
            autocomplete="off"
            class="min-w-0 flex-1 font-mono text-sm"
          />
          <Button type="button" variant="ghost" size="sm" :aria-label="`Remove header ${i + 1}`" @click="removeHeader(i)">
            <X class="size-4" aria-hidden="true" />
          </Button>
        </div>
      </div>
      <Button type="button" variant="secondary" size="sm" @click="addHeader">
        <Plus class="size-4" aria-hidden="true" />
        Add header
      </Button>
      <p v-if="insecureOverHTTP" class="text-xs text-destructive">
        The target is plain http, so these headers travel unencrypted.
      </p>
    </fieldset>
  </div>
</template>
