# go-web 当前实施计划

> 文档职责：当前唯一阶段排期与实施入口
> 上位目标：[ECOSYSTEM_EVOLUTION_GUIDE.md](../ECOSYSTEM_EVOLUTION_GUIDE.md)
> 相关决策：[ADR-001](../adr/001-search-service-boundary.md)、[ADR-002](../adr/002-document-index-ownership.md)、[ADR-004](../adr/004-bm25-migration-strategy.md)、[ADR-005](../adr/005-development-baseline-over-production-migration.md)、[ADR-006](../adr/006-mixin-search-control-state-commit-order.md)、[ADR-007](../adr/007-qdrant-control-projection-and-filtering.md)、[ADR-008](../adr/008-document-index-transactional-outbox.md)、[ADR-009](../adr/009-shadow-index-compose-and-health-boundary.md)、[ADR-010](../adr/010-shadow-query-evaluation-gate.md)、[ADR-011](../adr/011-bounded-cache-runtime-and-revision-fencing.md)、[ADR-012](../adr/012-multi-consumer-search-boundary-and-critical-path-shift.md)、[ADR-013](../adr/013-immutable-control-snapshot-and-background-projection.md)、[ADR-014](../adr/014-per-corpus-control-plane-isolation.md)
> 当前状态：生态阶段二已收口（P2.0-P2.5 全部通过）；阶段三实施基线已建立，P3.0、P3.0a、P3.1、P3.2、P3.3a 已完成；**P3.3 进行中**——独立聊天契约、独立控制面与持久化、独立 Qdrant alias 与 `_g1` 基线、capability 硬隔离（角色 + audience 双锁）与容器级验收均已落地，未完成项为聊天控制快照容量治理（见 §7.1 的"尚未落地"；蓝绿重建编排属 P3.5）
> 更新日期：2026-09-19

## 1. 当前全局进度结论

阶段 2 已收口，但阶段 3 的实施基线此前尚未建立。准确的全局进度是：

| 生态宏观阶段 | 状态 | 实际完成度 |
| --- | --- | --- |
| 阶段一：现状审计与边界固化 | 已完成 | 边界、事实源、Monorepo 与基础 ADR 已形成 |
| 阶段二：文档知识链路贯通 | 已完成 | 文档事实、Outbox、Qdrant、影子索引与评测闭环已完成 |
| BM25 正式交接 | 未完成 | PostgreSQL BM25 仍是正式读取方 |
| 阶段三：QQ 身份与知识空间融合 | 本计划 | 进行中 |
| 阶段四：聊天记录域 | 未进入 | Chat 代码保留但未启用；尚无聊天检索契约 |
| 阶段五：治理与持续演进 | 未进入 | 只有散落的基础设施，没有系统性阶段计划 |

因此当前工作既不是“继续完成阶段 2”，也不是“立即删除 PostgreSQL BM25”，而是：

> **文档知识的事实传播和影子检索基础已经完成。下一阶段应先把 `mixin-search` 建设为具有可信调用授权、在线并发能力和不中断重建能力的多消费者检索服务，随后接入 QQ 身份与知识空间，形成 `py-agent` 文档知识闭环，再建设独立聊天记录域。**

判断依据和完整决策见 [ADR-012](../adr/012-multi-consumer-search-boundary-and-critical-path-shift.md)。阶段 2 的完成事实、实测证据和当前有效限制只在 [PHASE2_IMPLEMENTATION_LOG.md](../reports/PHASE2_IMPLEMENTATION_LOG.md) 维护，本计划不重复改写历史过程。

### 1.1 为什么先做检索服务，而不是先切 BM25

下列四条是制定本路线时的实测状态（2026-09-17，提交 `c1f0b3b`）。保留它们是为了说明排序理由，不是当前状态：

- `mixin-search` 当时没有调用方身份认证，`SearchDocuments` 把请求中的 allow-list 当作授权输入而不是待证明的授权主张 —— **已由 P3.1 解决**；
- 七个 RPC 当时全部在全局排他锁下执行，且读路径在持锁期间发起 PostgreSQL 与 Qdrant 网络调用 —— **已由 P3.2 解决**；
- 控制投影当时由每次搜索同步，单次查询包含一次全量投影写入 —— **已由 P3.2 解决**；
- 评测集只有 7 条查询，两组 p95 都等于各自最大值，仍不能支撑尾延迟结论 —— 待 B 线处理。

## 2. 阶段 3 目标与边界

阶段 3 服务于生态“阶段三：QQ 身份与知识空间融合”，并在其内部先完成“多语料检索基础设计”，为阶段四铺路而不提前建设整个聊天产品域。

```text
文档/决策基线
  -> 调用身份与授权边界
  -> 在线并发模型
  -> 多语料契约与索引隔离
  -> QQ 身份与知识空间映射
  -> 在线可靠性门禁
  -> py-agent 文档知识闭环
```

本阶段遵循以下边界：

1. `gin-backend` 继续拥有文档事实、用户身份、空间成员关系和最终授权判定输入。
2. `mixin-search` 只拥有可重建的索引块、检索向量、操作幂等记录和索引控制状态。
3. PostgreSQL BM25 在阶段 3 全程保持正式读取方；读取切换属于独立的 B 线工作，不受本阶段排期驱动。
4. 权限、活动版本和删除状态必须由确定性逻辑与存储过滤保证，不能交由模型判断。
5. Model 只负责选择检索意图，不负责授予权限、扩大身份范围或绕过资源校验。
6. 阶段 3 只建立聊天语料的基础边界（契约、集合、生命周期、隔离），不实现聊天保存、采集、Web 查看与知识晋升产品能力。

## 3. 实施顺序

| 实施包 | 目标 | 依赖 | 状态 |
| --- | --- | --- | --- |
| P3.0 | 重建文档与决策基线 | 无 | 已完成 |
| P3.0a | 阶段三治理收口 | P3.0 | 已完成 |
| P3.1 | mixin-search 调用身份与授权边界 | P3.0 | 已完成 |
| P3.2 | 在线检索并发模型 | P3.0 | 已完成 |
| P3.3a | 多语料控制面隔离决策 | P3.2 | 已完成 |
| P3.3 | 多语料契约与索引隔离 | P3.0、P3.3a | 进行中（主体已落地并通过本地与容器验收，未完成项见 §7.1） |
| P3.4 | QQ 身份与知识空间映射 | P3.1、P3.3 | 待推进 |
| P3.5 | 在线可靠性门禁 | P3.1、P3.2、P3.4 | 待推进 |
| P3.6 | py-agent 文档知识闭环 | P3.5 | 待推进 |
| B 线 | BM25 正式交接 | P3.2、P3.5、真实语义评测 | 可并行，后置 |

P3.1 与 P3.2 都以 P3.0 为前置且彼此独立，可以并行推进。P3.3 必须在 P3.3a 完成后开始：控制面关系没定下来就写聊天契约，会把跨语料耦合固化进协议（见 [ADR-014](../adr/014-per-corpus-control-plane-isolation.md)）。P3.5 是 `py-agent` 正式依赖前的统一门禁，未通过不得进入 P3.6。

P3.0a 与 P3.3a 是补充实施包，编号不使用外部方案的历史编号：前者收口阶段三自身的文档治理，后者是 P3.3 编码前的架构阻断项（见 [ADR-014](../adr/014-per-corpus-control-plane-isolation.md)）。

