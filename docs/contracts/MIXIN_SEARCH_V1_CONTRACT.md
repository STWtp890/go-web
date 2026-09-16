# mixin-search/v1 文档索引契约

> 状态：P1.5 定型契约，P2.1 持久化控制面与 P2.2 Qdrant 候选过滤已落地；P3.1 已建立调用方 capability 边界（见 5.1）
> 日期：2026-09-14（2026-09-17 澄清授权语义并补入调用边界）
> Proto：`packages/proto/mixin-search/v1/mixin-search.proto`

## 1. 适用范围

契约路径使用 `mixin-search/v1`，Protobuf 完整服务名为 `mixin_search.v1.RAGService`。该服务只负责正式文档版本的派生索引，不承载聊天语料、用户体系、空间成员关系或文档发布流程。

`go-web` 是文档、版本、归属空间、访问策略、授权和生命周期的唯一事实源，负责分配 `activation_revision`、`access_revision`、`lifecycle_revision`，并在完成身份、成员和资源权限校验后计算搜索请求中的 allow-list。该 allow-list 同时决定签发调用方 capability 时写入的已授予范围，服务端据此拒绝任何扩大范围的请求，见 5.1 与 [SERVICE_CALL_CAPABILITY.md](./SERVICE_CALL_CAPABILITY.md)。

`mixin-search` 只保存可重建的文档索引、执行访问快照和索引控制状态，不解释用户、角色、成员或 QQ 身份，也不反向修改业务文档。P2.1 已为控制状态接入独立 PostgreSQL 持久化和 memory 测试适配器；P2.2 已为 Qdrant 接入候选级 ACL、活动版本、墓碑和 storage domain 过滤；P2.3/P2.4 已完成 gin-backend Outbox 消费、自动重试、对账、全量重建和根 Compose 持续影子索引；P2.5 已通过同一 SearchDocuments 契约运行异步影子查询和来源分层评估，未改变协议字段。

## 2. RPC

| RPC | 语义 |
|---|---|
| `IndexDocumentVersion` | 在指定 `lifecycle_revision` 下幂等写入不可变版本；使用 `owner_space_id` 表达真实归属空间；完成后为 `INDEXED`，尚不可检索 |
| `ActivateDocumentVersion` | 同时校验 `activation_revision` 与 `lifecycle_revision`，切换活动版本 |
| `UpdateDocumentAccess` | 使用 `access_revision` 和 `lifecycle_revision` 替换 `authenticated_public` 与 `granted_space_ids` 完整访问快照 |
| `DeleteDocumentVersion` | 在指定 `lifecycle_revision` 下幂等删除指定版本；响应说明是否删除了活动版本 |
| `DeleteDocument` | 以 `document_id + lifecycle_revision` 幂等删除全部派生索引，并留下墓碑 |
| `GetDocumentVersionState` | 查询版本存在性、状态、块数、摘要、`owner_space_id` 及已知修订状态 |
| `SearchDocuments` | 对活动且未被墓碑遮蔽的版本执行公开、空间、显式文档三路 OR 授权后检索 |

## 3. 写入、幂等与 fencing

所有写 RPC 必须携带非空 `operation_id`。同一业务操作重试时必须复用相同 `operation_id`；一个 `operation_id` 不得绑定不同 payload。

每个文档维护三类相互独立的高水位：

- `activation_revision`：只对活动内容版本排序；
- `access_revision`：只对公开性和授权空间快照排序；
- `lifecycle_revision`：对索引存在性、删除与重新发布周期排序；新建文档初值允许为 `0`。

`IndexDocumentVersion` 和 `ActivateDocumentVersion` 携带 `lifecycle_revision`；`UpdateDocumentAccess` 携带 `access_revision`、`lifecycle_revision`、`authenticated_public` 和完整 `granted_space_ids` 快照；两个删除 RPC 均携带 `lifecycle_revision`。

每种操作均遵循以下规则：

- 低于对应高水位的修订返回 `FAILED_PRECONDITION`；
- 修订与已接受操作相同且 payload 相同，返回与首次调用等价的幂等结果；
- 修订相同但 payload 不同，返回 `FAILED_PRECONDITION`；
- 推进一种高水位不得隐式推进或回退另外两种高水位。

服务端还必须按业务键保持幂等。`DeleteDocument` 的业务键是 `document_id + lifecycle_revision`；版本写入和删除还包含 `version_id`；激活和访问更新分别包含对应 fencing revision。同一不可变 `document_id + version_id` 不得被另一份内容覆盖。

