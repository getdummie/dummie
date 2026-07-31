<script setup lang="ts">
import { Check, Monitor, Moon, Sun } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
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

function setMode(value: 'light' | 'dark' | 'system') {
  colorMode.preference = value
}
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <Button variant="ghost" size="icon" aria-label="Toggle theme">
        <!-- Icon reflects the resolved mode. ClientOnly avoids an SSR/first-paint
             mismatch since the applied theme is only known on the client. -->
        <ClientOnly>
          <Moon v-if="colorMode.value === 'dark'" class="size-[1.15rem]" />
          <Sun v-else class="size-[1.15rem]" />
          <template #fallback>
            <Sun class="size-[1.15rem]" />
          </template>
        </ClientOnly>
      </Button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="end" class="w-40 font-mono">
      <DropdownMenuItem
        v-for="opt in options"
        :key="opt.value"
        class="justify-between text-xs"
        @select="setMode(opt.value)"
      >
        <span class="flex items-center gap-2">
          <component :is="opt.icon" class="size-4" />
          {{ opt.label }}
        </span>
        <Check v-if="colorMode.preference === opt.value" class="size-3.5 text-primary" />
      </DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
