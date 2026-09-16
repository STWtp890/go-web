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
6. [调用方 capability 契约](./contracts/SERVICE_CALL_CAPABILITY.md)
7. [阶段 1 实施日志](./reports/PHASE1_IMPLEMENTATION_LOG.md)
8. [阶段 2 实施日志](./reports/PHASE2_IMPLEMENTATION_LOG.md)
9. 需要追溯决策时阅读 [ADR](./adr/)；需要理解历史方案时阅读 [history](./history/)

## 当前执行重点

生态阶段二已收口，P2.0-P2.5 全部通过；阶段三的实施基线已建立，P3.0 与 P3.1 已完成。`mixin-search` 现在只接受携带 capability 的调用方，索引写入与检索分离，请求范围只能缩小不能扩大。

当前实施包为 P3.2：改造在线检索并发模型——去掉读路径上的每请求全量控制状态加载、全局排他锁和持锁网络调用，使索引写入与在线查询不再相互阻塞。PostgreSQL BM25 仍是正式读取方，读取切换属于独立的 B 线。

具体任务、依赖和验收门禁以 [当前实施计划](./planning/CURRENT_IMPLEMENTATION_PLAN.md) 为准；判断依据见 [ADR-012](./adr/012-multi-consumer-search-boundary-and-critical-path-shift.md)，调用凭据格式见 [调用方 capability 契约](./contracts/SERVICE_CALL_CAPABILITY.md)。

## 分类索引

| 分类 | 文档 | 职责 |
| --- | --- | --- |
| 上位目标 | [ECOSYSTEM_EVOLUTION_GUIDE.md](./ECOSYSTEM_EVOLUTION_GUIDE.md) | 定义跨项目产品定位、数据所有权、知识域隔离和宏观阶段 |
| 当前计划 | [planning/CURRENT_IMPLEMENTATION_PLAN.md](./planning/CURRENT_IMPLEMENTATION_PLAN.md) | 当前唯一排期与实施入口 |
| 架构 | [architecture/PROJECT_STRUCTURE.md](./architecture/PROJECT_STRUCTURE.md) | 目录职责、领域分层和依赖方向 |
| 约定 | [architecture/DEVELOPMENT_CONVENTIONS.md](./architecture/DEVELOPMENT_CONVENTIONS.md) | 新增代码落位规则、身份与 HTTP 出入口契约、遗留模块冻结基线与迁移待办 |
| 契约 | [contracts/MIXIN_SEARCH_V1_CONTRACT.md](./contracts/MIXIN_SEARCH_V1_CONTRACT.md) | 当前 RPC 边界和字段语义 |
| 调用边界 | [contracts/SERVICE_CALL_CAPABILITY.md](./contracts/SERVICE_CALL_CAPABILITY.md) | 调用方 capability 格式、角色权限与范围包含规则 |
| 实施证据 | [reports/PHASE0_COMPLETION_REPORT.md](./reports/PHASE0_COMPLETION_REPORT.md)、[reports/PHASE1_IMPLEMENTATION_LOG.md](./reports/PHASE1_IMPLEMENTATION_LOG.md)、[reports/PHASE2_IMPLEMENTATION_LOG.md](./reports/PHASE2_IMPLEMENTATION_LOG.md) | 记录已经验证的结果，不承担后续排期 |
| 证据快照 | [reports/evidence/](./reports/evidence/) | 阶段报告引用的运行产物归档，内容不随后续实现改写 |
| 决策 | [adr/](./adr/) | 保存已接受、被取代或附条件的架构决策 |
| 历史 | [history/](./history/) | 保存初始方案、冻结基线和已被取代的专项记录 |

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
| 阶段三：QQ 身份与知识空间融合 | 阶段 3（P3.0-P3.6） | P3.0、P3.1 已完成；P3.2 进行中 |
| 阶段四：聊天记录域建设 | 后续专项阶段 | 未进入；Chat/WebSocket 保持代码存在但不接入 |
| 阶段五：治理、可靠性与持续演进 | 持续治理阶段 | 未进入 |
| 文档 BM25 交接 | B 线（独立排期） | 未完成；PostgreSQL BM25 仍是正式读取方 |

## 维护规则

1. 产品定位、事实源、权限边界或知识域划分发生变化时，更新根目录的生态核心目标，必要时新增 ADR。
2. 当前范围、顺序、非目标和验收门禁只更新 `planning/CURRENT_IMPLEMENTATION_PLAN.md`。
3. 实施包通过后，将实测结果写入对应阶段报告；已冻结的阶段报告不因后续计划变化而改写。
4. `architecture/` 与 `contracts/` 只维护稳定结构和契约，不复制阶段排期。
5. `history/` 文档不随当前实现持续改写；事实变化通过当前计划、ADR 或实施日志表达。
6. 移动或新增文档时必须同步修改仓库内链接，并执行 `docs/check-doc-links.ps1`（CI hygiene 作业与 `deployments/verify.ps1` 都会执行同一脚本）。
7. 正式文档不得链接 `deployments/test-results/`：该目录按保留策略滚动清理。阶段报告引用的运行产物必须固化到 `reports/evidence/<phase>/`，由脚本强制。
8. 本地验证不得外推为生产、高并发或跨服务可靠性结论。
