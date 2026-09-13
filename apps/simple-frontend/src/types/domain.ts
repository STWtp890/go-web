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

export interface DocumentDetail extends Omit<DocumentSummary, 'id' | 'ownerId'> {
  content: string
}

export interface CreateDocumentInput {
  title: string
  content: string
  visibility: Visibility
}

export type UpdateDocumentInput = CreateDocumentInput

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
  status: 'sending' | 'accepted' | 'unknown' | 'failed' | 'received'
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
