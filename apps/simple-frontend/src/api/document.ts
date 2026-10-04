import { apiRequest } from './client'
import type { ApiMeta, PageResult } from '@/types/api'
import type { CreateDocumentInput, DocumentDetail, DocumentSaveResult, DocumentSummary, UpdateDocumentInput } from '@/types/domain'

/**
 * 游标分页的列表参数。
 *
 * cursor 必须来自上一次响应的 meta.nextCursor，客户端不构造游标：游标是来源服务
 * 自己排序中的位置，只有来源服务能给出。page 只用于展示当前是第几页。
 */
interface CursorListOptions {
  pageSize?: number
  cursor?: string
  page?: number
  signal?: AbortSignal
}

/** 检索仍是页码分页：响应带真实总数，页码算术才有意义。 */
interface SearchOptions {
  page?: number
  pageSize?: number
  signal?: AbortSignal
}

async function list(path: string, signal?: AbortSignal): Promise<PageResult<DocumentSummary>> {
  const result = await apiRequest<{ documentList: DocumentSummary[] }>(path, { scope: 'user', signal })
  return {
    items: result.data.documentList,
    meta: result.meta as ApiMeta,
  }
}

function cursorQuery({ pageSize = 9, cursor, page }: CursorListOptions): string {
  const params = new URLSearchParams({ pageSize: String(pageSize) })
  if (cursor) params.set('cursor', cursor)
  if (page && page > 1) params.set('page', String(page))
  return params.toString()
}

export const documentApi = {
  mine: (options: CursorListOptions = {}) =>
    list(`/api/v1/protected/documents/mine?${cursorQuery(options)}`, options.signal),
  public: (options: CursorListOptions = {}) =>
    list(`/api/v1/protected/documents/public?${cursorQuery(options)}`, options.signal),
  search: (keyword: string, { page = 1, pageSize = 9, signal }: SearchOptions = {}) =>
    list(`/api/v1/protected/documents/search?keyword=${encodeURIComponent(keyword)}&page=${page}&pageSize=${pageSize}`, signal),
  detail: async (documentId: string) =>
    (await apiRequest<DocumentDetail>(`/api/v1/protected/documents/${encodeURIComponent(documentId)}`, { scope: 'user' })).data,
  create: async (input: CreateDocumentInput) =>
    (await apiRequest<Omit<DocumentDetail, 'content' | 'ownerId'>>('/api/v1/protected/documents', { method: 'POST', scope: 'user', body: input })).data,
  update: async (documentId: string, input: UpdateDocumentInput) =>
    (await apiRequest<DocumentSaveResult>(`/api/v1/protected/documents/${encodeURIComponent(documentId)}`, { method: 'PUT', scope: 'user', body: input })).data,
  delete: async (documentId: string) =>
    (await apiRequest<{ documentId: string }>(`/api/v1/protected/documents/${encodeURIComponent(documentId)}`, { method: 'DELETE', scope: 'user' })).data,
}
