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
  utils/        # token vault、JWT 展示信息和日期格式化
  views/        # 路由页面
  styles/       # 设计令牌、组件和响应式样式
```

页面只消费归一化后的领域对象。Markdown 列表的蛇形字段、详情/创建的驼峰字段，以及管理员列表字段差异，都在 `api/*` adapter 内消化。

## 4. 请求与会话策略

- 普通用户和管理员分别使用 `user`、`manager` scope，令牌不会跨业务发送。
- 每个 scope 都有独立的 refresh 单飞锁；并发 401 只产生一次 refresh。
- refresh 成功后原子覆盖 access/refresh token pair，并只重放原请求一次。
- refresh 失败或重放仍为 401 时清除对应会话；403 不会触发刷新。
- 登出请求无论成功或失败，最终都会清理本地会话。
- 使用 `BroadcastChannel` 和 `storage` 事件同步跨标签页登录态。
- JWT payload 仅用于显示 subject 和过期时间，绝不用于前端授权判断。

当前令牌存储在 `localStorage`，是基于现有 Bearer token 接口的工程折中。生产环境更推荐后端改为 Secure、HttpOnly、SameSite cookie，并配套 CSRF 防护。

## 5. 接口覆盖矩阵

| 模块 | 已覆盖接口/能力 |
| --- | --- |
| Health | `/healthz`、`/readyz` |
| Auth | 注册、登录、refresh、登出 |
| Markdown | 创建、我的列表、公开列表、详情、搜索、分页、字段归一化 |
| Chat | HTTP 私信/群消息、我的群、加入、退出、成员列表；ACK 客户端方法已预留 |
| Manager | 申请、登录、refresh、登出、按状态分页、通过、拒绝、409 后刷新 |

### Chat 的明确边界

当前后端要求 WebSocket/SSE 握手携带 `Authorization`，浏览器原生 `WebSocket` 和 `EventSource` 无法设置该请求头。前端因此：

- 不把 access token 放入 URL；
- 不创建不可工作的伪实时连接；
- 明确标注当前仅 HTTP 投递可用；
- 发送记录仅保存于本次 `sessionStorage`，不冒充服务端历史；
- 保留 delivery ACK API，待后端提供浏览器可用的短期连接凭证或 HttpOnly cookie 后接入实时收件。

## 6. 状态、错误和空态

- HTTP 状态码驱动流程，`error.message` 只负责展示。
- 400 就地展示；401 进入刷新流程；403 保留登录态；404 展示资源不可用；409 刷新审批列表；503 提供重试。
- 所有远程列表都有加载骨架、空态、错误态和分页。
- Markdown 编辑器把未发布内容存为本地草稿，发布成功后清理。
- Markdown 使用 `marked` 解析，并在 `v-html` 前通过 DOMPurify 清洗。
- 时间戳统一按 Unix 秒乘以 1000 后格式化。

## 7. 后续演进建议

1. 后端增加一次性、短时有效的 chat connection ticket，或切换为 HttpOnly cookie，随后实现 WS/SSE 接收、ACK 重试队列和断线恢复。
2. 增加用户 profile/me 接口，替换当前从 JWT `sub` 推导的简化身份展示。
3. Markdown 后端增加更新、删除和草稿接口，再扩展编辑器为完整生命周期。
4. 引入 Vitest、MSW 与 Playwright，覆盖 refresh 轮换、路由权限、字段 adapter 和核心用户流。
5. 若内容公开给搜索引擎，再评估 Nuxt SSR/SSG；当前登录后工作台采用 SPA 更直接。
