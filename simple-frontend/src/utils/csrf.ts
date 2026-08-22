import type { SessionScope } from '@/types/api'

const csrfCookieByScope: Record<SessionScope, string> = {
  user: 'pp_user_csrf',
  manager: 'pp_manager_csrf',
}

export function readCSRFCookie(name: string): string {
  if (typeof document === 'undefined') return ''
  const prefix = `${encodeURIComponent(name)}=`
  const item = document.cookie.split('; ').find((part) => part.startsWith(prefix))
  return item ? decodeURIComponent(item.slice(prefix.length)) : ''
}

export function readCSRFToken(scope: SessionScope): string {
  return readCSRFCookie(csrfCookieByScope[scope])
}
