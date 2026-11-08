<script setup lang="ts">
import { Check, ChevronsUpDown } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'

// An empty value stands for "the default", offered as the first choice.
const props = defineProps<{
  id: string
  options: { id: string, name: string }[]
  defaultLabel: string
  searchPlaceholder: string
}>()
const model = defineModel<string>({ required: true })
const open = ref(false)

const label = computed(() => {
  if (!model.value) return props.defaultLabel
  return props.options.find(o => o.id === model.value)?.name ?? 'no longer available'
})

function pick(id: string) {
  model.value = id
  open.value = false
}
</script>

<template>
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
        <span class="truncate" :class="!model && 'text-muted-foreground'">{{ label }}</span>
        <ChevronsUpDown class="size-4 shrink-0 opacity-50" aria-hidden="true" />
      </Button>
    </PopoverTrigger>
    <PopoverContent class="w-(--reka-popover-trigger-width) p-0">
      <Command>
        <CommandInput :placeholder="searchPlaceholder" />
        <CommandList>
          <CommandEmpty>Nothing matches that.</CommandEmpty>
          <CommandGroup>
            <CommandItem :value="defaultLabel" class="gap-2" @select="pick('')">
              <Check class="size-4 shrink-0" :class="!model ? 'opacity-100' : 'opacity-0'" aria-hidden="true" />
              <span class="truncate font-mono text-xs text-muted-foreground">{{ defaultLabel }}</span>
            </CommandItem>
            <CommandItem v-for="o in options" :key="o.id" :value="`${o.name} ${o.id}`" class="gap-2" @select="pick(o.id)">
              <Check class="size-4 shrink-0" :class="o.id === model ? 'opacity-100' : 'opacity-0'" aria-hidden="true" />
              <span class="truncate font-mono text-xs">{{ o.name }}</span>
            </CommandItem>
          </CommandGroup>
        </CommandList>
      </Command>
    </PopoverContent>
  </Popover>
</template>
