# go-web 项目结构与依赖约束

> 状态：阶段 1 结构基线，P2.1-P2.5 控制面、Qdrant 过滤、可靠投递、影子索引与影子查询评估已落地
> 生效日期：2026-09-14

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
│   │   └── mixin-search/  # 文档契约 v1 与聊天契约 chat/v1（独立语料契约）
│   └── gen/               # 由契约生成的共享 Go 代码
├── deployments/           # Compose、数据库空库初始化制品和验收脚本
├── docs/                  # 架构、契约、ADR 和阶段实施记录
│   ├── check-doc-links.ps1    # 文档链接与证据引用检查（ASCII-only）
│   └── reports/evidence/      # 阶段报告引用的不可变证据快照
├── docker-compose.yaml    # 本地系统组合入口
└── go.work                # Go workspace，仅组织本仓库 Go module
```

应用之间只能通过稳定协议或部署接口协作，不直接导入另一个应用的 `internal` 实现。

## 3. gin-backend 领域结构

```text
apps/gin-backend/
├── cmd/
│   ├── document-index-admin/   # 对账、重建和失败项管理入口
│   ├── document-index-worker/  # 索引 Outbox 独立消费入口
│   ├── document-search-eval/   # P2.5 固定样本检索质量评估入口
│   ├── server/                 # 生产服务入口
│   └── tools/                  # 不参与产品部署的开发与验收工具
│       ├── pemgenerator/       # 密钥生成工具
│       └── runtimeapitest/     # 运行时 API 验证工具
├── configs/                    # 配置模板
└── internal/
    ├── app/                    # 组合根：启动、配置、依赖和生命周期
    ├── architecture/           # 可执行的包依赖约束测试
    ├── modules/
    │   ├── document/
    │   │   ├── domain/         # 聚合、值对象、命令/查询端口
    │   │   ├── application/    # 文档用例、索引投递与异步影子查询
    │   │   ├── evaluation/     # 检索数据集校验、指标和报告生成
    │   │   ├── interfaces/
    │   │   │   └── http/       # Documents HTTP 契约的 Gin 适配器
    │   │   └── infrastructure/
    │   │       ├── cache/      # 修订号参与键的版本化读缓存
    │   │       ├── mixinsearch/# mixin-search/v1 gRPC 端口适配器
    │   │       └── postgresql/ # 文档、投递和影子观测的 PostgreSQL 实现
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

## 4. mixin-search 结构与控制面依赖

```text
apps/mixin-search/
├── cmd/
│   ├── rag-server/                         # 向量存储、控制存储、调用边界和 gRPC 的组合根
│   ├── rag-healthcheck/                    # 标准 gRPC Health 容器探针
│   ├── rag-grpc-client/                    # 最小远程调用客户端
│   ├── rag-token/                          # 本地调试用 capability 签发工具（不进产品镜像）
│   └── demo/                               # 文档管道与检索演示
├── internal/
│   ├── security/                           # 调用方 capability、身份、审计与限流
│   │   ├── capability.go                   # 签名、校验、角色与“只允许缩小”的范围判定
│   │   ├── identity.go                     # 已验证身份的上下文传递与结构化审计记录
│   │   └── limiter.go                      # 按调用方的令牌桶限流
│   ├── controlplane/                       # 两个语料共用的机制（不含任何语料状态）
│   │   └── state.go                        # 不可变快照发布（generation 单调）与投影收敛/reconciler
│   ├── rag/                                # 文档语料：控制面 + 通用向量核心
│   │   ├── contract.go                     # 文档索引用例、幂等、fencing 与提交编排
│   │   ├── snapshot.go                     # 不可变控制快照、发布模型转换与控制投影构造
│   │   ├── projection.go                   # 投影 generation 跟踪、后台 reconciler 与准入确认
│   │   ├── control_store.go                # ControlStore 端口、持久化模型、读写快照与故障收敛
│   │   ├── control_store_memory.go         # 单元测试/显式本地演示适配器
│   │   ├── control_store_postgres.go       # 默认 PostgreSQL 控制存储适配器
│   │   ├── control_schema.sql              # mixin_search_control schema 基线
│   │   ├── control_store_test.go           # 重启、并发、失败关闭和意图清理测试
│   │   ├── control_store_integration_test.go # 真实 PostgreSQL 恢复与 CAS 测试
│   │   ├── store.go                        # VectorStore 端口与 memory 实现
│   │   ├── store_qdrant.go                 # Qdrant 控制投影与候选级过滤实现
│   │   └── store_pgvector.go               # 实验性 pgvector 实现
│   ├── chat/                               # 聊天语料：独立控制面（不依赖文档语料）
│   │   ├── service.go                      # 会话/消息状态机、四类修订、投影与检索准入
│   │   ├── snapshot.go                     # 聊天不可变快照、持久化模型转换与控制投影构造
│   │   ├── types.go                        # 领域类型、三个端口、错误与规范化
│   │   ├── control_store.go                # 聊天 ControlStore 端口、持久化模型与校验
│   │   ├── control_store_memory.go         # 单元测试/显式本地演示适配器
│   │   ├── control_store_postgres.go       # 独立 chat_control_states 表适配器
│   │   ├── control_schema.sql              # 聊天控制面自己的表基线
│   │   └── isolation_test.go               # ADR-014 的跨语料隔离验收
│   └── transport/grpc/                     # Protobuf DTO 与业务 Service 的适配层
│       ├── server.go                       # 协议转换与错误码映射
│       └── auth.go                         # 调用方认证、角色策略与范围包含校验拦截器
├── compose.yaml                            # 控制 PostgreSQL、Qdrant、pgvector 本地依赖
├── verify-control-store.ps1                # 一次性 PostgreSQL P2.1 验收入口
└── verify-qdrant-control.ps1               # 一次性 Qdrant P2.2 验收入口
```

