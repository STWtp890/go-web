import { apiRequest } from './client'
import type { ApiMeta, PageResult } from '@/types/api'
import type { CreateDocumentInput, DocumentDetail, DocumentSummary, UpdateDocumentInput } from '@/types/domain'

interface DocumentPageOptions {
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

export const documentApi = {
  mine: ({ page = 1, pageSize = 9, signal }: DocumentPageOptions = {}) =>
    list(`/api/v1/protected/documents/mine?page=${page}&pageSize=${pageSize}`, signal),
  public: ({ page = 1, pageSize = 9, signal }: DocumentPageOptions = {}) =>
    list(`/api/v1/protected/documents/public?page=${page}&pageSize=${pageSize}`, signal),
  search: (keyword: string, { page = 1, pageSize = 9, signal }: DocumentPageOptions = {}) =>
    list(`/api/v1/protected/documents/search?keyword=${encodeURIComponent(keyword)}&page=${page}&pageSize=${pageSize}`, signal),
  detail: async (documentId: string) =>
    (await apiRequest<DocumentDetail>(`/api/v1/protected/documents/${encodeURIComponent(documentId)}`, { scope: 'user' })).data,
  create: async (input: CreateDocumentInput) =>
    (await apiRequest<Omit<DocumentDetail, 'content' | 'ownerId'>>('/api/v1/protected/documents', { method: 'POST', scope: 'user', body: input })).data,
  update: async (documentId: string, input: UpdateDocumentInput) =>
    (await apiRequest<DocumentDetail>(`/api/v1/protected/documents/${encodeURIComponent(documentId)}`, { method: 'PUT', scope: 'user', body: input })).data,
  delete: async (documentId: string) =>
    (await apiRequest<{ documentId: string }>(`/api/v1/protected/documents/${encodeURIComponent(documentId)}`, { method: 'DELETE', scope: 'user' })).data,
}