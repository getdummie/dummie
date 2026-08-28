<script setup lang="ts">
definePageMeta({
  middleware: [
    'guest',
    to => navigateTo({ path: '/signin', query: to.query }, { replace: true }),
  ],
})
</script>

<template>
  <div />
</template>
