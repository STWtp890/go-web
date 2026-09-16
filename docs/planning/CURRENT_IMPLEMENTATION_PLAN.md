# go-web 当前实施计划

> 文档职责：当前唯一阶段排期与实施入口
> 上位目标：[ECOSYSTEM_EVOLUTION_GUIDE.md](../ECOSYSTEM_EVOLUTION_GUIDE.md)
> 相关决策：[ADR-001](../adr/001-search-service-boundary.md)、[ADR-002](../adr/002-document-index-ownership.md)、[ADR-004](../adr/004-bm25-migration-strategy.md)、[ADR-005](../adr/005-development-baseline-over-production-migration.md)、[ADR-006](../adr/006-mixin-search-control-state-commit-order.md)、[ADR-007](../adr/007-qdrant-control-projection-and-filtering.md)、[ADR-008](../adr/008-document-index-transactional-outbox.md)、[ADR-009](../adr/009-shadow-index-compose-and-health-boundary.md)、[ADR-010](../adr/010-shadow-query-evaluation-gate.md)、[ADR-011](../adr/011-bounded-cache-runtime-and-revision-fencing.md)
> 当前状态：生态阶段二 / go-web 阶段 2 已完成，P2.0-P2.5 全部通过；正式读取仍保持 PostgreSQL BM25
> 更新日期：2026-09-14

## 1. 当前结论

阶段 1 已于 2026-09-13 完成。当前代码基线已经具备进入真实文档索引链路的条件：

- gin-backend/PostgreSQL 是正式文档、活动版本、访问策略和生命周期的唯一事实源；
- document 领域已经完成应用层、领域层、PostgreSQL/缓存基础设施和 HTTP 适配器收口；
- `document_search_projection` 继续承担正式 BM25 查询，属于可以从文档事实重建的查询投影；
- mixin-search 已具备文档解析、Eino 工作流、混合检索、七个 v1 RPC 和三类 fencing revision 契约；
- Qdrant 是首个持久化检索候选，pgvector 仍为实验性替代，memory 只用于测试和演示；
- Chat/WebSocket 源码保留但不注册，py-agent/QQ 与聊天记录域不进入本阶段。

P2.1-P2.4 已完成持久化控制状态、Qdrant 候选级过滤、可靠投递/重建和根 Compose 影子索引。P2.5 已在正式 BM25 返回之后通过有界非阻塞队列执行 mixin-search 影子查询，按 runtime/evaluation 来源记录结果差异、延迟、错误及授权/生命周期/活动版本复核，并建立七类固定评测集。当前实测质量达标但检索仍使用 local-hash-v1 评估型 embedding，因此书面结论为 KEEP_BM25，不进入读取切换。

阶段 1 的完成事实与实测证据只在 [PHASE1_IMPLEMENTATION_LOG.md](../reports/PHASE1_IMPLEMENTATION_LOG.md) 维护，本计划不重复改写历史过程。

阶段 2 的完成事实、实测证据和当前有效限制记录在 [PHASE2_IMPLEMENTATION_LOG.md](../reports/PHASE2_IMPLEMENTATION_LOG.md)。

## 2. 阶段 2 目标

阶段 2 继续服务于生态“阶段二：文档知识链路贯通”，完成以下闭环：

```text
正式文档事务
  -> 持久化索引事件
  -> mixin-search 派生索引
  -> 状态确认与对账
  -> 影子查询与质量评估
```

本阶段遵循以下边界：

1. gin-backend 继续拥有文档事实、用户身份、空间成员关系和最终授权判定输入。
2. mixin-search 只拥有可重建的索引块、检索向量、操作幂等记录和索引控制状态。
3. PostgreSQL BM25 在阶段 2 全程保持正式读取方；混合检索先作为影子结果运行。
4. 权限、活动版本和删除状态必须由确定性逻辑与存储过滤保证，不能交由模型判断。
5. 接入真实跨服务流量后，可靠投递、持久化幂等、重放和重建成为产品正确性要求，不再属于可省略的生产运维能力。
6. 当前仍没有不可丢弃的生产数据，因此不恢复旧数据库升级、备份恢复、灰度发布等历史设施。

## 3. 实施顺序

