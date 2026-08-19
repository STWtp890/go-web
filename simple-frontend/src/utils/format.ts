export function formatDate(timestamp: number): string {
  if (!timestamp) return '—'
  return new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: 'long', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(new Date(timestamp * 1000))
}

export function formatRelativeDate(timestamp: number): string {
  if (!timestamp) return '刚刚'
  const seconds = Math.round(timestamp - Date.now() / 1000)
  const formatter = new Intl.RelativeTimeFormat('zh-CN', { numeric: 'auto' })
  const units: Array<[Intl.RelativeTimeFormatUnit, number]> = [
    ['year', 31_536_000], ['month', 2_592_000], ['week', 604_800], ['day', 86_400], ['hour', 3_600], ['minute', 60],
  ]
  for (const [unit, value] of units) {
    if (Math.abs(seconds) >= value) return formatter.format(Math.round(seconds / value), unit)
  }
  return '刚刚'
}

export function getInitials(value: string): string {
  const clean = value.trim()
  return clean ? clean.slice(0, 2).toUpperCase() : 'PP'
}
