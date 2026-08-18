<script setup lang="ts">
import { ApiReference } from '@scalar/api-reference'
import '@scalar/api-reference/style.css'

// No layout: Scalar ships its own full-height shell with a sidebar of its own,
// and nesting it inside this app's would give the page two.
definePageMeta({ layout: false })
useHead({ title: 'dummie — API docs' })

const { apiBase } = useRuntimeConfig().public

// Follows the app's own light/dark toggle rather than picking one: docs opened
// from a dark UI that render white are the reason people stop reading them.
const colorMode = useColorMode()

const configuration = computed(() => ({
  url: `${apiBase}/api/v1/openapi.json`,
  darkMode: colorMode.value === 'dark',
  // The docs describe two credentials but only one is worth prefilling: a
  // personal access token is what a reader of this page is holding.
  authentication: { preferredSecurityScheme: 'BearerAuth' },
  hideDarkModeToggle: true,
}))
</script>

<template>
  <ApiReference :configuration="configuration" />
</template>
