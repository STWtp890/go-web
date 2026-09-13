# ADR-004：PostgreSQL BM25 迁移策略

- 状态：部分被 ADR-005 取代；长期检索所有权方向继续有效，生产式切换策略推迟
- 日期：2026-09-11
- 取代说明：[ADR-005](./005-development-baseline-over-production-migration.md) 允许当前开发阶段直接重建数据库和接口，不执行本文的影子切换、回退与兼容门禁

## 背景

现有 Markdown 搜索通过 PostgreSQL BM25 投影与 HTTP 接口提供服务。mixin-search 的 v1 RPC 已具备混合检索契约，但尚未进入真实流量，也未完成生产级 ACL 下推。

## 决策

1. 阶段 0 冻结现有 HTTP 与 BM25 行为作为回归基线。
2. 阶段 1 将搜索投影迁移为显式拥有的 document_search_projection，并把写入集中到单一应用服务；不改变读取提供方。
3. 阶段 2 使用 Outbox 影子写入 mixin-search，读取仍以 PostgreSQL 为准，并对结果做离线对比。
4. 阶段 3 通过受控开关按租户或请求切换读取提供方；发生错误、超时或质量退化时回退 PostgreSQL。
5. 在等价性、ACL、删除传播、延迟和回滚演练达标前，不移除 BM25 索引与验证脚本。

## 结果

- 迁移以可回退的提供方切换代替一次性替换。
- 阶段 1 可以进行破坏性内部重构，但对外 HTTP 行为保持稳定。
- 过渡期承担双索引成本，并获得可测量的质量与一致性证据。

## 验证

deployments/verify.ps1 继续执行 bm25_only_verify.sql；阶段 2 增加影子索引一致性与召回质量报告。