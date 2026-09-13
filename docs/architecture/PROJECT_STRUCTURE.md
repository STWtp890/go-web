# go-web 项目结构与依赖约束

> 状态：阶段 1 结构基线，P1.3 已落地
> 生效日期：2026-09-12

## 1. 结构原则

本仓库采用“仓库按部署单元划分、应用内部按领域纵向聚合、领域内部按必要层级横向分层”的结构：

1. `apps` 放置可独立构建和部署的应用，`packages` 只放跨应用共享且需要兼容承诺的协议与生成代码。
2. Go 可执行入口集中在 `cmd`，应用私有实现集中在 `internal`；当前没有对外发布的 Go 库，因此不建立 `pkg`。
3. 业务代码优先归入 `internal/modules/<domain>`，避免全局 `handler`、`service`、`repository` 目录按技术类型分散同一业务。
4. 一个领域规模足够大时，才在领域内部拆分 `domain`、`application`、`interfaces`、`infrastructure`；不预建空目录。
5. `internal/app` 是唯一组合根，负责配置、实例化和生命周期；`internal/platform/httpserver` 负责 Gin 引擎、探针、中间件及路由装配，二者都不承载业务规则。
6. 领域层不依赖 Gin、GORM 或其他领域；应用层只依赖领域模型和端口；基础设施层实现领域端口，由组合根注入。
7. 共享目录只承载真实的跨域能力。仍位于 `internal/common` 和 `internal/model` 的代码视为迁移中的兼容层，不再接收新的领域逻辑。

## 2. 仓库结构

```text
go-web/
├── apps/
│   ├── gin-backend/       # HTTP API、认证和文档应用服务
│   ├── mixin-search/      # 独立的文档处理与混合检索服务
│   └── simple-frontend/   # Vue Web 应用
├── packages/
│   ├── proto/             # 跨应用 RPC 契约源
│   └── gen/               # 由契约生成的共享 Go 代码
├── deployments/           # Compose、数据库空库初始化制品和验收脚本
├── docs/                  # 架构、契约、ADR 和阶段实施记录
├── docker-compose.yaml    # 本地系统组合入口
└── go.work                # Go workspace，仅组织本仓库 Go module
```

应用之间只能通过稳定协议或部署接口协作，不直接导入另一个应用的 `internal` 实现。

## 3. gin-backend 领域结构

```text
apps/gin-backend/
├── cmd/
│   ├── server/                 # 生产服务入口
│   ├── pemgenerator/           # 密钥生成工具
│   └── runtimeapitest/         # 运行时 API 验证工具
├── configs/                    # 配置模板
└── internal/
    ├── app/                    # 组合根：启动、配置、依赖和生命周期
    ├── architecture/           # 可执行的包依赖约束测试
    ├── modules/
    │   ├── document/
    │   │   ├── domain/         # 聚合、值对象、命令/查询端口
    │   │   ├── application/    # CommandService 与 QueryService
    │   │   ├── interfaces/
    │   │   │   └── http/       # Documents HTTP 契约的 Gin 适配器
    │   │   └── infrastructure/
    │   │       ├── cache/      # 修订号参与键的版本化读缓存
    │   │       └── postgresql/ # 命令/查询端口的 PostgreSQL 实现
    │   ├── auth/               # 认证领域，待按用例渐进内聚
    │   ├── manager/            # 管理员领域，待按用例渐进内聚
    │   ├── chat/               # 代码保留但不注册
    │   └── aiagent/            # 代码保留但未接入组合根
    ├── platform/httpserver/    # Gin 引擎、探针、路由和全局中间件
    ├── config/                 # 配置加载与校验
    ├── common/                 # 迁移中的跨域基础能力
    └── model/                  # 迁移中的旧 ORM/cache/store
```

`document` 是阶段 1 的目标结构样板。其他领域已完成纵向归组，但原有的 `api/handler/logic/types` 内部分层暂时保留；后续只在对应领域发生功能改造时迁移，不做无业务收益的一次性重写。

## 4. 依赖方向

```text
cmd ──> app (composition root)
          ├──> platform/httpserver ──> module interfaces
          ├──> application ────────────────> domain
          └──> infrastructure ─────────────> domain
```

约束如下：

| 位置 | 可以依赖 | 禁止依赖 |
| --- | --- | --- |
| `domain` | Go 标准库 | Gin、GORM、application、interfaces、infrastructure、其他领域 |
| `application` | 本领域 `domain`、标准库 | Gin、GORM、具体数据库实现、组合根 |
| `interfaces` | 本领域 application/domain、传输框架 | 具体仓储实现、旧存储模型 |
| `infrastructure` | 本领域 domain、驱动和 SDK | application、interfaces、组合根 |
| `platform/httpserver` | Gin、跨路由 middleware、模块路由注册接口 | 领域规则、具体仓储和连接初始化 |
| `app` | 各层公开构造函数 | 领域规则和持久化细节 |

这些约束由 `internal/architecture/dependencies_test.go` 检查。新增代码若反向依赖组合根、重新使用旧 `internal/service` 路径，或使 `document` 依赖旧 Markdown 存储，测试会失败。

## 5. 阶段性边界与待清理项

- 旧 `modules/markdown`、`model/orm/markdown`、`model/store/markdown` 与 `model/cache/markdown` 已从运行时删除；旧数据库表、索引与版本迁移资产已在 P1.4 删除；空库只建立当前 document 基线。
- `common` 与 `model` 不是长期业务归属地。共享设施应在确认被多个领域稳定复用后再提取；领域专属实现应迁回对应模块。
- Chat/WebSocket 保持“代码存在、服务不注册”的状态；目录归组不代表重新启用。
- `mixin-search/v1` 是跨进程边界，不应被伪装成 gin-backend 内部模块。阶段 1 当前仍不从文档写路径调用该 RPC。

## 6. P1.3 的结构结果

P1.3 已在 `modules/document` 内落地查询、缓存与 HTTP 适配层：

```text
modules/document/
├── domain/                 # 命令/查询模型与端口
├── application/            # CommandService + QueryService
├── interfaces/
│   └── http/               # 当前 Documents HTTP 适配器
└── infrastructure/
    ├── cache/              # cache:document:v1:view:* 版本化缓存
    └── postgresql/         # 事实写入、投影读取和 BM25 查询
```

组合根只注册 document HTTP 适配器；运行时不再引用旧 Markdown ORM、store、cache 或 handler。当前没有新旧表双写；P1.4 已同步更新仓库内前端和测试，并删除旧表、旧索引与旧 HTTP 路径。

## 7. 新增代码的落位规则

1. 先判断代码属于哪个业务能力，再决定技术层；不能确定领域归属的代码不得直接放进 `common`。
2. 只被一个领域使用的请求类型、错误、仓储和工具函数均留在该领域。
3. 只有跨应用稳定契约进入 `packages`；只在单应用复用的代码留在该应用 `internal`。
4. 目录名称使用完整、稳定的领域词；Go 包名不使用下划线，因此统一使用 `aiagent`。
5. 每次结构调整必须同时更新导入路径、测试、开发初始化入口、验收门禁和本文件中的阶段状态。

## 8. 验收命令

在 `apps/gin-backend` 执行：

```bash
go test ./internal/architecture
go test ./...
go vet ./...
```

涉及文档持久化或应用服务的改造，还必须从仓库根目录执行当前计划定义的空库初始化与集成门禁；涉及部署边界时执行 `deployments/verify.ps1`。P1.0-P1.3 的生产式迁移专项脚本已在 P1.4 清理，历史结论保留在实施日志。