## 3.1 P3.0a：阶段三治理收口

### 目标

让阶段三的自身进度也遵守 P3.0 确立的文档规则：证据可追溯、状态只有一个来源、检查脚本与它的注释一致。

### 任务

- 建立 `docs/reports/evidence/phase3/`，归档 P3.1a 与 P3.2 的最终验收集，并记录来源提交、验收范围与结果；
- 修复实施过程中产生的状态漂移（上位指南、本计划、项目结构各处的过时进度表述）；
- 上位指南与文档导航不再重复维护实施包编号与完成进度，只保留指向本计划的入口；
- `docs/check-doc-links.ps1` 明确排除 `.agents/` 第三方技能包，并分别报告项目文档数与跳过数。

### 验收

- `DOC_LINKS=PASS`，且输出中项目文档数与第三方跳过数分别可见、与实际一致；
- 仓库内不存在与本计划冲突的进度表述；
- ADR-013 与 ADR-014 至少被上位指南或文档导航引用一次；
- 阶段三验收报告可从 `docs/reports/evidence/phase3/` 直接打开。

### 完成状态

P3.0a 已于 2026-09-17 完成。`docs/reports/evidence/phase3/` 归档了 P3.1a（验收提交 `54cc1c8`）与 P3.2（验收提交 `8e7abea`）各三个报告族的 12 个文件，并以 `README.md` 记录来源、范围与结论；修复了上位指南、本计划与项目结构中的四处状态漂移，把 §1.1 改写为“制定路线时的实测状态”而非当前状态；上位指南与文档导航改为只保留指向本计划的入口；链接检查明确排除 `.agents/`，分别报告项目文档数与第三方跳过数，最终输出 `DOC_LINKS=PASS`（**具体数量随文档增删变化，不在此固定记录**）。

## 4. P3.0：文档与决策基线重建

### 目标

让 `docs/` 重新成为唯一可信的计划入口，并把最新架构结论从外部评估提升为治理树内的决策。

### 任务

- 新增 [ADR-012](../adr/012-multi-consumer-search-boundary-and-critical-path-shift.md)，确定消费者因果链、两个转折点、可重建与在线重建的区别、首个消费者接入前的门禁，以及 BM25 回退投影的退出条件；
- 修订 [ADR-001](../adr/001-search-service-boundary.md) 背景，补全“为何必须独立部署”的完整理由；
- 将本计划从阶段 2 回顾重写为阶段 3 执行计划；
- 恢复阶段报告引用的不可变证据，并调整清理策略，使正式文档不再依赖会被清理的制品；
- 将本地文档链接检查加入仓库验证流程；
- 明确“文档契约”和“调用授权契约”的区别。

### 验收

- `docs/check-doc-links.ps1` 输出 `DOC_LINKS=PASS`，断链为 0，且 `docs/` 下不存在指向 `deployments/test-results/` 的链接；
- 阶段 1 与阶段 2 报告引用的运行产物可在 `docs/reports/evidence/` 下直接打开；
- 本计划只描述阶段 3 的范围、顺序和门禁，不再承载阶段 2 排期；
- ADR-012 覆盖拆分因果链、两个转折点、重建语义、接入门禁、回退资产定位和多语料隔离六项决策。

### 完成状态

P3.0 已于 2026-09-17 完成。新增 ADR-012 并修订 ADR-001；阶段 1 与阶段 2 报告引用的 26 个运行产物已从提交 `ac01d7e` 固化到 `docs/reports/evidence/phase1/` 与 `docs/reports/evidence/phase2/`，23 处断链全部修复；新增 `docs/check-doc-links.ps1` 并接入 CI hygiene 作业与 `deployments/verify.ps1`；`deployments/prune-test-results.ps1` 增加“被受版本控制 Markdown 引用的制品不删除”保护；v1 契约与上位指南同步澄清授权语义与阶段三前置。

## 5. P3.1：mixin-search 调用身份与授权边界

### 目标

在第一个正式在线消费者接入前，使 `mixin-search` 具备可信的调用方身份与授权范围校验，并确保调用方**只能缩小、不能扩大**可检索空间。

### 任务

- 建立服务调用身份认证，未认证调用失败关闭；认证选型在实现时确定，但必须支持独立于网络位置的调用方区分；
- 区分 `go-web`、`py-agent` 和运维/开发调用方三类主体；
- 将索引写入权限与检索权限分离：写 RPC 只允许 `gin-backend` 的索引 Worker 身份调用；
- 为检索建立范围 capability：由事实源签发短时、限定用户/空间/文档范围的凭证，`mixin-search` 校验 `requested ⊆ granted`，超出即拒绝；
- 增加调用审计（调用方、用户、范围、结果量级）、按调用方的限流与最小暴露面（含 reflection 的取舍）；
- 更新 v1 契约文档中的授权语义表述，明确“候选过滤”与“调用授权”的区别。

### 验收

- 未携带有效身份的调用被拒绝，且不返回任何候选；
- 构造超出已授予范围的请求时返回拒绝，而不是被静默裁剪后按扩大范围检索；
- 索引写入 RPC 在检索身份下被拒绝，检索 RPC 在越权范围下被拒绝，两类用例都有测试；
- 审计记录可回答“谁在什么范围内发起了哪次检索”；
- 开启认证后，`gin-backend` 的增量投递、对账、全量重建与影子查询链路仍全部通过原有回归；
- 契约文档不再宣称 `mixin-search` 已完成最终调用授权。

### 完成状态

P3.1 已完成，调用边界契约见 [SERVICE_CALL_CAPABILITY.md](../contracts/SERVICE_CALL_CAPABILITY.md)：

- `mixin-search` 新增 `internal/security`（capability 签名与校验、角色、范围包含判定、按调用方限流、结构化审计）与 `internal/transport/grpc/auth.go` 一元拦截器；缺少有效能力凭证返回 `UNAUTHENTICATED`，角色不符与范围越界返回 `PERMISSION_DENIED`，超出预算返回 `RESOURCE_EXHAUSTED`；
- 索引写入（`index-writer`）、检索（`searcher`）与只读运维（`ops`）是三个独立角色，写 RPC 不接受检索身份，`SearchDocuments` 不接受写入身份；
- 范围判定是包含而非取交集：请求中出现任何未授予标识即整体拒绝，不做静默裁剪；写入角色签发的 token 不携带范围，因此不能当作受限检索重放；
- 服务端在缺少边界密钥时拒绝启动；密钥由 `deployments/bootstrap.ps1` 随机生成到 `deployments/secrets/mixin_search_capability.key`，不入库、不入镜像，由根 Compose 只读挂载；`reflection` 增加开关并在根 Compose 中关闭；健康检查刻意不要求凭证，容器探针无需持有密钥；
- `go-web` 侧在 `mixinsearch` 适配器逐调用签发 capability，配置新增 `mixin_search_security` 段；索引 Worker、对账、评测与影子查询均已携带凭证；
- 两侧各有一条相同的 golden vector 测试固定凭据格式，任一侧改动格式都会让另一侧失败；`mixin-search` 新增的测试覆盖未认证拒绝、越权范围拒绝、读写角色交叉拒绝、限流与审计记录。

