# go-web 生态演进初步改造方案

> 文档状态：历史初步方案（已冻结，由 [`CURRENT_IMPLEMENTATION_PLAN.md`](../planning/CURRENT_IMPLEMENTATION_PLAN.md) 接管）
> 用途：保留初始审计和方案形成过程，不再作为当前排期或进度来源
> 基准日期：2026-09-11
> 上位约束：[`ECOSYSTEM_EVOLUTION_GUIDE.md`](../ECOSYSTEM_EVOLUTION_GUIDE.md)
> 适用范围：`go-web` 当前 monorepo，以及它与 `py-agent/314`、检索服务的边界

## 1. 文档目标

本文基于 `ECOSYSTEM_EVOLUTION_GUIDE.md` 与 `go-web` 当前代码，记录：

1. 当前实现与长期目标之间的差距；
2. 建议的目标边界和分阶段改造顺序；
3. 阶段 0 需要确认的架构决策、产物和验收标准；
4. 后续领域模型、服务契约和迁移方案的输入条件。

本文是初步改造方案，不替代后续数据库、HTTP、gRPC、权限和迁移专项设计。

## 2. 当前代码基线

### 2.1 已核实能力

- 仓库已经采用 `apps/`、`packages/`、`docs/`、`deployments/` 组织方式；
- `apps/gin-backend` 是 Go/Gin 模块化后端，包含认证、Markdown、WebSocket 聊天和管理端能力；
- `apps/simple-frontend` 使用 Vue 3、TypeScript、Pinia、Vue Router 和 Vite；
- Markdown 当前支持作者所有权、`public/private` 可见性、分页、详情、更新、删除和 BM25 全文搜索；
- Markdown 搜索由 `gin-backend` 直接查询 PostgreSQL `pg_search`；
- `apps/mixin-search` 当前实现了 Markdown、DOC、DOCX 解析，Eino 编排，dense/sparse 两路召回和 RRF 融合；
- `packages/proto/mixin-search/v1/mixin-search.proto` 已统一为版本索引、激活、删除、状态查询和授权范围搜索六个 RPC；
- `gin-backend/internal/modules/aiagent` 已有独立 Agent 服务的 HTTP/SSE 客户端，但尚未接入应用依赖和 Gin 路由；
- 当前 `apps/gin-backend` 与 `apps/mixin-search` 分别执行 `go test ./...`，结果均为退出码 0。

### 2.2 基线限制

- 当前工作树已有未提交的 `mixin-search`、生成代码和相关协议调整；这些属于在建实现，不视为稳定发布基线；
- 根 `docker-compose.yaml` 尚未编排 `mixin-search`，当前验证只能证明两个 Go 模块分别通过测试，不能证明跨服务闭环；
- `mixin-search` 默认使用本地确定性 Embedder，适合验证流程，不代表真实语义检索质量；
- 当前版本清单和活动版本控制位于进程内；服务重启后失败关闭，持久化控制面留待后续 Outbox 阶段；
- 当前前端 `package.json` 没有组件或端到端测试命令；
- 本方案不把本地测试结果扩大为生产可用性、容量或高并发结论。

## 3. 差距矩阵

| 领域 | 当前实现 | 目标状态 | 优先级 |
|---|---|---|---|
| 仓库组织 | 已有 `apps/packages/docs/deployments` | 独立应用、共享协议、可独立部署 | 基本具备 |
| 文档模型 | 单一 `markdowns + markdown_contents` | 空间、文档、版本、资产、来源关系 | P0 |
| 权限 | 作者 + `public/private` | 私人空间、团队空间、成员角色、共享与撤销 | P0 |
| 生命周期 | 更新覆盖正文；部分软删除 | 草稿、处理、发布、归档、回收站、撤销发布 | P0 |
| 搜索 | `gin-backend` 内置 BM25 | Web 保持入口和权限语义，检索实现迁往检索服务 | P1 |
| 索引一致性 | 文档事务后只失效缓存 | Outbox、幂等消费、重试、状态和删除传播 | P1 |
| 多格式资产 | Web 仅接受标题、Markdown 正文 | 原始文件资产归 `go-web`，解析和索引归检索服务 | P1 |
| RAG 契约 | v1 已具备版本索引、激活、删除、状态和权限范围搜索 | 增加持久化版本清单、Outbox 重放和真实后端闭环 | P1 |
| 前端体验 | 我的、公开、搜索、Markdown 编辑 | 空间、版本、发布、文件、来源、归档和回收站 | P2 |
| QQ 融合 | 尚无身份或空间绑定 | QQ 身份、群空间绑定、受控文档草稿入口 | P3 |
| 聊天记录 | `go-web` 有 WebSocket 聊天表 | 与 `py-agent` QQ 聊天事实源保持明确边界 | P0 决策 |

