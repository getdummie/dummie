<script setup lang="ts">
import AgentFileEditor from '@/components/agent/AgentFileEditor.vue'
import AgentMediaViewer from '@/components/agent/AgentMediaViewer.vue'

defineProps<{ hint: string }>()
const ws = useInjectedAgentWorkspace()
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col">
    <AgentMediaViewer
      v-if="ws.openedMedia && ws.openedFile"
      :key="`${ws.openedFile.cwd}/${ws.openedFile.path}`"
      :cwd="ws.openedFile.cwd"
      :path="ws.openedFile.path"
      :kind="ws.openedMedia.kind"
      :mime="ws.openedMedia.mime"
      :read-raw="ws.sock.readRaw"
      @close="ws.openedFile = null"
    />
    <AgentFileEditor
      v-else-if="ws.openedFile"
      :key="`${ws.openedFile.cwd}/${ws.openedFile.path}`"
      :cwd="ws.openedFile.cwd"
      :path="ws.openedFile.path"
      mode="file"
      :read-file="ws.sock.readFile"
      :write-file="ws.sock.writeFile"
      @close="ws.openedFile = null"
      @saved="ws.onEdited"
    />
    <p v-else class="p-4 font-mono text-xs text-muted-foreground">{{ hint }}</p>
  </div>
</template>
