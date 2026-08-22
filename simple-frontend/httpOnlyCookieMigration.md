# HttpOnly Cookie 前端迁移指南

> 状态：前后端已切换为 Cookie-only JWT 会话。本文记录当前契约、实现要点与联调验收项。

## 目标与边界

迁移完成后，浏览器不再保存或读取 access / refresh JWT。后端将 JWT 放入 HttpOnly Cookie，浏览器会在同源请求、WebSocket 协议升级和 EventSource 请求中自动附带 Cookie。

这解决了原生 `WebSocket` 与 `EventSource` 不能设置 `Authorization` 请求头的问题，同时避免 JWT 暴露给页面 JavaScript。

后端不返回 `accessToken`、`refreshToken`，也不接受 `Authorization: Bearer ...`。前端只能依赖浏览器自动发送的 HttpOnly Cookie。

## 后端当前契约

### Cookie 名称与作用域

| 会话 | Cookie | 属性 / 路径 | 前端是否可读 |
|---|---|---|---|
| 用户 access | `pp_user_at` | `HttpOnly`，`Path=/api/v1/protected` | 否 |
| 用户 refresh | `pp_user_rt` | `HttpOnly`，`Path=/api/v1/public/auth/refresh` | 否 |
| 用户 CSRF | `pp_user_csrf` | `Path=/`，非 HttpOnly | 是 |
| 管理员 access | `pp_manager_at` | `HttpOnly`，`Path=/api/v1/protected/manager` | 否 |
| 管理员 refresh | `pp_manager_rt` | `HttpOnly`，`Path=/api/v1/public/manager/refresh` | 否 |
| 管理员 CSRF | `pp_manager_csrf` | `Path=/`，非 HttpOnly | 是 |

开发环境的 Cookie 配置为 `Secure=false; SameSite=Lax`，用于 `http://localhost` 联调。生产环境必须使用 HTTPS、`Secure=true`，并替换后端的 `custom.cookie.csrf_secret`。

### 认证规则

1. 用户保护接口读取 `pp_user_at`，管理员保护接口读取 `pp_manager_at`。
2. JWT 必须通过 RS256 验签，且其 `sid` 必须是 Redis 中的当前有效会话。
3. 登录、刷新会覆盖对应 Cookie；登出会撤销 `sid` 并清理 Cookie。

### CSRF 规则

当请求实际由 access Cookie 鉴权时，所有 `POST`、`PUT`、`PATCH`、`DELETE` 都必须带：

```http
X-CSRF-Token: <与 pp_user_csrf 或 pp_manager_csrf 完全相同的值>
```

该值是后端签名并绑定当前 `sid` 的双提交 Token。缺失或不匹配返回 `403` 与 `CSRF 校验失败`。`GET`、`HEAD`、`OPTIONS`，以及 WebSocket/SSE 建连不需要该头。

Cookie 方式的 refresh 请求同样必须带 CSRF 头。

## 前端改造清单

### 1. Token Vault 已移除

以下基于 `localStorage` 的 JWT 逻辑已移除：

- `src/utils/session-vault.ts`
- `src/stores/session.ts` 中的 `tokenPair`、JWT 解码和过期时间判断
- `src/api/client.ts` 中自动写入 Authorization、读取 refreshToken 和 `writeTokenPair`
- 路由守卫中对 `readTokenPair()` 的依赖

不要尝试读取 `pp_user_at`、`pp_user_rt`、`pp_manager_at` 或 `pp_manager_rt`：它们是 HttpOnly，JavaScript 本就无法读取。

### 2. 统一 HTTP 请求

所有请求都应携带 Cookie：

```ts
await fetch(`${API_BASE}${path}`, {
  method: 'POST',
  credentials: 'include',
  headers: {
    'Content-Type': 'application/json',
    'X-CSRF-Token': readCSRFCookie('pp_user_csrf'),
  },
  body: JSON.stringify(payload),
})
```

若 Vite 通过 `/api` 代理后端，`credentials: 'include'` 仍应显式保留，避免将来改为直连 API 时出现行为差异。

仅安全方法不需要 `X-CSRF-Token`：