## 4. 建议目标边界

```text
Vue Web
   │ HTTP
   ▼
gin-backend
   ├─ 用户、知识空间、成员和文档权限
   ├─ 文档、版本、原始资产和来源关系的事实源
   ├─ PostgreSQL 业务事务 + Index Outbox
   └─ SearchFacade
          ├─ PostgresBM25Provider（迁移基线/回退）
          └─ MixinSearchProvider ──gRPC──► mixin-search

py-agent ──内部服务接口──► gin-backend 创建带来源信息的文档草稿
```

边界约束：

- 浏览器和 `py-agent` 不直接写检索服务；
- `mixin-search` 不拥有用户、空间成员或正式文档生命周期；
- 只有有效发布版本能够进入正式文档检索集合；
- `gin-backend` 计算允许访问的空间范围，检索服务执行强制元数据过滤；
- 普通 Agent 回答不能自动成为正式文档；
- 文档检索和聊天检索使用独立语料类型、集合、流程和评测集。

## 5. 分阶段路线

### 阶段 0：现状审计与边界固化（已完成）

- 固化应用、数据和运行时边界；
- 收口当前 `mixin-search` 在建实现；
- 记录现有 HTTP、数据库、BM25 和 gRPC 行为基线；
- 明确 WebSocket 聊天与 QQ 聊天记录的关系；
- 为领域模型和跨服务契约提供决策输入。

### 阶段 1：文档领域模型与兼容迁移

