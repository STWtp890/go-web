<script setup lang="ts">
import { computed } from 'vue'
import DOMPurify from 'dompurify'
import { marked } from 'marked'

interface Props { content: string }
const props = defineProps<Props>()

marked.setOptions({ gfm: true, breaks: true })
const html = computed(() => DOMPurify.sanitize(marked.parse(props.content) as string))
</script>

<template>
  <!-- HTML is sanitized with DOMPurify before rendering. -->
  <article class="markdown-body" v-html="html" />
</template>