尚未建立的部分记录在契约第 8 节：`py-agent` 的 capability 签发入口属于 P3.4/P3.6，传输加密与按用户配额属于后续在线暴露前的加固项。

收口补丁（P3.1a）在复核后进一步收紧两处边界：

- `Verifier` 必须显式配置**可信签发方**并与 audience 一起做等值校验，空 issuer 或非 `go-web` issuer 的已签名 token 一律拒绝（`ErrWrongIssuer`）；服务入口新增 `-capability-issuer`（默认 `go-web`），根 Compose 显式传入；
- 限流调用方状态表改为**硬上限**（1024 个桶）：满表时先回收空闲桶，仍然满则拒绝新调用方而不是继续扩容；已有调用方的剩余预算不受影响；审计记录区分“调用方超出自身预算”与“调用方表已满”，两者共用 `RESOURCE_EXHAUSTED` 但 `detail` 不同。

## 6. P3.2：在线检索并发模型

### 目标

使 `mixin-search` 从影子服务具备升级为在线服务的结构条件：读路径不再被控制面全量加载和全局排他锁串行化。

### 任务

- 取消每请求全量加载控制状态：改为维护投影 generation，仅在过期时同步；
- 去掉读路径的全局排他锁，使并发查询不互相阻塞，写入按文档键串行；
- 将 `SyncDocumentControls` 移出搜索请求路径，改由后台 reconciler 收敛；
- 避免在持锁期间发起 PostgreSQL 与 Qdrant 网络调用；
- 保持索引写入与在线查询互不阻塞，且不破坏既有幂等、fencing、墓碑与重建语义。

### 验收

- 查询路径不再发起控制存储全量加载，并有测试或计数断言证明；
- 并发查询的吞吐随并发度提升，而不是被服务内单锁串行化；
- 并发索引写入期间的查询延迟与吞吐纳入门禁（与 P3.5 共用同一套测量）；
- 既有契约测试全部通过：幂等重放、三类高水位、墓碑、重新发布、候选过滤语义不回归。

### 完成状态

P3.2 已完成，并发模型见 [ADR-013](../adr/013-immutable-control-snapshot-and-background-projection.md)：

- 进程内控制状态改为**不可变快照 + 原子发布**（`snapshot.go`）：读 RPC 只做一次原子加载，不取锁；写 RPC 在写入锁内对私有副本执行 compare-and-swap，持久化成功后才发布，因此不再需要回滚路径，读者也不会看到从未持久化的状态；发布按 generation 单调，旧加载结果不会覆盖新状态；
- `ControlStore` 增加 `Generation(ctx)`，PostgreSQL 实现只读取 generation 列；读路径只在 generation 变化或快照确需维护时重新加载控制面。跨实例新鲜度不变：另一实例的授权撤销/墓碑仍在下一次请求被观察到（`TestRequestRefreshObservesExternalAccessRevocationAndTombstone` 保持通过）；
- 重新加载由独立的 `reloadMu` 串行，而不是写入者的 `writeMu`，因此读请求不会排在**本实例正在进行的向量写入**之后；维护（围栏到期意图、清理待删除向量）只在 lease 已过期或存在待清理删除时触发，且用 `TryLock` 机会式尝试，抢不到就用当前快照返回；
- 投影同步移出搜索路径：新增投影 generation 跟踪与后台 reconciler（写入提交后唤醒，另有兜底间隔），搜索只确认投影已追上所用快照，未追上时才完成这一次收敛。投影写入失败时搜索仍失败关闭且不执行召回，与 P2.2 语义一致；
- 新增证据测试：搜索不再加载控制面（25 次搜索 0 次加载）、8 个并发搜索在向量存储内同时执行、**一次索引写入挂起在向量存储内时搜索仍然完成**、未过期的写入意图不被当作待维护、后台 reconciler 在无请求时完成收敛、投影损坏时搜索失败关闭且不召回。

本次只解除**读**路径的串行。控制状态仍是单行快照，“整个控制面必须装进内存”的天花板与按文档行存储（行级 CAS）留作后续独立事项，不阻塞 P3.3。

## 7. P3.3a：多语料控制面隔离决策

### 目标

在写第一行聊天契约代码之前，确定聊天语料与文档语料在**控制面**上的关系，避免把跨语料耦合固化进协议与实现。

### 任务

- 记录 [ADR-014](../adr/014-per-corpus-control-plane-isolation.md)：文档与聊天拥有独立控制面实例、独立 generation、独立持久化状态、独立投影 reconciler 与独立索引 alias；
- 明确可复用的是机制（不可变快照、CAS、后台收敛、capability 边界），不可复用的是运行状态；
- 明确联合检索只在查询编排层融合，控制面不合并语料；
- 把 ADR-014 的最低验收条件写入 P3.3 的验收。

### 验收

- ADR-014 覆盖控制面隔离、机制复用边界、联合检索位置、索引与生命周期隔离、授权隔离五项决策；
- P3.3 的验收包含 ADR-014 的最低验收条件；
- 文档契约与上位指南都指向该决策，后续实现不需要再讨论“是否共享一套控制状态”。

### 完成状态

P3.3a 已于 2026-09-17 完成，决策记录为 [ADR-014](../adr/014-per-corpus-control-plane-isolation.md)。v1 契约 §1 明确拒绝给文档契约增加 `corpus_type`、或让 `SearchDocuments` 接受聊天请求；本计划 P3.3 的验收已并入 ADR-014 的最低验收条件（聊天 generation 变化不刷新文档快照、聊天重建不切换文档 alias、文档授权撤销不等待聊天投影、聊天规模不增加文档快照大小、任一语料故障不污染另一语料生命周期状态）。

## 7.1 P3.3：多语料契约与索引隔离

### 目标

在 `py-agent` 成为正式消费者前确定聊天语料契约与索引边界，但不提前实现完整聊天产品域。控制面关系已由 P3.3a 确定（见 [ADR-014](../adr/014-per-corpus-control-plane-isolation.md)），本实施包只做契约与索引边界。

### 任务

- 文档语料与聊天语料使用**独立领域契约**，不通过给文档 RPC 增加 `corpus_type` 字段实现；
- 为聊天语料建立**独立的控制服务与持久化状态**，与文档控制面不共享 generation 或快照（ADR-014 决策 1、2）；
- 为聊天语料定义独立集合、独立 alias、索引生命周期、删除语义和引用形式；
- 明确聊天消息“已撤回”“已归档”“可检索”三个状态的独立性，不复用文档墓碑语义；
- 联合回答可以同时使用两类结果，但必须保留语料来源，底层不得混为同一结果集合；
- 明确 `storage_domain` 是存储隔离而不是公开语料类型契约，文档与聊天保持硬隔离。

### 验收

- 文档契约无法表达聊天消息、会话时间线、群身份或消息撤回；
- 聊天索引集合可以独立重建和独立删除，不影响文档检索；
- 联合结果中的每一项都能标明语料类型、事实来源与时间或版本信息；
- 撤回、归档与索引移除的传播路径有明确契约和测试；
- 满足 [ADR-014](../adr/014-per-corpus-control-plane-isolation.md) 的最低验收条件：聊天 generation 变化不触发文档快照重新加载（以控制存储 `Generation`/`Load` 计数断言）、聊天索引重建不切换文档 alias、文档授权撤销不等待聊天投影收敛、聊天语料规模增长不增加文档控制快照大小、任一语料故障不污染另一语料的生命周期状态。

