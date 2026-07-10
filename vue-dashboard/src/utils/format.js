/**
 * 格式化时间戳为中文日期字符串
 * @param {string|number|Date} ts - 时间戳
 * @returns {string} 格式化后的日期，无效时返回 '-'
 */
export function fmtDate(ts) {
  if (!ts) return '-'
  return new Date(ts).toLocaleString('zh-CN')
}

/**
 * 截断文本（去除 Markdown/HTML 标记后截取）
 * @param {string} content - 原始文本
 * @param {number} maxLen   - 最大长度
 * @returns {string} 截断后的纯文本
 */
export function truncateText(content, maxLen = 60) {
  if (!content) return ''
  const plain = content
    .replace(/<[^>]+>/g, '')
    .replace(/[#*`>\[\]()!\-\n\r]/g, '')
  return plain.length > maxLen ? plain.slice(0, maxLen) + '...' : plain
}

/**
 * 去除 Markdown 语法标记，返回纯文本
 * @param {string} text - Markdown 文本
 * @returns {string} 纯文本
 */
export function stripMd(text) {
  if (!text) return ''
  return text
    .replace(/!\[.*?\]\(.*?\)/g, '')           // 图片
    .replace(/\[([^\]]+)\]\(.*?\)/g, '$1')     // 链接
    .replace(/^#{1,6}\s+/gm, '')               // 标题
    .replace(/(\*{1,3}|_{1,3})(.+?)\1/g, '$2') // 粗体/斜体
    .replace(/`{1,3}[^`]+`{1,3}/g, '')         // 行内代码
    .replace(/~~(.+?)~~/g, '$1')               // 删除线
    .replace(/^[>]+\s+/gm, '')                 // 引用
    .replace(/^[\s]*[-*+]\s+/gm, '')           // 无序列表
    .replace(/^[\s]*\d+\.\s+/gm, '')           // 有序列表
    .replace(/^[-*_]{3,}\s*$/gm, '')           // 水平线
    .replace(/\s+/g, ' ')                      // 合并空白
    .trim()
}

/**
 * HTML 实体转义，防止 XSS
 * @param {string} str - 原始字符串
 * @returns {string} 转义后的安全字符串
 */
export function escapeHtml(str) {
  if (!str) return ''
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}
