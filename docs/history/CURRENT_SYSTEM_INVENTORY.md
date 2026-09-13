# 当前系统清单

> 状态：阶段 0 冻结基线（含 P1.3 现状增量），2026-09-11
> 用途：保留阶段 1 启动时的所有者、运行边界与验证入口；当前排期见 [`CURRENT_IMPLEMENTATION_PLAN.md`](../planning/CURRENT_IMPLEMENTATION_PLAN.md)。

## 1. 应用与模块

| 对象 | 当前职责 | 唯一所有者 | 运行状态 |
|---|---|---|---|
| apps/simple-frontend | Vue Web UI、认证与 Markdown 交互 | Web 前端 | 根 Compose 接入 |
| apps/gin-backend | HTTP API、身份、管理员、Markdown 事实数据 | go-web | 根 Compose 接入 |
| apps/mixin-search | 文档解析、切块、dense/sparse 召回、RRF、v1 gRPC | 检索服务 | 独立运行，未接真实流量 |
| packages/proto | 跨应用 RPC 协议事实源 | 平台契约 | 构建期 |
| packages/gen | 由 Proto 生成的 Go 客户端与服务端类型 | 平台契约 | 构建期 |
| deployments | PostgreSQL 初始化、BM25 检查与总体验证 | go-web 运维边界 | 根 Compose 接入 |

chat 源码仍位于 gin-backend，但应用不初始化其服务、不注册路由、不展示前端入口，也不纳入 readiness。

## 2. 数据与索引所有权

| 表、集合或键 | 内容 | 唯一所有者 | 阶段 0 约束 |
|---|---|---|---|
| users | Web 用户与认证状态 | gin-backend/auth | 业务事实 |
| managers | 管理员身份 | gin-backend/manager | 业务事实 |
| manager_registration_requests | 管理员注册审批 | gin-backend/manager | 业务事实 |
| markdowns | Markdown 身份、元数据、可见性、search_text | gin-backend/markdown | 阶段 1 被新文档内核替换 |
| markdown_contents | 当前 Markdown 正文 | gin-backend/markdown | 阶段 1 迁移到版本表 |
| chat_groups、chat_group_members、chat_messages、chat_message_deliveries | 搁置的 Web Chat 数据 | gin-backend/chat | 保留但无运行写入 |
| cache:user:id、cache:user:email | 用户读缓存 | gin-backend/auth | 由 auth 失效 |
| cache:manager:id、cache:manager:username | 管理员读缓存 | gin-backend/manager | 由 manager 失效 |
| cache:markdown:meta、cache:markdown:content | Markdown 读缓存 | gin-backend/markdown | 阶段 1 删除并换版本化键 |
| PostgreSQL idx_markdowns_paradedb | 当前 BM25 检索投影 | gin-backend/markdown | 切流前保留 |
| Qdrant rag_chunks | dense/sparse 派生索引 | mixin-search | 首个生产候选后端 |
| pgvector rag_chunks | dense/tsvector 派生索引 | mixin-search | 实验性替代后端 |
| memory store | 进程内版本与切块数据 | mixin-search | 仅测试和演示 |

正式文档、权限、发布和删除事实归 go-web；所有 mixin-search 数据都必须可以从事实数据重建。

## 3. 当前 HTTP 表面

- 健康：GET /healthz、GET /readyz。
- 用户认证：POST /api/v1/public/auth/login、register、refresh；POST /api/v1/protected/auth/logout。
- 管理员：POST /api/v1/public/manager/register、login、refresh；保护端提供 logout、审批、拒绝和申请列表。
- Markdown：保护端提供 upload、mine、public、search、detail、update、delete。
- Chat：/api/v1/protected/chat 及 /ws 未注册，当前返回 404。

保护路由使用用户认证中间件；管理员保护路由使用独立管理员认证中间件。浏览器修改请求继续遵守当前 Cookie 与 CSRF 约束。

## 4. 当前 RPC 表面

服务全名为 mixin_search.v1.RAGService，共六个方法：

1. IndexDocumentVersion
2. ActivateDocumentVersion
3. DeleteDocumentVersion
4. DeleteDocument
5. GetDocumentVersionState
6. SearchDocuments

服务同时注册 grpc.health.v1.Health，空服务名和 RAG 服务名均报告 SERVING。协议事实源为 packages/proto/mixin-search/v1/mixin-search.proto。

## 5. 配置与部署

- 根 docker-compose.yaml 运行 Web、Gin、PostgreSQL、Redis 和 Nginx 相关栈，不包含 mixin-search。
- apps/mixin-search/compose.yaml 只提供 Qdrant 与 pgvector 依赖，供独立集成验证。
- mixin-search 默认监听 127.0.0.1:9090，默认 memory store；真实候选后端明确为 Qdrant。
- QDRANT_API_KEY、PGVECTOR_DSN 等敏感配置只从环境读取，不写入协议或生成代码。
- 将 mixin-search 接入根 Compose 前，必须先完成真实 Qdrant 的 ACL、修订 fencing 和独立集成测试。

## 6. 验证入口与类别

| 类别 | 命令 | 证明范围 |
|---|---|---|
| 协议生成 | packages/proto/verify-generated.ps1 | Proto 与已提交 Go 生成物逐文件一致 |
| 单模块 | 在 apps/gin-backend 运行 go test ./... | HTTP、服务、存储与禁用 Chat 路由回归 |
| 单模块 | 在 apps/mixin-search 运行 go test ./... | 契约、解析、存储适配器与 gRPC Health |
| 前端静态 | 在 apps/simple-frontend 运行 npm run type-check 和 npm run build | Vue/TypeScript 与生产构建 |
| 依赖配置 | docker compose -f apps/mixin-search/compose.yaml config --quiet | 独立检索依赖编排合法 |
| 根栈集成 | deployments/verify.ps1 | Compose、BM25、HTTP 健康和模块检查 |

当前清单不证明高并发、生产检索质量、跨服务可靠投递或真实流量闭环。

## 7. P1.3 现状增量（2026-09-12）

阶段 0 清单保留为历史基线；当前运行事实以本节覆盖：

- gin-backend 的文档事实已由 `documents`、`document_versions`、`document_access_policies` 等新领域表承载；列表和搜索读取 `document_search_projection`。
- `/api/v1/protected/markdown/*` 仍是冻结的外部 HTTP 路径，但由 `modules/document/interfaces/http` 适配，运行时不再引用旧 Markdown handler、ORM、store 或 cache。
- 当前读缓存命名空间为 `cache:document:v1:view:*`，键包含活动版本与三类修订号；`cache:markdown:*` 已退出运行时。
- PostgreSQL 当前 BM25 读取索引为 `idx_document_search_projection_bm25`；`idx_markdowns_paradedb` 与旧 Markdown 表仅保留作 P1.6 回滚资产。
- Chat/WebSocket 仍不注册并返回 404；文档写路径仍不调用 mixin-search RPC。
- P1.3 隔离门禁与完整 92 项运行时 API 回归均通过；这仍不证明高并发或跨服务可靠投递。