<script setup lang="ts">
import type { OIDCProvider } from '@/composables/useAuth'
import { Button } from '@/components/ui/button'
import { safeRedirect } from '@/lib/redirect'

const props = withDefaults(defineProps<{ label?: string, divider?: boolean }>(), { divider: true })

const route = useRoute()
const { oidcProviders, oidcStartURL } = useAuth()

const providers = ref<OIDCProvider[]>([])
const going = ref<string | null>(null)

onMounted(async () => {
  providers.value = await oidcProviders()
})

function start(p: OIDCProvider) {
  going.value = p.slug
  window.location.href = oidcStartURL(p.slug, safeRedirect(route.query.redirect))
}
</script>

<template>
  <div v-if="providers.length" class="mb-6 space-y-3">
    <Button
      v-for="p in providers"
      :key="p.slug"
      type="button"
      variant="secondary"
      class="w-full font-mono text-sm"
      :disabled="!!going"
      @click="start(p)"
    >
      {{ going === p.slug ? 'Redirecting…' : `${props.label ?? 'Continue with'} ${p.display_name}` }}
    </Button>

    <div v-if="props.divider" class="flex items-center gap-3" aria-hidden="true">
      <span class="h-px flex-1 bg-border" />
      <span class="eyebrow text-muted-foreground">or</span>
      <span class="h-px flex-1 bg-border" />
    </div>
  </div>
</template>
