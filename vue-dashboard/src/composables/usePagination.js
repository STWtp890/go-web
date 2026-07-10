import { ref, computed, watch, nextTick } from 'vue'

/**
 * 通用分页 composable
 * 管理 page/pageSize/totalCount/totalPages，并监听 page 自动触发 fetch
 *
 * @param {Function} fetchFn - 数据获取函数（页面组件提供）
 * @param {Object}   options  - { pageSize?: number }
 * @returns {Object} page, pageSize, totalCount, totalPages, resetPage
 */
export function usePagination(fetchFn, { pageSize = 10 } = {}) {
  const page = ref(1)
  const _pageSize = ref(pageSize)
  const totalCount = ref(0)
  const totalPages = computed(() => Math.ceil(totalCount.value / _pageSize.value) || 0)

  // 切换页码时自动请求，immediate 通过 nextTick 避免初始化循环引用
  watch(page, (newPage) => fetchFn(newPage))
  nextTick(() => fetchFn(page.value))

  /** 重置到第一页（如搜索/筛选条件变更时调用） */
  function resetPage() {
    page.value = 1
  }

  return { page, pageSize: _pageSize, totalCount, totalPages, resetPage }
}
