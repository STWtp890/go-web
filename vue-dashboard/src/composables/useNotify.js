import { ref } from 'vue'
import { floatAlert } from '@/composables/useFloatAlert'
import { escapeHtml } from '@/utils/format'

const confirms = ref([])  // FixedAlert 卡片式确认（交互式）
let _id = 0

/**
 * 通知系统：
 *   notify.error / success / warning / info → FloatAlert（右下角，自动消失）
 *   notify.confirm / confirmExternalLink      → FixedAlert（居中卡片，交互按钮）
 */
export const notify = {
  // ── 常规通知（委托给 floatAlert）──
  error(msg)   { floatAlert.error(msg) },
  success(msg) { floatAlert.success(msg) },
  warning(msg) { floatAlert.warning(msg) },
  info(msg)    { floatAlert.info(msg) },

  // ── 交互式确认（FixedAlert 卡片）──
  confirm({ msg, title = '提示', type = 'warning', confirmText = '确定', cancelText = '取消', confirmClass = 'btn-primary' }) {
    return new Promise((resolve) => {
      const id = ++_id
      const actions = [
        {
          text: confirmText,
          class: confirmClass,
          onClick: () => resolve(true)
        },
        {
          text: cancelText,
          class: 'btn-outline-secondary',
          onClick: () => resolve(false)
        }
      ]
      confirms.value.push({ id, title, msg, type, actions })
    })
  },

  /** 外部链接确认快捷方法 */
  confirmExternalLink(href) {
    return this.confirm({
      title: '外部链接提醒',
      msg: `<code style="word-break:break-all;font-size:.85em">${escapeHtml(href)}</code><br><span style="opacity:.7;font-size:.85em">该链接非本站内容，请注意安全</span>`,
      confirmText: '在新标签页打开',
      cancelText: '返回文章'
    })
  },

  /** 删除确认快捷方法 */
  confirmDelete(msg) {
    return this.confirm({
      title: '确认删除',
      msg,
      type: 'danger',
      confirmText: '确认删除',
      confirmClass: 'btn-outline-danger'
    })
  }
}

function remove(id) {
  confirms.value = confirms.value.filter(m => m.id !== id)
}

/** 清理所有待处理确认（组件卸载时调用），全部 resolve(false) */
function dismissAll() {
  confirms.value.forEach(m => {
    const cancel = m.actions?.[1]
    if (cancel?.onClick) cancel.onClick()
  })
  confirms.value = []
}

export function useNotify() {
  return { confirms, remove, dismissAll }
}
