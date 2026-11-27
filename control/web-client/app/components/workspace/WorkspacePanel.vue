<script setup lang="ts">
import AgentChanges from '@/components/agent/AgentChanges.vue'
import AgentChatPanel from '@/components/agent/AgentChatPanel.vue'
import AgentFilesPanel from '@/components/agent/AgentFilesPanel.vue'
import AgentSessions from '@/components/agent/AgentSessions.vue'
import AgentTransfer from '@/components/agent/AgentTransfer.vue'
import VmDestinations from '@/components/VmDestinations.vue'
import WorkspaceConsole from '@/components/workspace/WorkspaceConsole.vue'
import type { PanelType } from '@/lib/workspace'

defineProps<{ panel: PanelType, vmId: string, active: boolean }>()
const ws = useInjectedAgentWorkspace()
</script>

<template>
  <AgentChatPanel v-if="panel === 'chat'" />
  <WorkspaceConsole v-else-if="panel === 'console'" :vm-id="vmId" />
  <AgentFilesPanel v-else-if="panel === 'files'" hint="Pick a file in the diff panel's files tab to open it here." />
  <AgentTransfer
    v-else-if="panel === 'transfer'"
    :initial-path="ws.activeCwd || ws.hello?.cwd || ws.hello?.home || ''"
    :connected="ws.phase === 'open'"
    :active="active"
    :list="ws.sock.list"
    :upload="ws.sock.upload"
    :read-raw="ws.sock.readRaw"
  />
  <div v-else-if="panel === 'network'" class="min-h-0 flex-1 overflow-y-auto p-4">
    <VmDestinations :vm-id="vmId" />
  </div>
  <AgentChanges
    v-else-if="panel === 'diff'"
    :diff="ws.diff"
    :read-file="ws.sock.readFile"
    :write-file="ws.sock.writeFile"
    @git-init="ws.gitInit"
    @edited="ws.onEdited"
    @open-file="ws.openFile"
  />
  <AgentSessions
    v-else-if="panel === 'sessions'"
    :sessions="ws.sessions"
    :active="ws.activeKey"
    :home="ws.hello?.home"
    :renamable="ws.renamable"
    :rename="ws.renameSession"
    @open="ws.openSession"
    @new="ws.startNew"
  />
</template>
