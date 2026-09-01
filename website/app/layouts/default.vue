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
            dummie<span class="text-primary-text">/</span>
          </span>
        </NuxtLink>

        <nav aria-label="Site" class="ml-auto flex items-center gap-4 font-mono text-xs">
          <NuxtLink to="/docs" class="text-muted-foreground transition-colors hover:text-primary-text">
            Docs
          </NuxtLink>
          <NuxtLink to="/casestudies" class="text-muted-foreground transition-colors hover:text-primary-text">
            Case studies
          </NuxtLink>
        </nav>

        <nav aria-label="Account and preferences" class="ml-4 flex items-center gap-1.5">
          <Button as-child variant="default" size="sm" class="font-mono text-xs">
            <a :href="`${consoleUrl}/signin`">Sign in</a>
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
