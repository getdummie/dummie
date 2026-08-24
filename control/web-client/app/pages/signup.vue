<script setup lang="ts">
import { ArrowRight } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'

definePageMeta({ middleware: 'guest' })
useHead({ title: 'dummie — create account' })

const { signup, signupEnabled } = useAuth()

// Starts null so the form isn't flashed on screen before we know it is offered.
const enabled = ref<boolean | null>(null)
onMounted(async () => {
  enabled.value = await signupEnabled()
})

const username = ref('')
const email = ref('')
const firstName = ref('')
const lastName = ref('')
const password = ref('')
const confirm = ref('')
const loading = ref(false)
const error = ref<string | null>(null)
// Which input the current error belongs to, so `aria-invalid` and
// `aria-describedby` land on that field rather than smearing across the form.
// `null` means the error came from the server and isn't attributable.
const errorField = ref<string | null>(null)

// The password rules, stated once so the hint text under the field and the
// validator below can't drift apart.
const PASSWORD_RULES = 'At least 8 characters, including an uppercase letter, a lowercase letter, a number, and a special character.'

// Mirror the server's rules for UX. The backend is authoritative and applies
// strong rules only when APP_ENV=prod; here we key off the client build mode.
function validate(): { message: string, field: string } | null {
  if (!username.value.trim()) return { message: 'Username is required', field: 'username' }
  if (!email.value.trim()) return { message: 'Email is required', field: 'email' }
  if (password.value !== confirm.value) return { message: 'Passwords do not match', field: 'confirm' }
  if (import.meta.dev) {
    if (!password.value) return { message: 'Password is required', field: 'password' }
    return null
  }
  const p = password.value
  if (p.length < 8) return { message: 'Password must be at least 8 characters', field: 'password' }
  if (!/[A-Z]/.test(p) || !/[a-z]/.test(p) || !/[0-9]/.test(p) || !/[^A-Za-z0-9]/.test(p)) {
    return { message: 'Password must include uppercase, lowercase, number, and a special character', field: 'password' }
  }
  return null
}

// Ties a field to the error region only when the error is actually about it.
function describedBy(field: string, ...extra: string[]) {
  const ids = [...extra]
  if (error.value && errorField.value === field) ids.unshift('signup-error')
  return ids.length ? ids.join(' ') : undefined
}

function invalid(field: string) {
  return error.value && errorField.value === field ? true : undefined
}

async function onSubmit() {
  const failure = validate()
  error.value = failure?.message ?? null
  errorField.value = failure?.field ?? null
  if (error.value) {
    // Move focus to the offending field so the user isn't left hunting for it.
    await nextTick()
    document.getElementById(failure!.field)?.focus()
    return
  }
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
    errorField.value = null
  }
  finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="mx-auto flex min-h-[70vh] w-full max-w-md flex-col justify-center px-4 py-16 sm:px-6">
    <p class="eyebrow mb-4 text-primary-text">// Get started</p>

    <Card v-if="enabled === false">
      <CardHeader>
        <CardTitle as="h1" class="text-2xl tracking-tight">Sign-ups are disabled</CardTitle>
        <CardDescription>
          This control plane is not accepting new accounts. Ask an administrator to create one for you.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <p class="text-center text-sm text-muted-foreground">
          Already have an account?
          <NuxtLink to="/signin" class="text-primary-text underline underline-offset-4">Sign in</NuxtLink>
        </p>
      </CardContent>
    </Card>

    <Card v-else-if="enabled">
      <CardHeader>
        <CardTitle as="h1" class="text-2xl tracking-tight">Create your account</CardTitle>
        <CardDescription>Spin up your first sandbox in minutes.</CardDescription>
      </CardHeader>
      <CardContent>
        <form class="space-y-4" :aria-busy="loading" @submit.prevent="onSubmit">
          <!-- Grid drops to one column under 380px so the two name fields
               don't get squeezed below a usable width at 320px. -->
          <div class="grid grid-cols-1 gap-3 min-[380px]:grid-cols-2">
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
            <Input
              id="username"
              v-model="username"
              autocomplete="username"
              placeholder="neo"
              required
              :aria-invalid="invalid('username')"
              :aria-describedby="describedBy('username')"
            />
          </div>
          <div class="space-y-2">
            <Label for="email">Email</Label>
            <Input
              id="email"
              v-model="email"
              type="email"
              autocomplete="email"
              placeholder="neo@example.com"
              required
              :aria-invalid="invalid('email')"
              :aria-describedby="describedBy('email')"
            />
          </div>
          <div class="space-y-2">
            <Label for="password">Password</Label>
            <Input
              id="password"
              v-model="password"
              type="password"
              autocomplete="new-password"
              required
              :aria-invalid="invalid('password')"
              :aria-describedby="describedBy('password', 'password-help')"
            />
            <!-- Stating the rules up front beats only revealing them after a
                 rejected submit. (WCAG 3.3.2) -->
            <p id="password-help" class="text-xs text-muted-foreground">
              {{ PASSWORD_RULES }}
            </p>
          </div>
          <div class="space-y-2">
            <Label for="confirm">Confirm password</Label>
            <Input
              id="confirm"
              v-model="confirm"
              type="password"
              autocomplete="new-password"
              required
              :aria-invalid="invalid('confirm')"
              :aria-describedby="describedBy('confirm')"
            />
          </div>

          <FormError id="signup-error" :message="error" />

          <Button type="submit" class="w-full font-mono text-sm" :disabled="loading">
            {{ loading ? 'Creating…' : 'Create account' }}
            <ArrowRight v-if="!loading" class="size-4" aria-hidden="true" />
          </Button>
        </form>

        <p class="mt-6 text-center text-sm text-muted-foreground">
          Already have an account?
          <NuxtLink to="/signin" class="text-primary-text underline underline-offset-4">Sign in</NuxtLink>
        </p>
      </CardContent>
    </Card>

    <Card v-else aria-busy="true">
      <CardHeader>
        <Skeleton class="h-7 w-56" />
        <Skeleton class="h-4 w-64" />
      </CardHeader>
      <CardContent class="space-y-4">
        <Skeleton v-for="n in 4" :key="n" class="h-9 w-full" />
      </CardContent>
    </Card>
  </div>
</template>
