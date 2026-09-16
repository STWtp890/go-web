# ADR-007：Qdrant 控制投影与候选级过滤

- 状态：已接受
- 日期：2026-09-14

## 背景

`mixin-search/v1` 要求检索只返回活动、未删除且满足公开、空间或显式文档授权的版本。P2.1 已把权威索引控制状态持久化到 PostgreSQL，但 Qdrant 原有 payload 只有块内容与内部文档键，dense/sparse 查询也没有过滤条件。仅在召回后做进程内过滤虽然能阻止越权结果返回，却会让无权、非活动或其他控制 namespace 的高分块占满固定候选集，造成系统性漏召回。

Qdrant 与控制 PostgreSQL 之间没有共享事务。Qdrant 仍是可重建的向量派生存储，不成为文档、授权或生命周期事实源。

## 决策

### 1. Qdrant 保存规范化控制投影

每个 chunk payload 保存：

- 内部隔离键：`storage_domain`、`storage_id`；
- 文档身份：`document_id`、`version_id`、`owner_space_id`；
- 完整访问快照：`authenticated_public`、`granted_space_ids`；
- 生命周期状态：`active`、`tombstoned`、`activation_revision`、`access_revision`、`lifecycle_revision`；
- 完整性字段：`content_sha256`；
- 原有块内容、位置、格式、来源和章节字段。

新写入向量默认 `active=false` 且公开/授权集合为空。只有从持久化控制状态生成的投影可以把块变为正式候选。用于等值过滤的 storage、文档、版本、空间、公开、活动和墓碑字段建立 Qdrant payload index。

`storage_id` 继续是向量副作用与清理的稳定键；`storage_domain` 来自 `ControlStore.StorageDomain()`。即使多个控制 namespace 共享一个 collection，查询也不会吸收其他 namespace 的候选。

### 2. 正式搜索先同步投影，再选择候选

`SearchDocuments` 按以下顺序执行：

1. 从 ControlStore 刷新最新 generation。
2. 先把到期 pending write 转成持久化 pending delete，再尝试物理清理。
3. 从当前版本、清单、访问快照和 pending delete 构造完整 Qdrant 控制投影；不可映射的待删除块投影为墓碑。
4. 等待 Qdrant payload 更新完成；任一更新失败时停止本次搜索，不使用可能过期的候选状态。
5. dense 与 sparse 查询使用完全相同的过滤条件后再执行 RRF。
6. 返回前由 `DocumentIndexService` 再次复核活动版本、墓碑和授权。

因此，版本激活、授权撤销、版本/文档删除和更高 lifecycle 重新发布最迟在下一次正式搜索准入时更新 Qdrant 投影，并在候选选择前生效。P2.2 不把 Qdrant 更新并入写 RPC 的控制状态事务，也不改变 ADR-006 的成功提交定义。

### 3. 授权条件使用 Must 与最少一项 OR

所有正式 Qdrant 查询必须同时满足：

- `storage_domain` 等于当前控制存储域；
- `active=true`；
- `tombstoned=false`。

并至少满足以下一项：

- `authenticated_public=true`；
- `document_id` 命中 `allowed_document_ids`；
- `owner_space_id` 命中 `allowed_space_ids`；
- `granted_space_ids` 与 `allowed_space_ids` 相交。

两个 allow-list 都为空时，OR 集合只保留公开条件，因此只可能返回公开文档。dense 与 sparse 不允许使用不同的授权过滤器。

### 4. 契约复核和有界回填继续保留

Qdrant payload 是派生投影，正式返回仍以当前进程刚加载的控制状态复核。初始融合候选数取 `max(top_k * 2, 16)`；若复核后不足且底层候选未耗尽，则按两倍增长，最大到 `min(max(top_k * 16, 128), 800)`。每轮保持底层分数/RRF 顺序，并按公开 chunk ID 稳定去重。

这一策略防止固定小候选集造成可复现漏召回，同时给最坏查询成本设置明确上限。若到达上限仍不足，响应通过 `truncated=true` 表示候选可能尚未耗尽。

### 5. 后端能力保持显式

基础 `VectorStore` 继续支持 memory、Qdrant 和 pgvector。只有实现 `ControlledVectorStore` 的后端会接收控制投影并执行候选级过滤；P2.2 只把 Qdrant 验证为正式候选语义。memory 与 pgvector 保留契约层过滤兼容，但不能据此宣称完成生产候选级 ACL 下推。

## 结果

- 未授权、非活动、墓碑和其他 storage domain 的 Qdrant 块在 dense/sparse 候选选择前被排除。
- 空 allow-list 保持“仅公开”语义；owner space、授权空间和显式文档三类入口保持 OR 语义。
- 控制投影同步失败时搜索失败关闭，不回退到无过滤 Qdrant 查询。
- 契约层复核继续承担纵深一致性检查；有界回填降低派生状态短暂不一致或非 Qdrant 后端过滤造成的漏召回。
- P2.2 不包含 gin-backend Outbox、跨服务重试/对账、根 Compose 影子索引或正式读取切换；这些仍属于 P2.3 以后。

## 验证

- `go test ./... -count=1` 与 `go vet ./...` 在 `apps/mixin-search` 通过。
- `verify-qdrant-control.ps1` 使用随机 Compose project、随机回环端口和一次性空数据卷启动固定版本 `qdrant/qdrant:v1.19.1`，等待 TCP 健康检查后执行真实集成测试。
- 真实 Qdrant 测试覆盖基础 dense/sparse、规范化 payload、空 allow-list、owner/granted space、显式文档、授权撤销、非活动版本、版本切换、删除、重新发布和共享 collection 的 storage domain 隔离，并输出 `P2.2_QDRANT_CONTROL=PASS`。
