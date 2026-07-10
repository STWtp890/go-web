import { ref } from 'vue'

/**
 * 通用异步状态管理
 * 统一 loading / errorMsg 状态 + try/catch 模式
 *
 * @returns {Object} loading, errorMsg, run, clearError
 */
export function useAsync() {
  const loading = ref(false)
  const errorMsg = ref('')

  /**
   * 包装异步操作，自动管理 loading/error 状态
   * @param {Function} fn         - 异步函数
   * @param {string}   errorPrefix - 错误消息前缀（默认 '操作失败'）
   * @returns {Promise} fn 的返回值，出错时抛出
   */
  async function run(fn, errorPrefix = '操作失败') {
    loading.value = true
    try {
      const result = await fn()
      errorMsg.value = ''   // 成功后才清除错误
      return result
    } catch (e) {
      const msg = (e.response && e.response.data && e.response.data.message) || '网络错误'
      errorMsg.value = `${errorPrefix}：${msg}`
      throw e
    } finally {
      loading.value = false
    }
  }

  function clearError() {
    errorMsg.value = ''
  }

  return { loading, errorMsg, run, clearError }
}
