<script setup lang="ts">
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

definePageMeta({ middleware: 'auth' })
useHead({ title: 'dummie — dashboard' })

const { user } = useAuth()

const greeting = computed(() =>
  user.value?.first_name?.trim() || user.value?.username || 'there',
)
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-16 sm:px-6 sm:py-24">
    <p class="eyebrow mb-4 text-primary-text">// Dashboard</p>
    <h1 class="text-3xl font-semibold tracking-tight sm:text-4xl">
      Welcome, {{ greeting }}.
    </h1>
    <p class="mt-3 max-w-xl text-muted-foreground">
      You're signed in to the dummie control plane. Your sandboxes will live here.
    </p>

    <Card class="mt-10 max-w-md">
      <CardHeader>
        <CardTitle as="h2" class="font-mono text-base">session</CardTitle>
        <CardDescription>Signed-in account</CardDescription>
      </CardHeader>
      <CardContent>
        <dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 font-mono text-sm">
          <dt class="text-muted-foreground">user</dt>
          <dd>{{ user?.username }}</dd>
          <dt class="text-muted-foreground">email</dt>
          <dd>{{ user?.email }}</dd>
          <dt class="text-muted-foreground">role</dt>
          <dd>{{ user?.user_type }}</dd>
        </dl>
      </CardContent>
    </Card>
  </div>
</template>
