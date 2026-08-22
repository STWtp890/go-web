# Paperplane 完整前端方案

## 1. 产品定位

前端以“安静的 Markdown 创作空间”为核心，将当前后端能力组织成三条用户路径：

1. 普通用户完成注册/登录后，创作、浏览、搜索和阅读 Markdown 文稿。
2. 普通用户通过 HTTP 投递私信或群消息，并管理已加入群组。
3. 管理员申请权限；已有管理员使用独立会话审批申请。

视觉采用暖灰纸张、陶土强调色与低对比边界，桌面端突出编辑和阅读空间，移动端使用抽屉导航和编辑/预览切换。

## 2. 页面与路由

| 路由 | 页面 | 鉴权 |
| --- | --- | --- |
| `/` | 产品首页、服务就绪状态 | 游客 |
| `/login`、`/register` | 普通用户登录/注册 | 游客 |
| `/app` | 工作台概览 | 普通用户 |
| `/app/mine` | 我的公开和私密文稿 | 普通用户 |
| `/app/explore` | 公开文稿广场 | 普通用户 |
| `/app/search` | 我的文稿全文搜索 | 普通用户 |
| `/app/new` | Markdown 编辑、预览与本地草稿 | 普通用户 |
| `/app/markdown/:markdownId` | 文稿详情 | 普通用户 |
| `/app/chat` | HTTP 发信、发送记录和群组管理 | 普通用户 |
| `/manager/apply` | 管理员权限申请 | 游客 |
| `/manager/login` | 管理员登录 | 管理员游客 |
| `/manager/requests` | 申请列表与审批 | 管理员 |

路由组件使用动态 `import()` 做页面级拆包；守卫只判断对应会话是否存在，最终权限始终由后端决定。

## 3. 工程分层

```text
src/
  api/          # 请求内核、认证刷新、各业务 API adapter
  components/   # 无业务归属的展示组件
  layouts/      # 普通用户与管理端独立布局
  router/       # 路由表和鉴权守卫
  stores/       # 会话与全局提示
  types/        # API 包装和归一化领域模型
  utils/        # CSRF、跨标签会话事件和日期格式化
  views/        # 路由页面
  styles/       # 设计令牌、组件和响应式样式
```

页面只消费归一化后的领域对象。Markdown 列表的蛇形字段、详情/创建的驼峰字段，以及管理员列表字段差异，都在 `api/*` adapter 内消化。

## 4. 请求与会话策略

- 普通用户和管理员分别使用 `user`、`manager` scope，Cookie 不会跨业务发送。
- 每个 scope 都有独立的 refresh 单飞锁；并发 401 只产生一次 refresh。
- refresh 成功后由服务端原子轮换 Cookie 会话，并只重放原请求一次。
- refresh 失败或重放仍为 401 时清除对应会话；403 不会触发刷新。
- 登出请求无论成功或失败，最终都会清理本地会话。
- 使用 `BroadcastChannel` 和 `storage` 事件同步跨标签页登录态。
- 前端不读取 JWT；路由守卫通过受保护接口探测会话，后端是唯一的授权事实来源。

当前使用 HttpOnly、SameSite Cookie 并配套 CSRF 防护；生产部署必须启用 `Secure`，页面不存储 access / refresh JWT。

## 5. 接口覆盖矩阵

| 模块 | 已覆盖接口/能力 |
| --- | --- |
| Health | `/healthz`、`/readyz` |
| Auth | 注册、登录、refresh、登出 |
| Markdown | 创建、我的列表、公开列表、详情、搜索、分页、字段归一化 |
| Chat | Cookie WebSocket 实时收件、会话列表与未读数、HTTP 可靠发信、ACK 失败重试、我的群、加入、退出、成员列表 |
| Manager | 申请、登录、refresh、登出、按状态分页、通过、拒绝、409 后刷新 |

### Chat 的明确边界

当前后端通过 HttpOnly Cookie 鉴权 WebSocket/SSE 握手。前端因此：

- 不把 access token 放入 URL；
- 使用原生 WebSocket 建立实时收件，并在会话有效时退避重连；
- 发信继续调用可返回 delivery ID 的 HTTP 接口，确保接收端可确认投递；
- 收到消息后先显示，再通过 CSRF 保护的 ACK 请求确认；失败的 ACK 会保留到下一次连接恢复；
- 浏览器会话缓存仅保存短期消息 UI 状态与待确认 delivery ID，绝不保存 JWT，也不作为服务端历史。

## 6. 状态、错误和空态

- HTTP 状态码驱动流程，`error.message` 只负责展示。
- 400 就地展示；401 进入刷新流程；403 保留登录态；404 展示资源不可用；409 刷新审批列表；503 提供重试。
- 所有远程列表都有加载骨架、空态、错误态和分页。
- Markdown 编辑器把未发布内容存为本地草稿，发布成功后清理。
- Markdown 使用 `marked` 解析，并在 `v-html` 前通过 DOMPurify 清洗。
- 时间戳统一按 Unix 秒乘以 1000 后格式化。

## 7. 后续演进建议

1. 为聊天补充服务端历史、离线补偿与更完整的 ACK 重试队列。
2. 增加用户 profile/me 接口，替换当前从 JWT `sub` 推导的简化身份展示。
3. Markdown 后端增加更新、删除和草稿接口，再扩展编辑器为完整生命周期。
4. 引入 Vitest、MSW 与 Playwright，覆盖 refresh 轮换、路由权限、字段 adapter 和核心用户流。
5. 若内容公开给搜索引擎，再评估 Nuxt SSR/SSG；当前登录后工作台采用 SPA 更直接。