`internal/rag` 中的 `ControlState` 是独立持久化模型，不依赖 Protobuf DTO。进程内控制状态自 P3.2 起是**不可变快照**（`snapshot.go`）：读请求只做一次原子加载与一次 generation 探测，写请求在独立写入锁内对私有副本执行 compare-and-swap，成功后发布。控制投影由 `projection.go` 的后台 reconciler 按投影 generation 收敛，搜索只确认投影已追上所用快照。`DocumentIndexService` 同时依赖 `ControlStore` 和向量业务 `Service`；`cmd/rag-server` 作为组合根选择 PostgreSQL 或 memory 控制适配器并启动 reconciler。gRPC 层只完成协议转换和错误码映射，不读取数据库，也不复制幂等、修订或提交顺序规则。并发模型见 [ADR-013](../adr/013-immutable-control-snapshot-and-background-projection.md)。

**两个语料、两套状态、一套机制**（[ADR-014](../adr/014-per-corpus-control-plane-isolation.md)）：`internal/controlplane` 只放与语料无关的机制——不可变快照的单调发布，以及派生投影的收敛与后台 reconciler；它不持有任何语料状态，因此没有一个包的依赖指向某个语料。`internal/rag`（文档）与 `internal/chat`（聊天）各自拥有快照类型、generation、持久化与 reconciler：文档用 `mixin_search_control.control_states`，聊天用同 schema 下自己的 `chat_control_states` 表。两条边界由 `internal/architecture/dependencies_test.go` 强制：`internal/controlplane` 不得 import 任何 `mixin-search/` 包；`internal/chat` 不得 import `internal/rag`、`transport` 或 `cmd`。聊天通过自己的端口（消息索引、投影、检索）访问向量存储，由组合根注入，因此两个语料既不能共享控制状态，也不能互相依赖。

gRPC 层自 P3.1 起先认证调用方、再映射协议：`internal/transport/grpc/auth.go` 的一元拦截器校验 capability 的签名、audience、有效期与角色，并对 `SearchDocuments` 执行“请求范围 ⊆ 已授予范围”的包含校验，未通过时不进入业务路径。索引写入（`index-writer`）、检索（`searcher`）与只读运维（`ops`）是三个独立角色，`internal/rag` 保持不感知身份。凭据格式与校验规则见 [SERVICE_CALL_CAPABILITY.md](../contracts/SERVICE_CALL_CAPABILITY.md)，背景决策见 [ADR-012](../adr/012-multi-consumer-search-boundary-and-critical-path-shift.md) 决策 4。

控制面并发已在 P3.2 改造完成：读路径不再全量加载控制状态、不取全局排他锁、不执行投影同步。控制状态仍是单行快照，“整个控制面必须装进内存”的天花板与按文档行存储（行级 CAS）属于后续独立事项。

控制 PostgreSQL 与 VectorStore 之间没有共享事务。索引使用持久化 pending write 租约进行两阶段提交，删除先持久化逻辑删除与 pending delete 后执行物理清理；完整顺序见 [ADR-006](../adr/006-mixin-search-control-state-commit-order.md)。

Qdrant 通过 `ControlledVectorStore` 能力接口接收规范化控制投影；正式搜索在候选选择前统一下推 storage domain、活动/墓碑状态和三路 OR 授权，随后仍由 `DocumentIndexService` 复核并在不足时有界回填。memory 与 pgvector 保持基础 `VectorStore` 兼容，但不作为 P2.2 候选级过滤的验收后端。完整决策见 [ADR-007](../adr/007-qdrant-control-projection-and-filtering.md)。

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

