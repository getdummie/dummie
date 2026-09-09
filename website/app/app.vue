<script setup lang="ts">
const { siteUrl } = useRuntimeConfig().public
const route = useRoute()

const ogImage = `${siteUrl}/og.png`
const canonical = computed(() => `${siteUrl}${route.path === '/' ? '' : route.path.replace(/\/$/, '')}`)

useSeoMeta({
  ogSiteName: 'dummie',
  ogType: 'website',
  ogLocale: 'en_US',
  ogUrl: () => canonical.value,
  ogImage,
  ogImageWidth: 1200,
  ogImageHeight: 630,
  ogImageType: 'image/png',
  ogImageAlt: 'dummie — secure computers for everyone',
  twitterCard: 'summary_large_image',
  twitterImage: ogImage,
  twitterImageAlt: 'dummie — secure computers for everyone',
})

useHead({
  link: [{ rel: 'canonical', href: () => canonical.value }],
})
</script>

<template>
  <div>
    <NuxtRouteAnnouncer />
    <NuxtLayout>
      <NuxtPage />
    </NuxtLayout>
  </div>
</template>
