> 状态：**迁移前时点快照（2026-09-14 前后）**。ADR-017 阶段 C 之后，本文引用的旧 `public.*` 文档表、`document-index-worker` / `document-index-admin` / `document-search-eval`、`gin-backend` 的 `spacectl` 与 `connection.ServiceDocument` 均已退场；本文保留为当时的评估记录，不代表当前实现。`docs/` 治理树内的当前状态见 [docs/architecture/PROJECT_STRUCTURE.md](./docs/architecture/PROJECT_STRUCTURE.md) 与 [docs/planning/CURRENT_IMPLEMENTATION_PLAN.md](./docs/planning/CURRENT_IMPLEMENTATION_PLAN.md)。

# gin-backend / mixin-search 项目结构组织评估（终版）

> **评估对象**：`apps/gin-backend`、`apps/mixin-search`
> **基线提交**：`a8ab94a`「功能：冻结阶段1文档领域与搜索契约基线」（阶段 2 起点；工作树含 141 个未提交变更，本评估包含这些变更）
> **评估方法**：`go list` 编译级依赖图、`go list -deps` 全入口可达性并集、全量行数统计、`go vet` 静态门禁、源码精读
> **文档性质**：本文件是**外部结构评估**，不属于 `docs/` 治理树。`docs/` 的维护规则只接纳入口文档、当前计划、稳定结构与契约、阶段证据；本文件不参与该规则，项目可自行归档或删除。

---

## 1. 评估方法与证据强度

| 维度 | 手段 | 强度 |
|---|---|---|
| 跨包依赖方向 | `go list -f '{{.ImportPath}}\|{{range .Imports}}{{.}} {{end}}' ./...` | 编译器级，不可争议 |
| 生产可达性 | `go list -deps <entry>`，对**全部 6 个入口求并集** | 编译器级 |
| 代码归属 | 全量文件树 + 行数统计 | 实测 |
| 静态门禁 | `go vet ./...` × 3 module，全部 `exit=0` | 实测 |
| 包内职责划分 | 源码精读 | 人工判断 |

