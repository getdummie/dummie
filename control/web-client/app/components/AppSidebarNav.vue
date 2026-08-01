<script setup lang="ts">
import type { Component } from 'vue'
import { ChevronDown, KeyRound, LayoutDashboard, LogOut, Server, Shield, Ticket, Users } from '@lucide/vue'
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
const emit = defineEmits<{ expand: [] }>()

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
]

const adminItems: NavItem[] = [
  { label: 'Users', to: '/admin/model/users', icon: Users },
  { label: 'Tokens', to: '/admin/model/tokens', icon: Ticket },
  { label: 'Agents', to: '/admin/model/agents', icon: Server },
  { label: 'Keys', to: '/admin/model/agent-keys', icon: KeyRound },
]

function isActive(to: string) {
  return route.path === to || route.path.startsWith(`${to}/`)
}

const adminActive = computed(() => adminItems.some(i => isActive(i.to)))
const adminOpen = ref(adminActive.value)

watch(adminActive, (v) => {
  if (v) adminOpen.value = true
})

/** Collapsed: the group is a single button that expands the rail. */
function onAdminClick() {
  if (!props.expanded) {
    emit('expand')
    adminOpen.value = true
    return
  }
  adminOpen.value = !adminOpen.value
}

// `focus-visible:` is not optional here: without it the entire rail is
// invisible to keyboard users, since none of these are shadcn primitives that
// bring their own ring. (WCAG 2.4.7)
const itemBase
  = 'group relative flex items-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring'
const itemSize = computed(() => (props.expanded ? 'h-9 w-full gap-3 px-2.5' : 'size-9 justify-center'))
</script>

<template>
  <TooltipProvider :delay-duration="150">
    <div class="flex min-h-0 flex-1 flex-col" :class="expanded ? 'px-2' : 'items-center px-1.5'">
      <!-- Named so the landmark list distinguishes this from the admin tab
           strip and the header nav. (WCAG 1.3.1) -->
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

      <!-- Admin group, pinned to the bottom -->
      <div v-if="isAdmin" class="flex flex-col gap-0.5 pb-2">
        <Tooltip :disabled="expanded">
          <TooltipTrigger as-child>
            <button
              type="button"
              :class="[itemBase, itemSize, adminActive && !adminOpen && 'text-primary-text']"
              :aria-expanded="expanded ? adminOpen : undefined"
              :aria-controls="expanded ? 'admin-subnav' : undefined"
              @click="onAdminClick"
            >
              <Shield class="size-[18px] shrink-0" aria-hidden="true" />
              <template v-if="expanded">
                <span class="flex-1 truncate text-left font-mono text-[0.8rem]">Admin Dashboard</span>
                <ChevronDown class="size-4 shrink-0 transition-transform" :class="adminOpen && 'rotate-180'" aria-hidden="true" />
              </template>
              <span v-else class="sr-only">Admin Dashboard</span>
            </button>
          </TooltipTrigger>
          <TooltipContent side="right" class="font-mono text-xs">
            Admin Dashboard
          </TooltipContent>
        </Tooltip>

        <div v-if="expanded && adminOpen" id="admin-subnav" class="ml-[1.05rem] flex flex-col gap-0.5 border-l border-border pl-2">
          <NuxtLink
            v-for="item in adminItems"
            :key="item.to"
            :to="item.to"
            :class="[itemBase, 'h-8 w-full gap-2.5 px-2.5', isActive(item.to) && 'bg-primary/12 text-primary-text hover:bg-primary/12 hover:text-primary-text']"
            :aria-current="isActive(item.to) ? 'page' : undefined"
          >
            <component :is="item.icon" class="size-4 shrink-0" aria-hidden="true" />
            <span class="truncate font-mono text-[0.8rem]">{{ item.label }}</span>
          </NuxtLink>
        </div>
      </div>

      <!-- Account + theme -->
      <div
        class="flex border-t border-border/80 py-2"
        :class="expanded ? '-mx-2 items-center gap-1 px-2' : '-mx-1.5 flex-col items-center gap-1 px-1.5'"
      >
        <DropdownMenu>
          <Tooltip :disabled="expanded">
            <TooltipTrigger as-child>
              <DropdownMenuTrigger
                :class="[itemBase, expanded ? 'h-9 min-w-0 flex-1 gap-2.5 px-1.5' : 'size-9 justify-center']"
                :aria-label="user ? `Account menu for ${user.username}` : 'Account menu'"
              >
                <Avatar class="size-6 shrink-0" aria-hidden="true">
                  <AvatarFallback class="bg-primary/15 text-[0.6rem] text-primary-text">{{ initials }}</AvatarFallback>
                </Avatar>
                <span v-if="expanded" class="truncate font-mono text-[0.8rem] text-foreground">{{ user?.username }}</span>
              </DropdownMenuTrigger>
            </TooltipTrigger>
            <TooltipContent side="right" class="font-mono text-xs">
              {{ user?.username }}
            </TooltipContent>
          </Tooltip>

          <DropdownMenuContent side="right" align="end" class="w-52 font-mono">
            <DropdownMenuLabel class="truncate text-xs font-normal text-muted-foreground">
              {{ user?.email }}
            </DropdownMenuLabel>
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
