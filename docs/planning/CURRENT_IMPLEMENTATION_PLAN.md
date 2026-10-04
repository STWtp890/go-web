# go-web 当前实施计划

> 文档职责：当前唯一阶段排期与实施入口
> 上位目标：[ECOSYSTEM_EVOLUTION_GUIDE.md](../ECOSYSTEM_EVOLUTION_GUIDE.md)
> 相关决策：[ADR-001](../adr/001-search-service-boundary.md)、[ADR-002](../adr/002-document-index-ownership.md)、[ADR-004](../adr/004-bm25-migration-strategy.md)、[ADR-005](../adr/005-development-baseline-over-production-migration.md)、[ADR-006](../adr/006-mixin-search-control-state-commit-order.md)、[ADR-007](../adr/007-qdrant-control-projection-and-filtering.md)、[ADR-008](../adr/008-document-index-transactional-outbox.md)、[ADR-009](../adr/009-shadow-index-compose-and-health-boundary.md)、[ADR-010](../adr/010-shadow-query-evaluation-gate.md)、[ADR-011](../adr/011-bounded-cache-runtime-and-revision-fencing.md)、[ADR-012](../adr/012-multi-consumer-search-boundary-and-critical-path-shift.md)、[ADR-013](../adr/013-immutable-control-snapshot-and-background-projection.md)、[ADR-014](../adr/014-per-corpus-control-plane-isolation.md)、[ADR-015](../adr/015-control-plane-idempotency-ledger-retention.md)、[ADR-016](../adr/016-qq-identity-and-knowledge-space-mapping.md)、[ADR-017](../adr/017-source-owned-document-and-search-services.md)
> 当前状态：**阶段 A（Web 文档链路与 Go 查询权限）、阶段 B（检索能力、索引管理与评测接管）、阶段 C（旧实现清理）均已完成并有实测证据**。P3.0–P3.3 的既有实现与本地验收记录保留为历史基线，P3.4 的身份绑定与资源范围代码作为改造输入保留（未提交）。2026-09-26 起按 [ADR-017](../adr/017-source-owned-document-and-search-services.md) v2 的**责任划分**实施拆分；2026-09-30 完成三阶段，随后按验收反馈完成 F01–F07 修复。下文原 P3.4–P3.6 与 B 线排期已被 §0 的当前任务替代。
> 更新日期：2026-09-30

## 0. 当前开发方向：按来源划分写入与检索服务

本节是 2026-09-25 起的唯一当前任务顺序。[ADR-017](../adr/017-source-owned-document-and-search-services.md) 规定目标架构；上位产品边界见 [生态演进指南](../ECOSYSTEM_EVOLUTION_GUIDE.md)。下文 §1–15 保存上一轮阶段三方案及 P3.0–P3.3 的完成事实，其中尚未完成的 P3.4–P3.6、B 线任务不再作为当前排期或服务所有权依据。仓库中正在进行的 P3.4 修改属于改造输入，不因本次文档更新被视为完成。

当前没有上线流量或需要保留的数据。实施时核对并仅清理本项目的开发数据库与索引，从空数据建立新结构；不安排旧表迁移、双写、灰度切换、旧接口兼容或生产回退工作。

### 0.1 目标边界

| 边界 | 唯一负责人 | 接口与存储要求 |
| --- | --- | --- |
| Web 界面、Web 账号、Web 侧业务入口 | go-web | 通过文档服务接口完成文档操作，不直接读写文档服务表 |
| QQ Bot 身份命名空间、QQ 用户与群的会话事实、渠道权限、QQ 原始消息/文件/撤回事实 | py-agent 的 QQ 数据模块 | 证明 QQ 身份与会话上下文；晋升正式文档时调用文档服务并保留来源标识 |
| 正式文档、版本、知识空间、成员、群空间绑定、资源权限、审计、文档 Outbox、资源访问主体登记、文档详情读取 | document-service | 只接受业务命令与详情查询，不是通用 SQL 代理 |
| 正式文档索引与检索 | document-search | 只消费 document-service 事件，不回调事实源 |
| QQ 原始文件与聊天消息索引与检索 | qq-search | 只消费 QQ 来源事件；文件与消息分别建模、分别索引 |
| 跨来源组合查询 | 按需求增加的组合查询服务 | 只合并带来源的结果，不拥有事实、索引或权限 |

