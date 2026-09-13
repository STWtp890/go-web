# 阶段 0 契约基线

> 状态：历史阶段 0 快照；ADR-005 后不再作为旧 HTTP 或数据库兼容门禁

> 冻结日期：2026-09-11
> 适用范围：阶段 1 内部破坏性重构的兼容测试。

## 1. HTTP 兼容面

### 身份与管理

- 用户登录、注册和刷新位于 /api/v1/public/auth；退出位于 /api/v1/protected/auth。
- 管理员登录、注册申请和刷新位于 /api/v1/public/manager；审批面位于保护路由。
- 阶段 1 保持现有 Cookie、刷新令牌、CSRF、中间件顺序和 JSON 错误结构。

### Markdown

| 接口 | 必须保持的行为 |
|---|---|
| POST /api/v1/protected/markdown/upload | 当前用户创建文档，可指定现有可见性 |
| GET /api/v1/protected/markdown/mine | 只列出当前作者文档 |
| GET /api/v1/protected/markdown/public | 登录用户可见公开文档 |
| GET /api/v1/protected/markdown/search | 只检索当前作者文档，按 PostgreSQL BM25 相关度返回 |
| GET /api/v1/protected/markdown/:markdownId | 公开文档可读；私有文档仅作者可读 |
| PUT /api/v1/protected/markdown/:markdownId | 仅作者可更新；当前实现同事务更新元数据、正文与 search_text |
| DELETE /api/v1/protected/markdown/:markdownId | 仅作者可删除；删除后不得继续出现在详情、列表和搜索 |

阶段 1 可以重写内部表和包，但上述路径、请求字段、响应字段、分页、public/private 语义与主要 HTTP 状态保持不变。markdownId 映射为新 document_id。

### Chat

当前运行时不注册 Chat。/api/v1/protected/chat、其子路径以及 WebSocket 升级请求均以 404 结束。chat 包内的路由单元测试只证明代码仍可编译，不代表应用接入。

## 2. PostgreSQL BM25 基线

- idx_markdowns_paradedb 是当前 search_text 上的 BM25 索引。
- deployments/postgresql/sql/plugin/bm25_only_verify.sql 是可执行边界检查。
- 在检索提供方切换完成前，BM25 仍是 HTTP 搜索的权威读取路径。
- 阶段 1 将投影迁出业务主表时，必须提供等价回归与回退脚本。

## 3. mixin-search/v1 RPC 基线

命名空间为 mixin_search.v1，Go 导入路径为 packages/gen/mixin-search/v1，服务为 RAGService。

- IndexDocumentVersion：同一 operation_id 与自然键幂等；索引完成不自动激活。
- ActivateDocumentVersion：由 go-web 分配单调 activation_revision；低修订不得覆盖高修订。
- DeleteDocumentVersion：删除指定版本，并报告是否移除了活动版本。
- DeleteDocument：删除该文档的全部派生索引。
- GetDocumentVersionState：查询版本是否存在及其索引状态。
- SearchDocuments：allowed_space_ids 由 go-web 计算；空集合 fail closed；top_k 为 0 时采用默认值 3，有效上限 100。

索引状态只包含 INDEXED 与 ACTIVE。无效输入映射 InvalidArgument，不存在的前置对象映射 NotFound，版本不可变或激活前置条件冲突映射 FailedPrecondition，内部存储错误映射 Internal。

生成物必须通过 packages/proto/verify-generated.ps1。gRPC 服务同时提供标准 Health；空服务名和 mixin_search.v1.RAGService 均返回 SERVING。

## 4. 兼容边界之外

阶段 0 不承诺：

- memory store 重启后的版本状态；
- 当前本地确定性 Embedder 的语义质量；
- Qdrant/pgvector 两套后端的生产等价性；
- Outbox、跨服务删除传播、ACL 下推或端到端零丢失；
- Chat、QQ 身份绑定或实时通讯。

这些能力分别进入阶段 1 设计和阶段 2 以后实施，不得用模块单测结果替代生产声明。