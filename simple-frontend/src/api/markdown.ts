import { apiRequest } from './client'
import type { ApiMeta, PageResult } from '@/types/api'
import type { CreateMarkdownInput, MarkdownDetail, MarkdownSummary, Visibility } from '@/types/domain'

interface RawMarkdownSummary {
  id: number
  markdown_id: string
  author_id: string
  title: string
  summary: string
  visibility: Visibility
  created_at: number
  updated_at: number
}

function normalize(item: RawMarkdownSummary): MarkdownSummary {
  return {
    id: item.id,
    markdownId: item.markdown_id,
    authorId: item.author_id,
    title: item.title,
    summary: item.summary,
    visibility: item.visibility,
    createdAt: item.created_at,
    updatedAt: item.updated_at,
  }
}

async function list(path: string): Promise<PageResult<MarkdownSummary>> {
  const result = await apiRequest<{ markdownList: RawMarkdownSummary[] }>(path, { scope: 'user' })
  return {
    items: result.data.markdownList.map(normalize),
    meta: result.meta as ApiMeta,
  }
}

export const markdownApi = {
  mine: (page = 1, pageSize = 9) => list(`/api/v1/protected/markdown/mine?page=${page}&pageSize=${pageSize}`),
  public: (page = 1, pageSize = 9) => list(`/api/v1/protected/markdown/public?page=${page}&pageSize=${pageSize}`),
  search: (keyword: string, page = 1, pageSize = 9) => list(`/api/v1/protected/markdown/search?keyword=${encodeURIComponent(keyword)}&page=${page}&pageSize=${pageSize}`),
  detail: async (markdownId: string) => (await apiRequest<MarkdownDetail>(`/api/v1/protected/markdown/${encodeURIComponent(markdownId)}`, { scope: 'user' })).data,
  create: async (input: CreateMarkdownInput) => (await apiRequest<Omit<MarkdownDetail, 'content' | 'id' | 'authorId'>>('/api/v1/protected/markdown/upload', { method: 'POST', scope: 'user', body: input })).data,
}