上表由两个应用各自的 `internal/architecture/dependencies_test.go` **部分**强制：gin-backend 覆盖 document 四层正向依赖、业务模块禁止依赖组合根、`common/base` 与 `common/service` 禁止依赖业务模块、`common/service/jwt` 禁止依赖 Gin（共 8 条**方向性 import 断言**）；mixin-search 强制 `document_pipeline → internal/rag → internal/transport` 的单向边界。其余条目靠 review 与 [DEVELOPMENT_CONVENTIONS.md](./DEVELOPMENT_CONVENTIONS.md) 维持。**不含遗留文件清单快照**——该机制曾被移除，理由见约定第 5 节。

## 6. 阶段性边界与待清理项

- 旧 `modules/markdown`、`model/orm/markdown`、`model/store/markdown` 与 `model/cache/markdown` 已从运行时删除；旧数据库表、索引与版本迁移资产已在 P1.4 删除；空库只建立当前 document 基线。
- `common` 与 `model` 不是长期业务归属地。共享设施应在确认被多个领域稳定复用后再提取；领域专属实现应迁回对应模块。
- Chat/WebSocket 保持“代码存在、服务不注册”的状态；目录归组不代表重新启用。
- `mixin-search/v1` 是跨进程边界，不应被伪装成 gin-backend 内部模块。P2.3 已通过事务 Outbox 与独立 Worker 调用该边界；HTTP 文档事务不直接发起 RPC。
- P2.1 的 PostgreSQL 控制状态和向量索引均为可重建派生数据；P2.3 已提供失败重放、差异对账和 repeatable-read 全量重建编排。
- P2.2 已完成 Qdrant 授权、活动版本、墓碑与 storage domain 过滤下推；P2.3 已完成 gin-backend 可靠投递；P2.4 已完成根 Compose、分层健康状态与持续影子索引；P2.5 已完成非阻塞影子查询、事实复核、来源分层观测和质量报告。当前结论为 KEEP_BM25，正式读取方地位仍未改变。
- P2.5 完成后的缓存加固统一了进程级 Redis/内存/singleflight 运行时。内存回退按实体与文档分区受 TTL、LRU、条目和字节预算约束；User/Manager 使用 PostgreSQL 单调 `cache_revision` 版本键隔离延迟旧回填，JWT 会话状态继续保持 Redis 故障时失败关闭。完整边界见 [ADR-011](../adr/011-bounded-cache-runtime-and-revision-fencing.md)。
- 阶段 3 的实施基线已于 2026-09-17 建立（P3.0），调用方 capability 边界与不可变控制快照已落地。当前未完成项集中在 `mixin-search`：多语料控制面隔离（[ADR-014](../adr/014-per-corpus-control-plane-isolation.md)）与聊天语料契约和索引隔离（P3.3）。`go-web` 侧在本阶段只新增 `py-agent` 接入所需的身份映射与治理边界，不建设完整聊天产品域。范围与门禁见 [CURRENT_IMPLEMENTATION_PLAN.md](../planning/CURRENT_IMPLEMENTATION_PLAN.md)。

## 7. P1.3 的结构结果

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

## 8. 新增代码的落位规则

1. 先判断代码属于哪个业务能力，再决定技术层；不能确定领域归属的代码不得直接放进 `common`。
2. 只被一个领域使用的请求类型、错误、仓储和工具函数均留在该领域。
3. 只有跨应用稳定契约进入 `packages`；只在单应用复用的代码留在该应用 `internal`。
4. 目录名称使用完整、稳定的领域词；Go 包名不使用下划线，因此统一使用 `aiagent`。
5. 每次结构调整必须同时更新导入路径、测试、开发初始化入口、验收门禁和本文件中的阶段状态。

## 9. 验收命令

在 `apps/gin-backend` 执行：

```bash
go test ./internal/architecture
go test ./...
go vet ./...
```

涉及文档持久化或应用服务的改造，还必须从仓库根目录执行当前计划定义的空库初始化与集成门禁；涉及部署边界时执行 `deployments/verify.ps1`。P1.0-P1.3 的生产式迁移专项脚本已在 P1.4 清理，历史结论保留在实施日志。

涉及 mixin-search 控制状态时，在 `apps/mixin-search` 执行：

```powershell
go test ./...
go vet ./...
./verify-control-store.ps1
```

最后一项使用一次性 PostgreSQL 空数据卷验证真实持久化、服务实例恢复和 generation CAS，并在结束时默认删除该环境。