各服务可共享 PostgreSQL 与 Qdrant 设施，但各有独立的 schema/库、写入账号、业务表、collection 和 alias。**文档服务持有自己的资源访问主体标识**：Web 请求与 QQ 请求分别通过受信入口提供身份材料，文档服务据此判定空间成员关系与文档权限；**两类身份不以互相绑定为前提**。QQ 请求中的 Bot、用户及当前群上下文由 `py-agent` 证明，资源授权由 `document-service` 判定。文档服务不依赖检索服务完成写入；检索服务不在查询时回调来源服务。

### 0.2 实施顺序

| 顺序 | 实施任务 | 可验证交付 | 状态 |
| --- | --- | --- | --- |
| 1 | 修订 ADR-016/017 与计划的身份和服务所有权描述；定义文档命令、详情读取、来源标识、服务身份与权限凭证契约 | ADR-016 v3、ADR-017 v2、[文档服务契约](../contracts/DOCUMENT_SERVICE_V1_CONTRACT.md)、[身份与凭证契约](../contracts/SERVICE_IDENTITY_AND_CAPABILITY.md)、§0.5 唯一负责人清单 | 已完成 |
| 2 | 固定契约：`packages/proto` 新增 `document/v1`、`qqsource/v1`、`documentsearch/v1`、`qqsearch/v1` 并生成代码；`packages/serviceauth` 落地凭证格式与边界拦截器 | 四份 proto、生成代码一致性检查、共享认证包测试 | 已完成 |
| 3 | 建立 `apps/document-service`、`apps/document-search`、`apps/qq-search` 三个独立 Go module、`go.work`、根 Compose、数据库账号与 schema、跨服务架构检查 | 三个模块可构建、可启动、有健康检查与测试入口；写入权限与索引隔离检查 | 已完成 |
| 4 | 提取 document-service：正式文档写入与详情读取、版本与生命周期、空间与成员、群空间绑定、资源权限、审计、Outbox | 事务提交、失败回滚与资源权限测试在真实 PostgreSQL 上通过 | 已完成（22 个集成测试 + 1 个传输测试，0 跳过） |
| 5 | 提取 document-search：正式文档索引、查询、授权范围校验、幂等消费与独立重建 | 创建/更新/撤销/删除/重复/乱序事件与重建测试通过 | 已完成（16 个集成测试，0 跳过） |
| 6 | 提取 qq-search：QQ 原始文件与消息的独立模型、生命周期与索引集合 | 保存/更新/撤回/删除/重复事件与来源隔离测试通过 | 已完成（12 个集成测试，0 跳过） |
| 7 | 接通 Web 文档操作、文档事件消费、QQ 原始事件消费与 QQ 受信身份输入 | 三条链路各有集成测试；调用图没有同步调用环 | 已完成（见 §0.6） |
| 8 | 清理不涉及检索实现的过时内容、不可达 Chat 代码与验收入口，并完成同包文件拆分和结构文档对齐；旧文档索引/检索及运维链路保留 | 清理与拆分可单独复核；全文/向量检索代码、协议、schema、alias 与运行链路无改动 | 进行中（见 §0.7、§0.9） |
| 9 | 从空的项目开发数据库与索引运行集成验证 | 三个服务分别构建、测试、启动；写权限与索引隔离成立；不存在跨服务 internal 导入、跨服务业务表写入或同步调用环 | 已完成（见 §0.6、§0.8） |

每一步只宣称实际完成和通过的测试，不以本表中的目标文字替代实现结果。

### 0.3 完成标准

- 只有文档服务持有正式文档与空间业务表的写权限，只有 QQ 来源模块写 QQ 原始事实；
- 文档检索与 QQ 检索各自能消费变更、查询和从对应事实源重建；
- 不存在跨服务私有包导入、跨服务业务表写入或同步反向调用；
- QQ 内容转为正式文档时经过文档服务生命周期，并保留来源 ID；
- 共享数据库设施时服务的表、账号与索引仍隔离；
- Go 测试、协议检查、架构测试、从空数据启动的 PostgreSQL/Qdrant 集成测试及文档链接检查通过。

### 0.4 P3.4 既有实现核对（2026-09-25）

本节记录可复用的现有代码及尚缺的验收证据，不表示 ADR-017 的目标服务边界已经落地。P3.4 代码目前仍是未提交的工作区修改，作为 §0.2 步骤 4 的改造输入保留。