| 实施包 | 目标 | 依赖 | 状态 |
| --- | --- | --- | --- |
| P2.0 | 冻结阶段 1 基线 | 无 | 已完成 |
| P2.1 | 持久化 mixin-search 控制状态 | P2.0 | 已完成 |
| P2.2 | 完成 Qdrant 授权与生命周期过滤 | P2.1 | 已完成 |
| P2.3 | 建立 gin-backend 可靠索引投递与对账 | P2.1、P2.2 | 已完成 |
| P2.4 | 接入根 Compose 并运行影子索引 | P2.3 | 已完成 |
| P2.5 | 运行影子查询和检索质量评估 | P2.4 | 已完成 |

P2.1 与 P2.2 都在 mixin-search 内实施，可以连续推进；正式文档流量必须等两者通过后再由 P2.3 接入。

阶段 2 已完成。根 Compose 已通过正常、故障和恢复三段影子索引/查询验收，收敛后的 evaluation 观测没有权限、生命周期或活动版本违规；故障期间影子失败单独记录，正式 BM25 与 HTTP readiness 不受影响。P2.0-P2.5 完成事实和实测证据见 [PHASE2_IMPLEMENTATION_LOG.md](../reports/PHASE2_IMPLEMENTATION_LOG.md)。

## 4. P2.0：冻结阶段 1 基线

### 目标

将当前大量目录迁移、document 领域改造、数据库基线和 mixin-search/v1 契约整理为可追溯基线，避免跨服务实现与阶段 1 重构混在同一变更中。

### 任务

- 审查当前工作树，确认删除、移动和新增文件均属于阶段 1 预期结果；
- 将阶段 1 变更整理为边界清晰的提交或 PR；
- 从新检出或等价干净环境运行一次完整门禁；
- 记录阶段 2 的起点提交和仍然有效的限制。

### 验收

- Proto 生成一致性、三个 Go module 的 test/vet、前端构建、根 Compose 健康和完整运行时 API 回归通过；
- 旧 Markdown HTTP 与运行模块不再出现，Chat/WebSocket 仍未注册；
- 工作树中的阶段 1 结果可以独立审查和恢复。

## 5. P2.1：持久化 mixin-search 控制状态

### 目标

使服务重启后仍能正确处理幂等、乱序、活动版本、访问快照和删除墓碑。

### 任务

- 为文档清单、版本状态、三类修订高水位、访问快照、墓碑和 `operation_id` 结果建立持久化存储；
- 默认采用由 mixin-search 独立拥有的 PostgreSQL schema 或数据库保存控制状态，Qdrant 继续保存文档块与检索向量；
- 保持 RPC 适配层、应用逻辑与存储实现分离，不让协议 DTO 成为持久化模型；
- 建立启动恢复、并发写入和失败关闭行为；
- 明确控制状态与 Qdrant 写入之间的提交顺序和故障收敛方式。

### 验收

- 服务重启后活动版本仍可检索；
- 相同 `operation_id` 与相同载荷精确重放首次响应；
- 跨载荷改绑、低修订和同修订冲突继续被拒绝；
- 删除后重启和迟到事件都不能复活文档；
- 控制存储丢失时服务失败关闭，并可以由 gin-backend 事实源重建。

## 6. P2.2：完成 Qdrant 授权与生命周期过滤

### 目标

让 Qdrant 满足 mixin-search/v1 的正式候选存储语义，而不是只在召回结果之后做内存过滤。

### 任务

- 在索引 payload 中保存规范化的文档、版本、归属空间、访问快照、活动状态和生命周期信息；
- Dense 与 Sparse 查询统一下推公开、空间、显式文档授权、活动版本和非墓碑过滤；
- 对过滤后的不足结果进行补召回，并在融合前后保持去重和稳定行为；
- 使版本激活、授权撤销、文档回收和重新发布同步更新检索状态；
- 用真实 Qdrant 集成测试覆盖上述语义。

### 验收

- 未授权、非活动和已删除内容不会进入返回结果；
- 空 allow-list 只能返回公开文档；
- 授权撤销、版本切换和回收在规定的本地验收窗口内生效；
- 过滤不会因为固定候选集过小而产生可复现的系统性漏召回。

