import { ref } from 'vue'

/**
 * 通用 Modal 表单状态管理
 * 管理 show/editing/loading/error 等通用状态
 *
 * @returns {Object} showModal, editing, modalLoading, modalError, openModal, closeModal
 */
export function useModal() {
  const showModal = ref(false)
  const editing = ref(null)       // 编辑中的记录（null = 新建模式）
  const modalLoading = ref(false)
  const modalError = ref('')

  /**
   * 打开 Modal
   * @param {Object|null} item - 编辑时传入记录，新建时传 null
   */
  function openModal(item = null) {
    editing.value = item
    modalError.value = ''
    modalLoading.value = false
    showModal.value = true
  }

  function closeModal() {
    showModal.value = false
    editing.value = null
    modalError.value = ''
  }

  return { showModal, editing, modalLoading, modalError, openModal, closeModal }
}