```ts
await fetch(`${API_BASE}/api/v1/protected/chat/groups/mine`, {
  credentials: 'include',
})
```

建议在 `apiRequest` 中自动完成：

| 条件 | 请求行为 |
|---|---|
| 所有请求 | `credentials: 'include'` |
| 含 JSON body | 设置 `Content-Type: application/json` |
| `POST/PUT/PATCH/DELETE` 且 scope 为 `user` | 写入 `pp_user_csrf` 到 `X-CSRF-Token` |
| `POST/PUT/PATCH/DELETE` 且 scope 为 `manager` | 写入 `pp_manager_csrf` 到 `X-CSRF-Token` |
| 所有请求 | 不再写入 Authorization |

Cookie 读取函数可保持最小：

```ts
export function readCSRFCookie(name: string): string {
  const prefix = `${encodeURIComponent(name)}=`
  const item = document.cookie.split('; ').find((part) => part.startsWith(prefix))
  return item ? decodeURIComponent(item.slice(prefix.length)) : ''
}
```

若 CSRF Cookie 缺失，不要带空头重试；应将会话视为失效或先完成登录/刷新。

### 3. 改造登录、刷新、登出与会话状态

#### 登录

用户登录：`POST /api/v1/public/auth/login`；管理员登录：`POST /api/v1/public/manager/login`。

登录成功后 Cookie 已由响应 `Set-Cookie` 写入。前端不再读取响应中的 token，也不保存 token；仅保存非敏感的展示状态，例如已登录标记、昵称或角色。

#### 刷新

用户刷新：`POST /api/v1/public/auth/refresh`；管理员刷新：`POST /api/v1/public/manager/refresh`。

refresh Cookie 会按路径自动发送，前端只需携带 `credentials: 'include'` 与对应的 CSRF 头。保留现有“按 scope 单飞”的刷新锁，移除 token 参数和存储：

```ts
await fetch(`${API_BASE}/api/v1/public/auth/refresh`, {
  method: 'POST',
  credentials: 'include',
  headers: { 'X-CSRF-Token': readCSRFCookie('pp_user_csrf') },
})
```

保护接口返回 `401` 时：单飞刷新一次，再重放原请求；刷新失败则清除前端展示会话并跳转登录页。

#### 登出

调用受保护的 logout 接口时必须添加 CSRF 头。成功或返回 `401` 后，都应清空前端内存态，并通过 `BroadcastChannel` 通知其他标签页；不需要也不能主动删除 HttpOnly Cookie，后端会处理。

#### 初始化会话

HttpOnly Cookie 无法让前端本地判断“是否登录”。推荐后端后续提供：

```text
GET /api/v1/protected/auth/session
GET /api/v1/protected/manager/session
```

页面启动和路由守卫请求对应 endpoint：成功则建立内存态，`401` 则视为未登录。该 endpoint 尚未在当前后端实现；在它加入前，可用一个已有的轻量受保护 GET 作为过渡探测，但不要以解析 Cookie 或 JWT 代替。

### 4. 启用原生 WebSocket

用户登录成功后可以直接建立连接，不传 query token，也不能手动设置 Authorization：

```ts
function chatWebSocketURL(): string {
  const base = import.meta.env.VITE_API_BASE_URL || window.location.origin
  const url = new URL('/api/v1/protected/chat/ws', base)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  return url.toString()
}

const socket = new WebSocket(chatWebSocketURL())
socket.onmessage = ({ data }) => {
  // data 是后端 chat wire JSON；校验并渲染后再确认投递
}
socket.onclose = (event) => {
  // 1008 / 握手失败或随后 HTTP 401：刷新会话后采用退避策略重连
}
```

浏览器会自动携带路径匹配的 `pp_user_at`。后端会校验 Cookie 中 JWT、当前 `sid` 和请求 `Origin`，成功才会执行协议升级。

不要：

- 将 access token 拼到 URL query；
- 在 `Sec-WebSocket-Protocol` 中传 JWT；
- 用前端可读 token 作为 WebSocket 鉴权兜底。

### 5. 启用原生 SSE