`content_sha256` 使用原始请求字节计算。调用方提供该值时服务端必须校验；调用方省略时服务端计算并返回。

## 4. 状态、删除与重新发布

```text
INDEXED --ActivateDocumentVersion--> ACTIVE
   |                                     |
   +------ DeleteDocumentVersion <-------+

ACTIVE --DeleteDocument--> TOMBSTONED
TOMBSTONED --Index(higher lifecycle)--> INDEXED
INDEXED --Activate(non-decreasing activation)--> ACTIVE
```

约束：

- 索引成功不等于正式发布；每个文档最多只有一个活动版本；
- `expected_previous_version_id` 非空时按 CAS 语义校验；
- 删除活动版本后，搜索必须立即停止返回该版本；
- `DeleteDocument` 删除全部派生索引，但保留文档墓碑、三类修订高水位和判定重复/冲突所需的最小状态；
- 墓碑存在时，旧 lifecycle 的索引、激活、访问更新或删除请求均不得复活文档；
- 重新发布必须使用高于墓碑的 `lifecycle_revision`，先完成 `IndexDocumentVersion`，再执行 `ActivateDocumentVersion`；仅索引不会恢复可检索状态；
- 重新发布时 `activation_revision` 不得低于既有高水位。内容、访问、生命周期仍分别排序，不因重新发布而互相重置；
- 最新已接受访问快照由 `access_revision` 独立保护；改变公开性或授权空间必须显式调用 `UpdateDocumentAccess`。

## 5. 访问快照与搜索授权

文档始终保留真实 `owner_space_id`。公开访问和持续共享属于访问快照，不会改变文档归属，也不会把文档移动到虚拟公共空间。

`UpdateDocumentAccess` 是完整快照替换而不是增量补丁：

- `authenticated_public` 表示已认证调用方可公开读取；
- `granted_space_ids` 是当前持续授权空间的完整集合，遗漏已有空间表示撤销该授权；
- 相同 `access_revision` 下，集合顺序或重复项不得改变业务结果；规范化后的不同快照属于冲突 payload；
- mixin-search 只执行快照，不自行查询或推断空间成员关系。

`SearchDocuments` 对每个活动且未删除的候选文档应用以下 OR 授权，满足任一条件即可进入结果集：

1. 文档访问快照的 `authenticated_public=true`；
2. 请求 `allowed_space_ids` 与文档 `{owner_space_id} ∪ granted_space_ids` 存在交集；
3. 文档 ID 被请求 `allowed_document_ids` 显式命中。

两个 allow-list 都允许为空；此时搜索仍可返回公开文档，但不得返回只依赖空间或显式文档授权的文档。空 allow-list 不再单独构成 `INVALID_ARGUMENT`。

### 5.1 调用授权与候选过滤的区别

本契约当前**假设调用方已经正确计算 allow-list**，并且**没有证明调用方是否有权声明这些范围**。`SearchDocuments` 直接用请求中的 `allowed_space_ids` / `allowed_document_ids` 与文档访问快照做 OR 授权，因此 allow-list 目前是**授权输入**，而不是**待证明的授权主张**。

准确表述是：

> `mixin-search` 完成了文档**候选过滤**（未授权、非活动、已删除内容不会进入结果集，访问快照按事实源给定值执行）。**调用方身份认证与授权范围证明已由 [SERVICE_CALL_CAPABILITY.md](./SERVICE_CALL_CAPABILITY.md) 在传输边界建立**：调用方必须携带由 `go-web` 签发的短时 capability，且请求范围必须包含在已授予范围内。

因此“搜索结果正确”仍然不等于“`mixin-search` 完成了最终调用授权”。区别在于：

- **调用授权**由 `go-web` 在签发 capability 时完成，它决定某个用户此刻可以在哪些空间和文档上检索；
- **候选过滤与范围包含校验**由 `mixin-search` 完成，它保证结果集不含未授权内容，并拒绝任何超出已授予范围的请求；
- Model 只选择检索意图，两条链路都不接受它的权限判定。

在 capability 边界建立之前，唯一调用方是 `gin-backend` 的索引 Worker 与影子链路，allow-list 由事实源在自身权限校验之后产生，且根 Compose 只绑定 `127.0.0.1`。这是当时的时序事实，不是可依赖的安全属性，已于 P3.1 被显式校验替代。

搜索还必须：