- 采用受控停写窗口进行一次性领域内核切换，不把长期双写作为正式架构；
- 用空间、文档、不可变版本、访问策略、授权、搜索投影和 Outbox 替换旧 Markdown 表；
- 内部数据库、Go 包、缓存和未发布 RPC 允许不兼容修改；
- 保持现有 /api/v1/protected/markdown/* 用户契约与权限语义兼容；
- 迁移前必须具备备份、前置审计、反向导出和真实 PostgreSQL 往返演练；
- 详细方向见 PHASE1_BREAKING_REFACTOR_DIRECTION.md。

### 阶段 2：文档—索引可靠链路

- 启动阶段 1 已建立的索引 Outbox 消费器和状态机；
- 按已统一的 v1 契约实现持久化版本清单和 Outbox 消费；
- 支持索引、激活、删除、状态查询和重建；
- 为 Qdrant 或 pgvector 建立真实集成测试。

### 阶段 3：BM25 渐进迁移

- 在 `gin-backend` 引入 `DocumentSearchProvider`；
- 保留现有 PostgreSQL BM25 Provider；
- 增加检索服务 Provider；
- 依次执行影子写入、影子查询、小流量主读、故障回退和全量切换；
- 验证完成前不移除现有 BM25。

### 阶段 4：Web 产品能力升级

- 增加空间、成员、版本、发布、归档、回收站、上传和来源页面；
- 保留 Vue 3 Composition API、`<script setup>` 和 TypeScript；
- 抽取 `useDocumentEditor`、`useDocumentSearch`、`useDocumentVersions` 等 composable；
- 增加组件测试和关键用户路径的端到端测试。

### 阶段 5：py-agent 与聊天记录域

- 建立 QQ 身份和 Web 用户绑定；
- 建立 QQ 群与团队空间绑定；
- 允许 `py-agent` 通过内部 API 创建带来源的文档草稿；
- 建立与正式文档完全隔离的聊天保存、索引和检索流程；
- 建立聊天内容晋升为正式文档的审核和发布流程。

## 6. 阶段 0 实现细节

### 6.1 阶段目标

阶段 0 不建设新业务能力。它的完成状态是：后续开发者能够明确回答“数据由谁拥有、调用通过什么边界、失败后如何回退、哪些行为必须保持兼容”。

### 6.2 非目标

- 不迁移现有 Markdown 数据；
- 不切换搜索流量；
- 不修改当前公开/私有语义；
- 不把 `mixin-search` 加入生产式部署声明；
- 不开始 QQ 身份绑定；
- 不移动或删除现有聊天表和 WebSocket 代码。

### 6.3 建议固化的架构决策

#### ADR-001：检索服务的工程归属

建议决策：保留 `apps/mixin-search` 作为当前 monorepo 内可独立构建和部署的应用。

理由：

- 当前代码、Proto 和生成代码已经按该方向组织；
- 独立 Go module 和 gRPC 边界能够保持运行时隔离；
- 目录集中不等于共享业务表或合并进程；
- 现阶段拆回另一个仓库只增加迁移成本，不改善数据边界。

阶段 0 已固化：服务默认监听 `127.0.0.1:9090`，提供标准 gRPC Health；Qdrant 为首个生产候选后端，配置来自命令行与环境变量，并在阶段 2 接入流量前保持独立部署。

#### ADR-002：正式文档与索引职责

建议决策：

- `gin-backend` 是文档、版本、权限、发布和删除的唯一事实源；
- `mixin-search` 只保存可重建的文档版本索引；
- `mixin-search` 不能根据模型输出自行发布、共享或扩大检索范围；
- 跨服务写入采用至少一次投递，因此 RPC 和存储操作必须幂等。

#### ADR-003：现有聊天模块边界（已确认）

确认决策：搁置现有 WebSocket 实时通讯功能。`gin-backend/internal/modules/chat`、前端聊天页面和相关数据结构保留在代码中，但后端不初始化服务、不注册路由、不纳入就绪检查，前端不注册路由或展示导航入口。

后续需要单独决定：

- Web 聊天是否继续作为独立产品能力保留；
- Web 聊天历史是否需要长期治理；
- 若 Web 与 QQ 聊天需要统一查看，`go-web` 应通过只读服务边界聚合，而不是共同写 `py-agent` 业务表。

#### ADR-004：BM25 迁移方式

建议决策：采用 Provider + 双轨验证，不直接替换。

```go
type DocumentSearchProvider interface {
    Search(context.Context, DocumentSearchQuery) (DocumentSearchResult, error)
}
```

阶段 0 只定义接口语义和基线用例；Provider 实现和切流在后续阶段完成。

### 6.4 阶段 0 产物

已交付以下文档：

| 产物 | 内容 |
|---|---|
| `docs/adr/001-search-service-boundary.md` | `mixin-search` 工程和运行时边界 |
| `docs/adr/002-document-index-ownership.md` | 文档事实源、索引派生数据和一致性责任 |
| `docs/adr/003-chat-domain-boundary.md` | Web 聊天与 QQ 聊天记录边界 |
| `docs/adr/004-bm25-migration-strategy.md` | Provider、影子流量、回退和退出条件 |
| `docs/history/CURRENT_SYSTEM_INVENTORY.md` | 模块、表、接口、配置、测试和部署清单 |
| `docs/history/CONTRACT_BASELINE.md` | 当前 HTTP、gRPC、错误码和兼容行为 |
| `docs/history/PHASE1_BREAKING_REFACTOR_DIRECTION.md` | 已批准的阶段 1 破坏性改造、模型、迁移、回滚和验收方向 |

### 6.5 当前契约基线

阶段 0 应冻结以下现有行为，作为后续兼容测试：

- `/api/v1/protected/markdown/mine`：仅当前作者文档；
- `/api/v1/protected/markdown/public`：所有登录用户可见的公开文档；
- `/api/v1/protected/markdown/search`：仅搜索当前作者文档，按 BM25 相关度排序；
- `/api/v1/protected/markdown/:markdownId`：公开文档可读，私有文档仅作者可读；
- 更新在同一事务内修改元数据、正文和 `search_text`；
- 删除后现有搜索结果不再包含该文档；
- `RAGService.IndexDocumentVersion` 索引版本但不自动激活；
- `RAGService.SearchDocuments` 仅返回活动版本，并要求非空授权空间范围。

### 6.6 mixin-search 收口结果

进入领域改造前的收口项已经完成：

1. Proto 唯一事实源为 packages/proto/mixin-search/v1，生成物唯一位置为 packages/gen/mixin-search/v1；
2. packages/proto/verify-generated.ps1 可在临时目录复现生成并检查漂移；
3. 生成文件保留 protoc-gen-go 与 protoc-gen-go-grpc 版本头；
4. apps/mixin-search 为独立 module，不依赖 gin-backend/internal；
5. 服务已注册标准 gRPC Health，并覆盖空服务名与 RAG 服务名；
6. memory store 明确仅用于测试和演示；
7. Qdrant 明确为首个生产候选，pgvector 为实验性替代；
8. 根 Compose 保持不接入；独立 Compose 配置与适配器集成测试入口已建立；
9. mixin-search/v1 已统一版本索引、激活、删除、状态查询和权限范围搜索六个 RPC。
### 6.7 阶段 0 验收标准

验收结果与实测命令见 [PHASE0_COMPLETION_REPORT.md](../reports/PHASE0_COMPLETION_REPORT.md)。

- 四个 ADR 均具有 `Accepted` 或明确的待决状态；
- 每个业务表、缓存键和索引集合都有唯一所有者；
- 当前 HTTP 与 BM25 行为形成可自动执行的回归基线；
- 当前 gRPC 契约有生成一致性检查；
- `gin-backend` 与 `mixin-search` 均可从干净检出状态构建、测试；
- 根验证脚本能够区分单模块测试、依赖集成测试和跨服务端到端测试；
- 文档中不再把 Web 聊天、QQ 聊天和正式文档混为同一数据域；
- 阶段 1 可以在不重新讨论工程归属的前提下开始数据模型设计。

## 7. 阶段 1 已批准模型输入

阶段 1 的第一批运行模型包括：

- knowledge_spaces
- space_members
- documents
- document_versions
- document_access_policies
- document_grants
- document_search_projection
- index_outbox
- document_index_states

document_assets、document_relations 只预留标识规则，不进入第一批运行路径。

状态必须分离：

- 文档治理：active -> archived -> trashed；
- 版本发布：draft -> published -> superseded，或 draft -> withdrawn；
- 索引处理：pending -> indexing -> indexed -> active，失败进入 failed；
- documents.active_version_id 只能指向本文件的已发布版本；
- 索引延迟或失败不能回滚已经提交的 Web 文档事实；
- 完整字段、约束和修订号见 PHASE1_BREAKING_REFACTOR_DIRECTION.md。

## 8. 主要风险

1. **过早切换搜索**：当前版本清单仍位于进程内，直接接入真实流量会在服务重启后丢失活动状态；
2. **把索引成功等同发布成功**：需要由 `go-web` 决定有效版本，不能由检索服务决定；
3. **混淆聊天数据域**：现有 WebSocket 聊天不能自动视为 QQ 聊天事实源；
4. **一次性改造范围过大**：领域、权限、RPC、搜索和 UI 应按兼容切片推进；
5. **把演示能力视为生产能力**：memory store、本地 Embedder 和模块单测不能替代真实后端集成与端到端验证。

## 9. 已批准的阶段 1 实施方向

阶段 1 以长期系统健康性优先，批准对内部持久化、包结构、缓存和未发布 RPC 执行破坏性改造。

采用“受控停写、一次迁移、领域内核重写”路线：

1. 先交付有序迁移器、数据审计、备份和反向导出；
2. 用新领域表和独立 BM25 搜索投影替换 markdowns + markdown_contents；
3. 后端运行时只读写新模型，不保留长期双写和写视图；
4. 旧表仅在一个回滚窗口内以只读快照形式保留；
5. 现有 Markdown HTTP 路径和用户语义保持兼容；
6. 阶段 1 只写入 Outbox，不连接检索服务；
7. 在接入前继续修订 mixin-search/v1，补齐 ACL 与删除 fencing。

详细审计、模型、迁移、回滚和验收标准见
[PHASE1_BREAKING_REFACTOR_DIRECTION.md](./PHASE1_BREAKING_REFACTOR_DIRECTION.md)。
