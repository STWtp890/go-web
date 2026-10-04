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
├── operations/                       # 运维操作说明
├── reports/                          # 阶段完成报告与实施证据
│   └── evidence/                     # 阶段报告引用的不可变证据快照
├── adr/                              # 架构决策记录
└── history/                          # 冻结或已被取代的历史基线
```

## 推荐阅读顺序

1. [生态演进核心目标](./ECOSYSTEM_EVOLUTION_GUIDE.md)
2. [按来源划分写入与检索服务的目标架构（ADR-017）](./adr/017-source-owned-document-and-search-services.md)
3. [当前实施计划](./planning/CURRENT_IMPLEMENTATION_PLAN.md)
4. [项目结构与依赖约束](./architecture/PROJECT_STRUCTURE.md)（当前实现）
5. [结构与复用开发约定](./architecture/DEVELOPMENT_CONVENTIONS.md)
6. [现有 mixin-search/v1 契约](./contracts/MIXIN_SEARCH_V1_CONTRACT.md)
7. [聊天语料契约](./contracts/CHAT_SEARCH_V1_CONTRACT.md)（含 §8 三方责任边界）
8. [现有调用方 capability 契约](./contracts/SERVICE_CALL_CAPABILITY.md)
9. 需要追溯已完成的工作时阅读 [阶段报告](./reports/)；需要追溯历史方案时阅读 [history](./history/)

## 当前执行重点

当前按 [当前实施计划 §0.10](./planning/CURRENT_IMPLEMENTATION_PLAN.md) 的三阶段推进：**阶段 A**（修复 Web 文档链路与 Go 查询权限）已完成并通过真实 Web 验收；**阶段 B**（把保留的检索能力、索引管理与评测职责接管到所属新服务）进行中；**阶段 C**（清理已被替代的旧代码、入口与初始化内容）已完成。

2026-09-26 的“只清理不改检索实现”临时冻结已被取代：检索能力要接管到所属新服务，旧实现要在核对证据后删除。具体范围、顺序和验收只在 [当前实施计划](./planning/CURRENT_IMPLEMENTATION_PLAN.md) 维护。

## 分类索引

| 分类 | 文档 | 职责 |
| --- | --- | --- |
| 上位目标 | [ECOSYSTEM_EVOLUTION_GUIDE.md](./ECOSYSTEM_EVOLUTION_GUIDE.md) | 定义跨项目产品定位、数据所有权和知识域隔离 |
| 目标架构 | [ADR-017](./adr/017-source-owned-document-and-search-services.md) | 独立文档服务与按来源划分检索服务的目标边界 |
| 当前计划 | [planning/CURRENT_IMPLEMENTATION_PLAN.md](./planning/CURRENT_IMPLEMENTATION_PLAN.md) | 当前唯一排期与实施入口 |
| 架构 | [architecture/PROJECT_STRUCTURE.md](./architecture/PROJECT_STRUCTURE.md) | 目录职责、领域分层和依赖方向 |
| 约定 | [architecture/DEVELOPMENT_CONVENTIONS.md](./architecture/DEVELOPMENT_CONVENTIONS.md) | 新增代码落位规则、身份与 HTTP 出入口契约、遗留模块冻结基线与迁移待办 |
| 契约 | [contracts/MIXIN_SEARCH_V1_CONTRACT.md](./contracts/MIXIN_SEARCH_V1_CONTRACT.md) | mixin-search/v1 的 RPC 边界和字段语义（迁移期） |
| 文档服务契约 | [contracts/DOCUMENT_SERVICE_V1_CONTRACT.md](./contracts/DOCUMENT_SERVICE_V1_CONTRACT.md) | 文档命令、保存用例、详情读取、检索查询语义与文档变更事件 |
| 身份与凭证 | [contracts/SERVICE_IDENTITY_AND_CAPABILITY.md](./contracts/SERVICE_IDENTITY_AND_CAPABILITY.md) | 服务身份断言与资源范围 capability 的格式、签发方规则与校验顺序 |
| py-agent 交付 | [contracts/PY_AGENT_INTEGRATION_DELIVERY.md](./contracts/PY_AGENT_INTEGRATION_DELIVERY.md) | 交付给独立 py-agent 任务的 Go 侧接口材料、固定凭证样例与依赖登记 |
| 运维 | [operations/QQ_SPACE_BINDINGS.md](./operations/QQ_SPACE_BINDINGS.md) | QQ 主体、团队空间、资源范围解析与 capability 签发的当前模型 |
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
| [ADR-016](./adr/016-qq-identity-and-knowledge-space-mapping.md) | QQ 身份与知识空间映射（现有阶段决策；目标所有权见 ADR-017） |
| [ADR-017](./adr/017-source-owned-document-and-search-services.md) | 独立文档服务与按来源划分检索服务 |

`history/` 当前包含：

- [阶段 0 契约基线](./history/CONTRACT_BASELINE.md)
- [阶段 1 启动时系统清单](./history/CURRENT_SYSTEM_INVENTORY.md)
- [生态演进初步方案](./history/GO_WEB_ECOSYSTEM_EVOLUTION_INITIAL_PLAN.md)
- [阶段 1 破坏性改造初始方向](./history/PHASE1_BREAKING_REFACTOR_DIRECTION.md)
- [HttpOnly Cookie 专项迁移记录](./history/HTTPONLY_COOKIE_MIGRATION.md)

## 状态入口

已完成阶段的结果见 [阶段报告](./reports/)；当前代码结构见 [项目结构](./architecture/PROJECT_STRUCTURE.md)。2026-09-25 起的目标架构见 [ADR-017](./adr/017-source-owned-document-and-search-services.md)，其实施状态只在 [当前实施计划](./planning/CURRENT_IMPLEMENTATION_PLAN.md) 更新。历史阶段报告不因新方向改写。

## 维护规则

1. 产品定位、事实源、权限边界或知识域划分发生变化时，更新根目录的生态核心目标，必要时新增 ADR。
2. 当前范围、顺序、非目标和验收门禁只更新 `planning/CURRENT_IMPLEMENTATION_PLAN.md`。
3. 实施包通过后，将实测结果写入对应阶段报告；已冻结的阶段报告不因后续计划变化而改写。
4. `architecture/` 与 `contracts/` 只维护稳定结构和契约，不复制阶段排期。
5. `history/` 文档不随当前实现持续改写；事实变化通过当前计划、ADR 或实施日志表达。
6. 移动或新增文档时必须同步修改仓库内链接（含本文件的分类索引与 [ADR 一览](#adr-一览)），并执行 `docs/check-doc-links.ps1`（CI hygiene 作业与 `deployments/verify.ps1` 都会执行同一脚本）。
7. 正式文档不得链接 `deployments/test-results/`：该目录按保留策略滚动清理。阶段报告引用的运行产物必须固化到 `reports/evidence/<phase>/`，由脚本强制。
8. 本地验证不得外推为生产、高并发或跨服务可靠性结论。
