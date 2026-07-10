<template>
  <Transition name="fade">
    <div v-if="show" class="modal fade show d-block" tabindex="-1" @click.self="closeModal">
      <div class="modal-dialog" :class="{ 'modal-dialog-centered': centered }">
        <div class="modal-content">
          <div v-if="$slots.header" class="modal-header">
            <slot name="header" />
            <button v-if="showClose" type="button" class="close" @click="closeModal">
              <i class="icon-cancel"></i>
            </button>
          </div>
          <div v-if="$slots.default" class="modal-body"><slot /></div>
          <div v-if="$slots.footer" class="modal-footer"><slot name="footer" /></div>
        </div>
      </div>
    </div>
  </Transition>
</template>

<script setup>
const props = defineProps({ show: Boolean, showClose: { type: Boolean, default: true }, centered: { type: Boolean, default: true } })
const emit = defineEmits(['update:show'])
function closeModal() { emit('update:show', false) }
</script>
