<script setup lang="ts">
import { Plus, Trash2 } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'

definePageMeta({ middleware: ['auth', 'admin'] })
useHead({ title: 'dummie — admin · users' })

interface AdminUser {
  id: string
  username: string
  email: string
  first_name: string
  last_name: string
  user_type: string
  created_at: string
}

const { authFetch, user: currentUser } = useAuth()

const items = ref<AdminUser[]>([])
const total = ref(0)
const limit = ref(20)
const offset = ref(0)
const loading = ref(true)
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

function fmtDate(s: string) {
  const d = new Date(s)
  return Number.isNaN(d.getTime()) ? s : d.toLocaleString()
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await authFetch(`/admin/users?limit=${limit.value}&offset=${offset.value}`)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = await res.json()
    items.value = data.items ?? []
    total.value = data.total ?? 0
  }
  catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load users'
  }
  finally {
    loading.value = false
  }
}
onMounted(load)

const page = computed(() => Math.floor(offset.value / limit.value) + 1)
const pageCount = computed(() => Math.max(1, Math.ceil(total.value / limit.value)))
function next() {
  if (offset.value + limit.value < total.value) {
    offset.value += limit.value
    load()
  }
}
function prev() {
  if (offset.value > 0) {
    offset.value = Math.max(0, offset.value - limit.value)
    load()
  }
}

// --- create ---
const createOpen = ref(false)
const creating = ref(false)
const createError = ref<string | null>(null)
const form = reactive({ username: '', email: '', password: '', first_name: '', last_name: '', user_type: 'user' })

function resetForm() {
  Object.assign(form, { username: '', email: '', password: '', first_name: '', last_name: '', user_type: 'user' })
  createError.value = null
}

async function create() {
  creating.value = true
  createError.value = null
  try {
    const res = await authFetch('/admin/users', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(form),
    })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    createOpen.value = false
    resetForm()
    offset.value = 0
    await load()
  }
  catch (e) {
    createError.value = e instanceof Error ? e.message : 'Could not create user'
  }
  finally {
    creating.value = false
  }
}

// --- delete ---
const toDelete = ref<AdminUser | null>(null)
const deleting = ref(false)
const deleteError = ref<string | null>(null)

