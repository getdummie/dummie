<script setup lang="ts">
import { highlightVersion, renderMarkdown } from '@/lib/markdown'

const props = defineProps<{ source: string, streaming?: boolean }>()

const html = computed(() => {
  void highlightVersion.value
  return renderMarkdown(props.source, !props.streaming)
})
</script>

<template>
  <!-- eslint-disable-next-line vue/no-v-html -- markdown-it runs with html disabled -->
  <div class="agent-md" v-html="html" />
</template>

<style>
.agent-md { overflow-wrap: anywhere; line-height: 1.6; }
.agent-md > :first-child { margin-top: 0; }
.agent-md > :last-child { margin-bottom: 0; }
.agent-md p, .agent-md ul, .agent-md ol, .agent-md pre, .agent-md blockquote, .agent-md table { margin: 0.6em 0; }
.agent-md ul { list-style: disc; padding-left: 1.4em; }
.agent-md ol { list-style: decimal; padding-left: 1.4em; }
.agent-md h1, .agent-md h2, .agent-md h3, .agent-md h4 { font-weight: 600; margin: 1em 0 0.4em; }
.agent-md h1 { font-size: 1.25em; }
.agent-md h2 { font-size: 1.125em; }
.agent-md a { color: var(--primary-text); text-decoration: underline; text-underline-offset: 3px; }
.agent-md blockquote { border-left: 2px solid var(--border); padding-left: 0.8em; color: var(--muted-foreground); }
.agent-md :not(pre) > code { font-family: var(--font-mono); font-size: 0.85em; background: var(--muted); border-radius: 4px; padding: 0.1em 0.35em; }
.agent-md pre { font-family: var(--font-mono); font-size: 0.8rem; background: var(--muted); border: 1px solid var(--border); border-radius: 6px; padding: 0.75em 0.9em; overflow-x: auto; }
.agent-md table { border-collapse: collapse; display: block; overflow-x: auto; }
.agent-md th, .agent-md td { border: 1px solid var(--border); padding: 0.3em 0.6em; }
.agent-md .shiki, .agent-md .shiki span { color: var(--shiki-light); }
.agent-md .shiki { background-color: var(--shiki-light-bg); }
.dark .agent-md .shiki, .dark .agent-md .shiki span { color: var(--shiki-dark); }
.dark .agent-md .shiki { background-color: var(--shiki-dark-bg); }
</style>
