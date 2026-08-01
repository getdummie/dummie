<script setup lang="ts">
const links = [
  { label: 'Users', to: '/admin/model/users' },
  { label: 'Tokens', to: '/admin/model/tokens' },
  { label: 'Agents', to: '/admin/model/agents' },
  { label: 'Keys', to: '/admin/model/agent-keys' },
]
</script>

<template>
  <nav class="flex items-center gap-1 overflow-x-auto border-b border-border pb-px">
    <NuxtLink
      v-for="l in links"
      :key="l.to"
      :to="l.to"
      class="eyebrow -mb-px shrink-0 border-b-2 border-transparent px-3 py-2.5 whitespace-nowrap text-muted-foreground transition-colors hover:text-foreground"
      active-class="!border-primary !text-foreground"
    >
      {{ l.label }}
    </NuxtLink>
  </nav>
</template>
