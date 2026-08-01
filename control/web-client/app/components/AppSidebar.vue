<script setup lang="ts">
import { PanelLeftClose } from '@lucide/vue'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'

/** Sheet open state for the mobile drawer; the header owns the trigger. */
const open = defineModel<boolean>('open', { default: false })

const { expanded, toggle } = useSidebar()
const route = useRoute()

// Close the drawer whenever navigation happens.
watch(() => route.fullPath, () => {
  open.value = false
})
</script>

<template>
  <!-- Desktop: icon rail, expandable via the logo -->
  <aside
    aria-label="Sidebar"
    class="fixed inset-y-0 left-0 z-50 hidden flex-col border-r border-border/80 bg-sidebar transition-[width] duration-200 md:flex"
    :class="expanded ? 'w-56' : 'w-12'"
  >
    <div
      class="flex h-14 shrink-0 items-center border-b border-border/80"
      :class="expanded ? 'gap-2.5 pl-3 pr-1.5' : 'justify-center'"
    >
      <!-- Collapsed, the mark expands the rail; expanded, it links home. -->
      <button
        v-if="!expanded"
        type="button"
        class="grid size-6 place-items-center rounded-sm bg-primary font-mono text-sm leading-none font-bold text-primary-foreground transition-opacity hover:opacity-80"
        aria-label="Expand sidebar"
        aria-expanded="false"
        @click="toggle"
      >
        <span aria-hidden="true">0</span>
      </button>

      <NuxtLink v-else to="/dashboard" class="flex min-w-0 items-center gap-2.5">
        <span
          aria-hidden="true"
          class="grid size-6 shrink-0 place-items-center rounded-sm bg-primary font-mono text-sm leading-none font-bold text-primary-foreground"
        >
          0
        </span>
        <span class="truncate font-mono text-sm font-semibold tracking-tight">
          dummie<span class="text-primary-text">/</span>
        </span>
      </NuxtLink>

      <TooltipProvider v-if="expanded" :delay-duration="150">
        <Tooltip>
          <TooltipTrigger as-child>
            <button
              type="button"
              class="ml-auto grid size-7 shrink-0 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
              aria-label="Collapse sidebar"
              aria-expanded="true"
              @click="toggle"
            >
              <PanelLeftClose class="size-4" aria-hidden="true" />
            </button>
          </TooltipTrigger>
          <TooltipContent side="right" class="font-mono text-xs">
            Collapse
          </TooltipContent>
        </Tooltip>
      </TooltipProvider>
    </div>

    <AppSidebarNav :expanded="expanded" @expand="expanded = true" />
  </aside>

  <!-- Mobile: same nav as a drawer, always labelled -->
  <Sheet v-model:open="open">
    <SheetContent side="left" class="w-64 gap-0 p-0">
      <SheetHeader class="h-14 shrink-0 flex-row items-center gap-2.5 border-b border-border/80 px-4">
        <span
          aria-hidden="true"
          class="grid size-6 place-items-center rounded-sm bg-primary font-mono text-sm leading-none font-bold text-primary-foreground"
        >
          0
        </span>
        <SheetTitle class="font-mono text-sm font-semibold tracking-tight">
          dummie<span class="text-primary-text">/</span>
        </SheetTitle>
        <SheetDescription class="sr-only">Site navigation</SheetDescription>
      </SheetHeader>

      <AppSidebarNav expanded />
    </SheetContent>
  </Sheet>
</template>
