import { ref } from 'vue'

/** 文章列表刷新触发器 — 审核完成后文章中心自动刷新 */
const tick = ref(0)

export function useArticleRefresh() {
  return {
    tick,
    trigger() { tick.value++ }
  }
}
