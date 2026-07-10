import { ref } from 'vue'

const alerts = ref([])
let _id = 0

/**
 * 右下角悬浮警告（多条堆叠，旧在上新在下）
 * 用法：floatAlert.warning('登录失败') / floatAlert.error('出错了')
 */
export const floatAlert = {
  show(msg, opts = {}) {
    const id = ++_id
    alerts.value = [...alerts.value, { id, msg, type: opts.type || 'warning' }]
    const duration = opts.duration ?? 5000
    if (duration > 0) {
      setTimeout(() => { alerts.value = alerts.value.filter(a => a.id !== id) }, duration)
    }
  },
  warning(msg) { this.show(msg, { type: 'warning' }) },
  error(msg)   { this.show(msg, { type: 'error' }) },
  success(msg) { this.show(msg, { type: 'success' }) },
  info(msg)    { this.show(msg, { type: 'info' }) },
  dismiss(id) { alerts.value = alerts.value.filter(a => a.id !== id) }
}

export function useFloatAlert() {
  return { alerts, dismiss: floatAlert.dismiss }
}
