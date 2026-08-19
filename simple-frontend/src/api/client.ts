import type { ApiFailure, ApiSuccess, SessionScope, TokenPair } from '@/types/api'
import { clearTokenPair, readTokenPair, writeTokenPair } from '@/utils/session-vault'

const API_BASE = (import.meta.env.VITE_API_BASE_URL ?? '').replace(/\/$/, '')

export class ApiError extends Error {
  constructor(
    message: string,
    public readonly status: number,
    public readonly code: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

interface RequestOptions extends Omit<RequestInit, 'body'> {
  scope?: SessionScope | 'public'
  body?: unknown
  retryAuth?: boolean
}

const refreshLocks: Partial<Record<SessionScope, Promise<boolean>>> = {}

async function parseResponse<T>(response: Response): Promise<{ data: T; meta?: ApiSuccess<T>['meta'] }> {
  if (response.status === 204) return { data: undefined as T }
  let payload: ApiSuccess<T> | ApiFailure | null = null
  try {
    payload = (await response.json()) as ApiSuccess<T> | ApiFailure
  } catch {
    throw new ApiError('服务器返回了无法解析的响应', response.status, 'INVALID_RESPONSE')
  }
  if (!response.ok || !payload.success) {
    const failure = payload as ApiFailure
    throw new ApiError(failure.error?.message ?? '请求失败，请稍后再试', response.status, failure.error?.code ?? 'UNKNOWN_ERROR')
  }
  return { data: payload.data, meta: payload.meta }
}

async function refresh(scope: SessionScope): Promise<boolean> {
  if (refreshLocks[scope]) return refreshLocks[scope]
  refreshLocks[scope] = (async () => {
    const pair = readTokenPair(scope)
    if (!pair?.refreshToken) return false
    const path = scope === 'user' ? '/api/v1/public/auth/refresh' : '/api/v1/public/manager/refresh'
    try {
      const response = await fetch(`${API_BASE}${path}`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${pair.refreshToken}` },
      })
      const result = await parseResponse<TokenPair>(response)
      writeTokenPair(scope, result.data)
      return true
    } catch {
      clearTokenPair(scope)
      return false
    }
  })().finally(() => {
    delete refreshLocks[scope]
  })
  return refreshLocks[scope]
}

export async function apiRequest<T>(path: string, options: RequestOptions = {}): Promise<{ data: T; meta?: ApiSuccess<T>['meta'] }> {
  const { scope = 'public', body, retryAuth = true, headers: customHeaders, ...init } = options
  const headers = new Headers(customHeaders)
  if (body !== undefined) headers.set('Content-Type', 'application/json')
  if (scope !== 'public') {
    const token = readTokenPair(scope)?.accessToken
    if (token) headers.set('Authorization', `Bearer ${token}`)
  }

  let response: Response
  try {
    response = await fetch(`${API_BASE}${path}`, {
      ...init,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch {
    throw new ApiError('无法连接后端服务，请检查服务是否启动', 0, 'NETWORK_ERROR')
  }

  if (response.status === 401 && scope !== 'public' && retryAuth && (await refresh(scope))) {
    return apiRequest<T>(path, { ...options, retryAuth: false })
  }

  if (response.status === 401 && scope !== 'public') clearTokenPair(scope)
  return parseResponse<T>(response)
}

export function getApiError(error: unknown): ApiError {
  return error instanceof ApiError ? error : new ApiError('发生未知错误', 0, 'UNKNOWN_ERROR')
}
