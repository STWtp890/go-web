# go-web 项目结构与依赖约束

> 状态：七个 Go module 与三个来源专属服务已在工作区；阶段 A（Web 文档链路与 Go 查询权限）已完成并通过真实 Web 验收，阶段 B（检索能力接管）与阶段 C（旧实现清理）进行中。
> 生效日期：2026-09-30（原 2026-09-14）
> 当前任务与验收：[当前实施计划](../planning/CURRENT_IMPLEMENTATION_PLAN.md) §0.10

## 1. 结构原则

本仓库采用“仓库按部署单元划分、应用内部按领域纵向聚合、领域内部按必要层级横向分层”的结构：

1. `apps` 放置可独立构建和部署的应用，`packages` 只放跨应用共享且需要兼容承诺的协议与生成代码。
2. Go 可执行入口集中在 `cmd`，应用私有实现集中在 `internal`；当前没有对外发布的 Go 库，因此不建立 `pkg`。
3. 业务代码优先归入 `internal/modules/<domain>`，避免全局 `handler`、`service`、`repository` 目录按技术类型分散同一业务。
4. 一个领域规模足够大时，才在领域内部拆分 `domain`、`application`、`interfaces`、`infrastructure`；不预建空目录。
5. `internal/app` 是唯一组合根，负责配置、实例化和生命周期；`internal/platform/httpserver` 负责 Gin 引擎、探针、中间件及路由装配，二者都不承载业务规则。
6. 领域层不依赖 Gin、GORM 或其他领域；应用层只依赖领域模型和端口；基础设施层实现领域端口，由组合根注入。
7. 共享目录只承载真实的跨域能力。仍位于 `internal/common` 和 `internal/model` 的代码视为迁移中的兼容层，不再接收新的领域逻辑。
8. **跨服务只共享版本化协议与生成代码**（`packages/proto`、`packages/gen`）与两类共享技术包（`packages/serviceauth`、`packages/gen/servicearch`）；不共享 GORM 模型、Repository 实现或其他应用的 `internal` 包。

## 2. 仓库结构

```text
go-web/
├── apps/
│   ├── gin-backend/       # HTTP API、认证和 Web 侧业务入口（文档操作走 document-service）
│   ├── mixin-search/      # 迁移期：mixin-search 自身的检索基线与 Chat 语料
│   ├── document-service/  # 正式文档、空间、成员、群绑定、资源权限、审计与 Outbox 的唯一写入方
│   ├── document-search/   # 正式文档索引与检索，只消费 document-service 事件
│   ├── qq-search/         # QQ 原始文件与聊天消息索引与检索，只消费 QQ 来源事件
│   └── simple-frontend/   # Vue Web 应用
├── packages/
│   ├── proto/             # 跨应用 RPC 契约源
│   │   ├── mixin-search/  # 迁移期文档契约 v1 与 chat/v1（mixin-search 自身实现的检索）
│   │   ├── document/      # 文档服务命令、详情读取与文档变更事件契约
│   │   ├── qqsource/      # py-agent 的 QQ 原始内容事件契约
│   │   ├── documentsearch/# 正式文档检索契约
│   │   └── qqsearch/      # QQ 原始内容检索契约
│   ├── gen/               # 由契约生成的共享 Go 代码 + 共享跨服务架构检查（servicearch）
│   └── serviceauth/       # 服务身份断言与资源范围 capability 的格式与边界拦截器
├── deployments/           # Compose、数据库空库初始化制品和验收脚本
├── docs/                  # 架构、契约、ADR 和阶段实施记录
│   ├── check-doc-links.ps1    # 文档链接与证据引用检查（ASCII-only）
│   └── reports/evidence/      # 阶段报告引用的不可变证据快照
├── docker-compose.yaml    # 本地系统组合入口
└── go.work                # Go workspace，组织本仓库全部 Go module
```

应用之间只能通过稳定协议或部署接口协作，不直接导入另一个应用的 `internal` 实现。

**迁移期说明**：mixin-search 的 Chat 语料检索实现与运行链路保持现状；qq-search 是负责 QQ 原始消息与文件的独立来源服务。二者的事实来源、契约与持久化边界各自独立。

gin-backend 的 `interfaces/sourceowned` 适配器经 document-service 和 document-search 提供文档 HTTP 能力。**旧 document application、旧 PostgreSQL 存储、旧索引 Worker/Admin/Eval 与旧 spacectl 已在阶段 C 删除**；`public.*` 旧文档表与旧影子观测表随之退场（见 [当前实施计划](../planning/CURRENT_IMPLEMENTATION_PLAN.md) §0.10 阶段 C）。

## 2.1 三个来源专属服务

`apps/document-service`、`apps/document-search`、`apps/qq-search` 是 ADR-017 引入的独立部署单元。三者结构一致：