| 核对项 | 已有实现 | 验证状态 |
| --- | --- | --- |
| Bot 隔离的 QQ 用户和群身份 | 用户、群身份以 channel、bot_id、external_id 区分；活动群绑定还限制同一 Bot 下一个空间只能绑定一个群 | 已核对表定义、部分唯一索引和应用校验；数据库唯一性及群空间反向并发约束未实测 |
| 受信写入口与审计 | spacectl 是目前唯一的非测试调用入口；source 固定为 spacectl，actor、reason 必填；绑定、撤销、改绑与审计在同一事务处理 | 已核对代码；审计插入失败后的事务回滚测试已编写，但因无数据库连接而跳过。actor 由 CLI 参数填写，工具本身没有将其与已认证操作者身份核对 |
| 资源范围解析 | 单条 SQL 读取 QQ 用户绑定、群绑定、用户、空间与成员，返回 Granted/Denied 以及 Private/CurrentTeam/OtherTeams；请求空间越界时整体拒绝 | 单元测试通过；数据库解析测试已编写但跳过。首发私聊/群聊范围收窄、capability 签发和 py-agent 接入仍未实现 |
| ADR-016 验收 | 已有私聊/群聊、撤销、Bot 隔离、并发用户绑定、审计回滚、成员不自动增加和账号禁用等集成测试 | 六个 PostgreSQL 集成测试均因未设置 DOCUMENT_REPOSITORY_TEST_DSN 跳过；群空间反向唯一性的并发测试、改绑期间的读取一致性、未授权写入、generation/alias 不变及 QQ 入群事件的不增权断言仍缺证据 |

本轮实际执行以下命令，退出码均为 0：

- go test -v ./apps/gin-backend/internal/modules/space/... ./apps/gin-backend/cmd/tools/spacectl -count=1：七个单元测试通过、六个 PostgreSQL 集成测试跳过。
- go test ./apps/gin-backend/... -count=1：Go 测试通过；不代表已跳过的数据库测试通过。
- git diff --check：通过。

本机执行 docker info --format '{{.ServerVersion}}' 退出码为 1，Docker daemon 未运行。上述结果不构成 PostgreSQL 事务、约束或并发行为的验收证据。

**处置（2026-09-26）**：按 ADR-017 v2 重新设计，不再修补旧模型。`qq_user_bindings` 取消；资源侧事实（主体登记、空间、成员、群绑定、解析、审计）全部迁入 `apps/document-service`；六个旧集成测试中仍然成立的语义（判定二态与标签、群聊当前团队、资源侧不增成员、撤销生效点、包含规则、并发活动唯一性、审计回滚、未授权写入）在真实 PostgreSQL 上重写为文档服务的集成测试；依赖 `users` 表外键与“QQ 绑定到 Web 用户”的用例被替换。

### 0.5 唯一负责人清单（步骤 1 的交付）

每个对象只有一个负责人。"读"只在服务不拥有该对象时单独标注。

#### 数据表

| 表 / schema | 唯一负责人 | 写入账号 | 其他服务权限 |
| --- | --- | --- | --- |
| `auth.*` 用户认证表 | go-web | go-web | 无 |
| `manager.*` 管理员与审批表 | go-web | go-web | 无 |
| `chat.*` 站内聊天保留表 | go-web（未启用） | go-web | 无 |
| `public.knowledge_spaces`、`public.space_members`、`public.documents`、`public.document_versions`、`public.document_access_policies`、`public.document_grants`、`public.document_search_projection`、`public.document_index_delivery_events`、`public.document_index_rebuild_runs`、`public.document_search_shadow_observations`、`public.qq_user_bindings`、`public.group_space_bindings`、`public.space_audit_events` | go-web（迁移期基线，随步骤 8 删除） | go-web | 无 |
| `document_service.access_subjects`、`knowledge_spaces`、`space_members`、`documents`、`document_versions`、`document_sources`、`document_access_policies`、`document_grants`、`group_space_bindings`、`space_audit_events`、`document_events` | document-service | `document_service_writer` | document-search 只读 `document_events` 的 `(sequence, event_id, document_id, event_kind, aggregate_revision, payload, occurred_at)`；其余无权限 |
| `document_search.consumer_cursors`、`document_index_events`、`document_index`、`document_index_tombstones`、`rebuild_runs`、`index_generations` | document-search | `document_search_writer` | 无 |
| `qq_search.qq_messages`、`qq_files`、`qq_applied_events`、`consumer_state`、`rebuild_runs`、`index_collections` | qq-search | `qq_search_writer` | 无 |
| py-agent 的 QQ 原始事实表（消息、文件、会话、撤回） | py-agent | py-agent | 无（qq-search 通过网络接收事件，不读表） |

