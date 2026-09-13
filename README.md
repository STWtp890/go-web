# go-web monorepo

本仓库承载文档知识管理生态中的 Web 产品、检索服务、共享协议和本地部署资源。

```text
apps/
  gin-backend/       Gin HTTP 服务；Chat/WebSocket 源码保留但未接入
  simple-frontend/   Vue Web 应用
  mixin-search/      Eino 文档处理与混合检索服务
packages/
  proto/             跨应用 RPC 协议源
  gen/               跨应用共享的 RPC 生成代码
docs/                产品、架构与演进文档
deployments/         本地依赖、Compose 与验证脚本
```

两个 Go 应用与共享生成代码保持独立 module，并由根目录 `go.work` 统一组织。应用之间通过稳定协议协作，不直接依赖彼此的私有实现。

文档分类与阅读顺序见 [`docs/README.md`](./docs/README.md)。产品生态的长期边界与演进原则见 [`docs/ECOSYSTEM_EVOLUTION_GUIDE.md`](./docs/ECOSYSTEM_EVOLUTION_GUIDE.md)，当前阶段排期与验收门禁见 [`docs/planning/CURRENT_IMPLEMENTATION_PLAN.md`](./docs/planning/CURRENT_IMPLEMENTATION_PLAN.md)。

当前目录职责、领域分层和依赖约束见 [docs/architecture/PROJECT_STRUCTURE.md](./docs/architecture/PROJECT_STRUCTURE.md)。
