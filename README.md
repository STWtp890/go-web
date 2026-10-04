# go-web monorepo

本仓库包含 Web 产品、来源专属服务、RPC 协议与本地部署资源。

## 模块结构

| 模块 | 职责 |
| --- | --- |
| apps/gin-backend | Gin HTTP 入口、Web 身份与来源服务适配器 |
| apps/simple-frontend | Vue Web 应用 |
| apps/document-service | 正式文档、知识空间与生命周期事实源 |
| apps/document-search | 正式文档检索服务，检索实现保持现状 |
| apps/qq-search | QQ 原始内容服务，检索实现保持现状 |
| apps/mixin-search | 既有检索运行时，检索实现保持现状 |
| packages/proto | 跨服务 RPC 协议源 |
| packages/gen | RPC 生成代码 |
| packages/serviceauth | 服务身份与调用边界 |

七个 Go module 由根目录 go.work 统一组织。服务之间通过协议协作，不直接依赖彼此的私有实现。

本轮改造处理过时内容、代码拆分、项目结构与冗余设计；全文和向量检索实现不在改动范围内。

文档分类与阅读顺序见 [docs/README.md](./docs/README.md)。长期产品边界见 [生态演进指南](./docs/ECOSYSTEM_EVOLUTION_GUIDE.md)。服务所有权架构见 [ADR-017](./docs/adr/017-source-owned-document-and-search-services.md)。当前任务与验收见 [实施计划](./docs/planning/CURRENT_IMPLEMENTATION_PLAN.md)。

目录职责、领域分层和依赖约束见 [项目结构](./docs/architecture/PROJECT_STRUCTURE.md)。
