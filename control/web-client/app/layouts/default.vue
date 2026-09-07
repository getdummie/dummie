<script setup lang="ts">
import { LayoutDashboard, LogOut, Menu, Settings, Shield } from '@lucide/vue'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

const { user, isAuthenticated, signout } = useAuth()
const route = useRoute()

// Routes that get the app shell rather than the marketing header. A page linked
// from AppSidebarNav has to be here too, or it loses the sidebar it links from.
const appRoutes = ['/dashboard', '/vms', '/integrations', '/settings', '/admin']
const showSidebar = computed(() =>
  isAuthenticated.value && appRoutes.some(p => route.path === p || route.path.startsWith(`${p}/`)),
)

const navOpen = ref(false)
const { expanded } = useSidebar()

const initials = computed(() => {
  const u = user.value
  if (!u) return '0'
  const first = (u.first_name?.[0] || u.username?.[0] || '').toUpperCase()
  const last = (u.last_name?.[0] || '').toUpperCase()
  return (first + last) || '0'
})
</script>

<template>
  <div
    class="min-h-svh flex flex-col bg-background text-foreground antialiased transition-[padding] duration-200"
    :class="showSidebar && (expanded ? 'md:pl-56' : 'md:pl-12')"
  >
    <a href="#main-content" class="skip-link">Skip to main content</a>

    <AppSidebar v-if="showSidebar" v-model:open="navOpen" />

    <header
      v-if="showSidebar"
      class="sticky top-0 z-40 flex h-14 items-center gap-1 border-b border-border/80 bg-background/80 px-4 backdrop-blur md:hidden"
    >
      <Button variant="ghost" size="icon" class="-ml-2" aria-label="Open navigation" @click="navOpen = true">
        <Menu class="size-4" />
      </Button>
      <NuxtLink to="/dashboard" class="flex items-center gap-2.5">
        <img src="/logo.svg" alt="" aria-hidden="true" class="size-6">
        <span class="font-mono text-sm font-semibold tracking-tight">
          dummie<span class="text-primary-text">/</span>
        </span>
      </NuxtLink>
    </header>

    <header v-else class="sticky top-0 z-40 border-b border-border/80 bg-background/80 backdrop-blur">
      <div class="mx-auto flex h-14 max-w-6xl items-center px-4 sm:px-6">
        <NuxtLink to="/" class="group flex items-center gap-2.5">
          <img src="/logo.svg" alt="" aria-hidden="true" class="size-6">
          <span class="font-mono text-sm font-semibold tracking-tight">
            dummie<span class="text-primary-text">/</span>
          </span>
        </NuxtLink>

        <nav aria-label="Site" class="ml-auto flex items-center gap-4 font-mono text-xs">
          <NuxtLink to="/docs" class="text-muted-foreground transition-colors hover:text-primary-text">
            API docs
          </NuxtLink>
        </nav>

        <nav aria-label="Account and preferences" class="ml-4 flex items-center gap-1.5">
          <ClientOnly>
            <DropdownMenu v-if="isAuthenticated">
              <DropdownMenuTrigger as-child>
                <Button
                  variant="ghost"
                  size="sm"
                  class="gap-2 font-mono text-xs"
                  :aria-label="user ? `Account menu for ${user.username}` : 'Account menu'"
                >
                  <Avatar class="size-6">
                    <AvatarFallback aria-hidden="true" class="bg-primary/15 text-[0.6rem] text-primary-text">{{ initials }}</AvatarFallback>
                  </Avatar>
                  <span class="hidden sm:inline">{{ user?.username }}</span>
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" class="w-52 font-mono">
                <DropdownMenuLabel class="truncate text-xs font-normal text-muted-foreground">
                  {{ user?.email }}
                </DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem v-if="user?.user_type === 'admin'" as-child class="text-xs">
                  <NuxtLink to="/admin/model/users">
                    <Shield class="size-4" />
                    Admin
                  </NuxtLink>
                </DropdownMenuItem>
                <DropdownMenuItem as-child class="text-xs">
                  <NuxtLink to="/dashboard">
                    <LayoutDashboard class="size-4" />
                    Dashboard
                  </NuxtLink>
                </DropdownMenuItem>
                <DropdownMenuItem as-child class="text-xs">
                  <NuxtLink to="/settings">
                    <Settings class="size-4" />
                    Settings
                  </NuxtLink>
                </DropdownMenuItem>
                <DropdownMenuItem class="text-xs text-destructive focus:text-destructive" @select="signout">
                  <LogOut class="size-4" />
                  Sign out
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>

            <Button v-else as-child variant="default" size="sm" class="font-mono text-xs">
              <NuxtLink to="/signin">Sign in</NuxtLink>
            </Button>

            <template #fallback>
              <div class="size-8" />
            </template>
          </ClientOnly>
          <ThemeToggle />
        </nav>
      </div>
    </header>

    <main id="main-content" tabindex="-1" class="flex-1 focus-visible:outline-none">
      <slot />
    </main>
  </div>
</template>
