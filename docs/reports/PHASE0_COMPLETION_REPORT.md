# 阶段 0 完成记录

> 状态：Completed（历史完成报告，已冻结）
> 完成日期：2026-09-11
> 历史下一阶段：P1.0 迁移基础设施与可逆演练（已完成）
> 当前排期：[`CURRENT_IMPLEMENTATION_PLAN.md`](../planning/CURRENT_IMPLEMENTATION_PLAN.md)

## 完成结论

阶段 0 的现状审计、边界固化、v1 RPC 收口和工程门禁已完成。阶段 1 可以按照 [`PHASE1_BREAKING_REFACTOR_DIRECTION.md`](../history/PHASE1_BREAKING_REFACTOR_DIRECTION.md) 开始，不再重新讨论检索服务归属、事实源、Chat 接入或 BM25 迁移路线。

## 交付清单

- 四份 ADR 均为已接受状态。
- [`CURRENT_SYSTEM_INVENTORY.md`](../history/CURRENT_SYSTEM_INVENTORY.md) 记录应用、数据、缓存、索引、接口、配置、部署和验证入口。
- [`CONTRACT_BASELINE.md`](../history/CONTRACT_BASELINE.md) 冻结 HTTP、BM25、Chat 404 和 mixin-search/v1 行为。
- mixin-search/v1 已统一六个 RPC 与生成代码目录。
- gRPC Health 已注册，并有进程内 RPC 回归测试。
- Proto 生成一致性检查已接入 deployments/verify.ps1。
- Qdrant 明确为首个生产候选；pgvector 为实验性替代；memory 仅测试和演示。
- Chat 保持代码存在但应用不接入。
- 阶段 1 破坏性改造方案已经审计并批准。

## 验收映射

| 标准 | 结果 | 证据 |
|---|---|---|
| ADR 有明确状态 | 通过 | docs/adr/001 至 004 |
| 数据、缓存和索引唯一所有者 | 通过 | [`CURRENT_SYSTEM_INVENTORY.md`](../history/CURRENT_SYSTEM_INVENTORY.md) |
| HTTP 与 BM25 回归基线 | 通过 | [`CONTRACT_BASELINE.md`](../history/CONTRACT_BASELINE.md)、bm25_only_verify.sql、现有测试 |
| gRPC 生成一致性 | 通过 | packages/proto/verify-generated.ps1 |
| 两个 Go 模块可构建测试 | 通过 | go test ./... |
| 验证类别可区分 | 通过 | [`CURRENT_SYSTEM_INVENTORY.md`](../history/CURRENT_SYSTEM_INVENTORY.md)、deployments/verify.ps1 |
| Chat、QQ 与文档领域分离 | 通过 | ADR-003 |
| 阶段 1 输入充分 | 通过 | [`PHASE1_BREAKING_REFACTOR_DIRECTION.md`](../history/PHASE1_BREAKING_REFACTOR_DIRECTION.md) |

## 保留说明

根 Compose 仍不接入 mixin-search，这是阶段 0 的明确隔离规则而非缺项。真实 Qdrant 集成、ACL 下推、Outbox 与跨服务端到端验证属于阶段 2 进入流量前的门禁。

当前工作树包含本轮及此前未提交变更，因此“干净检出”在提交或 PR 后由同一验证命令复跑；本完成记录只陈述当前源码快照的实测结果。

## 本轮实测

- apps/gin-backend 执行 go test ./...：退出码 0。
- apps/mixin-search 执行 go test ./...：退出码 0，包含 gRPC Health 测试。
- packages/proto/verify-generated.ps1：输出 PASS generated mixin-search/v1 Go code matches its proto source，退出码 0。
- apps/simple-frontend 执行 npm run type-check：退出码 0。
- apps/simple-frontend 执行 npm run build：Vite 完成 1863 个模块转换，退出码 0。
- 根 Compose 与 apps/mixin-search/compose.yaml 执行 config --quiet：退出码 0。
- git diff --check：退出码 0，仅报告既有 Windows 行尾转换提示。