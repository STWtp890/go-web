export interface ApiMeta {
  page: number
  per_page: number
  total: number
  total_pages: number
  /**
   * 游标分页的前向游标。后端在还有下一页时下发，客户端只能原样回传，不能自己
   * 构造：游标是来源服务自己的排序位置，不是页码换算出来的偏移。
   */
  nextCursor?: string
  /**
   * 检索结果未完整展示（本页之后仍有结果）。为 true 时必须明确告知用户，不能
   * 让用户以为看到的就是全部命中。
   */
  truncated?: boolean
}

export interface ApiSuccess<T> {
  success: true
  data: T
  meta?: ApiMeta
}

export interface ApiFailure {
  success: false
  error: { code: string; message: string }
}

export interface PageResult<T> {
  items: T[]
  meta: ApiMeta
}

export type SessionScope = 'user' | 'manager'
