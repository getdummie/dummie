<script setup lang="ts">
const route = useRoute()

const links = [
  { label: 'Settings', to: '/admin/model/settings' },
  { label: 'Users', to: '/admin/model/users' },
  { label: 'Tokens', to: '/admin/model/tokens' },
  { label: 'Agents', to: '/admin/model/agents' },
  { label: 'VMs', to: '/admin/model/vms' },
  { label: 'Keys', to: '/admin/model/agent-keys' },
]

function isActive(to: string) {
  return route.path === to || route.path.startsWith(`${to}/`)
}
</script>

<template>
  <!-- Named landmark: the sidebar's "Main" nav is on screen at the same time,
       so an unnamed second nav is ambiguous in the landmark list. -->
  <nav aria-label="Admin sections" class="flex items-center gap-1 overflow-x-auto border-b border-border pb-px">
    <NuxtLink
      v-for="l in links"
      :key="l.to"
      :to="l.to"
      class="eyebrow -mb-px shrink-0 border-b-2 border-transparent px-3 py-2.5 whitespace-nowrap text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
      :class="isActive(l.to) && 'border-primary-text font-semibold text-foreground'"
      :aria-current="isActive(l.to) ? 'page' : undefined"
    >
      {{ l.label }}
    </NuxtLink>
  </nav>
</template>