#### 索引与集合

| 索引 / 集合 | 唯一负责人 | 命名空间 |
| --- | --- | --- |
| `document_search.document_index.search_vector`（tsvector GIN）与标题三元组索引 | document-search | schema `document_search` |
| Qdrant alias `go_web_document_v1`，storage domain `document-search:documents:v1` | document-search | 仅正式文档 |
| `qq_search.qq_messages` / `qq_files` 的 tsvector 索引 | qq-search | schema `qq_search` |
| Qdrant alias `qq_source_messages_v1` / `qq_source_files_v1`，storage domain `qq-search:messages:v1` / `qq-search:files:v1` | qq-search | 消息与文件各自独立，不共用 |
| `public.idx_document_search_projection_bm25` 与 `public.*` 旧索引 | go-web（迁移期，随步骤 8 删除） | schema `public` |
| `mixin_search_control.control_states`、`chat_control_states`、文档/聊天 Qdrant collection | mixin-search（迁移期，随步骤 8 收敛） | schema `mixin_search_control` |

#### 事件与接口

| 对象 | 生产者（唯一） | 消费者 |
| --- | --- | --- |
| `document.v1.DocumentService`（命令与详情读取） | document-service（实现） | go-web、py-agent |
| `document.v1.DocumentEventEnvelope` / `ListDocumentEvents` | document-service | document-search（唯一预定消费者） |
| `documentsearch.v1.DocumentSearchService` | document-search（实现） | go-web、py-agent（持 document-service 签发的 capability） |
| `qqsource.v1.QQSourceService` / `QQSourceEventEnvelope` | py-agent | qq-search（唯一预定消费者） |
| `qqsearch.v1.QQSearchService` | qq-search（实现） | py-agent（持 py-agent 签发的渠道 capability） |
| `mixin-search/v1` 与 `chat/v1` | mixin-search（迁移期实现） | go-web（迁移期）；目标由上述两个检索服务取代 |
| 服务身份断言与资源范围 capability（`packages/serviceauth`） | 断言由调用方自签；capability 由 document-service（文档范围）或 py-agent（QQ 渠道范围）签发 | document-service、document-search、qq-search |

#### 调用边

| 调用边 | 方向 | 同步/异步 | 说明 |
| --- | --- | --- | --- |
| go-web → document-service | 单向 | 同步 gRPC | Web 文档操作与详情读取；Web 账号身份只在此声明 |
| py-agent → document-service | 单向 | 同步 gRPC | 提供经验证的 QQ 身份与会话上下文；资源授权由 document-service 判定 |
| py-agent → document-service（晋升） | 单向 | 同步 gRPC | QQ 内容提交为正式文档，保存来源关系 |
| document-service → document-search | 单向 | 异步事件（Outbox + 消费游标） | 只有事件；文档服务不调用检索服务 |
| py-agent → qq-search | 单向 | 同步推送事件入口 | QQ 原始内容变更 |
| go-web / py-agent → document-search | 单向 | 同步 gRPC + capability | 先取范围，再查询 |
| py-agent → qq-search（查询） | 单向 | 同步 gRPC + capability | 渠道范围由 py-agent 判定 |
| 任何检索服务 → 事实源 | **禁止** | — | 检索服务不在查询时回调事实源 |

### 0.6 已执行的集成验证（2026-09-26）

命令与真实结果，均为在 `D:/.../go-web` 与运行中的开发设施上实际执行：

