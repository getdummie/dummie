<script setup lang="ts">
import { splitHostname } from '@/lib/targets'

const props = defineProps<{ name: string }>()
const parts = computed(() => splitHostname(props.name))
</script>

<template>
  <span><span v-if="parts.prefix">{{ parts.prefix }}</span><span class="font-semibold">{{ parts.root }}</span></span>
</template>
