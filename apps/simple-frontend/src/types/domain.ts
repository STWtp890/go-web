export type Visibility = 'public' | 'private'

export interface DocumentSummary {
  id: number
  documentId: string
  ownerId: string
  title: string
  summary: string
  visibility: Visibility
  createdAt: number
  updatedAt: number
}

export interface DocumentDetail extends Omit<DocumentSummary, 'id'> {
  content: string
  /** 保存时的乐观并发令牌：详情读取返回的当前聚合修订号。 */
  revision?: number
}

export interface CreateDocumentInput {
  title: string
  content: string
  visibility: Visibility
  /** 幂等键：同一次保存的重试复用同一个键，后端不会追加第二个版本。 */
  requestId?: string
}

export interface UpdateDocumentInput extends CreateDocumentInput {
  /** 编辑器打开详情时的修订号；带上它，保存就是乐观并发更新而不是盲写。 */
  expectedRevision?: number
}

/**
 * 保存结果。
 *
 * replayed=true 表示这次请求此前已经生效过（幂等重放）：调用方必须按「已生效」
 * 处理，而不是当作刚刚写入。appliedVersionId 是这次请求对应的版本：重放时它是
 * 首次尝试写入的版本。
 */
export interface DocumentSaveResult extends DocumentDetail {
  replayed: boolean
  appliedVersionId: string
}



export interface RegistrationRequest {
  id: number
  username: string
  email: string
  reason: string
  status: 'pending' | 'approved' | 'rejected'
  reviewerId: number
  reviewComment: string
  reviewedAt: string | null
  createdAt: number
  updatedAt: number
}
