import { computed, ref } from 'vue'

/**
 * 游标分页的翻页历史。
 *
 * 后端只下发前向游标，因此“上一页”不能由服务端给出：客户端把每一页用过的游标
 * 记在栈里，后退就是回到栈中的上一个游标重新请求。这样前进/后退读到的是同一批
 * 数据，页与页之间不重复也不遗漏。
 *
 * 游标一律来自服务端响应，客户端只负责保存与回传，绝不根据页码推算。
 */
export function useCursorPager(initialPageSize = 9) {
  const pageSize = ref(initialPageSize)
  // 栈中第 N 项是第 N 页的入参游标；第一页没有游标。
  const history = ref<Array<string | undefined>>([undefined])
  const nextCursor = ref<string | undefined>(undefined)
  const total = ref(0)

  const page = computed(() => history.value.length)
  const currentCursor = computed(() => history.value[history.value.length - 1])
  const totalPages = computed(() => (total.value > 0 ? Math.ceil(total.value / pageSize.value) : 1))
  const hasNext = computed(() => Boolean(nextCursor.value))
  const hasPrev = computed(() => history.value.length > 1)

  /** 记录当前页的响应：真实总数与下一个前向游标。 */
  function record(responseNextCursor: string | undefined, responseTotal: number): void {
    nextCursor.value = responseNextCursor || undefined
    total.value = Math.max(0, responseTotal)
  }

  /** 清空游标与历史。切换账号、切换列表类型、修改每页数量时都必须调用。 */
  function reset(): void {
    history.value = [undefined]
    nextCursor.value = undefined
    total.value = 0
  }

  function setPageSize(size: number): void {
    if (size <= 0 || size === pageSize.value) return
    pageSize.value = size
    reset()
  }

  /** 前进一页；返回要请求的游标，没有下一页时返回 null。 */
  function advance(): string | undefined | null {
    if (!nextCursor.value) return null
    history.value = [...history.value, nextCursor.value]
    return currentCursor.value
  }

  /** 后退一页；已在第一页时返回 null。 */
  function retreat(): string | undefined | null {
    if (history.value.length <= 1) return null
    history.value = history.value.slice(0, -1)
    return currentCursor.value
  }

  /**
   * 跳到指定页。只有相邻前进与任意后退能直接定位：更远的前向跳页需要先逐页取回
   * 中间游标，这里不做猜测，最多前进一步。
   */
  function goTo(target: number): { cursor?: string } | null {
    if (target <= 0 || target === page.value) return null
    if (target < page.value) {
      history.value = history.value.slice(0, target)
      return { cursor: currentCursor.value }
    }
    const cursor = advance()
    return cursor === null ? null : { cursor }
  }

  return {
    pageSize,
    page,
    totalPages,
    total,
    hasNext,
    hasPrev,
    currentCursor,
    nextCursor,
    record,
    reset,
    setPageSize,
    advance,
    retreat,
    goTo,
  }
}
