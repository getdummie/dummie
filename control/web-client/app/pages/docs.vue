<script setup lang="ts">
import { ApiReference } from '@scalar/api-reference'
import '@scalar/api-reference/style.css'

definePageMeta({ layout: false })
useHead({ title: 'dummie — API docs' })

const { apiBase } = useRuntimeConfig().public

const colorMode = useColorMode()

const configuration = computed(() => ({
  url: `${apiBase}/api/v1/openapi.json`,
  darkMode: colorMode.value === 'dark',
  authentication: { preferredSecurityScheme: 'BearerAuth' },
  hideDarkModeToggle: true,
}))
</script>

<template>
  <ApiReference :configuration="configuration" />
</template>