| 命令 | 结果 |
| --- | --- |
| `apps/document-service`: `go build ./...` / `go vet ./...` / `gofmt -l .` | 通过（0 输出） |
| `apps/document-service`: `go test ./... -count=1 -run Integration -v` | 23 个 `TestIntegration*` 全部 PASS，0 SKIP（22 个在 `internal/application`，1 个在 `internal/interfaces/grpcapi`），全部连真实 PostgreSQL |
| `apps/document-search`: 同上 | 16 个 `TestIntegration*` 全部 PASS，0 SKIP |
| `apps/qq-search`: 同上 | 12 个 `TestIntegration*` 全部 PASS，0 SKIP |
| `apps/mixin-search`: `go build/vet/test ./...` | 通过 |
| `apps/gin-backend`: `go build/vet/test ./...` | 通过（含新增"gin-backend 不得导入来源专属服务 module"断言） |
| `packages/serviceauth`: `go test ./...` | 通过 |
| `packages/proto/verify-generated.ps1` | 六份契约全部 PASS |
| `deployments/postgresql/verify-service-isolation.ps1` | `SERVICE_ISOLATION=PASS`（每个服务只能写自己的 schema；document-search 只按列只读 `document_service.document_events`；qq-search 不读其他 schema） |
| `deployments/verify-source-owned-services.ps1` | `ADR017_E2E=PASS` |

`ADR017_E2E` 门禁真实启动三个服务进程并断言：Web 命令创建文档与详情读回、事件被 document-search 消费后检索命中该文档、越界空间请求整体拒绝、跨 audience capability 被拒、无 `document-index-writer` 的索引写入被拒、索引状态可读；以及 QQ 链路：原始消息与文件分别入索引且互不串源、撤回后不再命中而记录仍为 `RECALLED`、越界会话请求整体拒绝。

三个服务的测试数据全部走真实 PostgreSQL（`127.0.0.1:15432/gin_demo`），各用专属写入账号；连接失败即 `t.Fatalf`，因此**不存在被跳过的集成测试**。

### 0.7 清理进度（步骤 8，已由 §0.10 阶段 C 完成）

已完成：

- 不可达的 Web Chat 页面、专用 API、composable 与会话存储已从工作树清理；后端 Chat/WebSocket 路由仍未注册。apps/mixin-search 的 Chat 语料实现、chat/v1 协议、Compose 参数与隔离验收保持原状。
- `apps/gin-backend` 的文档 HTTP 直连实现删除：`internal/modules/document/interfaces/http/handler.go` 与 `errors.go`（直接读写 `public.documents*` 表的路径），改为 `interfaces/sourceowned` 适配器经 document-service 与 document-search 提供服务。
- `apps/gin-backend` 组合根不再构造旧文档仓储/查询服务与影子查询观察器，文档路由只在 `source_owned_services.enabled` 时注册（未接线即无路由，不退回写表）。

阶段 C 已完成（2026-09-30，逐项「旧调用者 → 新负责人 → 替代入口 → 验证证据」见 [阶段 C 证据](../reports/evidence/phase4/stage-c-cleanup-go-web.md)）：

- 已删除：`internal/modules/document/application`、`internal/modules/document/infrastructure/{postgresql,cache}`、`internal/modules/document/evaluation`、`internal/modules/space`、`cmd/tools/spacectl`、`cmd/document-index-worker`、`cmd/document-index-admin`、`cmd/document-search-eval`、三件旧配置类型与其验签脚本、`connection.ServiceDocument`、`common/base/cache.PartitionDocuments`、mixin-search 的三个手工调试命令。
- 已删除的库对象与初始化内容：`public.*` 旧文档表（`knowledge_spaces`、`space_members`、`documents`、`document_versions`、`document_access_policies`、`document_grants`、`document_search_projection`、`document_index_rebuild_runs`、`document_index_delivery_events`、`document_search_shadow_observations`、`qq_user_bindings`、`group_space_bindings`、`space_audit_events`）与其触发器函数、`deployments/postgresql/sql/service/document/`；`bm25_only_verify.sql` 现在断言这些关系与函数**不存在**。
- Compose 的 `document-index-worker` 与 `document-index-admin` 入口、`apps/gin-backend/Dockerfile` 的对应二进制已移除；`deployments/verify.ps1` 重写为当前架构的门禁编排。

阶段 B 也已完成（2026-09-30，见 [阶段 B 证据](../reports/evidence/phase4/stage-b-vector-flow.md)、[F02 分页修正证据](../reports/evidence/phase4/fix-f02-pagination.md)）：

- `document-search` 接管 `mixin-search` 的**本地哈希向量流程**（embedding → Qdrant collection/alias → 索引/删除 → 重建 → 查询）与 **RRF 混合检索**；Qdrant alias `go_web_document_v1` 已实际创建并被检索使用。
- 评测职责落在 `document-search`：固定数据集 `deployments/evaluation/document-search-v1.json` 在该服务内跑通，同进程多次运行与重建后结果逐字一致。

