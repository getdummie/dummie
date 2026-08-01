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
    <p class="eyebrow mb-4 text-primary-text">// Authenticate</p>
    <Card>
      <CardHeader>
        <!-- This is the page's only title, so it has to be the h1 — otherwise
             the document starts at h3 and heading navigation has no entry point. -->
        <CardTitle as="h1" class="text-2xl tracking-tight">Sign in</CardTitle>
        <CardDescription>Welcome back. Use your username or email.</CardDescription>
      </CardHeader>
      <CardContent>
        <form class="space-y-4" :aria-busy="loading" @submit.prevent="onSubmit">
          <div class="space-y-2">
            <Label for="identifier">Username or email</Label>
            <Input
              id="identifier"
              v-model="identifier"
              autocomplete="username"
              placeholder="neo"
              required
              :aria-invalid="error ? true : undefined"
              :aria-describedby="error ? 'signin-error' : undefined"
            />
          </div>
          <div class="space-y-2">
            <Label for="password">Password</Label>
            <Input
              id="password"
              v-model="password"
              type="password"
              autocomplete="current-password"
              required
              :aria-invalid="error ? true : undefined"
              :aria-describedby="error ? 'signin-error' : undefined"
            />
          </div>

          <FormError id="signin-error" :message="error" />

          <Button type="submit" class="w-full font-mono text-sm" :disabled="loading">
            {{ loading ? 'Signing in…' : 'Sign in' }}
            <ArrowRight v-if="!loading" class="size-4" aria-hidden="true" />
          </Button>
        </form>

        <p class="mt-6 text-center text-sm text-muted-foreground">
          No account?
          <NuxtLink to="/signup" class="text-primary-text underline underline-offset-4">Create one</NuxtLink>
        </p>
      </CardContent>
    </Card>
  </div>
</template>
