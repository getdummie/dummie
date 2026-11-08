<script setup lang="ts">
import { Trash2 } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import {
  applyPreset,
  describeTarget,
  destinationPlaceholder,
  domainPortChoices,
  portPresets,
  resetForKind,
  type TargetForm,
} from '@/lib/targets'

defineProps<{ index: number }>()
const emit = defineEmits<{ remove: [] }>()
const t = defineModel<TargetForm>({ required: true })
</script>

<template>
  <div class="space-y-3 rounded-lg border border-border p-3">
    <div class="flex items-center justify-between gap-2">
      <p class="eyebrow text-muted-foreground">Destination {{ index + 1 }}</p>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        class="size-7 text-destructive hover:text-destructive"
        :aria-label="`Remove destination ${index + 1}`"
        @click="emit('remove')"
      >
        <Trash2 class="size-4" aria-hidden="true" />
      </Button>
    </div>

    <div class="grid gap-3 sm:grid-cols-2">
      <div class="space-y-2">
        <Label :for="`t-kind-${index}`">Type</Label>
        <Select v-model="t.kind" @update:model-value="resetForKind(t)">
          <SelectTrigger :id="`t-kind-${index}`" class="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="domain">Domain</SelectItem>
            <SelectItem value="ip">IP address or CIDR</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div class="space-y-2">
        <Label :for="`t-dest-${index}`">Destination</Label>
        <Input
          :id="`t-dest-${index}`"
          v-model="t.destination"
          :placeholder="destinationPlaceholder(t.kind)"
        />
      </div>
    </div>

    <div v-if="t.kind === 'domain'" class="space-y-2">
      <Label :for="`t-dports-${index}`">Allow on</Label>
      <Select v-model="t.domainPorts">
        <SelectTrigger :id="`t-dports-${index}`" class="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem v-for="c in domainPortChoices" :key="c.value" :value="c.value">
            {{ c.label }}
          </SelectItem>
        </SelectContent>
      </Select>
    </div>

    <template v-else>
      <div class="space-y-2">
        <Label :for="`t-preset-${index}`">Protocol</Label>
        <Select v-model="t.preset" @update:model-value="(v: unknown) => applyPreset(t, String(v))">
          <SelectTrigger :id="`t-preset-${index}`" class="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem v-for="p in portPresets" :key="p.key" :value="p.key">
              {{ p.label }}
            </SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div class="grid gap-3 sm:grid-cols-2">
        <div class="space-y-2">
          <Label :for="`t-transport-${index}`">Transport</Label>
          <Select v-model="t.transport">
            <SelectTrigger :id="`t-transport-${index}`" class="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="tcp">tcp</SelectItem>
              <SelectItem value="udp">udp</SelectItem>
              <SelectItem value="icmp">icmp (ping)</SelectItem>
              <SelectItem value="any">any</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div v-if="t.transport !== 'icmp'" class="space-y-2">
          <Label :for="`t-ports-${index}`">Ports</Label>
          <Input
            :id="`t-ports-${index}`"
            v-model="t.ports"
            placeholder="443, 80,443, 1000:2000"
          />
        </div>
      </div>
    </template>

    <div class="grid gap-3 sm:grid-cols-2">
      <div class="space-y-2">
        <Label :for="`t-note-${index}`">Note</Label>
        <Input :id="`t-note-${index}`" v-model="t.note" placeholder="why this is needed" />
      </div>
      <div class="space-y-2">
        <Label :for="`t-ttl-${index}`">TTL (seconds)</Label>
        <Input
          :id="`t-ttl-${index}`"
          v-model="t.ttl_seconds"
          type="number"
          min="0"
          step="1"
          inputmode="numeric"
        />
      </div>
    </div>

    <p class="font-mono text-xs text-muted-foreground">
      {{ describeTarget(t) }}{{ Number(t.ttl_seconds) > 0 ? ` · expires in ${t.ttl_seconds}s` : '' }}
    </p>
  </div>
</template>
