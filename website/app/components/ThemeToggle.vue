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

// Provided by @nuxtjs/color-mode. `preference` is what the user picks
// (light | dark | system); `value` is the resolved mode actually applied.
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
      <!-- The label names the control *and* reports its state. A bare "Toggle
           theme" leaves a screen-reader user with no way to know which mode is
           active. (WCAG 4.1.2) -->
      <Button variant="ghost" size="icon" :aria-label="`Theme: ${currentLabel}. Change theme`">
        <!-- Icon reflects the resolved mode. ClientOnly avoids an SSR/first-paint
             mismatch since the applied theme is only known on the client. -->
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
      <!-- A radio group rather than plain items with a tick icon: reka-ui then
           emits role="menuitemradio" + aria-checked, so the current choice is
           exposed to assistive tech instead of being a purely visual checkmark. -->
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
