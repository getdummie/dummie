<script setup lang="ts">
import type { OIDCProvider } from '@/composables/useAuth'
import { Button } from '@/components/ui/button'
import { safeRedirect } from '@/lib/redirect'

// Rendered above the password form on both sign-in and sign-up. Nothing at all
// when no provider is configured, which is the common case -- an empty divider
// over an empty row is worse than the absence of the section.
// divider is off where there is no password form underneath for the "or" to
// separate this from.
const props = withDefaults(defineProps<{ label?: string, divider?: boolean }>(), { divider: true })

const route = useRoute()
const { oidcProviders, oidcStartURL } = useAuth()

const providers = ref<OIDCProvider[]>([])
const going = ref<string | null>(null)

onMounted(async () => {
  providers.value = await oidcProviders()
})

// A ?redirect= that survived the sign-in page has to survive the provider too:
// someone bounced here mid proxy hand-off wants the guest they were opening, not
// the dashboard. Narrowed to a same-origin path first, because it comes out of
// the address bar and the server would otherwise be asked to trust it.
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