验收反馈修复（2026-09-30，F01–F07，逐项证据与重新验收见 [验收反馈修复证据](../reports/evidence/phase4/fix-acceptance-f01-f07.md)）：

- **F01** 保存幂等改为持久请求账本 `document_service.document_save_requests`（见 [证据](../reports/evidence/phase4/fix-f01-save-idempotency.md)）；
- **F02** 检索 `total` = 可翻页读到的结果数、`truncated` = 本页之后还有结果，`vector.max_keyword_candidates` 改为 RRF 融合窗口（见 [证据](../reports/evidence/phase4/fix-f02-pagination.md)、[Web 接线证据](../reports/evidence/phase4/fix-f02-web-wiring.md)）；
- **F03** CI 增加 Qdrant 服务与就绪等待、零跳过断言；**F04** go-web 专属数据库账号 `go_web_app` 并纳入隔离检查；**F05** Web 验收脚本的 Cookie 解析跨 PowerShell 版本；**F06** 旧 mixin-search 客户端与死端口退场、旧检索基线移入 Compose profile；**F07** 本文件与结构文档的口径统一。

**算法变化的登记（F07）**：旧 `public.document_search_projection` 上的 ParadeDB BM25（`|||` + `pdb.score`）已随阶段 C 删除；当前正式文档关键词检索是 `document_search.document_index` 上的 tsvector + GIN（`websearch_to_tsquery` / `ts_rank_cd`）。**这是算法替换，不是效果等价**——本轮没有做两者效果对比，检索质量另行验证。

### 0.8 已知限制与遗留决策

- `qq-search` 的 `GetQQRecordState` 曾只按 scope 授权（契约里该请求没有渠道范围字段），因此持有 `qq-searcher` 的调用方可以探测其授权会话之外的记录是否存在。**已解决**：请求新增渠道范围字段并在 §0.10 阶段 A 修复。
- `qq-search` 没有容量上限与账本保留策略：`qq_applied_events` 会持续增长，而它是重建的唯一依据；重建不带载荷，因此"账本有记录但行已丢失"只能计为 `records_failed`。**仍未解决**，登记为依赖。
- `record_id` 是 QQ 检索两张表的主键，因此必须在同一记录类型内跨 Bot 唯一；这是 py-agent 的写入义务，契约的线上身份仍是 `(channel, bot_id, conversation_id, record_id)`。
- `document-service` 曾没有独立的"修改访问策略"用例。**已解决**：`SaveDocument` 的 `authenticated_public` 为 `optional`，presence 即显式改策略，且与版本切换同事务。
- 三个服务共用同一份边界密钥（开发基线）；生产部署应按服务拆分密钥，信封格式不变。
- **重建的来源接口未实现**：`qqsource.v1.QQSourceService/ListQQSourceEvents` 只有契约定义，`qq-search` 的架构测试明确禁止生产代码出现任何源客户端，因此当前重建只重放本地事件账本，不能从 py-agent 重新拉取。登记为依赖，不作为可直接接入的能力。
- **pgvector 是旧验证设施，不是受支持能力**（F07 裁决）：`apps/mixin-search` 的 `-store pgvector` 后端只服务于该 module 自己的三容器隔离验收（`apps/mixin-search/compose.yaml` + `verify-qdrant-control.ps1`），默认业务启动路径不使用它，主业务库 `gin_demo` 里也没有 vector 列（`bm25_only_verify.sql` 断言这一点）。因此它与「业务 schema 不得出现向量列」的门禁**不冲突**：两者作用域不同。若将来要把向量检索放进 PostgreSQL，需要先改这条门禁并把它写成一次有意的决策。
- **旧检索基线仍在仓库但已退出默认启动路径**（F06）：`mixin-search` 与 `control-postgres` 现在位于 Compose profile `legacy-retrieval`，`docker compose up -d` 不再启动它们；需要验证该基线时显式 `docker compose --profile legacy-retrieval up -d mixin-search control-postgres`。`mixin-search` 的文档检索实现与 `chat/v1` 语料仍是阶段 B 的对照基线，未被删除。
- `qq-search` 的 Qdrant 集合仍未创建（`index_collections` 只登记 alias，实际检索走 PostgreSQL tsvector + trigram）。**这与 document-search 不同**：document-search 的 `go_web_document_v1` alias 与集合已实际创建并被查询使用。
### 0.9 本轮改造范围（2026-09-26，已被 §0.10 取代）

