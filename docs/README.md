# go-web 文档导航

`docs/` 以“根目录只放入口文档、专题内容进入分类目录”为原则组织。阶段状态和下一步只在当前计划中维护，避免多份文档重复排期。

## 目录结构

```text
docs/
├── README.md                         # 文档导航与维护规则
├── check-doc-links.ps1               # 文档链接与证据引用检查
├── ECOSYSTEM_EVOLUTION_GUIDE.md      # 生态核心目标与长期边界
├── planning/                         # 当前实施计划
├── architecture/                     # 项目结构与依赖约束
├── contracts/                        # 当前跨服务契约
├── reports/                          # 阶段完成报告与实施证据
│   └── evidence/                     # 阶段报告引用的不可变证据快照
├── adr/                              # 架构决策记录
└── history/                          # 冻结或已被取代的历史基线
```

## 推荐阅读顺序

1. [生态演进核心目标](./ECOSYSTEM_EVOLUTION_GUIDE.md)
2. [当前实施计划](./planning/CURRENT_IMPLEMENTATION_PLAN.md)
3. [项目结构与依赖约束](./architecture/PROJECT_STRUCTURE.md)
4. [结构与复用开发约定](./architecture/DEVELOPMENT_CONVENTIONS.md)（新增代码落位与复用规则的唯一约定）
5. [mixin-search/v1 契约](./contracts/MIXIN_SEARCH_V1_CONTRACT.md)
6. [聊天语料契约](./contracts/CHAT_SEARCH_V1_CONTRACT.md)（含 §8 三方责任边界）
7. [调用方 capability 契约](./contracts/SERVICE_CALL_CAPABILITY.md)
8. [阶段 1 实施日志](./reports/PHASE1_IMPLEMENTATION_LOG.md)
9. [阶段 2 实施日志](./reports/PHASE2_IMPLEMENTATION_LOG.md)
10. 需要追溯决策时阅读 [ADR 一览](#adr-一览)；需要理解历史方案时阅读 [history](./history/)

## 当前执行重点

生态阶段二已收口，阶段三正在推进。`mixin-search` 现在只接受携带 capability 的调用方，索引写入与检索分离，请求范围只能缩小不能扩大；进程内控制状态是不可变快照，读路径不取全局锁、不加载全量控制状态，控制投影由后台 reconciler 收敛。文档与聊天是**两个独立语料**：各自拥有契约、控制面实例、generation、持久化、投影 reconciler 与索引 alias，共享的只是机制；联合检索只在编排层的结果层融合。PostgreSQL BM25 仍是正式文档搜索的读取方，读取切换属于独立的 B 线。

阶段三的多语料控制面隔离与聊天语料契约/索引隔离已经建立，推进方向转向 QQ 身份与知识空间映射、在线可靠性门禁，以及让 `py-agent` 成为正式在线消费者。三方责任边界（谁判定、谁执行、谁举证）见 [聊天语料契约 §8](./contracts/CHAT_SEARCH_V1_CONTRACT.md)。

**实施包编号与完成进度只在 [当前实施计划](./planning/CURRENT_IMPLEMENTATION_PLAN.md) 维护**，本文件不重复记录，以免两处状态漂移。判断依据见 [ADR-012](./adr/012-multi-consumer-search-boundary-and-critical-path-shift.md)（多消费者边界）、[ADR-013](./adr/013-immutable-control-snapshot-and-background-projection.md)（控制面并发模型）、[ADR-014](./adr/014-per-corpus-control-plane-isolation.md)（多语料控制面隔离）、[ADR-015](./adr/015-control-plane-idempotency-ledger-retention.md)（幂等账本保留窗口）与 [ADR-016](./adr/016-qq-identity-and-knowledge-space-mapping.md)（QQ 身份与知识空间映射）；调用凭据格式见 [调用方 capability 契约](./contracts/SERVICE_CALL_CAPABILITY.md)。

## 分类索引

| 分类 | 文档 | 职责 |
| --- | --- | --- |
| 上位目标 | [ECOSYSTEM_EVOLUTION_GUIDE.md](./ECOSYSTEM_EVOLUTION_GUIDE.md) | 定义跨项目产品定位、数据所有权、知识域隔离和宏观阶段 |
| 当前计划 | [planning/CURRENT_IMPLEMENTATION_PLAN.md](./planning/CURRENT_IMPLEMENTATION_PLAN.md) | 当前唯一排期与实施入口 |
| 架构 | [architecture/PROJECT_STRUCTURE.md](./architecture/PROJECT_STRUCTURE.md) | 目录职责、领域分层和依赖方向 |
| 约定 | [architecture/DEVELOPMENT_CONVENTIONS.md](./architecture/DEVELOPMENT_CONVENTIONS.md) | 新增代码落位规则、身份与 HTTP 出入口契约、遗留模块冻结基线与迁移待办 |
| 契约 | [contracts/MIXIN_SEARCH_V1_CONTRACT.md](./contracts/MIXIN_SEARCH_V1_CONTRACT.md) | 当前 RPC 边界和字段语义 |
| 聊天契约 | [contracts/CHAT_SEARCH_V1_CONTRACT.md](./contracts/CHAT_SEARCH_V1_CONTRACT.md) | 聊天语料的独立 RPC 边界、三态独立性与授权规则 |
| 调用边界 | [contracts/SERVICE_CALL_CAPABILITY.md](./contracts/SERVICE_CALL_CAPABILITY.md) | 调用方 capability 格式、角色权限与范围包含规则 |
| 实施证据 | [reports/PHASE0_COMPLETION_REPORT.md](./reports/PHASE0_COMPLETION_REPORT.md)、[reports/PHASE1_IMPLEMENTATION_LOG.md](./reports/PHASE1_IMPLEMENTATION_LOG.md)、[reports/PHASE2_IMPLEMENTATION_LOG.md](./reports/PHASE2_IMPLEMENTATION_LOG.md) | 记录已经验证的结果，不承担后续排期 |
| 证据快照 | [reports/evidence/](./reports/evidence/) | 阶段报告引用的运行产物归档，内容不随后续实现改写 |
| 决策 | [adr/](./adr/)（见下方 [ADR 一览](#adr-一览)） | 保存已接受、被取代或附条件的架构决策 |
| 历史 | [history/](./history/) | 保存初始方案、冻结基线和已被取代的专项记录 |

## ADR 一览

本表只做导航：**状态与内容以各 ADR 文件为准**（新增 ADR 或改变状态时必须同步本表，见维护规则 6）。

| 编号 | 决策 |
| --- | --- |
| [ADR-001](./adr/001-search-service-boundary.md) | mixin-search 服务边界与首发存储 |
| [ADR-002](./adr/002-document-index-ownership.md) | 文档事实与索引所有权 |
| [ADR-003](./adr/003-chat-domain-boundary.md) | Chat 领域边界与停用状态 |
| [ADR-004](./adr/004-bm25-migration-strategy.md) | PostgreSQL BM25 迁移策略 |
| [ADR-005](./adr/005-development-baseline-over-production-migration.md) | 开发基线重建优先于生产迁移 |
| [ADR-006](./adr/006-mixin-search-control-state-commit-order.md) | 控制状态提交顺序与故障收敛 |
| [ADR-007](./adr/007-qdrant-control-projection-and-filtering.md) | Qdrant 控制投影与候选级过滤 |
| [ADR-008](./adr/008-document-index-transactional-outbox.md) | 文档索引事务 Outbox 与重建边界 |
| [ADR-009](./adr/009-shadow-index-compose-and-health-boundary.md) | 影子索引 Compose 装配与健康边界 |
| [ADR-010](./adr/010-shadow-query-evaluation-gate.md) | 异步影子查询、来源分层观测与读取切换门禁 |
| [ADR-011](./adr/011-bounded-cache-runtime-and-revision-fencing.md) | 有界实体缓存运行时与 revision fencing |
| [ADR-012](./adr/012-multi-consumer-search-boundary-and-critical-path-shift.md) | 多消费者检索边界与关键路径转变 |
| [ADR-013](./adr/013-immutable-control-snapshot-and-background-projection.md) | 控制面不可变快照与后台投影收敛 |
| [ADR-014](./adr/014-per-corpus-control-plane-isolation.md) | 多语料控制面与索引隔离 |
| [ADR-015](./adr/015-control-plane-idempotency-ledger-retention.md) | 控制面幂等账本的保留窗口 |
| [ADR-016](./adr/016-qq-identity-and-knowledge-space-mapping.md) | QQ 身份与知识空间映射 |

`history/` 当前包含：

- [阶段 0 契约基线](./history/CONTRACT_BASELINE.md)
- [阶段 1 启动时系统清单](./history/CURRENT_SYSTEM_INVENTORY.md)
- [生态演进初步方案](./history/GO_WEB_ECOSYSTEM_EVOLUTION_INITIAL_PLAN.md)
- [阶段 1 破坏性改造初始方向](./history/PHASE1_BREAKING_REFACTOR_DIRECTION.md)
- [HttpOnly Cookie 专项迁移记录](./history/HTTPONLY_COOKIE_MIGRATION.md)

## 阶段映射

| 生态宏观阶段 | go-web 本地阶段 | 当前状态 |
| --- | --- | --- |
| 阶段一：现状审计与边界确认 | 阶段 0 | 已完成并冻结 |
| 阶段二：文档知识链路贯通 | 阶段 1 → 阶段 2 | 已完成；P2.5 结论为 KEEP_BM25 |
| 阶段三：QQ 身份与知识空间融合 | 阶段 3 | 进行中；进度见当前计划 |
| 阶段四：聊天记录域建设 | 后续专项阶段 | 未进入；Chat/WebSocket 保持代码存在但不接入 |
| 阶段五：治理、可靠性与持续演进 | 持续治理阶段 | 未进入 |
| 文档 BM25 交接 | B 线（独立排期） | 未完成；PostgreSQL BM25 仍是正式读取方 |

## 维护规则

1. 产品定位、事实源、权限边界或知识域划分发生变化时，更新根目录的生态核心目标，必要时新增 ADR。
2. 当前范围、顺序、非目标和验收门禁只更新 `planning/CURRENT_IMPLEMENTATION_PLAN.md`。
3. 实施包通过后，将实测结果写入对应阶段报告；已冻结的阶段报告不因后续计划变化而改写。
4. `architecture/` 与 `contracts/` 只维护稳定结构和契约，不复制阶段排期。
5. `history/` 文档不随当前实现持续改写；事实变化通过当前计划、ADR 或实施日志表达。
6. 移动或新增文档时必须同步修改仓库内链接（含本文件的分类索引与 [ADR 一览](#adr-一览)），并执行 `docs/check-doc-links.ps1`（CI hygiene 作业与 `deployments/verify.ps1` 都会执行同一脚本）。
7. 正式文档不得链接 `deployments/test-results/`：该目录按保留策略滚动清理。阶段报告引用的运行产物必须固化到 `reports/evidence/<phase>/`，由脚本强制。
8. 本地验证不得外推为生产、高并发或跨服务可靠性结论。