```text
apps/<service>/
├── cmd/
│   ├── <service>/              # 服务入口：监听、生命周期、探针
│   └── <service>-healthcheck/  # 容器探针（不含 shell，不持有凭据）
├── configs/                    # 配置模板
├── schema/schema_init.sql      # 本服务 schema 的全新库基线（只含自己的表）
└── internal/
    ├── app/                    # 组合根：配置、连接、拦截器装配与生命周期
    ├── architecture/           # 跨服务边界检查（导入共享的 servicearch 规则）
    ├── application/            # 用例与业务规则；不见传输层、不见组合根
    ├── config/                 # 配置加载与失败关闭式校验
    ├── infrastructure/         # 存储与外部适配器（只实现本服务端口）
    └── interfaces/             # gRPC 边界：认证、scope 策略、协议↔用例映射
```

`document-service` 另有三个 `cmd` 入口：`document-service-spacectl` 是管理入口（经服务接口创建团队空间、增删成员、绑定群空间，并记录 actor 与 reason，不直连数据库），`document-service-e2e` 是跨服务验收客户端（同时扮演 `go-web` 与 `py-agent`，驱动三条链路并断言越界与跨 audience 拒绝）。

边界要求（由 `packages/gen/servicearch` 的共享规则强制，三个服务各自在 `internal/architecture` 中运行）：

| 规则 | 内容 |
| --- | --- |
| 禁止跨服务 module 导入 | 任何服务不得 import 另一个服务的 Go module（含其 `internal`） |
| 禁止跨服务业务表写入 | 服务自己的 Go 与 SQL 制品不得对他服务 schema 执行写语句，也不得引用他服务的写入账号 |
| 业务代码不得依赖组合根 | `internal/**` 不得 import 本服务的 `cmd/**` |

此外，文档检索与 QQ 检索**不得在查询路径上导入事实源的任何 Go 包**；它们与事实源之间只有事件契约与 capability 校验。

## 3. gin-backend 领域结构

```text
apps/gin-backend/
├── cmd/
│   ├── server/                         # Gin HTTP 服务入口
│   └── tools/
│       ├── pemgenerator/               # 本地 JWT 密钥生成
│       └── runtimeapitest/             # HTTP/CORS/路由验收客户端
├── configs/                            # 本地与容器配置
└── internal/
    ├── app/                            # 组合根：配置、服务客户端与生命周期
    ├── architecture/                   # 依赖方向测试
    ├── modules/
    │   ├── document/
    │   │   ├── domain/                 # Web 侧展示模型（不含持久化）
    │   │   ├── interfaces/sourceowned/ # 当前文档 HTTP 适配器
    │   │   └── infrastructure/
    │   │       ├── documentservice/    # document-service gRPC 客户端
    │   │       └── documentsearch/     # document-search gRPC 客户端
    │   ├── auth/
    │   └── manager/
    ├── platform/httpserver/            # Gin 引擎、探针、中间件与路由装配
    └── config/                         # 配置加载与校验
```

`document/sourceowned` 路由通过 `document-service` 与 `document-search` 客户端提供服务：写入走 `SaveDocument` 单事务用例，列表走游标分页，检索走 capability 限定的正式文档索引。`source_owned_services.enabled` 决定新文档路由是否装配；**未装配时不会退回旧直连表路径**——那条路径的代码已经不存在。

## 4. mixin-search 结构与控制面依赖

```text
apps/mixin-search/
├── cmd/
│   ├── rag-server/              # 检索运行时组合根
│   └── rag-healthcheck/         # gRPC Health 容器探针
└── internal/
    ├── security/                # capability、身份、审计与限流
    ├── controlplane/            # 控制快照与派生投影的通用机制
    ├── rag/                     # 索引、控制投影、向量存储与检索工作流
    ├── chat/                    # Chat 语料服务
    ├── chatindex/               # Chat 语料索引
    ├── document_pipeline/       # 文档解析与分块
    └── transport/grpc/          # RPC 与调用方认证适配
```

mixin-search 的文档与 Chat 语料通过各自独立的控制状态、向量集合、适配器、角色和 audience 保持隔离；Chat 语料契约为 chat/v1。qq-search 另行负责 QQ 原始消息和文件的来源检索。

阶段 B 把**本地哈希向量流程**接管到 document-search，并由其承担评测职责；mixin-search 中已无引用入口的手工调试命令（`rag-grpc-client`、`rag-token`、`demo`）已随阶段 C 删除。mixin-search 仍是迁移期对照基线：其向量后端、控制面与 Chat 语料未被删除，但**已退出默认启动路径**——根 Compose 把它与 `control-postgres` 放进 profile `legacy-retrieval`，`docker compose up -d` 不再启动它，需要对照验证时显式启用。go-web 中已不存在任何 mixin-search 客户端，这条约束由 `internal/architecture` 的禁止导入断言固定。它的 `-store pgvector` 后端只服务于该 module 自己的隔离验收，不是受支持能力，主业务库中不存在 vector 列。

## 5. 依赖方向

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

上表由各应用自己的 `internal/architecture/dependencies_test.go` **部分**强制：gin-backend 覆盖业务模块禁止依赖组合根、`common/base` 与 `common/service` 禁止依赖业务模块、`common/service/jwt` 禁止依赖 Gin，以及「gin-backend 不得导入来源专属服务 module」；mixin-search 强制 `document_pipeline → internal/rag → internal/transport` 的单向边界；三个来源专属服务运行 `packages/gen/servicearch` 的共享规则。其余条目靠 review 与 [DEVELOPMENT_CONVENTIONS.md](./DEVELOPMENT_CONVENTIONS.md) 维持。**不含遗留文件清单快照**——该机制曾被移除，理由见约定第 5 节。

