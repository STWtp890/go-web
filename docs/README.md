# go-web 文档导航

`docs/` 以“根目录只放入口文档、专题内容进入分类目录”为原则组织。阶段状态和下一步只在当前计划中维护，避免多份文档重复排期。

## 目录结构

```text
docs/
├── README.md                         # 文档导航与维护规则
├── ECOSYSTEM_EVOLUTION_GUIDE.md      # 生态核心目标与长期边界
├── planning/                         # 当前实施计划
├── architecture/                     # 项目结构与依赖约束
├── contracts/                        # 当前跨服务契约
├── reports/                          # 阶段完成报告与实施证据
├── adr/                              # 架构决策记录
└── history/                          # 冻结或已被取代的历史基线
```

## 推荐阅读顺序

1. [生态演进核心目标](./ECOSYSTEM_EVOLUTION_GUIDE.md)
2. [当前实施计划](./planning/CURRENT_IMPLEMENTATION_PLAN.md)
3. [项目结构与依赖约束](./architecture/PROJECT_STRUCTURE.md)
4. [结构与复用开发约定](./architecture/DEVELOPMENT_CONVENTIONS.md)（新增代码落位与复用规则的唯一约定）
5. [mixin-search/v1 契约](./contracts/MIXIN_SEARCH_V1_CONTRACT.md)
6. [阶段 1 实施日志](./reports/PHASE1_IMPLEMENTATION_LOG.md)
7. [阶段 2 实施日志](./reports/PHASE2_IMPLEMENTATION_LOG.md)
8. 需要追溯决策时阅读 [ADR](./adr/)；需要理解历史方案时阅读 [history](./history/)

## 当前执行重点

生态阶段二的 go-web 阶段 1 与阶段 2 已完成，P2.0-P2.5 均通过。P2.5 已建立异步影子查询、来源分层观测和七类质量评估；当前 local-hash-v1 不满足正式语义读取切换条件，因此 PostgreSQL BM25 继续作为正式读取方。

具体任务、依赖和验收门禁以 [当前实施计划](./planning/CURRENT_IMPLEMENTATION_PLAN.md) 为准。
## 分类索引

| 分类 | 文档 | 职责 |
| --- | --- | --- |
| 上位目标 | [ECOSYSTEM_EVOLUTION_GUIDE.md](./ECOSYSTEM_EVOLUTION_GUIDE.md) | 定义跨项目产品定位、数据所有权、知识域隔离和宏观阶段 |
| 当前计划 | [planning/CURRENT_IMPLEMENTATION_PLAN.md](./planning/CURRENT_IMPLEMENTATION_PLAN.md) | 当前唯一排期与实施入口 |
| 架构 | [architecture/PROJECT_STRUCTURE.md](./architecture/PROJECT_STRUCTURE.md) | 目录职责、领域分层和依赖方向 |
| 约定 | [architecture/DEVELOPMENT_CONVENTIONS.md](./architecture/DEVELOPMENT_CONVENTIONS.md) | 新增代码落位规则、身份与 HTTP 出入口契约、遗留模块冻结基线与迁移待办 |
| 契约 | [contracts/MIXIN_SEARCH_V1_CONTRACT.md](./contracts/MIXIN_SEARCH_V1_CONTRACT.md) | 当前 RPC 边界和字段语义 |
| 实施证据 | [reports/PHASE0_COMPLETION_REPORT.md](./reports/PHASE0_COMPLETION_REPORT.md)、[reports/PHASE1_IMPLEMENTATION_LOG.md](./reports/PHASE1_IMPLEMENTATION_LOG.md)、[reports/PHASE2_IMPLEMENTATION_LOG.md](./reports/PHASE2_IMPLEMENTATION_LOG.md) | 记录已经验证的结果，不承担后续排期 |
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
| 阶段三：QQ 身份与知识空间融合 | 后续专项阶段 | 未进入 |
| 阶段四：聊天记录域建设 | 后续专项阶段 | 未进入；Chat/WebSocket 保持代码存在但不接入 |
| 阶段五：治理、可靠性与持续演进 | 持续治理阶段 | 未进入 |

## 维护规则

1. 产品定位、事实源、权限边界或知识域划分发生变化时，更新根目录的生态核心目标，必要时新增 ADR。
2. 当前范围、顺序、非目标和验收门禁只更新 `planning/CURRENT_IMPLEMENTATION_PLAN.md`。
3. 实施包通过后，将实测结果写入对应阶段报告；已冻结的阶段报告不因后续计划变化而改写。
4. `architecture/` 与 `contracts/` 只维护稳定结构和契约，不复制阶段排期。
5. `history/` 文档不随当前实现持续改写；事实变化通过当前计划、ADR 或实施日志表达。
6. 移动或新增文档时必须同步修改仓库内链接，并执行文档链接检查。
7. 本地验证不得外推为生产、高并发或跨服务可靠性结论。
