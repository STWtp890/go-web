# gin-backend

`gin-backend` 是仓库内的 Gin HTTP 服务。目录采用“领域纵向聚合、领域内部按需分层”：可执行入口集中在 `cmd`，不可供外部复用的实现放在 `internal`，业务能力集中在 `internal/modules`。

## 目录结构

```text
gin-backend/
├── cmd/
│   ├── server/          # HTTP 服务入口，只负责调用 internal/app
│   └── tools/           # 不参与产品部署的开发与验收工具
│       ├── pemgenerator/   # RSA 密钥生成命令
│       └── runtimeapitest/ # 运行时 API 验证命令
├── configs/             # 本地与容器配置模板
├── internal/
│   ├── app/             # 启动、配置和依赖生命周期装配
│   ├── architecture/    # 模块边界和依赖方向测试
│   ├── modules/         # 按 document/auth/manager 等领域纵向聚合
│   │   └── document/    # Web 文档面：interfaces/sourceowned + infrastructure 服务客户端
│   ├── platform/httpserver/ # Gin 引擎、探针、路由组和全局 middleware
│   ├── common/          # 迁移中的跨业务基础能力
│   ├── config/          # 配置模型、加载和校验
│   └── model/           # 迁移中的旧 ORM、缓存实体和数据存取
├── Dockerfile
├── go.mod
└── go.sum
```

旧文档索引链路（`cmd/document-index-worker`、`cmd/document-index-admin`、`cmd/document-search-eval`、`internal/modules/document/{application,evaluation,infrastructure/postgresql,infrastructure/cache}`、`internal/modules/space`、`cmd/tools/spacectl`）已在 ADR-017 阶段 C 删除：正式文档写入、空间与成员管理由 `apps/document-service` 负责，正式文档检索与评测由 `apps/document-search` 负责，运维入口改为 `apps/document-service/cmd/document-service-spacectl`。

`internal/app` 是组合根，只负责进程启动、配置和依赖生命周期；`internal/platform/httpserver` 集中创建 Gin 引擎、注册全局/路由组 middleware、探针和模块路由。目标依赖方向为 `interfaces → application → domain` 以及 `infrastructure → domain`，具体实现由 `app` 装配；业务模块不反向依赖 `cmd` 或 `app`。ADR-017 之后 `document` 的当前结构是：`interfaces/sourceowned` 是唯一的 Web 文档适配器，经 `infrastructure/documentservice`（写命令与详情读取）与 `infrastructure/documentsearch`（正式检索）访问来源专属服务，`domain` 保留适配器共用的领域类型；Web 不再读写正式文档业务表。旧 Markdown 运行模块与旧文档应用层/仓储已删除；其他领域先完成 `internal/modules/<domain>` 归组，再随业务改造渐进迁移。当前项目没有需要对外承诺兼容性的 Go 库，因此不设置 `pkg` 目录。

完整目录职责、依赖矩阵和阶段性兼容边界见 [`docs/architecture/PROJECT_STRUCTURE.md`](../../docs/architecture/PROJECT_STRUCTURE.md)。Chat/WebSocket 代码虽然保留在 `modules/chat`，但不初始化服务、不注册路由。

## 常用命令

在 `gin-backend` 目录执行：

```bash
# 启动服务
go run ./cmd/server

# 生成默认 RSA 密钥
# 注意：这是启动根 Compose 栈的前置步骤。密钥不进镜像（见根 .dockerignore 的 **/*.pem），
# 由 docker-compose.yaml 以只读卷挂载到容器内 /app/configs/。
go run ./cmd/tools/pemgenerator

# 生成服务边界密钥（同一前置：bootstrap 会同时生成 JWT 与边界密钥两者）
# 密钥不进镜像（见根 .dockerignore 的 **/*.key），由只读卷挂载到 /app/secrets/。
powershell -NoProfile -ExecutionPolicy Bypass -File ../../deployments/bootstrap.ps1

# 运行 API 验证程序
go run ./cmd/tools/runtimeapitest -help

# 质量检查
go test ./internal/architecture   # 依赖方向（10 条方向性 import 断言）
go test ./...
go vet ./...
```

文档索引的运维入口已不在本应用内：正式文档重建与索引状态见 `apps/document-search`（`RebuildIndex` / `GetIndexStatus`），空间与成员管理见 `apps/document-service/cmd/document-service-spacectl`。

服务默认读取 `configs/config.yaml`；设置 `GIN_CONFIG_PATH` 可以覆盖配置文件路径。数据库结构由 `deployments/postgresql/entryscript/00-init.sh` 在全新开发数据卷上统一初始化。

凡是要调用来源专属服务（document-service、document-search）的进程都需要边界密钥文件，路径由 `source_owned_services.capability_key_path` 指定，缺失时启动失败关闭。同一份密钥同时用于服务身份断言与资源范围 capability；`source_owned_services.caller` 必须是登记过、可以声明 `web:*` 主体的服务身份（本应用为 `go-web`）。凭据格式与角色划分见 [身份与凭证契约](../../docs/contracts/SERVICE_IDENTITY_AND_CAPABILITY.md)。

User 与 Manager 实体缓存统一由进程级运行时创建，共享 Redis 适配器和 singleflight；内存回退在 `entities` 分区执行 TTL + LRU，并受条目数和字节数双上限约束。User/Manager 每次先从 PostgreSQL 读取权威 `cache_revision`，再按 `id+revision` 访问缓存，避免 Evict 与在途回填竞态。Evict 只负责旧键回收并报告删除错误。JWT 会话状态不使用该内存回退：Redis 原子会话操作不可用时保持失败关闭。完整决策见 [ADR-011](../../docs/adr/011-bounded-cache-runtime-and-revision-fencing.md)。

历史说明：P2.3–P2.5 的文档索引投递 Worker、对账/重建命令与 BM25 后影子查询链路已在 ADR-017 阶段 C 删除。当前 `/readyz` 只检查本进程拥有的 PostgreSQL 与 Redis 连接，不再注册也不再健康检查文档业务库。

## 结构依据

- [Go 官方：Organizing a Go module](https://go.dev/doc/modules/layout)
- [Go 社区常用项目布局说明](https://github.com/golang-standards/project-layout)

社区布局并不是必须完整复制的模板。本项目只采用当前规模需要的目录，不预设空的 `api`、`pkg`、`scripts` 或 `test` 层。