> 验收现状（2026-09-19 核查后更新）：第 1、3、4、5 条有可执行证据（`internal/chat/isolation_test.go`），其中第 1、4 条另有容器与真实 PostgreSQL 证据，并已进入整栈门禁的自动断言；第 2 条自"第八片"起由**真实的 alias 机制与直接映射断言**保证（切换前后记录映射、聊天切到空世代后检索为空、文档 alias 不变、切回恢复），不再是"集合名独立"的弱形式。audience 按语料划分已于 2026-09-19 落地（见"第七片"）；容量上限确定后本实施包才能关闭（蓝绿重建编排本身仍是 P3.5）。

### 进展

P3.3 **仍在进行中，不能定性为"全部完成"**：主体功能与独立语料链路已落地并通过本地单元/架构门禁、进程内端到端与容器内验收（见下"容器级验收"），但 ADR-014 的三项要求尚未落地——Qdrant alias 的蓝绿原子切换（决策 1、4）、capability audience 按语料划分（决策 5）、以及聊天控制快照的容量上限与迁移触发条件（见"尚未落地"）。因此 P3.3 的验收只能算"主体验收通过"，不是"实施包关闭"。

已落地的部分：

**契约层（第一片）**

- `packages/proto/mixin-search/chat/v1/chat.proto` 定型 `mixin_search.chat.v1.ChatIndexService` 的 7 个 RPC，覆盖消息索引、归档、访问快照、撤回、会话删除、状态查询与检索；
- 契约文档见 [CHAT_SEARCH_V1_CONTRACT.md](../contracts/CHAT_SEARCH_V1_CONTRACT.md)：显式建模"已保存/已索引/已归档"三态独立与四类修订（archive、access、lifecycle、retract）互不推进，并规定聊天没有 `authenticated_public` 对应物、空 allow-list 返回空结果；
- `packages/proto/verify-generated.ps1` 与 CI 的 `generated-proto` 作业改为覆盖两个契约；
- `apps/mixin-search/internal/architecture/contracts_test.go` 把 ADR-014 的隔离要求变成可执行断言：两个服务的 RPC 集合不重叠、任一契约不得出现属于另一语料的字段名、任一契约不得增加 `corpus_type` 一类的语料选择器（该测试在本轮就纠正了我自己一处过宽的断言：`tombstone_revision` 是两侧各自需要的机制名，不属于文档专有语义）。

**控制面（第二片）**

- 新增 `internal/controlplane`：两个语料共用的机制——不可变快照的单调发布（`State`）与派生投影的收敛和后台 reconciler（`Projection`）；该包不持有任何语料状态，并由架构测试强制不得 import 任何 `mixin-search/` 包；
- **两个语料都接入了该机制**：聊天语料从一开始就用它；文档语料的 `internal/rag/projection.go` 已在本轮迁移过来，删掉了自己那份 `projectionMu`/`projectionSynced`/`projectionErr`/`projectionWake` 与 `convergeProjection`/`signalProjection`/`projectionGeneration`，改为 `controlplane.State`（`publish` 用单调 `Publish` + `Signal`，读路径用 `State.Load()`）与 `controlplane.Projection`（`ensureProjection` 走 `Converge`，reconciler 走 `StartReconciler`）。迁移只换机制、不动已验证的 P3.2 语义：文档侧 `internal/rag` 全量测试与 `internal/architecture` 边界测试通过，行为不变（含"reconciler 无请求也能收敛"与"投影失败时检索失败关闭"两条测试）。两套状态仍然完全分离：各自的快照类型、generation、控制存储表与 reconciler 都没有合并；

- 新增 `internal/chat`：聊天语料自己的控制面——会话/消息状态机、四类修订、幂等账本、写入意图围栏、独立 `ControlStore` 端口与内存/PostgreSQL 适配器（独立 `chat_control_states` 表）、以及按自己 generation 收敛的投影；
- 聊天通过三个端口访问向量侧（消息索引、控制投影同步、候选检索），由组合根注入，因此 `internal/chat` 不依赖 `internal/rag`（已由架构测试强制）；
- ADR-014 的五条最低验收条件已有可执行证据（`internal/chat/isolation_test.go`）：聊天活动使文档控制面的 `Load`/`Generation`/`Save` 计数**零增长**且文档控制负载字节数不变、文档活动不推进聊天 generation、聊天规模增长不改变文档控制快照大小、聊天投影故障不影响文档检索、聊天投影被挂起时文档授权撤销与检索照常完成；
- 契约语义测试（`internal/chat/service_test.go`）：索引不等于可检索（未归档检索为空）、撤回后仍被索引且计数、四类修订互不推进、墓碑拒绝迟到事件且删除可重放、operation_id 重放不重复写向量且改绑冲突、消息内容不可变、聊天无公开语料（空 allow-list 不召回）、投影不可用时检索失败关闭、索引失败留下可清理的持久化声明。

**传输与授权（第三片）**

- capability 新增三个聊天角色（`chat-index-writer`、`chat-searcher`、`chat-ops`），与文档角色是不相交的字符串集合，因此一个语料的凭证无法调用另一个语料的 RPC；
- 拦截器的方法策略表覆盖两个服务的全部 14 个 RPC，未登记方法一律拒绝；`SearchChatMessages` 复用与文档相同的**范围包含**校验（请求范围必须是已授予范围的子集），审计记录同时记录请求与已授予范围大小；
- 新增 `internal/transport/grpc/server_chat.go`：聊天契约的协议适配与错误码映射，与文档适配器是两个独立类型，互不可达；
- 测试断言：两个语料的角色集合不相交且每个 RPC 都有策略、文档凭证不能调用聊天 RPC（反之亦然）、聊天 ops 只能读状态、越界范围整体拒绝、适配器字段映射与错误码映射。

**向量集合与装配（第四片）**

- 新增 `internal/chatindex`（聊天语料与向量集合之间的适配层）：`Corpus` 用**专属 collection**、自己的向量核心与自己的平面切分管线装配聊天语料；`Store` 在 `SyncChatControls` 收到的投影上过滤候选（未归档、已撤回、已墓碑或非本存储域的块一律丢弃），并在过滤掉候选时按上限扩召回；它**故意不实现** `rag.ControlledVectorStore`——那个接口说的是文档 payload；
- 聊天消息不复用 Markdown/DOCX 解析：新增平面切分管线，一个消息一个段落块（指南要求的"不同切分方式"）；
- `cmd/rag-server` 装配第二个语料：`-chat-enabled`（默认关闭）、`-chat-collection`（默认 `go_web_chat_v1`）、`-chat-control-namespace`（默认 `chat-v1`），构造聊天语料并注册 `ChatIndexService`，同时启动它自己的 reconciler；启动时校验聊天集合名不得等于文档集合名，共享集合直接拒绝启动；
- 根 Compose 打开聊天语料并传入两个独立名字（`MIXIN_SEARCH_CHAT_ENABLED` 可关闭）；
- 新增 `cmd/rag-server/chat_e2e_test.go`：在真实 gRPC 连接（bufconn）上跑通注册、健康、capability 认证、角色隔离、范围包含、索引→归档→检索、撤回与状态查询。