### 完成状态

P2.2 已于 2026-09-14 完成。Qdrant payload、候选级过滤、storage domain 隔离、契约层复核和有界回填已经落地；真实 Qdrant 验收覆盖授权、撤销、版本切换、删除、重新发布和共享 collection 隔离。提交与搜索门禁细节见 [ADR-007](../adr/007-qdrant-control-projection-and-filtering.md)。

## 7. P2.3：建立 gin-backend 可靠索引投递与对账

### 目标

在不把 mixin-search 放入文档事务的前提下，使已提交的文档状态最终可靠地传播到派生索引。

### 任务

- 根据当前 v1 契约重新设计文档索引 Outbox 和投递状态，不恢复阶段 1 删除的旧实现；
- 在文档事务中只记录不可丢失的索引意图，事件引用不可变版本和修订，不重复保存大段正文；
- 增加独立 Worker，按文档顺序执行索引、访问更新、激活、版本删除和文档删除；
- 使用稳定事件标识作为 `operation_id`，支持超时重试、进程重启、失败检查和人工重放；
- 增加 reconciliation，根据 gin-backend 事实状态与 `GetDocumentVersionState` 检查差异并修复；
- 提供从空控制存储和空 Qdrant 全量重建派生索引的入口。

### 验收

- 文档事务回滚时不产生可投递事件；
- RPC 超时、重复发送和 Worker 重启不会造成冲突或错误激活；
- 同一文档的更新、授权和删除最终按修订收敛；
- mixin-search 长时间不可用不阻断正式文档写入，恢复后可继续投递；
- 全量重建后，活动版本、权限和删除状态与 gin-backend 一致。

### 完成状态

P2.3 已于 2026-09-14 完成。文档创建、更新和回收在原事务内写入只引用不可变版本的小型投递事件；独立 Worker 使用按文档顺序的租约领取、稳定子操作 ID、确定性退避、死信阻塞与人工重放完成跨服务传播。管理命令已经提供差异对账和 repeatable-read 全量重建，正式 BM25 查询路径保持不变。可靠投递真实 PostgreSQL 验收输出 `P2.3_INDEX_DELIVERY=PASS`，从空控制存储和空 Qdrant 重建的跨服务验收输出 `P2.3_INDEX_REBUILD_E2E=PASS`。事务、幂等和恢复边界见 [ADR-008](../adr/008-document-index-transactional-outbox.md)。

## 8. P2.4：根 Compose 与影子索引

### 目标

在统一开发环境中让真实 Documents 操作持续生成 mixin-search 派生索引，但不改变用户看到的搜索结果。

### 任务

- 将 mixin-search、Qdrant 和所需控制存储接入根 Compose；
- 增加 gin-backend 的 gRPC 地址、超时、开关和健康状态配置；
- 区分 HTTP 服务 readiness、投递积压和检索服务可用性；
- 建立创建、更新、访问变更、回收、重启、重试和全量重建的跨服务端到端测试；
- 为投递延迟、失败次数、积压量和对账差异提供最小可观测信息。

### 验收

- 从空数据卷可以启动完整栈并达到预期健康状态；
- Documents 操作在 PostgreSQL 成功后最终反映到 mixin-search；
- mixin-search 故障期间正式文档读写和 PostgreSQL BM25 仍可工作；
- 服务恢复后积压能够自动收敛，端到端测试不依赖手工修复。

### 完成状态

P2.4 已于 2026-09-14 完成。根 Compose 统一运行 PostgreSQL、Redis、gin-backend、simple-frontend、控制 PostgreSQL、Qdrant、mixin-search 与 document-index-worker；HTTP readiness 与影子依赖解耦，管理命令提供 gRPC 健康、积压、失败、最老未完成事件和投递延迟状态。正常与 mixin-search 停机期间的两轮 API 回归均为 `95 passed / 0 failed / 95 total`，恢复后积压自动排空，最终输出 `P2.4_SHADOW_INDEX=PASS`。装配、健康和开关边界见 [ADR-009](../adr/009-shadow-index-compose-and-health-boundary.md)。

## 9. P2.5：影子查询与质量评估

### 目标

