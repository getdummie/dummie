<script setup lang="ts">
import { ArrowLeft } from '@lucide/vue'

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · llm usage' })

const route = useRoute()
const id = computed(() => String(route.params.id))
const { authFetch } = useAuth()
const username = ref('')
onMounted(async () => {
  const res = await authFetch(`/admin/users/${id.value}`).catch(() => null)
  if (res?.ok) username.value = (await res.json()).username ?? ''
})
const month = computed(() => typeof route.query.month === 'string' ? route.query.month : undefined)
</script>

<template>
  <AdminShell>
    <NuxtLink to="/admin/model/llm-usage" class="mt-8 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
      <ArrowLeft class="size-4" aria-hidden="true" />
      All users
    </NuxtLink>
    <p class="eyebrow mb-2 mt-4 text-primary-text">// admin · llm usage</p>
    <h1 class="font-mono text-xl font-semibold tracking-tight sm:text-2xl">{{ username || id }}</h1>
    <LLMUsageReport :endpoint="`/admin/llm-usage/users/${id}`" :month="month" class="mt-6" />
  </AdminShell>
</template>