**本轮修掉的一个真实死锁**：`controlplane.Projection.Converge` 已经负责加锁与 synced 代数记账，而聊天 reconciler 的回调又调用了一次 `Converge`，于是自我重入死锁——后台收敛与并发检索会互相卡住。上面的进程内端到端测试把它暴露出来；修复是把回调改为只做原始投影写入，并补了 `TestProjectionReconcilerConvergesWithoutARequest` 覆盖这条路径。

**对抗性复审与修复（第五片）**

容器门禁仍然跑不了，于是本轮把"验证"换成另一种可执行形式：两个独立复审分别盯聊天控制面、传输与装配，只接受能给出文件与行号的结论。确认的缺陷全部修复，每条都配一条回归测试，并且每条测试都做过反向确认（把修复去掉后测试立刻失败，避免写出恒真的断言）：

- **索引重试被自己拒绝（中）**：写向量失败后意图被围栏成 pending delete；若物理删除此刻仍失败，重试会为同一 storage id 再建写意图，状态同时出现在 `pendingWrites` 与 `pendingDeletes`，`validateControlState` 拒绝，重试只能得到 `unavailable` 且毫无进展。修复：同一 storage id 的新写意图**取代**删除意图（重试写的正是那条待删的块，ingest 会整键替换；再失败时 `abandonWrite` 会把删除意图放回去）。测试 `TestIndexRetryAfterAFailedWriteIsAccepted`；
- **operation_id 在意图未落地期间可被改绑（中）**：幂等账本只记录已完成的操作，phase 1 与 phase 3 之间该操作只存在于写意图里，而 storage id 随消息变化，"同 id 不同载荷必须拒绝"在这段窗口失效。修复：写意图额外持久化整体请求指纹（`operation_fingerprint`），phase 1 先查未完成意图是否已绑定该 operation_id（字段缺省时按未知处理，不误判旧数据）。测试 `TestOperationIDRebindingIsRejectedWhileTheIntentIsPending`；
- **pgvector 下两个语料共用一张表（中）**：`-chat-collection` 在 pgvector 后端根本不是参数，聊天与文档会读写同一张 `rag_chunks`，共享索引与重建路径。修复：`-chat-enabled` 只允许能提供独立集合的后端（`qdrant`、`memory`），`pgvector` 直接拒绝启动。测试 `TestChatVectorBackendRejectsASharedTable`；
- **删除结果缓存跨过复活（低）**：会话被更高 lifecycle 修订复活后，用已关闭的旧修订再删除会命中缓存并回报"已墓碑"，而会话仍可检索。修复：先判陈旧修订再读缓存（同修订的幂等重放仍命中缓存）。测试 `TestStaleDeleteRevisionDoesNotOutliveAResurrection`；
- **同批重复 message_id 被接受（低）**：会重复写向量并重复返回同一消息；现按 `invalid_argument` 拒绝，契约同步更新。测试 `TestOneRequestRejectsADuplicatedMessageId`；
- **storage id 拼接有歧义（低）**：`domain/conversation/message/operation` 直接拼接时 `("a/b","c")` 与 `("a","b/c")` 得到同一 id；改为长度前缀编码。测试 `TestStorageIDsAreUnambiguous`；
- **`ProjectionInterval` 只是文档（低）**：配置项写了却从不读取，reconciler 永远 200ms。已接线，并给 `controlplane.StartReconciler` 补上"非正间隔 = 只用唤醒信号"的语义，避免零值进入 `time.NewTicker` 直接 panic。测试 `TestProjectionIntervalReachesTheReconciler`、`TestStartReconcilerWithoutAnIntervalConvergesOnSignal`；
- **聊天命中的 `dense_rank` / `sparse_rank` 恒为 0（低）**：契约声明了两个 rank，适配层却把它们丢掉，调用方会把 0 读成"每条都是最优"。修复：从共享向量核心一路传到协议层，契约 §5 同步说明语义。测试见 `internal/security`、`internal/chatindex` 与 `cmd/rag-server` 的相邻断言；
- **角色未规范化（低）**：`" searcher "` 这类角色能通过校验，却在下游每个角色查表都失败。修复：`Verify` 存规范化后的角色。测试 `TestVerifyStoresTheCanonicalRole`；
- **审计里的范围大小不可比（低）**：请求侧计原始条目、授予侧计规范化条目，重复项会让审计看起来像越权。修复：两侧同口径计数。测试 `TestAuditCountsRequestedScopesTheWayItCountsGrantedOnes`（文档与聊天各一条）；
- **架构规则的注释与能力不符（低）**：`transport` 那条规则其实允许整棵 transport 树引用任一语料（两个适配器同包），注释却读起来像隔离保证；改为如实描述，并新增 `internal/chatindex` 规则（唯一同时看到两个语料的包，不得向上引用 transport/cmd）。

复审确认**没有**问题的部分：方法策略表与两个 proto 的 14 个 RPC 一一对应（用生成的方法名常量，不存在拼写漂移）、限流调用方表硬上限、范围包含判定、三态与四类修订独立性、投影不可用时的失败关闭、storage domain 单一来源、投影 reconciler 非重入、候选补充循环硬上限、生成代码与 proto 一致。

**容器级验收（第六片）**

Docker Desktop 恢复运行后，P3.3 缺失的那一项终于有了容器内证据，并已按证据目录约定归档为
[phase3/p33-chat-corpus-container_20260919.md](../reports/evidence/phase3/p33-chat-corpus-container_20260919.md)
（同一次运行的三个通用报告族见该目录 README 的 P3.3 行）：

- 用根 Compose 只起 `mixin-search` 及其依赖（独立 project），容器日志为 `store=qdrant control_store=postgres chat=true chat_collection=go_web_chat_v1`，服务健康；
- PostgreSQL 侧：`mixin_search_control.chat_control_states` 出现 bootstrap 建立的 `chat-v1` 行，`mixin_search_control.control_states` 里只有 `go-web-shadow-v1`；一轮聊天写入把聊天 namespace 推到 generation 4，而文档 namespace 仍是 generation 0——**ADR-014 的"聊天 generation 变化不刷新文档控制面"与"聊天规模不增加文档快照"两条在容器与真实数据库上成立**（进程内测试之外的第二份证据）；
- 新增 `cmd/rag-server/chat_container_test.go`（门控 `CHAT_CONTAINER_INTEGRATION=1` + `CHAT_CONTAINER_ADDRESS` + `CHAT_CONTAINER_CAPABILITY_KEY_FILE`）：对着**部署中的 gRPC 端点**跑通两个语料的健康检查、索引→不可检索、归档→可检索、撤回→不可检索但仍计数、文档凭证不能调用聊天 RPC、文档检索看不到聊天消息、以及用已持久化的幂等账本重放同一 operation_id。该测试在本机栈上通过，覆盖的正是此前只有进程内证据的两项真实依赖（PostgreSQL 控制表、Qdrant collection）；
- `deployments/verify.ps1` 增加两步门禁：运行上述容器验收测试，以及用 psql 断言两个语料各自的控制 namespace 互不出现（聊天表必须有 `chat-v1`，文档表必须没有）。**本轮核查后升级为三步**：容器验收测试作为"聊天活动"的刺激，其前后各采样一次两个语料的 generation，断言聊天 generation 必须前进、文档 generation 必须不动——把 ADR-014 第 1 条从"本次专项取证"变成每次整栈门禁都会重复执行的断言；
- **整栈门禁已跑通**：宿主把 gin-backend 的发布端口参数化后（`GIN_BACKEND_PORT`，容器内仍是 8080；Windows 保留 8070–8169 时改用一个未保留的宿主端口，本次用 18080），`deployments/verify.ps1` 以 0 退出，末尾三项全部 PASS：`P1.5_BUILD_TEST_DEPLOYMENT=PASS`、`P2.4_SHADOW_INDEX=PASS`、`P2.5_SHADOW_QUERY_EVALUATION=PASS`；其中包含本轮新增的两步——`Run chat corpus container acceptance` 中 `TestChatCorpusAgainstADeployedStack` PASS，以及 `PASS control namespace isolation: chat=[chat-v1] documents=[go-web-shadow-v1]`。这次运行同时是**文档语料在容器内的回归证据**：控制面迁移到 `internal/controlplane` 之后，影子索引投递、对账排水、mixin-search 停机期间的可用性与恢复、以及 P2.5 评估门禁全部照旧通过。