async function confirmDelete() {
  if (!toDelete.value) return
  deleting.value = true
  deleteError.value = null
  try {
    const res = await authFetch(`/admin/users/${toDelete.value.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await readMessage(res)) || `HTTP ${res.status}`)
    toDelete.value = null
    await load()
  }
  catch (e) {
    deleteError.value = e instanceof Error ? e.message : 'Could not delete user'
  }
  finally {
    deleting.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-12 sm:px-6">
    <AdminNav />

    <div class="mt-8 flex items-end justify-between gap-4">
      <div>
        <p class="eyebrow mb-2 text-primary-text">// admin · users</p>
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">Users</h1>
      </div>
      <Dialog v-model:open="createOpen" @update:open="(v: boolean) => !v && resetForm()">
        <Button class="font-mono text-xs" @click="createOpen = true">
          <Plus class="size-4" aria-hidden="true" />
          New user
        </Button>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create user</DialogTitle>
            <DialogDescription>Add a new account to the control plane.</DialogDescription>
          </DialogHeader>
          <form class="space-y-4" :aria-busy="creating" @submit.prevent="create">
            <div class="grid grid-cols-1 gap-3 min-[380px]:grid-cols-2">
              <div class="space-y-2">
                <Label for="c-first">First name</Label>
                <Input id="c-first" v-model="form.first_name" />
              </div>
              <div class="space-y-2">
                <Label for="c-last">Last name</Label>
                <Input id="c-last" v-model="form.last_name" />
              </div>
            </div>
            <div class="space-y-2">
              <Label for="c-username">Username</Label>
              <Input id="c-username" v-model="form.username" autocomplete="off" required />
            </div>
            <div class="space-y-2">
              <Label for="c-email">Email</Label>
              <Input id="c-email" v-model="form.email" type="email" autocomplete="off" required />
            </div>
            <div class="space-y-2">
              <Label for="c-password">Password</Label>
              <Input id="c-password" v-model="form.password" type="password" autocomplete="new-password" required />
            </div>
            <div class="space-y-2">
              <Label for="c-role">Role</Label>
              <div class="*:w-full">
                <NativeSelect id="c-role" v-model="form.user_type">
                  <NativeSelectOption value="user">user</NativeSelectOption>
                  <NativeSelectOption value="admin">admin</NativeSelectOption>
                </NativeSelect>
              </div>
            </div>

            <FormError id="create-user-error" :message="createError" />

            <DialogFooter>
              <DialogClose as-child>
                <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
              </DialogClose>
              <Button type="submit" class="font-mono text-xs" :disabled="creating">
                {{ creating ? 'Creating…' : 'Create' }}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>

    <Alert v-if="error" variant="destructive" class="mt-6">
      <AlertTitle>Could not load users</AlertTitle>
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <!-- `aria-live` so a page change or a delete announces the new row count
         rather than silently swapping the table out. (WCAG 4.1.3) -->
    <div class="mt-6 rounded-lg border border-border" aria-live="polite" :aria-busy="loading">
      <p v-if="loading" class="sr-only">Loading users…</p>
      <Table label="Users">
        <TableHeader>
          <TableRow>
            <TableHead>Username</TableHead>
            <TableHead>Email</TableHead>
            <TableHead>Name</TableHead>
            <TableHead>Role</TableHead>
            <TableHead>Created</TableHead>
            <TableHead class="text-right">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <template v-if="loading">
            <TableRow v-for="n in 5" :key="n" aria-hidden="true">
              <TableCell v-for="c in 6" :key="c"><Skeleton class="h-4 w-full" /></TableCell>
            </TableRow>
          </template>
          <TableEmpty v-else-if="!items.length" :colspan="6">No users found.</TableEmpty>
          <TableRow v-for="u in items" v-else :key="u.id">
            <TableCell class="font-mono">{{ u.username }}</TableCell>
            <TableCell class="text-muted-foreground">{{ u.email }}</TableCell>
            <TableCell>{{ [u.first_name, u.last_name].filter(Boolean).join(' ') || '—' }}</TableCell>
            <TableCell>
              <Badge :variant="u.user_type === 'admin' ? 'default' : 'secondary'" class="font-mono">
                {{ u.user_type }}
              </Badge>
            </TableCell>
            <TableCell class="text-muted-foreground">{{ fmtDate(u.created_at) }}</TableCell>
            <TableCell class="text-right">
              <!-- Icon-only, and one per row — so the name has to include the
                   username, otherwise every button reads "Delete user" and a
                   screen-reader user can't tell them apart. (WCAG 2.4.6) -->
              <Button
                variant="ghost"
                size="icon"
                class="text-destructive hover:text-destructive"
                :disabled="u.id === currentUser?.id"
                :aria-label="u.id === currentUser?.id
                  ? `Cannot delete ${u.username}: this is your own account`
                  : `Delete user ${u.username}`"
                @click="toDelete = u"
              >
                <Trash2 class="size-4" aria-hidden="true" />
              </Button>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>

    <nav aria-label="Users pagination" class="mt-4 flex flex-wrap items-center justify-between gap-3 font-mono text-xs text-muted-foreground">
      <span>{{ total }} total · page {{ page }} / {{ pageCount }}</span>
      <div class="flex gap-2">
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset <= 0 || loading" aria-label="Previous page of users" @click="prev">Prev</Button>
        <Button variant="outline" size="sm" class="font-mono text-xs" :disabled="offset + limit >= total || loading" aria-label="Next page of users" @click="next">Next</Button>
      </div>
    </nav>

    <!-- delete confirm -->
    <Dialog :open="!!toDelete" @update:open="(v: boolean) => { if (!v) toDelete = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete user</DialogTitle>
          <DialogDescription>
            This permanently removes <span class="font-mono text-foreground">{{ toDelete?.username }}</span>
            and all of their sessions. This cannot be undone.
          </DialogDescription>
        </DialogHeader>
        <FormError id="delete-user-error" :message="deleteError" />
        <DialogFooter>
          <DialogClose as-child>
            <Button type="button" variant="outline" class="font-mono text-xs">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" class="font-mono text-xs" :disabled="deleting" @click="confirmDelete">
            {{ deleting ? 'Deleting…' : 'Delete' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
