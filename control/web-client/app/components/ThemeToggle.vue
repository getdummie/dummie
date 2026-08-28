<script setup lang="ts">
import { Monitor, Moon, Sun } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

const colorMode = useColorMode()

const options = [
  { value: 'light', label: 'Light', icon: Sun },
  { value: 'dark', label: 'Dark', icon: Moon },
  { value: 'system', label: 'System', icon: Monitor },
] as const

const currentLabel = computed(
  () => options.find(o => o.value === colorMode.preference)?.label ?? 'System',
)
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <Button variant="ghost" size="icon" :aria-label="`Theme: ${currentLabel}. Change theme`">
        <ClientOnly>
          <Moon v-if="colorMode.value === 'dark'" class="size-[1.15rem]" aria-hidden="true" />
          <Sun v-else class="size-[1.15rem]" aria-hidden="true" />
          <template #fallback>
            <Sun class="size-[1.15rem]" aria-hidden="true" />
          </template>
        </ClientOnly>
      </Button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="end" class="w-40 font-mono">
      <DropdownMenuRadioGroup
        :model-value="colorMode.preference"
        @update:model-value="(v: string) => colorMode.preference = v"
      >
        <DropdownMenuRadioItem
          v-for="opt in options"
          :key="opt.value"
          :value="opt.value"
          class="text-xs"
        >
          <component :is="opt.icon" class="size-4" aria-hidden="true" />
          {{ opt.label }}
        </DropdownMenuRadioItem>
      </DropdownMenuRadioGroup>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