复现命令见[附录 A](#附录-a-复现命令)。其中「依赖方向」与「可达性」两类结论不依赖人工解读，是本报告的主干证据。

---

## 2. 实测基线

| 指标 | gin-backend | mixin-search |
|---|---|---|
| `.go` 文件 | 178 | 36 |
| 其中 `_test.go` | 32 | 15 |
| 全部 Go 代码行 | 17,240 | 7,871 |
| 生产代码行 | 14,048 | 4,825 |
| `_test.go` 代码行占全部 Go 代码行比例 | **18.5%** | **38.7%** |
| `internal/` 包数 | 57 | 2 |
| 可执行入口 | 6 | 4 |
| 生产入口可达的 `internal` 包 | 45 / 57 | 2 / 2 |
| 架构约束测试 | 1 个文件 / 5 个测试 | **0** |
| `go vet` | exit 0 | exit 0 |
| 最大单文件 | `cmd/runtimeapitest/main.go` 1045 | `internal/rag/contract.go` 1091 |

> 注：`_test.go` 行占比**不是覆盖率**，也不代表测试质量，仅用于粗略比较测试投入分布。
>
> **上表为评估时点（H2）的实测值，其中「架构约束测试」一行已过时**——后续 C9/C10 落地为
> gin-backend 1 个文件 / 8 条规则、mixin-search 1 个文件 / 3 条规则；随后 C9 的文件清单冻结被移除，
> 但**方向性断言已恢复**，gin-backend 现为 8 条方向性 import 断言。当前状态见 §3.5 的后续变化说明与 §7 优先级表。

---

## 3. gin-backend

### 3.1 已验证成立：document 模块的分层是编译器事实

`go list` 解析出的内部依赖图（节选，完整图见[附录 B](#附录-b-完整内部依赖图)）：

```
internal/modules/document/domain          -> (零个内部依赖)
internal/modules/document/application     -> domain
internal/modules/document/infrastructure/cache        -> domain
internal/modules/document/infrastructure/mixinsearch  -> domain
internal/modules/document/infrastructure/postgresql   -> domain
internal/modules/document/interfaces/http -> application, domain, common/*
internal/modules/document/evaluation      -> (零个内部依赖)
```

四条硬结论：

1. `domain` 包**零内部 import** —— 领域模型是纯 Go。
2. `application` **只依赖 `domain`** —— 不含 Gin/GORM/传输层。
3. 三个基础设施适配器**各自只依赖 `domain`** —— 端口/适配器分离真实成立，不是注释承诺。
4. `evaluation` 也是零内部依赖的叶节点，是被 `cmd/document-search-eval` 消费的独立能力包。

这是教科书式的六边形结构，且在 57 个包中**唯一完全落地**。这也是本仓库结构质量的上限样本。

### 3.2 模块成熟度分层

| 模块 | 生产文件 | 生产行数 | 分层 | 包内单元测试 | 被 HTTP 集成套件覆盖 |
|---|---|---|---|---|---|
| `modules/document` | 20 | 4,076 | 五目录（四层 + `evaluation`） | 14 个测试文件 | ✅ |
| `modules/manager` | 14 | 831 | 遗留 `api/handler/logic/types` | **0** | ✅ |
| `modules/auth` | 11 | 562 | 遗留 `api/handler/logic/types` | **0** | ✅ |
| `modules/aiagent` | 2 | 391 | 无分层，平铺 3 文件 | 1 个测试文件 | ❌ 生产不可达 |
| `modules/chat` | 30 | 2,292 | 遗留 + `structure/bridge` | 6 个测试文件 | ❌ 生产不可达 |

### 3.3 缺陷清单

#### D1｜迁移只完成了一个模块（严重）

`document` 是唯一完成改造的模块。`auth`(562 行) 与 `manager`(831 行) 仍是 `api → handler → logic → types` 的技术分层，依赖图暴露真实耦合：

```
auth/logic     -> model/orm/auth, model/store, common/base/connection/postgresql, config
manager/logic  -> model/orm/manager, model/store, config, common/base/connection
manager/utils  -> common/base/connection/postgresql
```

`logic` 层**直接依赖 GORM ORM 模型与连接管理器** —— 这正是 `document` 已经消除的东西。`internal/model`（11 文件 / 505 行）因此**不是死代码，而是仍被 3 个模块使用的活跃遗留层**。

后果：`logic` 与数据库强耦合，**无法编写脱离 PostgreSQL 的单元测试**，这直接解释了 3.4 节的现象。

#### D2｜路由装配存在两套机制（严重）

```
internal/app                  -> platform/httpserver, modules/document/interfaces/http
internal/platform/httpserver  -> modules/auth/api, modules/manager/api   ← 硬编码
```

`document` 经 `Dependencies.DocumentRoutes RouteRegistrar` 接口由组合根注入；`auth`/`manager` 由 `platform` 层直接 import 具体 `api` 包并调用 `RegisterRoutes`。同一个关注点两种做法，且 `app → platform → auth/api` 这条边使 `platform` 事实上成为第二个组合点。项目结构文档声称 `platform/httpserver` 不承载装配职责，实测不符。

#### D3｜生产入口不可达的孤儿实现 3,211 行（中）

对全部 6 个入口求 `-deps` 并集后，**12 个 `internal` 包不可达**：

| 孤儿子树 | 包数 | 文件 | 行数 |
|---|---|---|---|
| `internal/modules/chat/**` | 11 | 36 | 2,688 |
| `internal/modules/aiagent` | 1 | 3 | 523 |
| 合计 | 12 | 39 | **3,211** |

**占 gin-backend 全部 Go 代码的 18.6%。**

导入方核验：
- `modules/aiagent`：**零个**非测试文件 import → 真孤儿。
- `modules/chat`：33 处 import **全部位于 chat 子树内部** → 子树整体孤儿。

**重要限定**：仓库内**不存在任何 CI 配置**（根目录隐藏目录仅 `.agents`/`.git`/`.vscode`；`.github/workflows` 只出现在 `apps/simple-frontend/node_modules/rfdc/` 这个第三方包内）。唯一的仓库级门禁是手工执行的 `deployments/verify.ps1`，其第 143 行对三个 module 逐个执行 `go test ./...`；`go vet ./...` 亦全部通过。

因此这些代码**仍被编译、仍被类型检查、仍被测试**，不属于脱离编译门禁的死代码。准确定性是：**生产入口不可达的孤儿实现**，成本是维护面与认知负担，而非门禁盲区。

#### D4｜域模型贫血，业务规则集中在 application（中）

`domain` 包基本只有数据结构（`Document`、`DocumentVersion`、`AccessPolicy`、`SearchProjection` 等），无行为方法。而修订号递增、发布状态迁移（`draft→published→superseded`）、摘要截断 100 字、dedupe key 拼装、`trashed` 前置校验全部位于 `application/command_service.go`（437 行）。结果：`Create`/`Update`/`Trash` 各是 40–70 行事务脚本，`domain` 目录存在但无领域逻辑。

#### D5｜仓储端口过宽（中）

`domain/repository.go`（37 行）单接口 **25 个方法**，横跨 `KnowledgeSpace`、`SpaceMember`、`Document`、`DocumentVersion`、`AccessPolicy`、`DocumentGrant`、`SearchProjection`、`IndexDeliveryEvent` 八类概念外加事务控制（含 `InTransaction`、`InRepeatableRead`）。任何实现或测试替身必须一次实现全部 25 个。`InRepeatableRead` 与 `ListDocumentsForIndexing` 是为运维用例（全量重建）加入的，属运维概念侵入领域端口。

#### D6｜跨服务投递语义住在 document 应用层（中）

`application/command_service.go` 内的 `newSyncIndexDeliveryEvent` / `newDeleteDocumentIndexDeliveryEvent` 构造 mixin-search 的投递事件（含 DedupeKey 格式、`IndexProfile`、三类 revision）。项目文档反复强调「`mixin-search/v1` 是跨进程边界，不应被伪装成 gin-backend 内部模块」，但投递事件模型确实住在 document 领域内，应独立为 `indexdelivery` 子域。

#### D7｜组合根使用 panic + 重复手工回滚链 + 包级全局状态（轻）

`app/dependencies.go`（180 行）四个失败分支重复 3–6 行 `Unregister`，启动失败以 `panic` 表达，cleanup 函数 20+ 行。同时 `postgresqlconn.PostgreSQLManager`、`redisconn.RedisManager`、`sessionevent.SetDefaultBus(nil)`、`basecache.DefaultRuntime()` 为包级可变全局状态，与「组合根显式注入」原则部分冲突。

#### D8｜`common` 是无护栏的共享内核（轻）

29 个生产文件 / 2,268 行，被**每一层**依赖，自身又依赖 `config`。其中 `common/base/logger → config/must`、`common/base/connection/postgresql → config/must` 意味着**基础设施反向依赖配置解析**。项目文档称其为「迁移中的兼容层，不再接收新领域逻辑」，但无机制阻止。

#### D9｜HTTP 适配层直接解析 JWT（轻）

`interfaces/http/handler.go`（279 行）多处调用 `jwt.SubjectUint(c)` 提取 ownerID。身份提取属横切关注点，应在 middleware 注入 context 值，而非让传输层耦合 JWT 实现并在每个 handler 重复。

### 3.4 关于测试覆盖的正确表述

**必须澄清一个易误判的点**：`modules/auth` 与 `modules/manager` 目录内**没有 `_test.go`**，但这**不等于零测试覆盖**。

`cmd/runtimeapitest/main.go`（1045 行）是跨进程 HTTP 集成套件，由 `deployments/verify.ps1:156` 在仓库门禁中执行，覆盖：

| 端点 | 断言 |
|---|---|
| `POST /api/v1/public/auth/register` | 参数校验 400、成功 201、重复邮箱 409 |
| `POST /api/v1/public/auth/login` | 错误密码 401、Cookie-only 登录 200、新登录建立新 sid |
| `POST /api/v1/public/auth/refresh` | 轮换 200、新登录使旧 refresh 失效 401 |
| `POST /api/v1/protected/auth/logout` | ✅ |
| `/api/v1/public/manager/{login,refresh,register}`、`/protected/manager/logout` | ✅ |
| `/api/v1/protected/manager/requests` | 列表、`status=` 过滤、分页边界 0/101 |
| `POST /api/v1/protected/manager/requests/999999999/approve` | ✅ |

并且 `verify.ps1` 在**正常运行**与 **mixin-search 停机**两种拓扑下各执行一轮，两轮均为 `95 passed / 0 failed / 95 total`。

**结论**：auth/manager 有强端到端覆盖，缺口在**单元级隔离测试**——而该缺口是 D1（`logic` 与 GORM/store 硬耦合）的直接后果，无法在不先解耦的情况下补齐。

### 3.5 对「架构约束测试」的准确描述

`internal/architecture/dependencies_test.go`（134 行）共 5 个测试，覆盖面并不均等：

| 测试 | 覆盖范围 | 类型 |
|---|---|---|
| `TestDocumentModuleDependencyDirection` | **仅 document** | 正向分层方向 |
| `TestDocumentModuleDoesNotDependOnLegacyMarkdownStorage` | 仅 document | 禁止回退 |
| `TestBackendHasNoObsoleteServiceImports` | 全仓库 | 禁止回退 |
| `TestBusinessModulesDoNotDependOnCompositionRoot` | 全仓库（所有 modules） | 禁止回退 |
| `TestHTTPPlatformOwnsGinAssembly` | 全仓库（废弃路径保持删除） | 禁止回退 |

**准确表述**：已有 3 条全仓库级规则，但**全部是「禁止回退」型**；**正向分层方向只对 `document` 强制**，`auth`/`manager`/`chat`/`aiagent` 没有任何方向约束。这才是 D1 能长期存在的机制原因。

> **后续变化（重要）**：本节描述的是评估时点的状态。此后该测试被升级为规则表（8 条依赖规则 + 5 组共 70 个文件的精确基线 + 2 条 import 冻结），随后**文件清单冻结与 import 冻结基线被移除**（项目不采用「精确文件清单冻结」，理由见 [`DEVELOPMENT_CONVENTIONS.md`](./docs/architecture/DEVELOPMENT_CONVENTIONS.md) §5）；**方向性 import 断言已恢复**，`apps/gin-backend/internal/architecture` 现有 8 条规则。mixin-search 侧的 `internal/architecture` 同期落地（3 条单向边界规则）。

---

## 4. mixin-search

### 4.1 已验证成立：依赖方向优秀且极简

```
document_pipeline            -> (零内部依赖，仅 stdlib + doc2txt)
internal/rag                 -> document_pipeline
internal/transport/grpc      -> internal/rag, packages/gen/mixin-search/v1
cmd/rag-server               -> internal/rag, internal/transport/grpc, packages/gen/...
cmd/rag-grpc-client          -> packages/gen/...
cmd/rag-healthcheck          -> packages/gen/...
cmd/demo                     -> internal/rag
```

三条硬结论：

1. **`internal/rag` 不 import `packages/gen`** —— Protobuf DTO 确实没有渗入领域层，这是编译器事实而非注释承诺。
2. **`internal/rag` 不 import `internal/transport/grpc`** —— 依赖方向单向正确，无环。
3. 整体是严格的线性链 `document_pipeline → rag → transport/grpc → cmd`，比 gin-backend 的遗留模块干净得多。

此外，以下设计值得肯定：

- `internal/transport/grpc/server.go`（277 行）是纯映射层：7 个 RPC 的字段翻译 + 3 个响应映射 + 集中的 `mapServiceError`，无业务语义。且定义 `KnowledgeService` 接口使传输层不依赖具体实现（注释明确为未来 MCP 适配器留口）。
- 端口使用**可选能力接口**而非布尔开关：`VectorStore`（最小 4 方法）+ `ControlledVectorStore`（Qdrant 实现，memory/pgvector 不实现）。
- `ControlStore.Save(ctx, expectedGeneration, state) (uint64, error)` 把 CAS 并发契约写进签名。
- `validateControlState` 校验引用完整性、墓碑一致性、pending write/delete 互斥、operation 单值联合，失败即拒绝启动 —— fail-closed 有代码支撑。

### 4.2 缺陷清单

#### M1｜核心包内部没有物理边界（严重）

`internal/rag/` 的 18 个文件同处一个包，**领域模型 / 应用服务 / 端口 / 适配器共居一室**：

| 关注点 | 文件 | 行数 |
|---|---|---|
| 应用服务 + 领域模型 + 授权判定 | `contract.go` | **1091** |
| 控制面端口 + 持久化模型 + 校验 | `control_store.go` | **572** |
| Eino 工作流 | `workflow.go` | 465 |
| Qdrant 适配器 | `store_qdrant.go` | 438 |
| pgvector 适配器 | `store_pgvector.go` | 208 |
| 向量端口 + memory 适配器 | `store.go` | 186 |
| PostgreSQL 控制适配器 | `control_store_postgres.go` | 167 |
| 共享类型 | `types.go` | 96 |
| embedding | `embedding.go` | 91 |

Go 的包是最小封装边界。同包意味着**没有任何机制阻止 `DocumentIndexService` 直接实例化某个具体适配器**。gin-backend 尚有 AST 测试兜底，这里连包边界都没有 —— 目前正确的依赖方向完全靠自觉。

#### M2｜`contract.go` 是 God 文件（中）

1091 行内含：7 个 RPC 实现 + 领域类型 + 授权判定（`authorizedDocument`）+ 集合/字符串工具 + `operationFingerprint` + `storageDocumentID`。注意 `types.go`(96) 已存在却仍有类型定义留在 `contract.go`，说明类型归属已开始分散。

#### M3｜两套并行模型 + 约 150 行手写双向映射（中）

领域侧未导出类型（`contractVersion`、`documentManifest`、`versionFingerprint`、`operationRecord`）对应持久化侧导出类型（`ControlVersion`、`ControlDocumentManifest`、`ControlVersionFingerprint`、`ControlOperation`）。`captureControlStateLocked`（64 行）与 `restoreControlStateLocked`（86 行）逐字段手工搬运。

「不让 DTO 进持久化」的意图正确，但代价是两侧可能漂移，唯一护栏是 `validateControlState`。映射代码应集中到单独文件并补双向往返测试。

#### M4｜`DocumentIndexService` 承担四重角色（中）

同一个 struct 同时是：应用服务、控制面内存投影（5 个 map + generation + mutex）、状态机（fencing / 墓碑 / 幂等重放）、以及 Qdrant 投影来源。绝大多数方法都在 `s.mu.Lock()` 下同时操作这四重身份。

#### M5｜每请求全量加载控制面（中，可扩展性天花板）

`refreshControlStateLocked` 在**每个请求开头**调用 `controlStore.Load(ctx)` 把整个控制状态读回内存，`cloneControlState` 再做一次 JSON marshal/unmarshal 往返；写请求还要 `captureControlStateLocked` 再序列化一次。

两个后果：
1. 控制面必须整体装入内存，随文档数线性增长。
2. 每请求至少一次全量读 + 一次 JSON 往返。

这是为「多实例 fail-closed + generation CAS」付出的明确代价，但**容量上限未在任何 ADR 中量化**。同一把全局 `s.mu` 串行化所有文档的所有 RPC，这一并发边界也未声明。

#### M6｜读路径带写副作用（轻）

`SearchDocuments` 是查询，却先 `refreshControlStateLocked`（可能触发 pending 清理与写回），再 `SyncDocumentControls` 把控制面投影写入 Qdrant。正确性上说得通（读前收敛），但让查询的失败面与延迟都包含了写成本。

#### M7｜测试文件命名混入过程产物（轻）

存在 `internal/rag/p2_2_test.go`（259 行）—— 以项目阶段号而非行为命名。同包内另有 `contract_test.go`(558)、`control_store_test.go`(807)，测试文件本身偏大。

---

## 5. 跨应用对比

### 5.1 共同的高水准习惯

两个应用一致做到了：小端口接口、可选能力接口、sentinel error + `errors.Is`、适配层集中错误码映射、构造函数 nil 校验返回 error、`cmd/*/main.go` 组合根、`internal/` 私有化、不互相 import 内部实现（仅经 gRPC + 端口适配器）。这说明**团队技术审美是统一的**。

### 5.2 共同的核心问题

**架构规则只覆盖了一小部分正在演进的代码。**

| | gin-backend | mixin-search |
|---|---|---|
| 依赖方向是否正确 | ✅ 仅 document；遗留模块不正确 | ✅ 全部正确 |
| 是否有机器护栏 | 部分（仅「禁止回退」型覆盖全仓） | **完全没有** |
| 塌陷点 | 包内分层未覆盖的 4 个模块 | 核心包内部无边界 |

因此两个应用的问题**性质相同、位置不同**：gin-backend 是「正向分层规则未覆盖 auth/manager/chat/aiagent」，mixin-search 是「核心包内部根本没有可执行的边界」。二者都不是「目录不统一」的审美问题。

### 5.3 值得单独说明：`document_pipeline` 的公开范围

`document_pipeline/`（6 个生产文件 / 687 行）位于 `internal/` **之外**，因此技术上允许被外部模块导入。取证结果：

| 检查项 | 结果 |
|---|---|
| README 是否定位为复用组件 | ✅ README「代码入口」逐条列出全部 5 个文件 |
| 是否有扩展点设计 | ✅ `Pipeline`(types.go:55)、`BytesPipeline`(types.go:63)、`Registry`+`NewRegistry`(registry.go:12,16) —— 可插拔加载器注册表 |
| 是否有自有测试 | ✅ 4 个测试文件 |
| 是否存在外部消费者 | ❌ 全部调用方只有 `internal/rag/types.go:6`、`workflow.go:10`，**同一模块内** |

**定性：有意的可复用组件设计，但当前零外部消费者。** 不能判定为「忘了移动」，也不能据此论证公开暴露的必要性。应作为设计决策复核项，而非缺陷。

---

## 6. 终版定性

> **`gin-backend`**：目标架构已被编译级依赖图验证成立，但迁移只完成了一个模块。`modules/document` 达到样板级（domain 零内部依赖、适配器只依赖 domain）；`auth`/`manager` 存在强端到端覆盖但缺单元级隔离，且该缺口由 `logic ↔ GORM` 耦合导致；`chat`/`aiagent` 是生产入口不可达的孤儿实现，占全部代码 18.6%，但仍被 `go test ./...` 与 `go vet ./...` 完整编译检查。
>
> **`mixin-search`**：跨包依赖方向优秀且是严格线性链（`document_pipeline → rag → transport/grpc → cmd`，`rag` 不 import proto、不 import transport），契约与并发正确性设计出色（CAS 入签名、fail-closed 有代码支撑、可选能力接口）；但核心包 `internal/rag` 内部没有任何物理边界，`contract.go` 1091 行承担五种职责。**（原句「没有任何测试保护当前正确的依赖方向」已过时：C10 已为该边界增加 `internal/architecture` 依赖测试，见 §7。）**
>
> **共同问题不是目录不统一，而是架构规则尚未覆盖所有正在演进的代码。**

---

## 7. 优先级建议

| 优先级 | 动作 | 依据 |
|---|---|---|
| **P0** | 根 `.dockerignore` 补 `**/*.pem`、`**/*.key`、`**/vendor`；私钥改 secret volume 挂载 | §10.1：私钥进镜像层，随 `docker push` 外泄；同一缺口还让 64.1 MB vendor 进入每次构建 |
| **P0** | release 配置 fail-fast：禁止占位密钥、要求 `cookie.secure: true` | §10.2：`config.docker.yaml` 是实际部署配置，却含开发密钥与空口令 |
| **P0** | 当前工作树拆 commit 入库 + 打 tag | §10.4：13 个 commit、188 行 dirty、106 项未跟踪，阶段二只存在于工作区 |
| **P0** | 加 CI（build/vet/test + `apps/mixin-search` 架构测试 + proto 校验 + 前端 type-check） | §10.3：零 CI，最大的资产全靠人记得跑 |
| **P0** | 确认 `chat`/`aiagent`（3,211 行）的产品去留：删除，或移出主 module | D3：缺乏产品结论时持续支付编译、测试与认知成本 |
| **P0** | 确认 auth/manager 既有集成覆盖边界，并在解耦 `logic ↔ GORM` 后补关键用例的 characterization 单元测试 | D1 + 3.4：当前缺口是单元隔离，不是覆盖率 |
| **P1** | **同包内**拆分 `internal/rag` 大文件，**不改变包边界** | M1/M2：机械整理，零依赖图风险 |
| **P1** | ~~mixin-search 增加架构测试~~ | **已完成**（C10）：3 条单向依赖规则，全部 PASS |
| **P1** | 统一 gin-backend 路由装配，全部经 `Dependencies` 注入，消除 `platform` 中的模块硬编码 | D2：消除双组合点 |
| **P1** | ~~架构测试升级为规则表：遗留依赖「基线冻结、禁止新增」~~ | **已按建议调整**：精确清单冻结被移除，改为**仅方向性 import 断言**（8 条），见 `DEVELOPMENT_CONVENTIONS.md` §5 |
| **P1** | 消除 `connection.ServiceAuth` / `ServiceDocument` 的双连接键（同一份 `conf.PostgresConfig`） | §10.5：`ready()` 对同一库做两次健康检查，并暗示不存在的服务隔离 |
| **P2** | 依据真实依赖图抽离 `rag` 的具体存储适配器（**不预设** `model/service/store` 横向切分） | M1：横向切分改变可见性并可能引入循环，风险等级不同于拆文件 |
| **P2** | 若 chat 保留，将 `types/group/manager.go` 的 `Manager` 迁出 `types/` | 见 §8 勘误 E7 |
| **P2** | 复核 `document_pipeline` 是否**有意**作为公开库（当前零外部消费者） | 5.3 |
| **P3** | `common` 主动读取 `config/must` 改为组合根注入 | D8 |
| **P3** | 按行为重命名 `p2_2_test.go` | M7 |
| **P3** | 在 ADR 中量化「每请求全量加载控制面」的容量上限与全局锁并发边界 | M5 |

**排序原则**（本表已按此调整）：交付面问题优先于结构问题——前者是**一次事故的不可逆损失**，后者是**长期维护成本**（详见 §10.6）。

---

## 8. 勘误记录

本报告历经三轮核验，共修正 10 处表述，其中 **3 处是实质性错误、1 处是范围错误**。列出以保证结论可追溯。

| 编号 | 早期表述 | 修正后 | 性质 |
|---|---|---|---|
| E1 | 「测试占比 18.5% / 38.7%」 | 「`_test.go` 代码行占全部 Go 代码行比例」 | 命名不准，易误读为覆盖率 |
| E2 | 「auth/manager 零测试」 | 「无包内 Go 单元测试；行为由 1045 行跨进程 HTTP 套件覆盖，且该套件在仓库门禁中执行」 | **实质性错误**，改变了定性 |
| E3 | 「死代码 3,211 行，无人编译验证」 | 「生产入口不可达的孤儿实现，占 18.6%；仍被 `go test ./...` 与 `go vet ./...` 编译检查」 | 可达性口径 + **实质性错误** |
| E4 | 「`evaluation` 是 document 的第五层，架构测试未覆盖」 | 「独立评测能力包，零内部依赖的叶节点；不属对称分层」 | 定性错误 |
| E5 | 「拆文件与拆包子包均为 P1 零语义变化」 | 拆文件（同包）低风险；拆子包改变可见性与依赖图，**非零语义变化**，且不宜横向 `model/service/store` | 风险分级错误 |
| E6 | 「`document_pipeline` 公开是 P2 缺陷」 | 「公开范围待确认」：有意设计（有接口/注册表/测试/文档），但零外部消费者 | 取证不足 |
| E7 | 「`types/group → store` 是依赖倒置」 | **不是倒置**。`manager.go` 实现 `CreateGroup`/`JoinGroup`/`LeaveGroup`/`EnsureAllLoaded` 等用例编排，`service → store` 合法；真实缺陷是**包名与代码归属不符**（`types/` 成了 catch-all） | **判断错误** |
| E8 | 「架构约束只覆盖 1 个模块」（自查发现） | 3 条全仓库级规则存在，但均为「禁止回退」型；**正向分层方向**只对 document 强制 | 表述不准确 |
| E9 | 评估范围只含 Go 代码结构（自查发现） | **范围盲区**：未检查 `.dockerignore`、`config.docker.yaml`、Dockerfile `COPY` 行与 CI。补录为 §10，且**交付面问题优先级高于原版全部结构问题** | **范围错误** |
| E10 | 「组合根问题只是风格/可维护性」（经核验修正） | `ServiceAuth` 与 `ServiceDocument` 注册同一份 `conf.PostgresConfig` → `ready()` 对同一数据库做两次健康检查并暗示不存在的隔离。这是**误导性命名 + 冗余就绪检查**，非风格问题 | 定性不足 |

---

## 9. 方法局限与未验证项

1. **未运行集成测试**。`go vet ./...` 全部通过，但 `go test ./...` 未执行（`deployments/verify.ps1` 需要 Docker 与空数据卷）。测试结论依据脚本静态阅读与文档记录，未独立复现。
2. **未测量运行时行为**。M5 的容量上限、全局锁竞争、读路径写副作用均为静态代码推断，未做压测或 profiling。
3. **未评估前端**。评估范围限于 `apps/gin-backend` 与 `apps/mixin-search`。
4. **未评估 `packages/` 与 `deployments/`**，也**未检查交付面**（build context、镜像内容、release 配置、CI）。此项经后继核验被证明是**真实盲区**，已在 §10 补录。
5. **包内职责划分（D4/D5/D6/M3/M4）含人工判断成分**，不如依赖图与可达性结论刚性。
6. **基线含未提交变更**。工作树 141 个文件处于 dirty 状态，评估覆盖的是工作树快照而非 `a8ab94a` 提交本身。
7. **范围自查**：原版把"结构"等同于"Go 包与依赖方向"，因此**系统性漏掉交付面**——而交付面问题的失败后果比结构问题更直接（见 §10.6）。

---

## 10. 补充：交付面评估（原版遗漏）

原版范围限定在 Go 代码结构、依赖图与可达性。后继核验发现交付面存在**后果更直接**的风险，补录如下。本节各项均已逐条实测。

### 10.1 私钥进入镜像层

完整证据链（6 环全部实测）：

| # | 环节 | 结果 |
|---|---|---|
| 1 | `apps/gin-backend/configs/rsa_private.pem` | ✅ 存在 |
| 2 | 是否被 git 忽略 | ✅ `apps/gin-backend/.gitignore:15:*.pem` → 未跟踪（好事） |
| 3 | 根 `.dockerignore`（13 行）是否含 `*.pem` | ❌ **无该条目** |
| 4 | build context | 所有 compose 中 gin / mixin 均为 `context: .`（仓库根） |
| 5 | Dockerfile | `COPY --chown=app:app apps/gin-backend/configs ./configs` |
| 6 | 运行时读取 | `config.docker.yaml:51 private_key_path: "configs/rsa_private.pem"` |

→ **私钥进入镜像层，随任何 `docker push` 外泄**；且运行时确实读取镜像内那一份（compose 只覆盖 `config.yaml` 单个文件，不移除同目录其他文件）。

**根因**：`.gitignore` 与 `.dockerignore` 规则不一致；且 **app 级 `.dockerignore` 全部静默失效**——`apps/gin-backend/.dockerignore`(16 行)、`apps/simple-frontend/.dockerignore` 都存在，但 build context 不是它们所在目录，**一行都不生效**；`apps/mixin-search/.dockerignore` 不存在。

**同一根因的第二后果**：`deployments/postgresql/vendor`（**64.1 MB**）同样未被排除 → **每次构建 gin-backend / mixin-search 镜像都会把这 64MB 送进 build context**。

**结论**：改根 `.dockerignore` 一处，同时消除一项安全泄漏与一项构建性能问题。

### 10.2 release 配置携带开发密钥

`configs/config.docker.yaml` 是 compose **实际使用**的部署配置：

| 行 | 内容 |
|---|---|
| L6 | `mode: release` |
| L57 | `cookie.secure: false` |
| L60 | `csrf_secret: "development-only-change-before-production"` |
| L14 | `postgres.password: "postgres"` |
| L22 | `redis.password: ""` |

L55 注释承认"生产应启用 secure 并提供独立 CSRF 密钥"，但**无任何机制强制**。

### 10.3 零 CI

仓库**没有任何 CI 配置**（`.github/workflows` 仅存在于第三方包 `apps/simple-frontend/node_modules/rfdc/` 内）。投入最大的资产——两套架构依赖测试、`verify-generated.ps1` 的 SHA 校验、95 项运行时断言——**全靠人记得跑**。

### 10.4 工作树未入库

`git HEAD` = `a8ab94a`（阶段 2 起点），**13 个 commit**、`git status --porcelain` **188 行**、未跟踪 **106** 项。**整个阶段二只存在于工作区**，一次误操作即等于丢掉一个阶段。

（历史较浅有客观原因：最旧提交为 `cc54fb3 first commit`，第二条即 `be9ecc5 迁移了项目实现的技术栈`——仓库在技术栈迁移后重建过。但这不改变"阶段二未入库"的风险。）

### 10.5 其他已核验项

| 项 | 实测 |
|---|---|
| compose 漂移 | 同名 volume `control-postgres-data` 挂载点不同：根 `/var/lib/postgresql/data` vs mixin `/var/lib/postgresql` |
| PG 大版本分裂 | 根 `postgres:17-bookworm` vs mixin `pgvector/pgvector:0.8.6-pg18`（PG17/PG18 混用） |
| Go module 路径 | `module gin-backend` / `mixin-search` / `packages/gen`，非可寻址 import path → 无法 `go get`、无法对生成代码做版本 |
| **组合根双连接键** | `connection.ServiceAuth`(L67) 与 `ServiceDocument`(L102) 注册**同一份 `conf.PostgresConfig`** → `ready()` 对**同一个数据库**做两次健康检查(L38/L50)，并对外暗示一个**并不存在的服务隔离** |
| 前端半死代码 | `ChatView.vue` / `useChatSocket.ts` / `api/chat.ts` / `utils/chat-storage.ts` 均存在，router 中 chat 路由 **0** 条；`stores/session.ts` 仍 import `chat-storage`（故非纯死代码） |
| 前端工具链 | 无根 `package.json`、无 `pnpm-workspace.yaml`；38 个 `.vue/.ts`、**0** 个 vitest/eslint/prettier |
| proto 工具链 | `verify-generated.ps1` 只比对 SHA，**不校验 `protoc` / `protoc-gen-go` 版本** |
| 制品待遇不一 | `deployments/test-results` 58 个文件、18 个已跟踪；`.gitignore` 未排除而 `.dockerignore` 排除了 |
| 仓库治理文件 | LICENSE / CONTRIBUTING / SECURITY / CODEOWNERS / PR 模板 / `.editorconfig` **全部缺失** |

### 10.6 为什么本节应优先于 §3/§4 的结构问题

| | 结构问题（D1–D9、M1–M7） | 交付面问题（本节） |
|---|---|---|
| 影响 | 长期维护成本 | **一次事故的不可逆损失** |
| 典型后果 | 新人上手慢、改动易腐化、测试缺口 | 私钥外泄、整个阶段丢失、门禁形同虚设 |
| 可逆性 | 可渐进修复 | 密钥泄漏**不可逆** |

**因此整改顺序应把本节内容置于结构改造之前。** `REFACTOR_CLEANUP_PLAN.md` 已据此修订排序（新增「阶段 A」）。

---

## 附录 A 复现命令

```powershell
# 0. 关闭 Go telemetry（沙箱下写 AppData 会被拒绝，产生噪音）
$env:GOTELEMETRY='off'

# 1. 规模统计
Get-ChildItem apps/gin-backend -Recurse -File -Filter *.go |
  ForEach-Object { [pscustomobject]@{
    Lines = @(Get-Content $_.FullName).Count
    Rel   = $_.FullName
  } } | Sort-Object Rel

# 2. 编译级内部依赖图
cd apps/gin-backend
go list -f '{{.ImportPath}}|{{range .Imports}}{{.}} {{end}}' ./... |
  Where-Object { $_ -match 'gin-backend/internal' }

cd ../mixin-search
go list -f '{{.ImportPath}}|{{range .Imports}}{{.}} {{end}}' ./...

# 3. 生产入口可达性（全部入口求并集）
cd ../gin-backend
$all = go list ./... | Where-Object { $_ -match '^gin-backend/internal' }
$cmds = go list ./cmd/...
$u = @(); foreach ($c in $cmds) {
  $u += go list -deps $c | Where-Object { $_ -match '^gin-backend/internal' }
}
$u = $u | Sort-Object -Unique
$all | Where-Object { $u -notcontains $_ }   # 不可达包

# 4. 孤儿实现是否被任何生产代码 import
Get-ChildItem . -Recurse -File -Filter *.go |
  Where-Object { $_.Name -notmatch '_test\.go$' } |
  Select-String -Pattern 'gin-backend/internal/modules/chat' -SimpleMatch

# 5. 静态门禁
foreach ($m in 'apps/gin-backend','apps/mixin-search','packages/gen') {
  Push-Location $m; go vet ./...; "exit=$LASTEXITCODE"; Pop-Location
}
```

## 附录 B 完整内部依赖图

### B.1 gin-backend（仅内部 import）

```
internal/app
      -> common/base/cache, common/base/connection, common/base/connection/postgresql,
         common/base/connection/redis, common/base/logger, common/service/sessionevent,
         config, modules/document/application, modules/document/infrastructure/cache,
         modules/document/infrastructure/mixinsearch, modules/document/infrastructure/postgresql,
         modules/document/interfaces/http, platform/httpserver
internal/common/base/cache
      -> common/base/connection, common/base/connection/redis
internal/common/base/connection/postgresql
      -> common/base/connection/registry, config/must
internal/common/base/connection/redis
      -> common/base/connection/registry, config/must
internal/common/base/logger
      -> config/must
internal/common/service/jwt
      -> common/base/connection, common/base/connection/redis, config
internal/common/service/sessioncookie
      -> common/service/jwt, config
internal/config
      -> config/custom, config/must
internal/config/custom
      -> config/custom/cookie, config/custom/cors, config/custom/jwt, config/custom/upload
internal/model/cache
      -> model/orm/auth, model/orm/chat, model/orm/manager
internal/model/orm/auth
      -> model/orm
internal/model/orm/chat
      -> model/orm
internal/model/orm/manager
      -> model/orm
internal/model/store
      -> common/base/cache, common/base/connection, common/base/connection/postgresql,
         model/cache, model/orm/auth, model/orm/manager
internal/modules/auth/api
      -> modules/auth/handler
internal/modules/auth/handler
      -> common/base/errors, common/base/responses, common/service/sessioncookie,
         modules/auth/logic, modules/auth/types/requests
internal/modules/auth/logic
      -> common/base/connection, common/base/connection/postgresql, common/base/constant,
         common/service/jwt, common/service/sessionevent, config, model/orm/auth,
         model/store, modules/auth/types/requests
internal/modules/chat
      -> common/service/sessionevent, config, modules/chat/store, modules/chat/structure/bridge
internal/modules/chat/api
      -> modules/chat/handler
internal/modules/chat/handler
      -> common/base/errors, common/base/responses, common/service/jwt, config,
         modules/chat, modules/chat/logic, modules/chat/types/client, modules/chat/types/group
internal/modules/chat/logic
      -> common/service/jwt, modules/chat, modules/chat/types/client
internal/modules/chat/store
      -> common/base/connection, common/base/connection/postgresql, model/orm/chat,
         modules/chat/types/constant, modules/chat/types/message
internal/modules/chat/structure/bridge
      -> modules/chat/store, modules/chat/types/client, modules/chat/types/constant,
         modules/chat/types/group, modules/chat/types/message
internal/modules/chat/types/client
      -> modules/chat/types/message
internal/modules/chat/types/group
      -> modules/chat/store, modules/chat/types/message
internal/modules/chat/types/message
      -> modules/chat/types/constant
internal/modules/document/application
      -> modules/document/domain
internal/modules/document/infrastructure/cache
      -> common/base/cache, modules/document/domain
internal/modules/document/infrastructure/mixinsearch
      -> modules/document/domain
internal/modules/document/infrastructure/postgresql
      -> modules/document/domain
internal/modules/document/interfaces/http
      -> common/base/errors, common/base/responses, common/service/jwt,
         modules/document/application, modules/document/domain
internal/modules/manager/api
      -> modules/manager/handler
internal/modules/manager/handler
      -> common/base/errors, common/base/responses, common/service/jwt,
         common/service/sessioncookie, modules/manager/logic,
         modules/manager/types/constant, modules/manager/types/requests
internal/modules/manager/logic
      -> common/base/constant, common/service/jwt, config, model/orm/manager, model/store,
         modules/manager/types/constant, modules/manager/types/requests, modules/manager/utils
internal/modules/manager/utils
      -> common/base/connection, common/base/connection/postgresql
internal/platform/httpserver
      -> common/base/responses, common/service/sessioncookie, config,
         modules/auth/api, modules/manager/api, platform/httpserver/middleware
internal/platform/httpserver/middleware
      -> common/base/constant, common/base/errors, common/base/responses,
         common/service/jwt, common/service/sessioncookie, config
```

未出现在本图中的内部包（含 `modules/document/domain`、`modules/document/evaluation`、`modules/aiagent`、`internal/architecture` 等）均为**零内部依赖**。（`internal/architecture` 仅测试、无生产代码：其精确清单冻结版曾被移除，**方向性断言版已恢复**。）

### B.2 mixin-search（仅内部 + 共享生成代码 import）

```
cmd/demo              -> internal/rag
cmd/rag-grpc-client   -> packages/gen/mixin-search/v1
cmd/rag-healthcheck   -> packages/gen/mixin-search/v1
cmd/rag-server        -> internal/rag, internal/transport/grpc, packages/gen/mixin-search/v1
document_pipeline     -> (无)
internal/rag          -> document_pipeline
internal/transport/grpc -> internal/rag, packages/gen/mixin-search/v1
```
