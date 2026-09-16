# gin-backend

`gin-backend` 是仓库内的 Gin HTTP 服务。目录采用“领域纵向聚合、领域内部按需分层”：可执行入口集中在 `cmd`，不可供外部复用的实现放在 `internal`，业务能力集中在 `internal/modules`。

## 目录结构

```text
gin-backend/
├── cmd/
│   ├── document-index-admin/  # P2.3 对账、重建与失败项管理命令
│   ├── document-index-worker/ # P2.3 文档索引可靠投递进程
│   ├── document-search-eval/  # P2.5 固定样本质量评估与报告
│   ├── server/          # HTTP 服务入口，只负责调用 internal/app
│   └── tools/           # 不参与产品部署的开发与验收工具
│       ├── pemgenerator/   # RSA 密钥生成命令
│       └── runtimeapitest/ # 运行时 API 验证命令
├── configs/             # 本地与容器配置模板
├── internal/
│   ├── app/             # 启动、配置和依赖生命周期装配
│   ├── architecture/    # 模块边界和依赖方向测试
│   ├── modules/         # 按 document/auth/manager 等领域纵向聚合
│   ├── platform/httpserver/ # Gin 引擎、探针、路由组和全局 middleware
│   ├── common/          # 迁移中的跨业务基础能力
│   ├── config/          # 配置模型、加载和校验
│   └── model/           # 迁移中的旧 ORM、缓存实体和数据存取
├── Dockerfile
├── go.mod
└── go.sum
```

`internal/app` 是组合根，只负责进程启动、配置和依赖生命周期；`internal/platform/httpserver` 集中创建 Gin 引擎、注册全局/路由组 middleware、探针和模块路由。目标依赖方向为 `interfaces → application → domain` 以及 `infrastructure → domain`，具体实现由 `app` 装配；业务模块不反向依赖 `cmd` 或 `app`。`document` 已按该结构落地：`application` 包含命令与查询用例，`interfaces/http` 承担当前 Documents HTTP 契约的适配，`infrastructure/postgresql` 与 `infrastructure/cache` 实现持久化和版本化缓存。旧 Markdown 运行模块已删除；其他领域先完成 `internal/modules/<domain>` 归组，再随业务改造渐进迁移。当前项目没有需要对外承诺兼容性的 Go 库，因此不设置 `pkg` 目录。

完整目录职责、依赖矩阵和阶段性兼容边界见 [`docs/architecture/PROJECT_STRUCTURE.md`](../../docs/architecture/PROJECT_STRUCTURE.md)。Chat/WebSocket 代码虽然保留在 `modules/chat`，但不初始化服务、不注册路由。

## 常用命令

在 `gin-backend` 目录执行：

```bash
# 启动服务
go run ./cmd/server

# 另开进程消费文档索引 Outbox；地址也可由 MIXIN_SEARCH_GRPC_ADDRESS 提供
go run ./cmd/document-index-worker -config configs/config.yaml -mixin-search-address 127.0.0.1:9090

# 查看失败项、重放、对账或启动全量重建
go run ./cmd/document-index-admin -config configs/config.yaml failures list
go run ./cmd/document-index-admin -config configs/config.yaml status
go run ./cmd/document-index-admin -config configs/config.yaml shadow-status -source evaluation
go run ./cmd/document-index-admin -config configs/config.yaml -mixin-search-address 127.0.0.1:9090 reconcile
go run ./cmd/document-index-admin -config configs/config.yaml rebuild start

# 生成默认 RSA 密钥
go run ./cmd/tools/pemgenerator

# 运行 API 验证程序
go run ./cmd/tools/runtimeapitest -help

# 在已启动的根 Compose 一次性环境中生成 P2.5 质量报告
go run ./cmd/document-search-eval -mixin-search-address 127.0.0.1:19090

# 质量检查
go test ./...
go vet ./...

# P2.3 独立真实数据库与跨服务重建验收
powershell -NoProfile -ExecutionPolicy Bypass -File ./verify-index-delivery.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File ./verify-index-rebuild-e2e.ps1
```

服务默认读取 `configs/config.yaml`；设置 `GIN_CONFIG_PATH` 可以覆盖配置文件路径。数据库结构由 `deployments/postgresql/entryscript/00-init.sh` 在全新开发数据卷上统一初始化。

User、Manager 与 Document 实体缓存统一由进程级运行时创建，共享 Redis 适配器和 singleflight；内存回退按 entities/documents 分区执行 TTL + LRU，并受条目数和字节数双上限约束。User/Manager 每次先从 PostgreSQL 读取权威 `cache_revision`，再按 `id+revision` 访问缓存，避免 Evict 与在途回填竞态。Evict 只负责旧键回收并报告删除错误。JWT 会话状态不使用该内存回退：Redis 原子会话操作不可用时保持失败关闭。完整决策见 [ADR-011](../../docs/adr/011-bounded-cache-runtime-and-revision-fencing.md)。

P2.3 将 HTTP 服务与索引投递进程分离：文档命令只在原事务内写入投递事件，不在事务中发起 RPC；Worker 在提交后按文档顺序调用 mixin-search，并使用稳定操作 ID、租约、重试和死信恢复。P2.4 已在根 Compose 持续运行该影子索引链路。P2.5 在 BM25 返回后通过有界队列异步执行同查询，按 runtime/evaluation 来源记录结果、延迟、错误和事实复核；队列满或 mixin-search 故障不会改变 HTTP 返回，`/readyz` 仍只依赖正式 PostgreSQL/Redis。当前 local-hash-v1 评估结论是 KEEP_BM25。

## 结构依据

- [Go 官方：Organizing a Go module](https://go.dev/doc/modules/layout)
- [Go 社区常用项目布局说明](https://github.com/golang-standards/project-layout)

社区布局并不是必须完整复制的模板。本项目只采用当前规模需要的目录，不预设空的 `api`、`pkg`、`scripts` 或 `test` 层。
