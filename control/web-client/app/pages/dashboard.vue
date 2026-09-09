<script setup lang="ts">
import { Plus } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'

definePageMeta({ middleware: 'auth' })
useHead({ title: 'dummie — dashboard' })

interface DashboardVM {
  id: string
  name: string
  status: 'pending' | 'running' | 'stopped' | 'failed' | 'gone'
  cpus: number
  memory_mib: number
  url: string
}

const { user, authFetch } = useAuth()

const greeting = computed(() =>
  user.value?.first_name?.trim() || user.value?.username || 'there',
)

const vms = ref<DashboardVM[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

const dotClass: Record<DashboardVM['status'], string> = {
  running: 'bg-primary-text',
  pending: 'bg-amber-500',
  stopped: 'bg-muted-foreground',
  gone: 'bg-muted-foreground',
  failed: 'bg-destructive',
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await authFetch('/vms?limit=100')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    vms.value = ((await res.json()).items ?? []).filter((v: DashboardVM) => v.status !== 'gone')
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not load your VMs'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

function fmtMiB(mib: number) {
  if (mib < 1024) return `${mib} MiB`
  const gib = mib / 1024
  return Number.isInteger(gib) ? `${gib} GiB` : `${gib.toFixed(1)} GiB`
}
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-12 sm:px-6 sm:py-16">
    <div class="flex flex-wrap items-end justify-between gap-4 border-b border-border pb-6">
      <div>
        <p class="eyebrow mb-3 text-primary-text">// Dashboard</p>
        <h1 class="text-3xl font-semibold tracking-tight sm:text-4xl">
          Welcome, {{ greeting }}.
        </h1>
        <p v-if="!loading && vms.length" class="mt-2 font-mono text-xs text-muted-foreground">
          {{ vms.length }} {{ vms.length === 1 ? 'machine' : 'machines' }} ·
          {{ vms.filter(v => v.status === 'running').length }} running
        </p>
      </div>
      <Button size="sm" as-child class="font-mono text-xs">
        <NuxtLink to="/vms">
          <Plus class="size-4" aria-hidden="true" />
          New VM
        </NuxtLink>
      </Button>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-8">
      <AlertTitle>Could not load your VMs</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <div v-if="loading" class="mt-8 grid gap-5 sm:grid-cols-2 xl:grid-cols-3" aria-busy="true">
      <p class="sr-only">Loading your VMs…</p>
      <Skeleton v-for="n in 3" :key="n" class="h-56 w-full rounded-xl" aria-hidden="true" />
    </div>

    <p v-else-if="!vms.length" class="mt-8 text-muted-foreground">
      You have no VMs yet. Create one from
      <NuxtLink to="/vms" class="underline underline-offset-4">here</NuxtLink>.
    </p>

    <ul v-else class="mt-8 grid gap-5 sm:grid-cols-2 xl:grid-cols-3">
      <li
        v-for="vm in vms"
        :key="vm.id"
        class="group relative overflow-hidden rounded-xl border border-border bg-card transition-all hover:-translate-y-0.5 hover:border-primary-text/40 hover:shadow-lg"
      >
        <VmPreview :url="vm.url" :name="vm.name" :live="vm.status === 'running'" />

        <div class="flex items-center justify-between gap-3 px-3 py-2.5">
          <div class="min-w-0">
            <p class="truncate font-mono text-sm font-medium">{{ vm.name }}</p>
            <p class="font-mono text-[11px] text-muted-foreground">
              {{ vm.cpus }} vCPU · {{ fmtMiB(vm.memory_mib) }}
            </p>
          </div>
          <span class="flex shrink-0 items-center gap-1.5 font-mono text-[11px] text-muted-foreground">
            <span :class="['size-1.5 rounded-full', dotClass[vm.status]]" aria-hidden="true" />
            {{ vm.status }}
          </span>
        </div>

        <NuxtLink
          :to="`/vms/${vm.id}`"
          class="absolute inset-0 z-10 rounded-xl focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
        >
          <span class="sr-only">Open {{ vm.name }}</span>
        </NuxtLink>
      </li>
    </ul>
  </div>
</template>
