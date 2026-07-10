/**
 * 后端错误 → 用户友好中文提示
 * 用法：userMsg(e, '删除') → '删除失败，权限不足'
 */
export function userMsg(e, action = '操作') {
  const raw = e?.response?.data?.message || e?.message || ''
  const code = e?.response?.data?.code || e?.code || 0

  // 根据 HTTP/Biz 错误码优先匹配
  if (code === 401 || /unauthorized|token|登录|expired/i.test(raw)) {
    return '登录已过期，请重新登录'
  }
  if (code === 403 || /forbidden|permission|role|not the author|权限/i.test(raw)) {
    return `${action}失败，权限不足`
  }
  if (code === 404 || /not found|不存在/i.test(raw)) {
    return `${action}失败，资源不存在`
  }
  if (raw) {
    // 去掉后端前缀 "forbidden: " / "unauthorized: " 等
    const clean = raw.replace(/^[a-z]+:\s*/i, '')
    if (clean.length < 40) return `${action}失败，${clean}`
  }
  return `${action}失败，请稍后重试`
}
