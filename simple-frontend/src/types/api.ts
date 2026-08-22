export interface ApiMeta {
  page: number
  per_page: number
  total: number
  total_pages: number
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