### 5.1 跨服务调用方向

```text
go-web  ──业务命令/详情读取──> document-service ──变更事件(Outbox)──> document-search
py-agent ──身份+会话上下文────> document-service
py-agent ──晋升正式文档──────> document-service
py-agent ──QQ 原始事件───────> qq-search
查询调用方 ──已获授权的查询──> 对应检索服务（document-search / qq-search）
```

- 调用方向单向，没有同步调用环；检索服务在查询时不回调事实源、`go-web` 或 `py-agent`。
- gin-backend 的旧索引 Worker、Admin 与 Eval 已删除；它们的职责由 document-search 与 qq-search 各自的消费者、`RebuildIndex` 与 `GetIndexStatus` 承担。
- 授权凭证由有权判定该资源的服务在请求检索前签发：正式文档范围由 `document-service` 签发，QQ 渠道范围由 `py-agent` 签发；**capability 的签发方必须是该 audience 的事实源，签发端与校验端都强制**（见 [SERVICE_IDENTITY_AND_CAPABILITY.md](../contracts/SERVICE_IDENTITY_AND_CAPABILITY.md) §4）。

## 6. 阶段性边界与待清理项

- `common` 与 `model` 仍是 gin-backend 的迁移中兼容层；新业务逻辑放入所属领域，不继续扩张这些目录。
- 不可达的 Web Chat 页面、专用 API、composable 与会话存储已从工作树清理；后端 Chat/WebSocket 路由未注册。mixin-search 的 Chat 语料服务与检索实现仍保留。
- 阶段 C 已删除：旧 document application 与 PostgreSQL 实现、旧索引 Worker/Admin/Eval、旧 `internal/modules/space` 与 `cmd/tools/spacectl`、三件旧配置类型与三个旧验签脚本、mixin-search 的三个手工调试命令。逐项记录见 [阶段 C 证据](../reports/evidence/phase4/stage-c-cleanup-go-web.md)。
- 阶段 B 进行中：本地哈希向量流程接管到 document-search；接管完成前 mixin-search 的检索实现保持原样。
- ADR-017 的七个 Go module、来源服务适配器、独立 schema/账号和边界检查已进入工作区；当前实现及验收状态以 [当前实施计划](../planning/CURRENT_IMPLEMENTATION_PLAN.md) 为准。
- P1/P2/P3 的历史验收结论与 ADR-008/009 等记录只用于追溯，**不作为当前实现状态的依据**。

## 7. 阶段结果

阶段 A：`SaveDocument` 单事务保存、列表真实公开状态与真实总数、游标分页、检索收窄与主体过滤、QQ 记录状态渠道校验全部落地，并通过真实 Web 验收（43 条断言）与七个 module 的门禁。

阶段 C：旧文档写入/索引/运维链路与旧空间模块从代码、Compose 与初始化制品中退场；`deployments/verify.ps1` 重写为当前架构的门禁编排（旧 P1.5/P2.3/P2.4/P2.5 影子索引门禁随其目标退场，mixin-search 自身的语料验收留在该 module 的 `verify-*.ps1`）。

## 8. 新增代码的落位规则

1. 先判断代码属于哪个业务能力，再决定技术层；不能确定领域归属的代码不得直接放进 `common`。
2. 只被一个领域使用的请求类型、错误、仓储和工具函数均留在该领域。
3. 只有跨应用稳定契约进入 `packages`；只在单应用复用的代码留在该应用 `internal`。
4. 目录名称使用完整、稳定的领域词；Go 包名不使用下划线。
5. 每次结构调整必须同时更新导入路径、测试、开发初始化入口、验收门禁和本文件中的阶段状态。

## 9. 验收命令

仓库根执行完整门禁（不需要容器的一侧可加 `-SkipServices`）：

```powershell
./deployments/verify.ps1
```

它依次执行：协议生成物一致性 → 七个 module 的 build/vet/gofmt/test → 数据库写权限矩阵 → 三个来源服务的真实 gRPC 链路 → 真实 HTTP 认证下的 Web 文档链路 → 前端构建 → 文档链接检查。

单独执行某一项：

```powershell
./packages/proto/verify-generated.ps1
./deployments/postgresql/verify-service-isolation.ps1
./deployments/verify-source-owned-services.ps1      # ADR017_E2E
./deployments/verify-stage-a-web.ps1                # STAGE_A_WEB
./docs/check-doc-links.ps1
```

涉及 mixin-search 控制状态或其语料隔离时，在 `apps/mixin-search` 执行该 module 自己的脚本：

```powershell
go test ./...
go vet ./...
./verify-control-store.ps1
./verify-qdrant-control.ps1
```

需要 PostgreSQL 或 Qdrant 的测试必须报告实际运行结果，不能把跳过记为通过；写权限与索引隔离由 `deployments/postgresql/verify-service-isolation.ps1` 实测。
