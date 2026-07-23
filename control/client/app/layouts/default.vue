<script setup lang="ts">
import { Button } from '@/components/ui/button'

const nav = [
  { label: 'Product', href: '/#product' },
  { label: 'Runtime', href: '/#runtime' },
  { label: 'Health', href: '/health' },
]

const year = 2026
</script>

<template>
  <div class="min-h-svh flex flex-col bg-background text-foreground antialiased">
    <!-- Header -->
    <header class="sticky top-0 z-50 border-b border-border/80 bg-background/80 backdrop-blur">
      <div class="mx-auto flex h-14 max-w-6xl items-center justify-between px-4 sm:px-6">
        <NuxtLink to="/" class="group flex items-center gap-2.5">
          <span class="grid size-6 place-items-center rounded-sm bg-primary text-primary-foreground font-mono text-sm font-bold leading-none">
            0
          </span>
          <span class="font-mono text-sm font-semibold tracking-tight">
            dummie<span class="text-primary">/</span>
          </span>
        </NuxtLink>

        <nav class="hidden items-center gap-1 md:flex">
          <a
            v-for="item in nav"
            :key="item.href"
            :href="item.href"
            class="eyebrow rounded-sm px-3 py-2 text-muted-foreground transition-colors hover:text-foreground"
          >
            {{ item.label }}
          </a>
        </nav>

        <div class="flex items-center gap-1.5">
          <Button as-child variant="default" size="sm" class="hidden font-mono text-xs sm:inline-flex">
            <a href="/#start">Start building</a>
          </Button>
          <ThemeToggle />
        </div>
      </div>
    </header>

    <!-- Page -->
    <main class="flex-1">
      <slot />
    </main>

    <!-- Footer -->
    <footer class="border-t border-border/80">
      <div class="mx-auto flex max-w-6xl flex-col items-start justify-between gap-4 px-4 py-8 sm:flex-row sm:items-center sm:px-6">
        <div class="flex items-center gap-2.5">
          <span class="grid size-5 place-items-center rounded-sm bg-primary text-primary-foreground font-mono text-xs font-bold leading-none">
            0
          </span>
          <span class="font-mono text-xs text-muted-foreground">
            dummie — secure sandbox runtime
          </span>
        </div>
        <p class="eyebrow text-muted-foreground">
          &copy; {{ year }} dummie labs
        </p>
      </div>
    </footer>
  </div>
</template>
