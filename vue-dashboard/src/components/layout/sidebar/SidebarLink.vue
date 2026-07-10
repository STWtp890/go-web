<template>
  <li :class="{ active: isActive }">
    <router-link :to="to" class="nav-link"><slot /></router-link>
  </li>
</template>

<script setup>
import { computed } from 'vue'
import { useRoute } from 'vue-router'
const props = defineProps({ to: { type: [String, Object], default: '' } })
const route = useRoute()
const isActive = computed(() => route.path.startsWith(props.to))
</script>

<style scoped>
.nav-link {
  border-left: 3px solid transparent;
  transition: border-color 0.2s ease, background-color 0.2s ease;
}

.nav-link:hover {
  border-left-color: var(--app-brand);
  background: var(--app-sidebar-hover-bg);
}

li.active .nav-link {
  border-left-color: var(--app-brand);
}
</style>