本节记录上一轮的临时冻结：只做清理、拆分与结构优化，全文/向量/混合检索实现保持现状，不退场旧文档索引 Worker、Admin、评测与 mixin-search 正式检索链路。

该冻结是**当时**为避免在服务拆分未完成时同时改造检索实现而设的临时边界，不改变 ADR-017 的目标架构。自 2026-09-30 起由 §0.10 的三阶段任务取代：检索能力要被接管到所属新服务，旧实现要在核对后删除。

§0.8 的遗留决策中，下列各项已在本轮契约修订中解决：

- ~~`qq-search` 的 `GetQQRecordState` 只按 scope 授权~~ → 请求新增渠道范围字段并执行包含规则（§0.10 阶段 A）；
- ~~`document-service` 没有独立的“修改访问策略”用例~~ → `SaveDocument` 的 `authenticated_public` 为 `optional`，presence 即显式改策略（§0.10 阶段 A）。

### 0.10 当前三阶段任务（2026-09-30 起）

本节是当前唯一任务顺序，取代 §0.9 的临时冻结。阶段 A 是首个必须完成的业务交付。

#### 阶段 A：修复 Web 文档链路和 Go 查询权限

| 任务 | 负责人 | 交付 | 状态 |
| --- | --- | --- | --- |
| A 文档服务保存用例 | `apps/document-service` | `SaveDocument` 单事务保存（权限、修订号、新版本、活动版本切换、可选策略修改、Outbox 事件）、幂等重放、列表真实公开状态与 `total_count`、事件 `created_at` | **已完成**，30 个集成测试 0 skip |
| B 正式文档检索 | `apps/document-search` | 请求收窄成为实际过滤、主体拥有过滤、命中与总数同条件、`page`/`page_size`、真实展示字段 | **已完成**，23 个集成测试 0 skip |
| C Web 与前端 | `apps/gin-backend`、`apps/simple-frontend` | 保存走完整用例、游标原样透传与前端翻页历史、搜索分页与真实总数、个人搜索所有者过滤、真实公开状态与时间、访问策略修改接通 | **已完成**，前端类型检查与生产构建通过 |
| D QQ 记录状态 | `apps/qq-search` | `GetQQRecordState` 渠道范围校验；范围外与不存在一致结果；消息与文件同规则 | **已完成**，17 个集成测试 0 skip |

阶段 A 验收结果见 [阶段 A 验收证据](../reports/evidence/phase4/stage-a-web-acceptance.md)：新增门禁 `deployments/verify-stage-a-web.ps1` 以真实进程、真实认证跑通 43 条断言（`STAGE_A_WEB=PASS`）；`deployments/verify-source-owned-services.ps1` 为 `ADR017_E2E=PASS`；七个 module 的 build/vet/test 全通过；协议生成物、数据库权限隔离、文档链接检查与前端构建全部通过。

契约修订（主 Agent）：`document.v1` 新增 `SaveDocument`、`DocumentSummary.authenticated_public`、`ListDocumentsResponse.total_count`、`DocumentEventEnvelope.created_at`，`UpdateDraftRequest.authenticated_public` 改 `optional`；`documentsearch.v1` 的 `top_k` 改名 `page_size`、新增 `owned_by_subject_only`、`SearchHit` 增加展示字段、事件请求增加 `created_at`；`qqsearch.v1` 的 `GetQQRecordStateRequest` 新增 `qq_channel_scope`；`packages/serviceauth` 强制 capability 的签发方必须是该 audience 的事实源，并新增两组固定凭证样例。

#### 阶段 B：接管保留的检索能力、索引管理与评测职责（已完成 2026-09-30）

前置：阶段 A 完成。逐项确定**唯一服务负责人**，提取到所属新服务并移除旧应用私有依赖；落实索引、集合、写入账号、删除与重建；用固定数据集验证结果。

结果见 [阶段 B 证据](../reports/evidence/phase4/stage-b-vector-flow.md)。该证据 §9 登记的两个未决项已在验收反馈修复中关闭：

