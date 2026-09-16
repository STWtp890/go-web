# ADR-013：控制面不可变快照与后台投影收敛

> 状态：已接受
> 日期：2026-09-17
> 决策范围：阶段 3 的 P3.2
> 相关决策：[ADR-006](./006-mixin-search-control-state-commit-order.md)、[ADR-007](./007-qdrant-control-projection-and-filtering.md)、[ADR-012](./012-multi-consumer-search-boundary-and-critical-path-shift.md) 决策 4

## 背景

`DocumentIndexService` 原先用一个 `sync.RWMutex` 保护进程内控制状态，并且：

- **每个** RPC 开头都执行 `controlStore.Load()` 全量加载，再做一次 JSON 往返深拷贝；
- 所有 RPC（包括 `SearchDocuments`）都取**排他** `Lock()`，`RWMutex` 的读并发从未被使用；
- 持锁期间发起网络调用：控制存储读写、`SyncDocumentControls`（写 Qdrant）、补召回 dense/sparse 查询（读 Qdrant）；
- 控制投影由**每次搜索**同步，因此单次查询的延迟包含一次全量投影写入；
- 写入失败靠 `restoreControlStateLocked(before)` 回滚内存状态。

结果是单实例内检索、写入、控制状态加载与投影同步全部可能串行，且每次请求的控制面开销近似随**全部**文档状态增长。这是 [ADR-012](./012-multi-consumer-search-boundary-and-critical-path-shift.md) 决策 4 列为接入门禁的第二项。

## 决策

### 1. 控制状态以不可变快照发布

服务持有 `atomic.Pointer[controlSnapshot]`。快照一经发布就不再修改；读 RPC 只做一次原子加载，**不取任何锁**。

写 RPC 在独立的 `writeMu` 上串行（该锁只约束写入者，不阻塞读者），把变更应用到快照的**私有副本**，通过控制存储的 generation compare-and-swap 持久化，**成功后才发布**。

由此：写入失败不再需要回滚路径——未发布的副本直接丢弃，读者也不会观察到从未持久化的状态。

### 2. 读路径只探测 generation，不再全量加载

`ControlStore` 增加 `Generation(ctx)`，PostgreSQL 实现只读取 generation 列，不传输也不解码控制面 payload。

读请求的稳态开销是：一次原子快照加载 + 一次 generation 探测。只有 generation 变化（例如另一个实例提交了写入）或快照仍需要控制面维护时，才会重新加载。

**读写锁分离**：重新加载由 `reloadMu` 串行，而不是写入者的 `writeMu`。否则另一个实例的写入会让本实例的读请求排在**本实例正在进行的向量写入**之后——那正是本次要消除的耦合。

**维护是机会式的**：只有 lease 已过期的写入意图或待清理的删除才算“需要维护”；lease 仍有效的写入意图属于正在进行的写入者，读者不会因为它去抢写锁。需要维护时读者用 `TryLock` 尝试，抢不到就直接用当前快照返回，把清理留给写入者或后续请求。因此读请求永不等待写入者。

这保留了原有的跨实例新鲜度语义：另一个实例撤销授权或删除文档后，本实例的**下一次**请求就能观察到（可执行证据见 `TestRequestRefreshObservesExternalAccessRevocationAndTombstone`）。

**发布单调**：`publish` 用 compare-and-swap 保证 generation 不回退。否则一个在写入提交前读到旧状态的加载者，可能在提交之后把更旧的快照重新发布出去。

### 3. 投影同步移出读路径，由后台 reconciler 收敛

新增投影 generation 跟踪与后台 reconciler：写入提交后主动唤醒，另外按固定间隔兜底检查。reconciler 把 Qdrant 控制投影收敛到最新已发布 generation，多次变更合并为一次投影写入。

搜索在候选选择前**确认**投影已追上本次所用的快照；未追上时才自行完成这一次收敛（同一时刻只允许一次投影写入）。因此：

- 稳态搜索不执行投影写入；
- 投影写入失败时搜索仍然失败关闭，并且不执行召回，与 P2.2 的行为一致；
- 未启动 reconciler 的场景（单元测试、显式本地演示）行为不变，因为回退路径保留了原有语义。

### 4. 待收敛的向量意图由同一维护路径处理

到期的 pending write 仍需先转成持久化 pending delete 再物理清理。该维护只在 lease 已过期或存在待清理删除时触发（内存判断，稳态零成本），且是机会式的（见第 2 节），因此既不会退化为每请求开销，也不会让读者排在写入者之后。

## 结果与权衡

- 读请求之间不再互相阻塞，也不再被写请求的向量 I/O 阻塞：`TestSearchesAreNotBlockedByAnIndexWriteInFlight` 把一次向量写入挂起，同时断言搜索仍然返回；并发查询吞吐随并发度提升（`TestConcurrentSearchesRunInParallel` 断言向量存储内同时存在多个查询）；
- 每次请求的全量控制加载与 JSON 往返被替换为一次 generation 探测；
- 写请求仍然串行，且仍在锁内执行向量写入：本次决策只解除**读**路径的串行，控制状态仍为单行快照，因此“整个控制面必须装进内存”的天花板仍在，按文档行存储（行级 CAS）是后续独立事项；
- 投影的可见性从“搜索准入时必然最新”变为“后台收敛 + 准入时兜底确认”，对外语义不变，但新增了后台 goroutine 与投影 generation 这一状态；
- 跨实例高频写入会让本实例每次读都触发一次重新加载；这与改造前的行为一致，不是新增退化；
- 维护从“每次请求都在锁内做”变成“机会式”，代价是清理可能被推迟到下一个不繁忙的请求，收益是读路径不再有等待写入者的路径。

## 验证

- `TestSearchesProbeGenerationWithoutLoadingTheControlPlane`：写请求之后连续 25 次搜索不再加载控制面，且每次都有 generation 探测；
- `TestConcurrentSearchesRunInParallel`：8 个并发搜索在向量存储内同时执行（`maxInFlight > 1`），并且都不触发控制面加载；
- `TestSearchesAreNotBlockedByAnIndexWriteInFlight`：一次索引写入被挂起在向量存储内时，搜索仍然完成（旧的全局排他锁与旧的维护触发条件都会让该用例超时）；
- `TestLiveWriteIntentDoesNotForceAReaderReload`：未过期的写入意图不被当作待维护；
- `TestProjectionReconcilerConvergesWithoutARequest`：未发起任何请求时，后台 reconciler 把投影收敛到当前 generation；随后的搜索不再写投影；
- `TestSearchStillFailsClosedWhenTheBackgroundProjectionIsBroken`：投影无法写入时搜索失败关闭且不召回；
- P2.1/P2.2 既有契约测试全部保持通过：幂等重放、三类 fencing、墓碑与重新发布、跨实例刷新、过期意图清理、故障关闭；
- 根 Compose 端到端验收（影子索引、影子查询评估、停机与恢复）保持通过，影子查询延迟从约 45–111ms 降至 3–16ms。