SSE 以 HTTP POST 发消息、EventSource 收消息：

```ts
const stream = new EventSource(`${API_BASE}/api/v1/protected/chat/sse`, {
  withCredentials: true,
})

stream.addEventListener('text', async (event) => {
  const message = JSON.parse((event as MessageEvent<string>).data)
  // 先落入 UI 状态，再确认该投递
  await apiRequest(`/api/v1/protected/chat/deliveries/${encodeURIComponent(message.metadata.deliveryId)}/ack`, {
    method: 'POST',
    scope: 'user',
  })
})
```

`EventSource` 会自动重连并携带 Cookie。当前服务端接受 `Last-Event-ID`，但后端持久化补发仍未实现，因此前端不可将其视为可靠离线补偿。

### 6. 聊天页当前实现

`src/views/app/ChatView.vue` 以“会话列表 + 对话线程”组织私聊和群聊，并通过原生 WebSocket 建立实时收件连接。发送仍使用 `POST /chat/messages`：这个接口返回每位接收者的 `deliveryId`，可保留服务端现有的可靠投递语义；已连接的接收者会立即通过 WebSocket 收到该消息。

`src/composables/useChatSocket.ts` 专门管理连接生命周期。它不读取或传递 JWT，仅接收 URL、会话复核和消息回调这些显式依赖。

1. 页面挂载时建立 WS；
2. 收到 `text` 消息后先落入对应私聊/群聊线程，非当前线程增加未读数；
3. 写入 UI 状态后将 `deliveryId` 放入待确认队列，并调用 ACK；失败时保留该 ID，连接恢复或浏览器重新联网后幂等重试；
4. 自己发送的群消息会经 WS 回显，前端按目标、内容和短时间窗口去重，避免同一条消息显示两次；
5. 组件卸载时关闭连接和退避计时器；
6. 连接中断时按指数退避重连，每次重连前都用受保护请求复核会话；复核失败则清理展示态并跳转登录页。

聊天页面仅把短期 UI 记录和待 ACK 的 delivery ID 放入 `sessionStorage`，不将其当作服务端历史，也不保存 access / refresh JWT 或其他认证凭据。

## 错误处理约定

| 场景 | 前端动作 |
|---|---|
| 保护 HTTP 接口 `401` | 单飞刷新一次，成功后重放；失败则退出当前 scope。 |
| Cookie 写接口 `403 / CSRF 校验失败` | 不重试原请求；确认 CSRF Cookie 是否存在，必要时重新登录。 |
| WS/SSE 首次连接失败 | 先执行刷新；刷新成功后退避重连，失败则退出。 |
| 另一端登录、登出或管理员撤销会话 | 后端 `sid` 校验会使会话失效；前端在下一次 HTTP 请求/重连时按 `401` 退出。 |
| `Origin` 不在白名单 | 修正 Vite/部署域名与后端 CORS 配置，不能前端绕过。 |

## 已完成的迁移步骤

1. `api/client.ts` 使用 `credentials: 'include'`、CSRF Header 与 Cookie refresh 单飞锁。
2. session store 仅保存内存态，并使用受保护接口探测会话。
3. 登录、刷新、登出、路由守卫与聊天 WebSocket 已改为 Cookie 会话。
4. 后端 Bearer 兼容与登录响应中的 Token 字段已删除。

仍需在目标部署环境执行跨标签登出、过期刷新、WS/SSE 断线重连和 CSRF 失败的端到端回归。

## 联调验收

- 登录响应包含对应三枚 Cookie；access、refresh Cookie 在 DevTools 中标记为 HttpOnly。
- `new WebSocket('/api/v1/protected/chat/ws')` 可升级成功，且前端没有 Authorization / query token。
- `new EventSource(..., { withCredentials: true })` 可建立流并收到消息。
- Cookie 方式的发消息、群加入/退出、投递确认、登出均带对应 CSRF Header 并成功。
- 删除 CSRF Header 的写请求返回 `403`。
- 登录新设备或登出后，旧会话的保护请求和重新建立的连接均失败。
- 页面源码、localStorage、sessionStorage 和网络 URL 中不再出现 access / refresh JWT。