**capability audience 按语料划分（第七片，2026-09-19）**

- `internal/security` 新增两个语料 audience 常量（`AudienceDocuments = "mixin-search"`、`AudienceChat = "mixin-search-chat"`）与 `roleAudiences` 绑定表：**每个角色绑定唯一的 audience**，`Verify` 在签名/签发方/有效期都通过之后、角色判定之前检查"角色所属语料 == 校验器 audience"，混合凭证（聊天角色 + 文档 audience，或反向）在校验阶段即被拒绝，返回 `UNAUTHENTICATED` 而不是 `PERMISSION_DENIED`；
- `internal/transport/grpc` 的 `AuthConfig` 增加 `ChatVerifier`，拦截器按方法所属服务**选择唯一校验器**，不存在"任一 audience 皆可"的路径；某语料未配置校验器时该语料的方法一律拒绝（失败关闭）；
- `cmd/rag-server` 新增 `-chat-capability-audience`（默认 `mixin-search-chat`），启动时 `validateCapabilityAudiences` **拒绝两个语料共用同一 audience**，启动日志同时打印两个 audience；
- `cmd/rag-token` 的 `-audience` 默认改为**按角色推导**，手工签发不会再产生"角色与 audience 不匹配"的混淆凭证；根 Compose 显式传入两个 audience；
- 测试：`TestVerifyBindsEveryRoleToItsCorpusAudience`（6 组角色×audience 组合）、`TestRoleAudience`、`TestCapabilityAudiencesMustDiffer`；传输层用例更新为"文档凭证与混合凭证调聊天 RPC 均 `UNAUTHENTICATED`"，并保留角色维度的 `PERMISSION_DENIED` 用例；容器端到端（bufconn 与部署端点）同步改用聊天 audience 并新增混合凭证断言；
- 文档：`SERVICE_CALL_CAPABILITY.md` 的角色表加 audience 列、校验顺序加入第 6 步、状态码表说明跨语料为 `UNAUTHENTICATED`；`CHAT_SEARCH_V1_CONTRACT.md` §4 同步。

**Qdrant alias 与 _g1 基线（第八片，2026-09-19）**

- 稳定名称即 alias：`QdrantConfig.Collection` 现在是被所有读写使用的 alias，物理集合为 `<alias>_<generation>`，首个世代 `g1`（`rag.DefaultQdrantGeneration`）。启动引导三种情况：alias 存在 → 直接解析使用；alias 不存在但 `<alias>_g1` 存在 → 建 alias 指向它；都不存在 → 建 `_g1`（向量参数 + payload 索引）再建 alias。**旧布局（物理集合恰好占用 alias 名）一律拒绝启动**并提示清空项目开发卷——按已拍板决策不写迁移逻辑；
- 新增 `PrepareGeneration`（为一个新世代建好物理集合，不切换）、`SwitchAlias`（单次 `UpdateAliases` 完成"删旧指向 + 建新指向"，切换前校验目标存在并补齐 payload 索引，切换后读回确认，未知目标/自指一律拒绝）、`Alias`/`PhysicalCollection`（映射可被直接断言），并以 `rag.AliasedVectorStore` 接口暴露；memory/pgvector 明确不支持 alias；
- 组合根：`validateCorpusIsolation` 增加"两个语料不得互用对方的物理集合名"；启动日志打印 `alias -> physical collection`，让世代成为可观测事实；
- 根 Compose 把 Qdrant gRPC 端口发布到回环（`QDRANT_GRPC_PORT`，默认 16334），使门禁可以直接读取 alias 映射（qdrant 镜像里没有 curl，此前该端口未发布）；
- **证据**：`internal/rag/qdrant_alias_integration_test.go`（真实 Qdrant：引导建 alias+`_g1`、切到空 `_g2` 后本语料检索为空、**另一语料 alias 映射不变**、切回恢复、未知目标被拒，且失败也恢复原映射并清理测试集合）；`cmd/rag-server/alias_container_test.go`（对部署栈：两个配置名必须是 alias 且指向 `_g1`、运行中切换 chat alias 后聊天检索为空、文档 alias 映射不变、切回后聊天检索恢复、未知目标被拒，清理阶段恢复映射并删除新建世代）；`deployments/verify.ps1` 新增对应门禁步骤；
- 旧数据兼容按决策取消：本轮先核对卷名再删除 `p33*` 项目卷（未使用全局 `docker volume prune`，更早的 `go-web_*` 旧卷未触碰），随后按 alias + `_g1` 重建，容器日志确认 `go_web_shadow_v1 -> go_web_shadow_v1_g1`、`go_web_chat_v1 -> go_web_chat_v1_g1`。

**容量测量与上限建议（第九片，2026-09-19，上限待确认）**

按已拍板决策先测量、后定限额。测量工具：`internal/chat/capacity_test.go`（`CHAT_CAPACITY_PROFILE=1`；设置 `CAPACITY_CAS_DSN` 时额外测真实 CAS）。假设：每条消息索引一次、每 50 条一批（对应一条幂等账本记录）、无撤回与删除（撤回本来也保留消息记录）、pending 状态为空。

| 消息数 | 快照大小 | 快照 clone+编码 | 真实 CAS（均值 / 最差，PostgreSQL 单行 `UPDATE ... RETURNING`） |
| ---: | ---: | ---: | ---: |
| 1,000 | 430 KiB | 6.1 ms | 13.1 / 15.1 ms |
| 10,000 | 4.0 MiB | 56.3 ms | 135.0 / 158.5 ms |
| 50,000 | 20.2 MiB | 337.0 ms | 741.9 / 833.6 ms |

按约 **423 字节/消息**线性增长（10 倍消息 ≈ 10 倍体积与耗时）。要读出的结论有三条：①写路径的代价是"整份快照重写"——50k 消息时每次写入约 0.74 s、重写约 20 MiB，写入频率与 WAL 放大直接由快照大小决定；②clone+编码占了其中约 45%，即使换更快的数据库也压不下去；③幂等账本不是主要驱动（每 50 条消息一条记录，量级可忽略），因此 operation TTL 按决策单独立 ADR，不在这里实现。

