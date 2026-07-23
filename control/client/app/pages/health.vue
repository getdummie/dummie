<script setup lang="ts">
import { AlertTriangle, CheckCircle2, RefreshCw, XCircle } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

useHead({ title: 'dummie — system health' })

interface Health {
  all: boolean
  db: boolean
  api: boolean
}

const config = useRuntimeConfig()

const data = ref<Health | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)

const checks = computed(() => {
  if (!data.value) return []
  return [
    { key: 'api', label: 'API server', desc: 'HTTP control plane', ok: data.value.api },
    { key: 'db', label: 'Database', desc: 'PostgreSQL connectivity', ok: data.value.db },
    { key: 'all', label: 'Overall', desc: 'All subsystems nominal', ok: data.value.all },
  ]
})

async function load() {
  loading.value = true
  error.value = null
  data.value = null
  try {
    const res = await fetch(`${config.public.apiBase}/api/v1/ht/`)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    data.value = (await res.json()) as Health
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Request failed'
  }
  finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="mx-auto max-w-2xl px-4 py-16 sm:px-6 sm:py-24">
    <p class="eyebrow mb-4 text-primary">// GET /api/v1/ht/</p>
    <h1 class="text-3xl font-semibold tracking-tight sm:text-4xl">
      System health
    </h1>
    <p class="mt-3 text-muted-foreground">
      Live status of the dummie control plane and its dependencies.
    </p>

    <Card class="mt-8">
      <CardHeader class="flex-row items-center justify-between gap-4 space-y-0">
        <div>
          <CardTitle class="font-mono text-base">healthcheck</CardTitle>
          <CardDescription>Polled on load — refresh to re-run.</CardDescription>
        </div>
        <Button variant="outline" size="sm" :disabled="loading" class="font-mono text-xs" @click="load">
          <RefreshCw class="size-3.5" :class="loading && 'animate-spin'" />
          Refresh
        </Button>
      </CardHeader>
      <CardContent>
        <!-- Loading -->
        <div v-if="loading" class="space-y-3">
          <Skeleton v-for="n in 3" :key="n" class="h-16 w-full rounded-md" />
        </div>

        <!-- Error -->
        <Alert v-else-if="error" variant="destructive">
          <AlertTriangle />
          <AlertTitle>Could not reach the healthcheck</AlertTitle>
          <AlertDescription>
            {{ error }} — open this page through the API server (port 1323) so
            <code class="font-mono">/api/v1/ht/</code> resolves.
          </AlertDescription>
        </Alert>

        <!-- Result -->
        <ul v-else class="divide-y divide-border">
          <li
            v-for="c in checks"
            :key="c.key"
            class="flex items-center justify-between gap-4 py-4 first:pt-0 last:pb-0"
          >
            <div class="flex items-center gap-3">
              <CheckCircle2 v-if="c.ok" class="size-5 text-primary" />
              <XCircle v-else class="size-5 text-destructive" />
              <div>
                <div class="font-mono text-sm font-medium">{{ c.label }}</div>
                <div class="text-xs text-muted-foreground">{{ c.desc }}</div>
              </div>
            </div>
            <span
              class="inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 font-mono text-[0.7rem] uppercase tracking-wider"
              :class="c.ok
                ? 'border-primary/30 bg-primary/10 text-primary'
                : 'border-destructive/30 bg-destructive/10 text-destructive'"
            >
              <span class="size-1.5 rounded-full" :class="c.ok ? 'bg-primary' : 'bg-destructive'" />
              {{ c.ok ? 'up' : 'down' }}
            </span>
          </li>
        </ul>
      </CardContent>
    </Card>
  </div>
</template>
