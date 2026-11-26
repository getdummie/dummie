<script setup lang="ts">
import { Check, ChevronsUpDown, X } from '@lucide/vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import type { DestinationGroup } from '@/lib/targets'

const props = defineProps<{ id: string, groups: DestinationGroup[] }>()
const model = defineModel<string[]>({ required: true })
const open = ref(false)

function nameOf(id: string) {
  return props.groups.find(g => g.id === id)?.name ?? 'no longer available'
}

function toggle(id: string) {
  model.value = model.value.includes(id) ? model.value.filter(x => x !== id) : [...model.value, id]
}
</script>

<template>
  <div class="space-y-2">
    <Popover v-model:open="open">
      <PopoverTrigger as-child>
        <Button
          :id="id"
          type="button"
          variant="outline"
          role="combobox"
          :aria-expanded="open"
          class="w-full justify-between font-mono text-xs font-normal"
        >
          <span class="truncate text-muted-foreground">
            {{ model.length ? `${model.length} group${model.length === 1 ? '' : 's'} selected` : 'Pick destination groups' }}
          </span>
          <ChevronsUpDown class="size-4 shrink-0 opacity-50" aria-hidden="true" />
        </Button>
      </PopoverTrigger>
      <PopoverContent class="w-(--reka-popover-trigger-width) p-0">
        <Command>
          <CommandInput placeholder="Search groups…" />
          <CommandList>
            <CommandEmpty>Nothing matches that.</CommandEmpty>
            <CommandGroup>
              <CommandItem
                v-for="g in groups"
                :key="g.id"
                :value="`${g.name} ${g.entries.map(e => e.destination).join(' ')}`"
                class="gap-2"
                @select="toggle(g.id)"
              >
                <Check class="size-4 shrink-0" :class="model.includes(g.id) ? 'opacity-100' : 'opacity-0'" aria-hidden="true" />
                <span class="min-w-0">
                  <span class="block truncate font-mono text-xs">{{ g.name }}</span>
                  <span class="block truncate text-xs text-muted-foreground">
                    {{ g.entries.map(e => e.destination).join(', ') || 'empty' }}
                  </span>
                </span>
              </CommandItem>
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
    <div v-if="model.length" class="flex flex-wrap gap-1.5">
      <Badge v-for="gid in model" :key="gid" variant="secondary" class="gap-1 font-mono text-xs font-normal">
        {{ nameOf(gid) }}
        <button
          type="button"
          class="ml-0.5 rounded-sm opacity-70 hover:opacity-100 focus-visible:outline-2 focus-visible:outline-ring"
          :aria-label="`Leave out ${nameOf(gid)}`"
          @click="toggle(gid)"
        >
          <X class="size-3" aria-hidden="true" />
        </button>
      </Badge>
    </div>
  </div>
</template>