**建议值（待你确认后才写进配置与契约）**：

- 硬限制：单 corpus 最多 **50,000** 条消息、单 corpus 快照最多 **24 MiB**（约 57k 消息）；超限的写入请求**明确拒绝**（`FAILED_PRECONDITION`），不做静默降级；
- SLO（不作为拒绝依据）：在硬限制内，单次写入 p95 ≤ **1 s**（50k 时实测最差 834 ms，留一档余量）；
- 迁移触发（改成分区/行级 CAS 的判据，任一命中即启动独立 ADR）：消息数 ≥ 50k、快照 ≥ 24 MiB、或持续观测到 p95 写入 > 1 s；
- 100k / 200k 的外推（**未实测**，仅用于说明为何要在 50k 处触发迁移）：约 42 MiB / 1.5 s、约 85 MiB / 3.0 s 每次写入。

复跑方式（真实 CAS 需要控制 PostgreSQL 可达）：

```powershell
docker compose -p p33cap -f docker-compose.yaml up -d --wait control-postgres
$env:CHAT_CAPACITY_PROFILE='1'
$env:CAPACITY_CAS_DSN='postgres://mixin_control:mixin_control@127.0.0.1:15433/mixin_control?sslmode=disable'
go test ./internal/chat -run TestChatCapacity -v
docker compose -p p33cap -f docker-compose.yaml down -v
```

**尚未落地**

- **alias 机制已落地，蓝绿重建编排仍属 P3.5**：两个语料各有独立 alias（配置名即为 alias），物理集合为 `_gN` 世代，切换为原子操作并有直接映射断言（见"第八片"）。尚未实现的是重建编排本身——把数据填进新世代、完整性校验、保留上一代用于回退，这些属于计划中 P3.5 的任务；
- **聊天控制快照的容量天花板尚未定义（容量治理，接入前必须定）**：P3.2 消除的是**读**路径的全局排他锁与每次全量加载；**写**路径仍是"一个全局 `writeMu` + 一份完整 `ControlState`"——每次变更复制整份快照、序列化整份快照、再做一次 CAS（`internal/chat/service.go`、`internal/chat/snapshot.go`），且幂等 operation ledger 没有清理策略。对聊天这种高基数、持续增长的语料，这比文档索引更容易触顶。这不影响当前正确性验收，但 `py-agent` 正式接入前必须定下四项：单 corpus 的最大消息数或快照字节数、operation ledger 的保留期限、单次 CAS 序列化的延迟阈值、迁移到分区存储或行级 CAS 的触发指标；
- **聊天侧 PostgreSQL 适配器仍没有集成测试**：文档语料有环境变量门控的 `internal/rag/control_store_integration_test.go`（`CONTROL_STORE_INTEGRATION=1` + `CONTROL_DATABASE_DSN`）。聊天侧本轮补的是两项**不依赖数据库**的替代证据，而不是一份无法执行的集成测试：
  - `internal/chat/control_schema_test.go`：断言聊天 schema 只创建自己的 `chat_control_states`，不触碰文档的 `control_states`（复制文档 schema 却漏改表名这类错误对 PostgreSQL 是合法的，只会在容器门禁里暴露）；
  - `internal/chat/control_store_encoding_test.go`：把真实控制状态走一遍持久化编码边界（`json.Marshal` → `Unmarshal` → `normalize` → `validate`），再断言恢复后的语料检索结果、对账视图、幂等账本与墓碑围栏与原来一致；该断言经一次刻意的反向改动确认有效（去掉 generation 列还原即失败）。
  两项都不覆盖 SQL 本身（DDL 执行、CAS `UPDATE ... RETURNING`、连接池行为），后者仍只能在容器门禁里验证。
- **复审记录在案、判定为"既有行为或纯加固"的两项**（不构成越权或错误状态，也不计入 P3.3 未完成项）：
  - 适配器把包装后的内部错误文本回给调用方（`codes.Internal`、`Unavailable` 分支），文档与聊天适配器同样如此，是既有行为而非本轮引入；
  - 聊天索引写入没有按意图租约设置 deadline（文档语料有），只影响写锁持有时长，不会产生错误状态（复审逐一推演过交错执行）。
- **远程 CI 尚无本次提交的运行证据**：`.github/workflows/verify.yml` 存在且覆盖两个契约，本地门禁（含整栈 `verify.ps1`）已通过，但仓库领先 `origin/main` 38 个提交，因此只能声明"workflow 已创建、本地门禁通过"，不能声明"远程 CI 已通过"。

## 8. P3.4：QQ 身份与知识空间映射

### 目标

完成宏观阶段三的核心领域能力：QQ 身份与 `go-web` 用户、群与团队空间之间的确定性权限映射。

### 任务

- 建立 QQ 身份与 `go-web` 用户之间可管理、可撤销的绑定；
- 建立私聊与默认私人空间的映射；
- 建立 QQ 群与团队空间的独立绑定关系，不把群标识固化为知识空间本身；
- 最终有效权限取渠道权限与知识资源权限的**交集**；
- 明确私聊访问团队知识、群聊临时读取私人知识的确定性约束；
- 保持 `py-agent` 判断渠道权限、`go-web` 判断资源权限的职责划分。

### 验收

- 解绑或撤销后立即失去对应访问能力，不需要重建索引；
- 成员变化、空间迁移和群绑定变更都能收敛到一致的权限结果；
- 渠道权限无法扩大资源权限，资源权限也无法绕过渠道限制，两类反例都有测试；
- 群聊公开回答不产生持续共享语义，持续共享与复制必须显式且可追踪；
- Model 只输出检索意图，其输出不能改变任何权限判定结果。

## 9. P3.5：在线可靠性门禁

### 目标

在 `py-agent` 正式依赖 `mixin-search` 之前，建立可复现的在线服务质量与不中断重建证据。

### 任务

- 分别为 QQ/Agent 检索与 Web 文档搜索定义 SLO；
- 定义并实现超时、限流与容量隔离，使一类消费者过载不拖垮另一类；
- 扩大检索评测集：覆盖真实查询分布，样本量必须让 p95 与最大值成为不同统计量；
- 报告 p50/p95/p99、错误率、吞吐与饱和点，并区分冷启动、热缓存与并发索引写入状态；
- 建立蓝绿索引 generation：后台重建、完整性验证、collection alias 原子切换、保留上一代以支持回退。

### 验收

- 评测集规模足以支撑尾延迟结论：报告中 p95 不再等于最大值，且给出并发索引写入下的延迟；
- 两类消费者分别有 SLO 达标证据，超载时按限流失败关闭而不是级联拖垮；
- 蓝绿切换期间在线查询不返回空索引、不中断、不出现越权结果；
- 回退到上一代 generation 有实测记录和明确的触发条件；
- 重建、切换与回退全流程不依赖手工修复。

## 10. P3.6：py-agent 文档知识闭环

### 目标

在上述门禁全部通过后，使 `py-agent` 成为 `mixin-search` 的第一个正式在线消费者，并保持 `go-web` 的文档治理权。

### 任务

- QQ 场景下检索 `go-web` 正式文档；
- 将 QQ 中产生的内容提交为 `go-web` 文档草稿；
- 文档创建、审核、发布继续由 `go-web` 治理，`py-agent` 不建立第二套正式文档事实源；
- 区分“群聊公开回答”与“永久共享”两种语义；
- 保留从聊天内容到文档的来源追踪。