- **Compose 全栈已在容器中实测**（2026-09-30）：默认 profile 8/8 服务 healthy，各 `/readyz` 与容器探针通过，并在**容器形态下跑通同一套 Web 验收 43/43**；旧的 mixin-search 基线与 `control-postgres` 已移出默认启动路径（profile `legacy-retrieval`）。
- **pgvector 口径已裁决**：它是 mixin-search 自身三容器隔离验收的旧验证设施，不是受支持能力，默认启动路径不使用它（见 §0.8）。

仍保留的登记项只有一条：`deployments/evaluation/` 的数据集按只读方式复用，不随检索服务迁移。

**两种 PowerShell 环境的 Web 验收均已通过**（2026-10-01 独立复核）：PowerShell 7.6.5 的本地进程形态与 Compose 容器形态各 **43/43**，Windows PowerShell 5.1 的本地进程形态 **43/43**；脚本另有 `-SelfTest` 覆盖 PS7 的 `HttpResponseHeaders` 与 5.1 的拼接头部两种形状。逐项证据见 [验收反馈修复证据](../reports/evidence/phase4/fix-acceptance-f01-f07.md)。

| 保留能力 | 唯一负责人 | 当前状态 |
| --- | --- | --- |
| 正式文档关键词检索 | document-search（tsvector + GIN）已完成 | 已接管 |
| 正式文档向量集合与 alias | document-search，`go_web_document_v1` / `document-search:documents:v1` | **已接管**（2026-09-30）：本地哈希向量流程（embedding → collection/alias → 索引/删除 → 重建 → 查询）落在 document-search，34 个集成测试 0 skip（真实 PostgreSQL + 真实 Qdrant）；语义模型与效果按原定不在本轮验收 |
| 混合检索（dense/sparse + RRF） | document-search | **已接管**：关键词臂 + 向量臂以 RRF（k=60）融合；`SearchHit.score` 变为融合分数（契约 §6.1.1 已登记）；mixin-search 的实现保留为迁移期基线 |
| QQ 原始消息/文件索引 | qq-search（已独立建模、独立集合） | 已接管 |
| 索引管理与重建 | document-search / qq-search 各自的 `RebuildIndex` + `GetIndexStatus` | **已接管**：旧 worker/admin 入口已随阶段 C 删除 |
| 评测 | document-search | **已接管**：固定数据集 `deployments/evaluation/document-search-v1.json` 在 document-search 内跑通，同进程多次运行与重建后结果逐字一致；旧入口随阶段 C 删除 |

**本地哈希向量的接管登记为“向量流程接管”**：完成 embedding→collection→alias→索引→查询的链路并可用固定数据集复现结果；**语义模型与效果另行验证**，本轮不以检索质量作为接管验收条件。

#### 阶段 C：清理已被替代的旧代码、入口和初始化内容（已完成 2026-09-30）

由主 Agent 统一执行，逐项记录**旧调用者、新负责人、替代入口、验证证据**后再删除。逐项记录见 [阶段 C 证据](../reports/evidence/phase4/stage-c-cleanup-go-web.md)。

- 旧文档应用层与仓储（`apps/gin-backend/internal/modules/document/{application,infrastructure/postgresql,infrastructure/cache,evaluation}`）；
- 旧空间模块与 `cmd/tools/spacectl`；
- 旧 worker/admin/eval（`cmd/document-index-worker`、`cmd/document-index-admin`、`cmd/document-search-eval`）；
- Compose 旧入口与 `apps/gin-backend/Dockerfile` 中对应二进制；
- 旧表、投影、事件与初始化内容（`public.*` 旧文档表、其触发器函数与 `deployments/postgresql/sql/service/document/`）；`bm25_only_verify.sql` 现在断言这些关系与函数**不存在**；
- 不再使用的配置、依赖与测试夹具（三件旧配置类型、三个旧验签脚本、`connection.ServiceDocument`、`cache.PartitionsDocuments`、`go mod tidy -diff` 无孤儿依赖）；
- 已被替代的 mixin-search 内容（`cmd/{rag-grpc-client,rag-token,demo}`；pgvector 后端经裁决保留并登记口径冲突）。

删除前必须确认对象属于本项目；核对实际数据库账号，使文档所有权在代码与运行权限上同时成立。

### 0.11 上一轮改造范围（历史）

## 历史计划

原 §1—§17：见 [历史实施计划](../history/LEGACY_IMPLEMENTATION_PLAN.md)。
