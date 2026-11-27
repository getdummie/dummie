<script setup lang="ts">
import { CircleCheck, FolderPlus } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import AgentComposer from '@/components/agent/AgentComposer.vue'
import AgentCwdInput from '@/components/agent/AgentCwdInput.vue'
import AgentHarnessIcon from '@/components/agent/AgentHarnessIcon.vue'
import AgentThread from '@/components/agent/AgentThread.vue'

const ws = useInjectedAgentWorkspace()
const uid = useId()
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col">
    <div v-if="!ws.activeKey" class="mx-auto mt-10 w-full max-w-3xl px-4">
      <p :id="`${uid}-harness`" class="eyebrow mb-2 text-muted-foreground">agent</p>
      <Tabs :model-value="ws.harness" class="mb-6" @update:model-value="ws.pickHarness(String($event))">
        <TabsList class="h-auto flex-wrap justify-start" :aria-labelledby="`${uid}-harness`">
          <TabsTrigger
            v-for="h in ws.hello?.harnesses ?? []"
            :key="h.name"
            :value="h.name"
            class="h-7 flex-none gap-1.5 font-mono text-xs"
            :class="!h.available && 'opacity-60'"
          >
            <AgentHarnessIcon :harness="h.name" />
            {{ h.name }}
          </TabsTrigger>
        </TabsList>
      </Tabs>
      <label :for="`${uid}-cwd`" class="eyebrow mb-2 block text-muted-foreground">working directory</label>
      <AgentCwdInput :id="`${uid}-cwd`" v-model="ws.newCwd" placeholder="~/app" :list="ws.sock.list" />
      <div class="mt-2 flex min-h-7 flex-wrap items-center gap-2 font-mono text-xs" role="status">
        <span v-if="ws.cwdState === 'dir'" class="inline-flex items-center gap-1 text-muted-foreground">
          <CircleCheck class="size-3.5 text-primary-text" aria-hidden="true" />
          exists
        </span>
        <span v-else-if="ws.cwdState === 'file'" class="text-destructive">that is a file, not a directory</span>
        <template v-else-if="ws.cwdState === 'missing'">
          <span class="text-muted-foreground">does not exist yet</span>
          <Button variant="outline" size="sm" class="h-7 font-mono text-xs" :disabled="ws.creatingDir" @click="ws.createCwd">
            <FolderPlus class="size-3.5" aria-hidden="true" />
            Create it
          </Button>
        </template>
      </div>
      <p class="mt-1 font-mono text-xs text-muted-foreground">
        A new {{ ws.harness }} session starts here when you send the first message. The diff pane shows this directory's uncommitted changes.
      </p>
    </div>

    <AgentThread v-if="ws.activeKey" :chat="ws.chat" />
    <div v-else class="flex-1" />

    <p v-if="ws.actionError" class="mx-auto w-full max-w-3xl px-4 pb-2 font-mono text-xs text-destructive" role="alert">
      {{ ws.actionError }}
    </p>
    <AgentComposer
      :busy="ws.chat.busy"
      :busy-hint="ws.busyHint"
      :sending="ws.sending"
      :disabled="ws.phase !== 'open' || !ws.harnessInfo?.available"
      :models="ws.models"
      :model="ws.model"
      @send="ws.send"
      @stop="ws.stop"
      @model="ws.setModel"
    />
  </div>
</template>
