<script setup lang="ts">
import { Button } from '@/components/ui/button'

const { consoleUrl } = useRuntimeConfig().public
</script>

<template>
  <div class="min-h-svh flex flex-col bg-background text-foreground antialiased">
    <a href="#main-content" class="skip-link">Skip to main content</a>

    <header class="sticky top-0 z-40 border-b border-border/80 bg-background/80 backdrop-blur">
      <div class="mx-auto flex h-14 max-w-6xl items-center px-4 sm:px-6">
        <NuxtLink to="/" class="group flex items-center gap-2.5">
          <img src="/logo.svg" alt="" aria-hidden="true" class="size-6">
          <span class="font-mono text-sm font-semibold tracking-tight">
            <span class="text-primary-text">dumm</span>ie<span class="text-primary-text">/</span>
          </span>
        </NuxtLink>

        <nav aria-label="Account and preferences" class="ml-auto flex items-center gap-1.5">
          <Button as-child variant="default" size="sm" class="font-mono text-xs">
            <a :href="`${consoleUrl}/signin`">Sign in</a>
          </Button>
          <Button as-child variant="ghost" size="sm" class="font-mono text-xs text-muted-foreground hover:text-foreground">
            <a
              href="https://github.com/getdummie/dummie"
              target="_blank"
              rel="noopener noreferrer"
              aria-label="Star dummie on GitHub (opens in a new tab)"
            >
              <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" class="size-4">
                <path d="M12 .3a12 12 0 0 0-3.8 23.4c.6.1.8-.3.8-.6v-2c-3.3.7-4-1.6-4-1.6-.6-1.4-1.4-1.8-1.4-1.8-1-.7.1-.7.1-.7 1.2.1 1.8 1.2 1.8 1.2 1 1.8 2.8 1.3 3.5 1 0-.8.4-1.3.7-1.6-2.7-.3-5.5-1.3-5.5-6 0-1.2.5-2.3 1.3-3.1-.2-.4-.6-1.6 0-3.2 0 0 1-.3 3.4 1.2a11.5 11.5 0 0 1 6 0c2.3-1.5 3.3-1.2 3.3-1.2.6 1.6.2 2.8 0 3.2.9.8 1.3 1.9 1.3 3.2 0 4.6-2.8 5.6-5.5 5.9.5.4.9 1 .9 2.2v3.3c0 .3.1.7.8.6A12 12 0 0 0 12 .3" />
              </svg>
              <span class="hidden sm:inline">Star</span>
            </a>
          </Button>
          <ThemeToggle />
        </nav>
      </div>
    </header>

    <main id="main-content" tabindex="-1" class="flex-1 focus-visible:outline-none">
      <slot />
    </main>

    <footer class="panel-end border-t border-border/80">
      <div class="mx-auto flex max-w-6xl flex-col items-start justify-between gap-4 px-4 py-8 sm:flex-row sm:items-center sm:px-6">
        <div class="flex items-center gap-2.5">
          <img src="/logo.svg" alt="" aria-hidden="true" class="size-5">
          <span class="font-mono text-xs text-muted-foreground">
            dummie — secure isolated computers
          </span>
        </div>
        <p class="eyebrow text-muted-foreground">
          Crafted by
          <a
            href="https://codingcoffee.dev"
            target="_blank"
            rel="noopener noreferrer"
            class="text-foreground underline-offset-4 transition-colors hover:text-primary-text hover:underline"
          >
            Ameya Shenoy<span class="sr-only"> (opens in a new tab)</span>
          </a>
        </p>
      </div>
    </footer>
  </div>
</template>
