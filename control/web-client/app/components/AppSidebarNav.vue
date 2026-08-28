<script setup lang="ts">
import type { Component } from 'vue'
import { BookOpen, Boxes, LayoutDashboard, LogOut, Settings, Shield } from '@lucide/vue'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'

interface NavItem {
  label: string
  to: string
  icon: Component
}

const props = defineProps<{ expanded: boolean }>()

const { user, signout } = useAuth()
const route = useRoute()

const isAdmin = computed(() => user.value?.user_type === 'admin')

const initials = computed(() => {
  const u = user.value
  if (!u) return '0'
  const first = (u.first_name?.[0] || u.username?.[0] || '').toUpperCase()
  const last = (u.last_name?.[0] || '').toUpperCase()
  return (first + last) || '0'
})

const main: NavItem[] = [
  { label: 'Dashboard', to: '/dashboard', icon: LayoutDashboard },
  { label: 'VMs', to: '/vms', icon: Boxes },
]

const adminEntry: NavItem = { label: 'Admin Dashboard', to: '/admin/model/settings', icon: Shield }

function isActive(to: string) {
  return route.path === to || route.path.startsWith(`${to}/`)
}

const adminActive = computed(() => isActive('/admin'))

const itemBase
  = 'group relative flex items-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring'
const itemSize = computed(() => (props.expanded ? 'h-9 w-full gap-3 px-2.5' : 'size-9 justify-center'))
</script>

<template>
  <TooltipProvider :delay-duration="150">
    <div class="flex min-h-0 flex-1 flex-col" :class="expanded ? 'px-2' : 'items-center px-1.5'">
      <nav aria-label="Main" class="flex flex-1 flex-col gap-0.5 overflow-y-auto py-3">
        <Tooltip v-for="item in main" :key="item.to" :disabled="expanded">
          <TooltipTrigger as-child>
            <NuxtLink
              :to="item.to"
              :class="[itemBase, itemSize, isActive(item.to) && 'bg-primary/12 text-primary-text hover:bg-primary/12 hover:text-primary-text']"
              :aria-current="isActive(item.to) ? 'page' : undefined"
            >
              <component :is="item.icon" class="size-[18px] shrink-0" aria-hidden="true" />
              <span v-if="expanded" class="truncate font-mono text-[0.8rem]">{{ item.label }}</span>
              <span v-else class="sr-only">{{ item.label }}</span>
            </NuxtLink>
          </TooltipTrigger>
          <TooltipContent side="right" class="font-mono text-xs">
            {{ item.label }}
          </TooltipContent>
        </Tooltip>
      </nav>

      <div class="flex flex-col gap-0.5 pb-2">
        <Tooltip v-if="isAdmin" :disabled="expanded">
          <TooltipTrigger as-child>
            <NuxtLink
              :to="adminEntry.to"
              :class="[itemBase, itemSize, adminActive && 'bg-primary/12 text-primary-text hover:bg-primary/12 hover:text-primary-text']"
              :aria-current="adminActive ? 'page' : undefined"
            >
              <component :is="adminEntry.icon" class="size-[18px] shrink-0" aria-hidden="true" />
              <span v-if="expanded" class="truncate font-mono text-[0.8rem]">{{ adminEntry.label }}</span>
              <span v-else class="sr-only">{{ adminEntry.label }}</span>
            </NuxtLink>
          </TooltipTrigger>
          <TooltipContent side="right" class="font-mono text-xs">
            {{ adminEntry.label }}
          </TooltipContent>
        </Tooltip>

        <Tooltip :disabled="expanded">
          <TooltipTrigger as-child>
            <a href="/docs" target="_blank" rel="noopener" :class="[itemBase, itemSize]">
              <BookOpen class="size-[18px] shrink-0" aria-hidden="true" />
              <span v-if="expanded" class="truncate font-mono text-[0.8rem]">API docs</span>
              <span class="sr-only">{{ expanded ? '(opens in a new tab)' : 'API docs (opens in a new tab)' }}</span>
            </a>
          </TooltipTrigger>
          <TooltipContent side="right" class="font-mono text-xs">
            API docs
          </TooltipContent>
        </Tooltip>
      </div>

      <div
        class="flex border-t border-border/80 py-2"
        :class="expanded ? '-mx-2 items-center gap-1 px-2' : '-mx-1.5 flex-col items-center gap-1 px-1.5'"
      >
        <DropdownMenu>
          <DropdownMenuTrigger as-child>
            <button
              type="button"
              :class="[itemBase, expanded ? 'h-9 min-w-0 flex-1 gap-2.5 px-1.5' : 'size-9 justify-center']"
              :aria-label="user ? `Account menu for ${user.username}` : 'Account menu'"
            >
              <Avatar class="size-6 shrink-0" aria-hidden="true">
                <AvatarFallback class="bg-primary/15 text-[0.6rem] text-primary-text">{{ initials }}</AvatarFallback>
              </Avatar>
              <span v-if="expanded" class="truncate font-mono text-[0.8rem] text-foreground">{{ user?.username }}</span>
            </button>
          </DropdownMenuTrigger>

          <DropdownMenuContent side="right" align="end" :side-offset="8" class="w-52 font-mono">
            <DropdownMenuLabel class="grid gap-0.5 font-normal">
              <span class="truncate text-xs text-foreground">{{ user?.username }}</span>
              <span class="truncate text-xs text-muted-foreground">{{ user?.email }}</span>
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuItem as-child class="text-xs">
              <NuxtLink to="/settings">
                <Settings class="size-4" aria-hidden="true" />
                Settings
              </NuxtLink>
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem class="text-xs text-destructive focus:text-destructive" @select="signout">
              <LogOut class="size-4" aria-hidden="true" />
              Sign out
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>

        <ThemeToggle />
      </div>
    </div>
  </TooltipProvider>
</template>
