<script setup lang="ts">
import type { DeniedAttempt, TargetRecord } from '@/lib/targets'
import { ClockPlus, RefreshCw } from '@lucide/vue'
import Hostname from '@/components/Hostname.vue'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { deniedCovered, deniedKey, deniedParent, isEverywhere, tempAllowTTL } from '@/lib/targets'

const props = defineProps<{
  targets: TargetRecord[]
  denied: DeniedAttempt[]
  deniedAvailable: boolean
  loaded: boolean
  allowing: string | null
  allowError: string | null
  refreshing: boolean
}>()

defineEmits<{ allow: [d: DeniedAttempt], refresh: [] }>()

const tempAllowMinutes = tempAllowTTL / 60

const allowAll = computed(() => props.targets.some(isEverywhere))

function portsLabel(t: TargetRecord) {
  if (t.kind !== 'domain') return [t.transport, t.ports].filter(Boolean).join(' ') || 'any'
  return t.ports === 'none' ? 'lookup only' : (t.ports || '443, 80')
}

function deniedLabel(d: DeniedAttempt) {
  return d.domain || d.address || '—'
}

function deniedDetail(d: DeniedAttempt) {
  if (d.kind === 'lookup') return 'lookup'
  return [d.proto, d.port].filter(Boolean).join(' ')
}

const deniedRows = computed(() => props.denied.map(d => ({
  d,
  covered: deniedCovered(d, props.targets),
  parent: deniedParent(d, props.targets),
})))
</script>

<template>
  <TooltipProvider :delay-duration="150">
    <div class="flex min-h-0 flex-1 flex-col overflow-y-auto">
      <section aria-labelledby="net-denied-heading" class="border-b border-border px-4 py-3">
        <div class="flex items-center justify-between gap-2">
          <h2 id="net-denied-heading" class="eyebrow text-muted-foreground">Denied</h2>
          <div class="flex items-center gap-1">
            <span class="font-mono text-[11px] text-muted-foreground">last hour · every 15s</span>
            <Tooltip>
              <TooltipTrigger as-child>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  :disabled="refreshing"
                  aria-label="Refresh denied and allowed destinations"
                  @click="$emit('refresh')"
                >
                  <RefreshCw :class="refreshing && 'animate-spin'" aria-hidden="true" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>Refresh now</TooltipContent>
            </Tooltip>
          </div>
        </div>
        <p v-if="allowError" class="mt-2 text-xs text-destructive">{{ allowError }}</p>
        <p v-if="!loaded" class="mt-2 text-xs text-muted-foreground">Loading…</p>
        <p v-else-if="!deniedAvailable" class="mt-2 text-xs text-muted-foreground">
          Denied attempts are not available right now.
        </p>
        <p v-else-if="!denied.length" class="mt-2 text-xs text-muted-foreground">Nothing has been denied.</p>
        <ul v-else class="mt-2 space-y-1" aria-live="polite">
          <li
            v-for="row in deniedRows"
            :key="deniedKey(row.d)"
            class="-mx-2 rounded-md px-2 py-1.5"
            :class="row.covered && 'bg-primary/10'"
          >
            <div class="grid grid-cols-[minmax(0,1fr)_auto_1.5rem] items-center gap-x-2">
              <Hostname :name="deniedLabel(row.d)" class="min-w-0 font-mono text-xs break-all" />
              <span class="font-mono text-xs tabular-nums text-muted-foreground">{{ row.d.attempts }}</span>
              <span v-if="row.covered" class="size-6" aria-hidden="true" />
              <Tooltip v-else>
                <TooltipTrigger as-child>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    class="shrink-0"
                    :disabled="allowing === deniedKey(row.d)"
                    :aria-label="`Temporarily allow ${deniedLabel(row.d)} for ${tempAllowMinutes} minutes`"
                    @click="$emit('allow', row.d)"
                  >
                    <ClockPlus :class="allowing === deniedKey(row.d) && 'animate-pulse'" aria-hidden="true" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>Allow for {{ tempAllowMinutes }} minutes</TooltipContent>
              </Tooltip>
              <div class="col-span-2 font-mono text-[11px] text-muted-foreground">
                <span v-if="row.covered" class="text-primary-text">
                  allowed<template v-if="row.parent"> via <span class="font-semibold">{{ row.parent }}</span></template>
                </span>
                <template v-else>{{ deniedDetail(row.d) }}</template>
              </div>
            </div>
          </li>
        </ul>
      </section>

      <section aria-labelledby="net-allowed-heading" class="px-4 py-3">
        <h2 id="net-allowed-heading" class="eyebrow text-muted-foreground">Allowed</h2>
        <p v-if="!loaded" class="mt-2 text-xs text-muted-foreground">Loading…</p>
        <p v-else-if="allowAll" class="mt-2 text-xs text-muted-foreground">Everything is allowed.</p>
        <p v-else-if="!targets.length" class="mt-2 text-xs text-muted-foreground">Nothing is allowed yet.</p>
        <ul v-else class="mt-2 space-y-1">
          <li v-for="t in targets" :key="t.id" class="py-1">
            <Hostname :name="t.destination" class="font-mono text-xs break-all" />
            <div class="mt-0.5 font-mono text-[11px] text-muted-foreground">{{ portsLabel(t) }}</div>
          </li>
        </ul>
      </section>
    </div>
  </TooltipProvider>
</template>
