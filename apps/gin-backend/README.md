# gin-backend

`gin-backend` 是仓库内的 Gin HTTP 服务。目录采用“领域纵向聚合、领域内部按需分层”：可执行入口集中在 `cmd`，不可供外部复用的实现放在 `internal`，业务能力集中在 `internal/modules`。

## 目录结构

```text
gin-backend/
├── cmd/
│   ├── server/          # HTTP 服务入口，只负责调用 internal/app
│   ├── pemgenerator/    # RSA 密钥生成命令
│   └── runtimeapitest/  # 运行时 API 验证命令
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

# 生成默认 RSA 密钥
go run ./cmd/pemgenerator

# 运行 API 验证程序
go run ./cmd/runtimeapitest -help

# 质量检查
go test ./internal/architecture
go test ./...
go vet ./...
```

服务默认读取 `configs/config.yaml`；设置 `GIN_CONFIG_PATH` 可以覆盖配置文件路径。数据库结构由 `deployments/postgresql/entryscript/00-init.sh` 在全新开发数据卷上统一初始化。

## 结构依据

- [Go 官方：Organizing a Go module](https://go.dev/doc/modules/layout)
- [Go 社区常用项目布局说明](https://github.com/golang-standards/project-layout)

社区布局并不是必须完整复制的模板。本项目只采用当前规模需要的目录，不预设空的 `api`、`pkg`、`scripts` 或 `test` 层。