### 验收

- QQ 检索只返回调用方在授权范围内可读的文档；
- Agent 生成内容进入知识库前必须经过 `go-web` 生命周期，未审核内容不进入正式检索；
- 群聊公开回答不改变文档归属，也不产生持续授权；
- 从聊天整理出的文档可以追溯到原始来源；
- 阶段 3 结束后 `mixin-search` 才真正成为第一个在线消费者链路的关键依赖，并已具备对应 SLO 与重建能力。

## 11. B 线：BM25 正式交接

BM25 交接不阻塞 P3.1-P3.6，可以在在线基础设施稳定后独立推进：

1. 接入真实语义 embedding（替代评估型 `local-hash-v1`）；
2. 扩大真实查询与标注集，覆盖真实使用分布；
3. 在 `gin-backend` 建立稳定的 `DocumentSearchProvider` 边界；
4. 定义 PostgreSQL BM25 的限时回退条件与触发方式；
5. 受控切换 `go-web` 正式读取；
6. 保留自动回退与质量观测；
7. 达到 [ADR-012](../adr/012-multi-consumer-search-boundary-and-critical-path-shift.md) 决策 5 的退役门禁后，删除 `go-web` 内部 BM25 内核。

`document_search_projection` 在交接初期是**限时回退资产**，不是永久双内核。B 线的每一步都不得反向阻塞 P3.1-P3.6。

## 12. 依赖关系

```text
P3.0 文档/决策基线
  ├─> P3.0a 阶段三治理收口
  ├─> P3.1 调用身份与授权
  ├─> P3.2 在线并发模型
  │      │
  │      └─> P3.3a 多语料控制面隔离决策
  │                │
  └─> P3.3 多语料契约与索引隔离 <──┘
          │
P3.1 + P3.3
  └─> P3.4 QQ 身份与空间映射
          │
P3.1 + P3.2 + P3.4
  └─> P3.5 在线可靠性门禁
          │
          └─> P3.6 py-agent 文档知识闭环
                    │
                    └─> P4 聊天记录域

P3.2 + P3.5 + 真实语义评测
  └─> B 线 BM25 受控交接
```

## 13. 阶段 3 统一门禁

每个实施包运行改动相关测试；阶段候选完成后统一验证：

- `packages/proto/verify-generated.ps1`；
- gin-backend、mixin-search、packages/gen 的 `go test` 与 `go vet`；
- Qdrant 与控制存储集成测试；
- 前端类型检查与构建；
- 根 Compose 从空数据卷启动、health/readiness 和完整 API 回归；
- 跨服务创建、更新、授权、回收、重试、重启、对账和重建场景；
- 认证与授权范围用例：未认证拒绝、越权范围拒绝、写读权限分离；
- 并发与可靠性验收：并发索引写入下的查询 p50/p95/p99、错误率、吞吐与饱和点；
- 蓝绿重建与回退全流程验收（P3.5 起）；
- `docs/check-doc-links.ps1` 输出 `DOC_LINKS=PASS`；
- `git diff --check`。

阶段 3 完成不等于生产部署、灾备或容量承诺。本地验证不得外推为生产、高并发或跨服务可靠性结论。

## 14. 阶段 3 非目标

本阶段明确不实施：

- 聊天记录的长期保存、采集、Web 查看入口与知识晋升产品能力（留给阶段四）；
- Chat/WebSocket 业务路由的重新启用（P3.3 只建立契约与索引隔离）；
- 在 P3.5 门禁通过前切换 `go-web` 正式读取；
- 删除 PostgreSQL BM25 或 `document_search_projection`；
- MCP 或基于检索结果的 Answer 生成链路；
- 面向不可丢弃生产数据的升级迁移、灰度、备份和灾难恢复；
- `mixin-search` 反向调用 `go-web` 做每次查询校验（见 [ADR-012](../adr/012-multi-consumer-search-boundary-and-critical-path-shift.md) 决策 4）。

## 15. 与宏观阶段的映射

| 宏观阶段 | 本计划对应 |
| --- | --- |
| 阶段三：QQ 身份与知识空间融合 | P3.0a、P3.1-P3.6、P3.3a；其中多语料控制面隔离（P3.3a）与多语料契约（P3.3）是阶段四的前置 |
| 阶段四：聊天记录域建设 | P4；聊天保存、索引、查看与晋升 |
| 阶段五：治理与持续演进 | 尚未建立实施包 |
| 文档 BM25 交接 | B 线，独立排期 |

宏观阶段顺序保持不变。阶段三需要先完成“多语料检索基础设计”，但不必提前完成整个聊天产品域，这样既不打乱宏观阶段，也满足 `py-agent` 接入的安全前置条件。

## 16. 跨阶段非阻塞治理待办

本节的条目是仓库治理事项，不是生态阶段交付包，因此**不使用 `P3.x` 编号**：它们不改变阶段三的产品范围，也不参与阶段门禁。放在这里而不是新建 backlog，是因为本计划是唯一排期入口；放在“非目标”也不合适——“非目标”表示阶段三明确排除且不需要继续追踪。

| 编号 | 事项 | 阻塞条件 | 是否阻塞 P3.3 |
| --- | --- | --- | --- |
| G1 | 增加 `LICENSE` | 需要仓库所有者选择许可证 | 否 |
| G2 | 增加 `CODEOWNERS` | 需要确定 GitHub 用户或团队标识 | 否 |
| G3 | 增加 PR 模板 | 无，可独立完成 | 否 |
| G4 | `pgvector` 移入 `experimental` Compose profile，并在启用时与主线 PostgreSQL 大版本对齐 | 无，可独立完成 | 否 |

规则：

- G1-G4 可以随时以独立小提交完成，不混入实施包的核心改造；
- **阶段切换时，未完成的条目必须随当前计划迁移**，不能留在已冻结的阶段材料里；
- 条目完成或关闭后从表中移除，并在对应提交信息中说明。

### 16.1 G4 的目标状态与验收

- `pgvector` 服务进入 `experimental` Compose profile，默认 `docker compose config` 中它不是必需服务；
- 显式 `--profile experimental` 可以启动它；
- 常规门禁（`deployments/verify.ps1`、CI）不依赖 pgvector；
- 启用时选择与主线控制 PostgreSQL 一致的大版本；
- 文档明确它**不是**当前正式检索后端（Qdrant 才是），且参考 `ADR-007` 的实验性定位。

Qdrant、控制 PostgreSQL 与根 Compose 的现有行为不受影响。

## 17. 已关闭议题

记录已经决定"不做"的事项，避免反复讨论；只有触发条件成立时才重新开启。

| 议题 | 结论 | 重新开启的触发条件 |
| --- | --- | --- |
| `packages/gen` 的模块名（当前为 `module packages/gen`） | **不改名**。它不是公开域名形式，但已被 `go.work`、两个消费者的 `require + replace`、`GOWORK=off` 的 CI 以及 Proto 生成一致性检查共同固化为仓库内部模块契约 | `packages/gen` 需要发布为外部 Go Module；Monorepo 外出现直接消费者；仓库需要移除本地 `replace` |

在此之前改名只有迁移成本，没有产品收益。
