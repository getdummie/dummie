<script setup lang="ts">
import { ArrowRight } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

definePageMeta({ middleware: 'guest' })
useHead({ title: 'dummie — sign in' })

const { signin } = useAuth()

const identifier = ref('')
const password = ref('')
const loading = ref(false)
const error = ref<string | null>(null)

async function onSubmit() {
  error.value = null
  loading.value = true
  try {
    await signin(identifier.value, password.value)
    await navigateTo('/dashboard')
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Sign in failed'
  }
  finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="mx-auto flex min-h-[70vh] w-full max-w-md flex-col justify-center px-4 py-16 sm:px-6">
    <p class="eyebrow mb-4 text-primary">// Authenticate</p>
    <Card>
      <CardHeader>
        <CardTitle class="text-2xl tracking-tight">Sign in</CardTitle>
        <CardDescription>Welcome back. Use your username or email.</CardDescription>
      </CardHeader>
      <CardContent>
        <form class="space-y-4" @submit.prevent="onSubmit">
          <div class="space-y-2">
            <Label for="identifier">Username or email</Label>
            <Input id="identifier" v-model="identifier" autocomplete="username" placeholder="neo" required />
          </div>
          <div class="space-y-2">
            <Label for="password">Password</Label>
            <Input id="password" v-model="password" type="password" autocomplete="current-password" required />
          </div>

          <p v-if="error" class="text-sm text-destructive">{{ error }}</p>

          <Button type="submit" class="w-full font-mono text-sm" :disabled="loading">
            {{ loading ? 'Signing in…' : 'Sign in' }}
            <ArrowRight v-if="!loading" class="size-4" />
          </Button>
        </form>

        <p class="mt-6 text-center text-sm text-muted-foreground">
          No account?
          <NuxtLink to="/signup" class="text-primary underline-offset-4 hover:underline">Create one</NuxtLink>
        </p>
      </CardContent>
    </Card>
  </div>
</template>
