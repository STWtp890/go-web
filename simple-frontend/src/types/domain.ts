export type Visibility = 'public' | 'private'

export interface MarkdownSummary {
  id: number
  markdownId: string
  authorId: string
  title: string
  summary: string
  visibility: Visibility
  createdAt: number
  updatedAt: number
}

export interface MarkdownDetail extends Omit<MarkdownSummary, 'id' | 'authorId'> {
  content: string
}

export interface CreateMarkdownInput {
  title: string
  content: string
  visibility: Visibility
}

export interface ChatMessageInput {
  metadata: { clientMessageId: string; type: 'text'; groupType: 'private' | 'group'; to: string }
  content: string
}

export interface SentMessage {
  localId: string
  messageId?: number
  deliveryId?: string
  direction: 'sent' | 'received'
  from?: string
  to: string
  groupType: 'private' | 'group'
  content: string
  createdAt: number
  status: 'sending' | 'accepted' | 'failed' | 'received'
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
