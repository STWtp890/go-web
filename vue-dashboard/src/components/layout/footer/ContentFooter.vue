<template>
  <footer class="footer">
    <div class="footer-inner">
      © {{ year }} <a href="javascript:void(0)">NephrenRuqInsania</a> — Made with Vue 3
    </div>
  </footer>
</template>

<script setup>
const year = new Date().getFullYear()
</script>
