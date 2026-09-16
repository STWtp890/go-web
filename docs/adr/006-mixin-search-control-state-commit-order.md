# ADR-006：mixin-search 控制状态提交顺序与故障收敛

- 状态：已接受
- 日期：2026-09-13

## 背景

`mixin-search` 的控制状态保存在 PostgreSQL，文档块和检索向量保存在可切换的 `VectorStore`。两种存储之间没有共享事务，因此索引、删除、并发更新或进程退出都可能发生在任意一个写入步骤之后。若只依赖进程内回滚，服务重启后无法判断向量写入是否完成，也无法保证删除墓碑先于物理清理生效。

控制状态包括文档清单、版本映射与指纹、三类修订高水位、访问快照、墓碑、`operation_id` 首次结果，以及未完成向量写入/删除的持久化意图。它是可由 `go-web` 事实源重放重建的派生控制面，不改变正式文档所有权。

## 决策

### 1. 控制状态以 generation CAS 提交

每个 namespace 使用一个完整、与 Protobuf DTO 分离的 BYTEA 控制快照。schema 可以幂等初始化，但缺失 namespace 只有在显式 bootstrap 时才创建，避免配置拼写错误静默形成空控制面。写入必须携带读取时的 `generation`，PostgreSQL 仅在该 generation 仍为当前值时提交并递增它。

- generation CAS 失败表示另一个实例已先提交，映射为 gRPC `ABORTED`；调用方应重新读取状态，并使用相同 `operation_id` 重试原操作。
- 业务 CAS、低修订、同修订冲突、墓碑后的旧事件及不可变版本冲突仍映射为 `FAILED_PRECONDITION`，不能与存储并发冲突混为一类。
- 控制存储无法读取/保存、generation 回退或快照校验失败时，服务不能继续使用可能过期的进程内状态，统一失败关闭并映射为 `UNAVAILABLE`。

### 2. 索引采用带租约的两阶段写入

`IndexDocumentVersion` 按以下顺序执行：

1. 刷新持久化控制状态，完成 `operation_id`、生命周期和不可变版本校验。
2. 先以 generation CAS 持久化 `pending_vector_writes[storage_id]`，其中绑定 `operation_id`、规范化载荷指纹和 15 分钟租约。
3. 使用稳定的 `storage_id` 写入 `VectorStore`。
4. 再以 generation CAS 原子持久化版本映射、指纹、清单、高水位与首次响应，同时移除 pending write。

第二次控制提交失败时，持久化 pending write 仍保留，未建立活动版本映射的向量块不会通过契约搜索返回。租约到期后，后续请求刷新控制状态时会尝试删除该孤儿向量并提交清理进度。租约避免其他实例仍在执行向量写入时被提前清理；P2.1 不在 generation 冲突后自动跨实例重放向量副作用。

向量写入本身失败或超过租约时，服务先以 generation CAS 将 pending write 转成 pending delete，再尝试物理清理；后续刷新会继续收敛未完成清理。只有版本映射与首次响应已经持久化后，索引 RPC 才返回成功。

### 3. 删除先提交逻辑事实，再执行物理清理

`DeleteDocumentVersion` 和 `DeleteDocument` 先完成以下同一控制状态提交：

- 移除可检索版本映射及活动版本；
- 推进生命周期高水位，必要时建立文档墓碑；
- 持久化精确重放所需的首次响应；
- 将对应 `storage_id` 加入 `pending_vector_deletes`。

控制提交成功后才删除 `VectorStore` 中的物理块。物理删除失败时，逻辑删除和墓碑已经生效，遗留块不可通过契约搜索返回；pending delete 会在后续请求刷新时继续清理。控制提交失败时不触碰向量存储，因此不会出现“物理数据已删但删除事实未提交”的窗口。

### 4. 恢复与重建边界

服务创建时必须从控制存储恢复完整快照；每个 RPC 在判定和执行前重新加载最新 generation，并处理到期 pending write 与 pending delete。控制存储丢失、不可读或快照损坏时，启动或请求失败关闭，不猜测或恢复活动状态。

控制状态和向量索引仍是派生数据。恢复路径是使用新的空 namespace/集合，从 `go-web` 正式文档、版本、访问策略和生命周期事实按确定顺序重放索引、激活、授权与删除操作。P2.1 保证服务端能够接受幂等事实重放并重新建立控制状态；P2.3 已完成 gin-backend 的持久化 Outbox、自动重试、对账和全量重建编排，P2.4 已在根 Compose 验证持续影子流量与自动恢复。

## 结果

- 活动版本、访问快照、修订高水位、墓碑和首次响应可以跨服务实例恢复。
- 索引的模糊提交收敛为带租约、不可见且可清理的孤儿；删除的模糊物理结果收敛为逻辑上已删除、可重试清理的孤儿。
- 多实例并发通过 generation CAS 检测，不隐式覆盖更新；收到 `ABORTED` 的调用方必须重试。
- PostgreSQL 控制存储是 `rag-server` 默认模式；memory 控制存储仅用于单元测试和显式本地演示，不提供进程重启持久性。
- P2.1 本身不证明 Qdrant 授权和生命周期过滤；该候选级门禁已由后续 [ADR-007](./007-qdrant-control-projection-and-filtering.md) 的 P2.2 实现补齐，但正式检索仍未从 PostgreSQL BM25 切换。

## 验证

- `go test ./...` 与 `go vet ./...` 在 `apps/mixin-search` 通过。
- `verify-control-store.ps1` 使用一次性 PostgreSQL 空数据卷运行 `TestPostgresControlStoreIntegration`，测试通过并输出 `P2.1_CONTROL_STORE=PASS`。
- 单元测试覆盖重启恢复、全部操作首次响应重放、墓碑后的迟到事件、generation CAS、跨实例刷新、控制存储失败关闭、损坏快照拒绝、模糊索引提交清理，以及逻辑删除先于物理清理。
- P2.2 完成后再次执行同一验收，真实 PostgreSQL 测试仍通过且临时环境完成清理。
