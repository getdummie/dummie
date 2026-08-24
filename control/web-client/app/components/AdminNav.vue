<script setup lang="ts">
import type { Component } from 'vue'
import {
  Box,
  Clock,
  Cpu,
  Globe,
  HardDrive,
  KeyRound,
  Server,
  Settings,
  Ticket,
  Users,
} from '@lucide/vue'

interface AdminLink {
  label: string
  to: string
  icon: Component
}

// Grouped rather than one flat list: these fall into settings, accounts, the
// fleet's plumbing, the VMs themselves, and the artifacts they boot from. The
// groups are what the separators between them draw.
const groups: AdminLink[][] = [
  [
    { label: 'Settings', to: '/admin/model/settings', icon: Settings },
  ],
  [
    { label: 'Users', to: '/admin/model/users', icon: Users },
    { label: 'Tokens', to: '/admin/model/tokens', icon: Ticket },
  ],
  [
    { label: 'Keys', to: '/admin/model/client-keys', icon: KeyRound },
    { label: 'Clients', to: '/admin/model/clients', icon: Server },
    { label: 'Domains', to: '/admin/model/domains', icon: Globe },
    { label: 'Scheduled tasks', to: '/admin/model/scheduled-tasks', icon: Clock },
  ],
  [
    { label: 'VMs', to: '/admin/model/vms', icon: Box },
  ],
  [
    { label: 'Kernels', to: '/admin/model/kernels', icon: Cpu },
    { label: 'OS images', to: '/admin/model/osimages', icon: HardDrive },
  ],
]

const route = useRoute()

function isActive(to: string) {
  return route.path === to || route.path.startsWith(`${to}/`)
}
</script>

<template>
  <!-- Named landmark: the app's own rail is on screen at the same time, so an
       unnamed second nav is ambiguous in the landmark list. -->
  <nav
    aria-label="Admin sections"
    class="flex gap-1 overflow-x-auto border-b border-border pb-px lg:sticky lg:top-6 lg:flex-col lg:gap-0.5 lg:overflow-visible lg:rounded-lg lg:border lg:p-2 lg:pb-2"
  >
    <template v-for="(group, gi) in groups" :key="gi">
      <!-- A rule between groups: vertical while the nav is a scrolling row on
           small screens, horizontal once it is a column. -->
      <span
        v-if="gi > 0"
        aria-hidden="true"
        class="mx-1 w-px shrink-0 self-stretch bg-border lg:mx-0 lg:my-1.5 lg:h-px lg:w-auto lg:self-auto"
      />
      <NuxtLink
        v-for="l in group"
        :key="l.to"
        :to="l.to"
        class="flex shrink-0 items-center gap-2.5 rounded-md px-3 py-2 text-sm whitespace-nowrap text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
        :class="isActive(l.to) && 'bg-muted font-medium text-foreground'"
        :aria-current="isActive(l.to) ? 'page' : undefined"
      >
        <component :is="l.icon" class="size-4 shrink-0" aria-hidden="true" />
        {{ l.label }}
      </NuxtLink>
    </template>
  </nav>
</template>
