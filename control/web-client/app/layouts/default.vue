<script setup lang="ts">
import { LayoutDashboard, LogOut, Menu, Shield } from '@lucide/vue'
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

const year = 2026

const { user, isAuthenticated, signout } = useAuth()
const route = useRoute()

// The rail is for the signed-in app shell only; the marketing/auth pages keep
// their full-bleed layout and the public header.
const appRoutes = ['/dashboard', '/admin']
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
    <AppSidebar v-if="showSidebar" v-model:open="navOpen" />

    <!-- App shell: no top bar on desktop; mobile keeps a bar to reach the drawer -->
    <header
      v-if="showSidebar"
      class="sticky top-0 z-40 flex h-14 items-center gap-1 border-b border-border/80 bg-background/80 px-4 backdrop-blur md:hidden"
    >
      <Button variant="ghost" size="icon" class="-ml-2" aria-label="Open navigation" @click="navOpen = true">
        <Menu class="size-4" />
      </Button>
      <NuxtLink to="/dashboard" class="flex items-center gap-2.5">
        <span class="grid size-6 place-items-center rounded-sm bg-primary text-primary-foreground font-mono text-sm font-bold leading-none">
          0
        </span>
        <span class="font-mono text-sm font-semibold tracking-tight">
          dummie<span class="text-primary">/</span>
        </span>
      </NuxtLink>
    </header>

    <!-- Public header -->
    <header v-else class="sticky top-0 z-40 border-b border-border/80 bg-background/80 backdrop-blur">
      <div class="mx-auto flex h-14 max-w-6xl items-center px-4 sm:px-6">
        <NuxtLink to="/" class="group flex items-center gap-2.5">
          <span class="grid size-6 place-items-center rounded-sm bg-primary text-primary-foreground font-mono text-sm font-bold leading-none">
            0
          </span>
          <span class="font-mono text-sm font-semibold tracking-tight">
            dummie<span class="text-primary">/</span>
          </span>
        </NuxtLink>

        <div class="ml-auto flex items-center gap-1.5">
          <ClientOnly>
            <DropdownMenu v-if="isAuthenticated">
              <DropdownMenuTrigger as-child>
                <Button variant="ghost" size="sm" class="gap-2 font-mono text-xs">
                  <Avatar class="size-6">
                    <AvatarFallback class="bg-primary/15 text-[0.6rem] text-primary">{{ initials }}</AvatarFallback>
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
