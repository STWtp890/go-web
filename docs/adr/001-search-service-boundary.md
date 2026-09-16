# ADR-001：mixin-search 服务边界与首发存储

- 状态：已接受
- 日期：2026-09-11
- 决策范围：阶段 0 至阶段 2

## 背景

检索能力需要从 Gin 应用内的 PostgreSQL BM25 实现逐步演进为可独立部署的混合检索服务，同时不能让阶段 0 引入双写、双读或新的线上故障面。

本 ADR 原先只写了“需要独立部署”，没有写出为什么。理由来自既定消费者拓扑，而不是架构偏好：

1. `py-agent` 必须经 RPC 检索知识库与聊天记录；
2. 聊天语料索引归 `mixin-search`；
3. 聊天 BM25 既不能落在消费者 `py-agent`（消费者不应持有检索内核），也不能落在 `go-web`（不拥有聊天），因此只能落在 `mixin-search`；
4. 文档 BM25 随后跟进，才能形成单一、可融合排序的检索内核；
5. `go-web` 因此不再维护一套与 `mixin-search` 并行演进的独立文档检索内核。

也就是说，“检索能力不分散”是这条链的**结论**，不是它的**前提**。完整论证、必要的限定（联邦检索同样可行，但因跨服务排序、权限过滤与故障组合代价被拒绝）、两个关键路径转折点，以及首个在线消费者接入前的门禁，见 [ADR-012](./012-multi-consumer-search-boundary-and-critical-path-shift.md)。

## 决策

1. apps/mixin-search 是 monorepo 内的独立 Go module，只通过 packages/proto/mixin-search/v1 与调用方交换数据，不导入 gin-backend 的 internal 包。
2. 服务默认监听 127.0.0.1:9090，注册 mixin_search.v1.RAGService、标准 gRPC Health 和 reflection。开放 reflection 与缺少调用方身份认证都是开发期可接受的临时状态：它只在根 Compose 绑定 127.0.0.1 且唯一调用方是自身事实源时成立。首个正式在线消费者接入前必须按 [ADR-012](./012-multi-consumer-search-boundary-and-critical-path-shift.md) 补上调用身份、范围 capability 与最小暴露面。
3. Qdrant 是首个生产候选后端；memory 仅用于单元测试和本地演示；pgvector 保留为实验性替代实现，不作为首发路径。
4. 阶段 0 保持独立 Compose 依赖栈，不加入根 docker-compose.yaml。根栈接入推迟到阶段 2 的影子写入前。
5. Qdrant 接入真实流量前，必须完成 ACL、active revision 与 lifecycle revision 的服务端过滤；P2.1 已按 [ADR-006](./006-mixin-search-control-state-commit-order.md) 将控制状态升级为 PostgreSQL 持久化，P2.2 已按 [ADR-007](./007-qdrant-control-projection-and-filtering.md) 完成候选级过滤，P2.3/P2.4 已完成可靠投递、对账与根 Compose 影子索引，P2.5 已按 [ADR-010](./010-shadow-query-evaluation-gate.md) 完成影子查询评估。当前 local-hash-v1 结论为 KEEP_BM25，正式读取尚未切换。

## 结果

- 可独立验证 RPC 和存储适配器，不改变现有 HTTP 生产路径。
- 增加一个明确的部署单元，但阶段 0 不增加根栈运行依赖。
- 首发存储选择唯一，避免同时产品化两套向量后端。

## 验证

- apps/mixin-search 执行 go test ./...
- packages/proto/verify-generated.ps1 通过
- docker compose -f apps/mixin-search/compose.yaml config --quiet 通过
