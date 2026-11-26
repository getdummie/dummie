<script setup lang="ts">
type PlanVM = { id: string, name: string, reason?: string }
type Plan = { deleted: PlanVM[], forgotten: PlanVM[], moved: PlanVM[], blocked: PlanVM[] }

const props = defineProps<{ clientId: string }>()
const blocked = defineModel<boolean>('blocked', { default: true })

const { authFetch } = useAuth()
const plan = ref<Plan | null>(null)
const error = ref<string | null>(null)

async function readMessage(res: Response): Promise<string | null> {
  try {
    const b = await res.json()
    return typeof b?.message === 'string' ? b.message : null
  }
  catch {
    return null
  }
}

async function load() {
  plan.value = null
  error.value = null
  blocked.value = true
  try {
    const res = await authFetch(`/admin/clients/${props.clientId}/delete-plan`)
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    plan.value = await res.json()
    blocked.value = plan.value!.blocked.length > 0
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not check this host\'s VMs'
  }
}
watch(() => props.clientId, load, { immediate: true })
</script>

<template>
  <div class="space-y-3 text-sm">
    <FormError id="delete-plan-error" :message="error" />
    <p v-if="!plan && !error" class="text-muted-foreground">Checking this host's VMs…</p>
    <template v-else-if="plan">
      <p v-if="!plan.deleted.length && !plan.forgotten.length && !plan.moved.length && !plan.blocked.length" class="text-muted-foreground">
        No VMs are on this host.
      </p>
      <section v-if="plan.deleted.length" class="space-y-1">
        <p class="font-medium text-destructive">
          Deleted for good: {{ plan.deleted.length }} VM{{ plan.deleted.length === 1 ? '' : 's' }} whose disk is on this host
        </p>
        <ul class="max-h-32 overflow-y-auto font-mono text-xs text-muted-foreground">
          <li v-for="v in plan.deleted" :key="v.id" class="truncate">{{ v.name || v.id }}</li>
        </ul>
      </section>
      <section v-if="plan.forgotten.length" class="space-y-1">
        <p class="font-medium">
          Records removed: {{ plan.forgotten.length }} VM{{ plan.forgotten.length === 1 ? '' : 's' }} on FSx that control holds no volume for
        </p>
        <p class="text-xs text-muted-foreground">
          Usually stale copies this host reported. If it is connected it shuts them down; their disks are not touched.
        </p>
        <ul class="max-h-32 overflow-y-auto font-mono text-xs text-muted-foreground">
          <li v-for="v in plan.forgotten" :key="v.id" class="truncate">{{ v.name || v.id }}</li>
        </ul>
      </section>
      <section v-if="plan.moved.length" class="space-y-1">
        <p class="font-medium">
          Moved to other hosts: {{ plan.moved.length }} VM{{ plan.moved.length === 1 ? '' : 's' }} on FSx
        </p>
        <p class="text-xs text-muted-foreground">
          If this host is connected, it shuts each one down and lets go of its disk first. Each then goes
          to the least loaded connected host, and those that were running start again there. With no other
          host connected they wait, and this host stays listed as deleting until they have moved.
        </p>
        <ul class="max-h-32 overflow-y-auto font-mono text-xs text-muted-foreground">
          <li v-for="v in plan.moved" :key="v.id" class="truncate">{{ v.name || v.id }}</li>
        </ul>
      </section>
      <section v-if="plan.blocked.length" class="space-y-1">
        <p class="font-medium text-destructive">
          Blocking the delete: {{ plan.blocked.length }} VM{{ plan.blocked.length === 1 ? '' : 's' }} on FSx that cannot move yet
        </p>
        <ul class="max-h-32 space-y-0.5 overflow-y-auto text-xs text-muted-foreground">
          <li v-for="v in plan.blocked" :key="v.id">
            <span class="font-mono text-foreground">{{ v.name || v.id }}</span>: {{ v.reason }}
          </li>
        </ul>
      </section>
    </template>
  </div>
</template>
