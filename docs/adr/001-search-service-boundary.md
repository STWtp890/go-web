# ADR-001：mixin-search 服务边界与首发存储

- 状态：已接受
- 日期：2026-09-11
- 决策范围：阶段 0 至阶段 2

## 背景

检索能力需要从 Gin 应用内的 PostgreSQL BM25 实现逐步演进为可独立部署的混合检索服务，同时不能让阶段 0 引入双写、双读或新的线上故障面。

## 决策

1. apps/mixin-search 是 monorepo 内的独立 Go module，只通过 packages/proto/mixin-search/v1 与调用方交换数据，不导入 gin-backend 的 internal 包。
2. 服务默认监听 127.0.0.1:9090，注册 mixin_search.v1.RAGService、标准 gRPC Health 和 reflection。
3. Qdrant 是首个生产候选后端；memory 仅用于单元测试和本地演示；pgvector 保留为实验性替代实现，不作为首发路径。
4. 阶段 0 保持独立 Compose 依赖栈，不加入根 docker-compose.yaml。根栈接入推迟到阶段 2 的影子写入前。
5. Qdrant 接入真实流量前，必须完成 ACL、active revision 与 lifecycle revision 的服务端过滤；当前进程内契约包装层只作为接口验证实现。

## 结果

- 可独立验证 RPC 和存储适配器，不改变现有 HTTP 生产路径。
- 增加一个明确的部署单元，但阶段 0 不增加根栈运行依赖。
- 首发存储选择唯一，避免同时产品化两套向量后端。

## 验证

- apps/mixin-search 执行 go test ./...
- packages/proto/verify-generated.ps1 通过
- docker compose -f apps/mixin-search/compose.yaml config --quiet 通过