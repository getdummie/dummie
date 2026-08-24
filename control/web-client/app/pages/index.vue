<script setup lang="ts">
// The console owns no landing page -- that lives on the marketing site, on its
// own domain. `/` exists only to send a visitor onward.
definePageMeta({
  middleware: [
    // Sends a signed-in visitor to the dashboard (or to their ?redirect, which
    // is how the proxy hand-off comes back through here).
    'guest',
    // Only reached when `guest` let it through, i.e. nobody is signed in. The
    // query rides along so an unauthenticated hand-off keeps its destination.
    to => navigateTo({ path: '/signin', query: to.query }, { replace: true }),
  ],
})
</script>

<template>
  <div />
</template>
