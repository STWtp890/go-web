# gin-backend

`gin-backend` 是仓库内的 Gin HTTP/WebSocket 服务。目录遵循 Go 官方对服务项目的建议：可执行入口集中在 `cmd`，不可供外部复用的实现放在 `internal`，配置模板放在 `configs`。

## 目录结构

```text
gin-backend/
├── cmd/
│   ├── server/          # HTTP 服务入口，只负责调用 internal/app
│   ├── pemgenerator/    # RSA 密钥生成命令
│   └── runtimeapitest/  # 运行时 API 验证命令
├── configs/             # 本地与容器配置模板
├── internal/
│   ├── app/             # 配置、依赖生命周期、路由和服务装配
│   ├── common/          # 跨业务复用的私有基础能力
│   ├── config/          # 配置模型、加载和校验
│   ├── middleware/      # Gin 全局与鉴权中间件
│   ├── model/           # ORM、缓存实体和数据存取
│   └── service/         # auth/chat/manager/markdown 等业务模块
├── Dockerfile
├── go.mod
└── go.sum
```

依赖方向保持为：`cmd/server` → `internal/app` → `config/middleware/service/model/common`。业务模块不反向依赖 `cmd` 或 `app`。当前项目没有需要对外承诺兼容性的 Go 库，因此不设置 `pkg` 目录。

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
go test ./...
go vet ./...
```

服务默认读取 `configs/config.yaml`；设置 `GIN_CONFIG_PATH` 可以覆盖配置文件路径。数据库结构继续由仓库根目录的 `deployments/postgresql` 脚本统一管理。

## 结构依据

- [Go 官方：Organizing a Go module](https://go.dev/doc/modules/layout)
- [Go 社区常用项目布局说明](https://github.com/golang-standards/project-layout)

社区布局并不是必须完整复制的模板。本项目只采用当前规模需要的目录，不预设空的 `api`、`pkg`、`scripts` 或 `test` 层。