- `top_k=0` 使用默认值 3，有效上限为 100；
- 只返回活动且未删除的版本；
- 返回文档、版本、`owner_space_id`、来源、内容摘要和定位信息；
- 不提供未经授权的跨空间默认搜索或聊天语料回退。

## 6. 错误映射

| 场景 | gRPC 状态 |
|---|---|
| 缺少字段、摘要不匹配、不支持的文件类型、非法 `top_k` | `INVALID_ARGUMENT` |
| 激活不存在或尚未索引的版本 | `NOT_FOUND` |
| 业务 CAS 失败、低修订、同修订冲突 payload、墓碑后的旧事件、不可变版本内容冲突 | `FAILED_PRECONDITION` |
| 控制状态 generation CAS 失败，即另一实例已先提交 | `ABORTED` |
| 控制存储不可读/不可写、generation 回退或持久化快照损坏 | `UNAVAILABLE` |
| 上下文取消或超时 | `CANCELLED` / `DEADLINE_EXCEEDED` |
| 其他向量存储或处理失败 | `INTERNAL` |

## 7. 实现与验证边界

### P1.5 历史边界

P1.5 的 `DocumentIndexService` 通过进程内控制状态验证七个 RPC、`operation_id` 载荷绑定与首次响应重放、三类高水位、完整访问快照、墓碑和重新发布语义。memory store 用于单元测试和本地演示；Qdrant、pgvector 可以继续保存文档块，但本阶段不以它们证明生产级 ACL 或 fencing。

P1.5 时进程重启后内存控制状态不会恢复，因此所有版本均视为非活动并停止返回。该限制已由 P2.1 的持久化控制面替代。

### P2.1/P2.2 当前边界

`rag-server` 默认使用由 mixin-search 独立拥有的 PostgreSQL 控制存储，持久化文档清单、版本映射与指纹、三类修订高水位、访问快照、墓碑、`operation_id` 首次结果以及未完成的向量写入/删除意图。持久化模型位于业务 Service 内部，不复用 Protobuf DTO。memory 控制存储仅用于单元测试和显式本地演示，不承诺进程重启恢复。PostgreSQL schema 可幂等创建，但新的控制 namespace 必须通过显式 bootstrap 创建，避免拼写错误静默形成空控制面。

索引采用持久化 `pending_vector_write` 租约、向量写入、最终控制提交的两阶段顺序；删除先持久化逻辑删除、墓碑、首次响应与 pending delete，再清理物理向量。具体提交顺序和故障收敛见 [ADR-006](../adr/006-mixin-search-control-state-commit-order.md)。

每次请求都以持久化 generation 为判定基础。控制存储 generation 冲突返回 `ABORTED`，调用方使用相同 `operation_id` 重新读取并重试；业务 revision/CAS 冲突返回 `FAILED_PRECONDITION`；无法证明控制状态有效时返回 `UNAVAILABLE` 并停止读写。

Qdrant payload 保存规范化控制投影；每次正式搜索在读取最新持久化 generation 并收敛 pending 操作后，先同步投影，再在 dense/sparse 两路候选选择中统一下推 `storage_domain + active + not tombstoned` 和三路 OR 授权。返回前仍执行相同的契约层复核；候选不足时有界扩大召回并按公开 chunk ID 稳定去重。完整门禁见 [ADR-007](../adr/007-qdrant-control-projection-and-filtering.md)。pgvector 与 memory 不作为 P2.2 生产候选级过滤的证明对象。

控制状态和向量索引可以由 `go-web` 事实源重放重建；P2.1 验证服务端持久化、幂等恢复和接受事实重放的边界，P2.3 已完成 gin-backend 的持久化 Outbox、自动重试、对账和全量重建编排，P2.4 已在根 Compose 持续运行影子索引，P2.5 已在 Outbox 收敛后复核影子结果的权限、生命周期、活动版本与正式 SearchMine 范围。当前 KEEP_BM25 结论不改变本 v1 契约。

契约测试至少覆盖：`operation_id` 同载荷精确重放、跨载荷/跨 RPC 改绑冲突、重复业务键、同修订冲突 payload、三类低修订事件、乱序事件、删除后迟到索引/激活、撤销授权后迟到授权、空 allow-list 的公开搜索、显式文档授权、更高 lifecycle 下先索引再激活的重新发布、Qdrant payload/候选过滤、授权撤销、版本切换、回填和 storage domain 隔离。

协议修改后必须重新生成 `packages/gen/mixin-search/v1`，并通过 `packages/proto/verify-generated.ps1` 与 `apps/mixin-search` 的测试和静态检查。