通过真实样本判断混合检索是否具备进入受控读取切换阶段的条件。

### 任务

- 正式 HTTP 搜索继续返回 PostgreSQL BM25 结果；
- 在不影响请求结果的前提下，对同一查询执行 mixin-search 影子检索；
- 分别记录权限/生命周期正确性、结果差异、延迟、超时和错误；
- 建立覆盖中文、英文、代码、标题、正文、精确关键词和语义表达的评测集；
- 以 Recall@K、MRR 或 nDCG 等指标评估排序质量，不要求混合检索与 BM25 顺序完全相同；
- 形成是否进入阶段 3 读取切换的书面结论。

### 验收

- 权限、删除和活动版本一致性没有已知违规；
- 影子请求不会扩大正式请求延迟或影响可用性；
- 检索质量、延迟和失败率具有可重复报告；
- 不满足门禁时可以继续以 BM25 为正式读取方，不删除现有投影。

### 完成状态

P2.5 已于 2026-09-14 完成。正式 SearchMine 在 PostgreSQL BM25 查询完成后只向有界内存队列提交观测任务；队列满、mixin-search 超时或不可用都不会改变 HTTP 返回。观察器使用独立 deadline 调用 SearchDocuments，再以 gin-backend 当前事实复核权限、生命周期、活动版本和 owner-only 正式语义，并将查询 SHA-256、两路结果、差异、延迟和错误写入 document_search_shadow_observations。边界与来源分层见 [ADR-010](../adr/010-shadow-query-evaluation-gate.md)。

固定评测集覆盖中文、英文、代码、标题、正文、精确关键词和语义表达。成功报告 [document-search-evaluation-p25_20260914_231537.md](../../deployments/test-results/document-search-evaluation-p25_20260914_231537.md) 中，BM25 的 Recall@5/MRR/nDCG@5 均为 1.0000，mixin-search 为 1.0000 / 0.9048 / 0.9286，影子 p95 为 459029 us，7 条收敛后 evaluation 观测的权限、生命周期、活动版本和正式范围差异均为 0。由于当前 embedding profile 是评估用 local-hash-v1，最终结论为 KEEP_BM25；P2.5 完成不代表读取切换获批。

## 10. 阶段 2 统一门禁

每个实施包运行改动相关测试；阶段候选完成后统一验证：

- `packages/proto/verify-generated.ps1`；
- gin-backend、mixin-search、packages/gen 的 `go test` 与 `go vet`；
- Qdrant 与控制存储集成测试；
- 前端类型检查与构建；
- 根 Compose 从空数据卷启动、health/readiness 和完整 API 回归；
- 跨服务创建、更新、授权、回收、重试、重启、对账和重建场景；
- BM25 基线与混合检索影子评估报告；
- `git diff --check` 和文档链接检查。

阶段 2 完成不等于生产部署、高并发、灾备或检索读取切换完成。

## 11. 阶段 2 非目标

本阶段明确不实施：

- py-agent/QQ 身份绑定与正式接入；
- 聊天记录保存、聊天索引或文档/聊天混合查询；
- Chat/WebSocket 重新启用；
- 团队知识空间完整管理界面；
- MCP 或基于检索结果的 Answer 生成链路；
- 删除 PostgreSQL BM25 或 `document_search_projection`；
- 面向不可丢弃生产数据的升级迁移、灰度、备份和灾难恢复。

## 12. 后续阶段

阶段 2 已通过，但 P2.5 的当前结论是 KEEP_BM25。只有接入非评估型语义 embedding、扩大真实标注样本并重新通过同一正确性/质量/延迟门禁后，下一阶段才建立 gin-backend 的稳定 DocumentSearchProvider 边界，在 PostgreSQL BM25 与 mixin-search 之间进行受控读取切换。只有授权、删除传播、重建、检索质量和故障回退全部达标后，才能讨论移除 go-web 内部 BM25 投影。

QQ 身份、私人/团队知识空间流通和聊天记录域仍按照 [ECOSYSTEM_EVOLUTION_GUIDE.md](../ECOSYSTEM_EVOLUTION_GUIDE.md) 的宏观阶段推进，不与本阶段的文档索引基础链路并行混入。
