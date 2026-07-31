<script setup lang="ts">
import { ArrowRight } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

definePageMeta({ middleware: 'guest' })
useHead({ title: 'dummie — create account' })

const { signup } = useAuth()

const username = ref('')
const email = ref('')
const firstName = ref('')
const lastName = ref('')
const password = ref('')
const confirm = ref('')
const loading = ref(false)
const error = ref<string | null>(null)

// Mirror the server's rules for UX. The backend is authoritative and applies
// strong rules only when APP_ENV=prod; here we key off the client build mode.
function validate(): string | null {
  if (!username.value.trim()) return 'Username is required'
  if (!email.value.trim()) return 'Email is required'
  if (password.value !== confirm.value) return 'Passwords do not match'
  if (import.meta.dev) {
    if (!password.value) return 'Password is required'
    return null
  }
  const p = password.value
  if (p.length < 8) return 'Password must be at least 8 characters'
  if (!/[A-Z]/.test(p) || !/[a-z]/.test(p) || !/[0-9]/.test(p) || !/[^A-Za-z0-9]/.test(p)) {
    return 'Password must include uppercase, lowercase, number, and a special character'
  }
  return null
}

async function onSubmit() {
  error.value = validate()
  if (error.value) return
  loading.value = true
  try {
    await signup({
      username: username.value.trim(),
      email: email.value.trim(),
      first_name: firstName.value.trim(),
      last_name: lastName.value.trim(),
      password: password.value,
    })
    await navigateTo('/dashboard')
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not create account'
  }
  finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="mx-auto flex min-h-[70vh] w-full max-w-md flex-col justify-center px-4 py-16 sm:px-6">
    <p class="eyebrow mb-4 text-primary">// Get started</p>
    <Card>
      <CardHeader>
        <CardTitle class="text-2xl tracking-tight">Create your account</CardTitle>
        <CardDescription>Spin up your first sandbox in minutes.</CardDescription>
      </CardHeader>
      <CardContent>
        <form class="space-y-4" @submit.prevent="onSubmit">
          <div class="grid grid-cols-2 gap-3">
            <div class="space-y-2">
              <Label for="firstName">First name</Label>
              <Input id="firstName" v-model="firstName" autocomplete="given-name" />
            </div>
            <div class="space-y-2">
              <Label for="lastName">Last name</Label>
              <Input id="lastName" v-model="lastName" autocomplete="family-name" />
            </div>
          </div>
          <div class="space-y-2">
            <Label for="username">Username</Label>
            <Input id="username" v-model="username" autocomplete="username" placeholder="neo" required />
          </div>
          <div class="space-y-2">
            <Label for="email">Email</Label>
            <Input id="email" v-model="email" type="email" autocomplete="email" placeholder="neo@example.com" required />
          </div>
          <div class="space-y-2">
            <Label for="password">Password</Label>
            <Input id="password" v-model="password" type="password" autocomplete="new-password" required />
          </div>
          <div class="space-y-2">
            <Label for="confirm">Confirm password</Label>
            <Input id="confirm" v-model="confirm" type="password" autocomplete="new-password" required />
          </div>

          <p v-if="error" class="text-sm text-destructive">{{ error }}</p>

          <Button type="submit" class="w-full font-mono text-sm" :disabled="loading">
            {{ loading ? 'Creating…' : 'Create account' }}
            <ArrowRight v-if="!loading" class="size-4" />
          </Button>
        </form>

        <p class="mt-6 text-center text-sm text-muted-foreground">
          Already have an account?
          <NuxtLink to="/signin" class="text-primary underline-offset-4 hover:underline">Sign in</NuxtLink>
        </p>
      </CardContent>
    </Card>
  </div>
</template>
