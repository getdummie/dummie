<script setup lang="ts">
import type { ThemePreference } from '@/composables/useConsoleTheme'
import { Monitor, Moon, Sun } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

// Without props it drives the app-wide theme; with them it drives whatever
// scoped theme the caller owns (the console keeps its own).
const props = defineProps<{
  preference?: ThemePreference
  isDark?: boolean
}>()

const emit = defineEmits<{ 'update:preference': [ThemePreference] }>()

const colorMode = useColorMode()

const options = [
  { value: 'light', label: 'Light', icon: Sun },
  { value: 'dark', label: 'Dark', icon: Moon },
  { value: 'system', label: 'System', icon: Monitor },
] as const

const current = computed(() => props.preference ?? colorMode.preference)
const dark = computed(() => props.isDark ?? colorMode.value === 'dark')

const currentLabel = computed(
  () => options.find(o => o.value === current.value)?.label ?? 'System',
)

function select(value: string) {
  if (props.preference !== undefined) emit('update:preference', value as ThemePreference)
  else colorMode.preference = value
}
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <Button variant="ghost" size="icon" :aria-label="`Theme: ${currentLabel}. Change theme`">
        <ClientOnly>
          <Moon v-if="dark" class="size-[1.15rem]" aria-hidden="true" />
          <Sun v-else class="size-[1.15rem]" aria-hidden="true" />
          <template #fallback>
            <Sun class="size-[1.15rem]" aria-hidden="true" />
          </template>
        </ClientOnly>
      </Button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="end" class="w-40 font-mono">
      <DropdownMenuRadioGroup
        :model-value="current"
        @update:model-value="(v: string) => select(v)"
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
