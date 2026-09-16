# go-web 结构改造与清理方案

> **依据**：[STRUCTURE_ASSESSMENT.md](./STRUCTURE_ASSESSMENT.md)（结构与复用评估）、[MIXIN_SEARCH_SPLIT_ASSESSMENT.md](./MIXIN_SEARCH_SPLIT_ASSESSMENT.md)（服务边界评估）、[docs/architecture/DEVELOPMENT_CONVENTIONS.md](./docs/architecture/DEVELOPMENT_CONVENTIONS.md)（现行约定）
> **文档性质**：外部改造提案，不属于 `docs/` 治理树。项目可自行归档，或将其条目并入 `docs/planning/CURRENT_IMPLEMENTATION_PLAN.md`。
> **当前基线**：C1–C8、C10–C12 已完成（身份契约、出入口统一、mixin-search 架构测试、`cmd/tools` 归置）；**C9（gin-backend 架构规则表 + 遗留基线冻结）已撤销**——项目不采用精确清单冻结方式，见 [`DEVELOPMENT_CONVENTIONS.md`](./docs/architecture/DEVELOPMENT_CONVENTIONS.md) §5；C13（chat/aiagent）已搁置。

---

## 0. 方案总则

**三条排序原则**

1. **先决策，再动代码**——凡是会改变契约、所有权或可用性语义的，先落 ADR；否则改动会被反复推翻。
2. **先机械，后语义**——同包拆文件、重命名、归位这类零行为变化的先做，为后续改造腾出可读空间。
3. **风险按「是否改变可见性/依赖图/运行时行为」分级**，而不是按代码量分级。

**风险分级**

| 级别 | 含义 | 典型动作 | 验收 |
|---|---|---|---|
| **机** | 同包拆文件、重命名、移动目录 | 零依赖图变化 | `go build/vet/test` |
| **语** | 改变可见性、依赖方向或包边界 | **无自动化护栏**，需人工核对 | 同上 + `go list` 依赖图人工确认 |
| **行** | 改变运行时行为、并发或可用性 | 需集成测试与压测 | 同上 + 集成/端到端门禁 |

**工作规模**：S ≤ 半天；M = 1–2 天；L ≥ 3 天或涉及架构决策。

---

## 阶段 A：交付面风险消除（**最高优先 / 先于阶段 0**）

**为什么排在最前**：以下各项属于**尾部风险**——一次事故的代价远超全部结构改造的收益。本方案初版把工程交付面整体排在结构改造之后，是**排序错误**，本版修正。

| 编号 | 动作 | 依据（已逐项核验） | 规模 |
|---|---|---|---|
| **WA-1** | 把当前工作树拆 commit 入库 + 打 tag | `git HEAD` = `a8ab94a`（阶段 2 起点），**13 个 commit**、`git status --porcelain` **188 行**、未跟踪 **106** 项——整个阶段二只存在于工作区 | M |
| **WA-2** | 根 `.dockerignore` 补 `**/*.pem`、`**/*.key`、`**/vendor` | 私钥被 `.gitignore:15 *.pem` 忽略，但根 `.dockerignore`（13 行）**无该条目**；build context = 仓库根 → **私钥进入镜像层**，随任何 `docker push` 外泄。同一缺口还使 **64.1 MB** 的 `deployments/postgresql/vendor` 进入每次构建的 context | S |
| **WA-3** | 私钥改为启动时从 secret volume 挂载 | `config.docker.yaml:51 private_key_path: "configs/rsa_private.pem"` 指向镜像内那份；compose 只覆盖了 `config.yaml` 单个文件 | M |
| **WA-4** | release 配置 fail-fast 校验 | `config.docker.yaml` 实为 `mode: release`(L6) + `cookie.secure: false`(L57) + `csrf_secret: "development-only-change-before-production"`(L60) + `postgres.password: "postgres"`(L14) + `redis.password: ""`(L22)；L55 注释承认应替换但**无机制强制** | S |
| **WA-5** | 加 CI：两 module 的 build/vet/test + `apps/mixin-search` 架构测试 + proto 生成物校验 + 前端 `vue-tsc` | 仓库**零 CI**；`verify-generated.ps1`、95 项运行时断言、mixin-search 架构测试全靠人记得跑 | M |
| **WA-6** | 修 `DEVELOPMENT_CONVENTIONS.md` §6.1 与 §7 的自相矛盾 | §6.1 写 mixin-search「当前**无任何架构测试**」，而 §7 的 C10 写「**完成**」，且 `apps/mixin-search/internal/architecture/dependencies_test.go` 确实存在 | S |

**WA-2 的根因**：`.gitignore` 与 `.dockerignore` 规则不一致；且 **app 级 `.dockerignore` 全部静默失效**——`apps/gin-backend/.dockerignore`、`apps/simple-frontend/.dockerignore` 都存在，但 build context 不是它们所在目录，**一行都不生效**；`apps/mixin-search/.dockerignore` 不存在。

**验收**：`docker build` 后用 `docker history` 或导出层内容确认镜像内**无 `.pem`/`.key`**；构建 context 体积显著下降；CI 状态在仓库首页可见。

**与后续的关系**：WA-5（CI）是**阶段 1–4 全部工作包的前置**——没有 CI 的脚本只是"希望有人记得跑"。

---

## 1. 阶段 0：决策（阻塞后续，无代码）

不做完这些，后面 3 个阶段会返工。

| 编号 | 决策项 | 为什么阻塞 | 产出 |
|---|---|---|---|
| **W0-1** | 确立 **多消费者** 为拆分理由，「不分散」为其结论 | 当前理由写在指南 L45 是结论位置；不纠正会导致验收标准错配 | ADR-012，并修订 ADR-001 背景节 |
| **W0-2** | 声明 **「派生数据可随意重建」以「尚未交接」为前提** | 这是全套"安全"结论的隐藏前提，交接后全部失效 | 写入 ADR-012 的"后果"节 |
| **W0-3** | **pgvector 去留** | 三套检索实现中唯一无主者，影响 W2-1 的收敛目标 | 删除，或定为主线并登记 owner |
| **W0-4** | **BM25 交接后的降级策略** | `document_search_projection` 删还是留，决定 W4 全部设计 | 写入 ADR-010 后续项 |
| **W0-5** | **chat/aiagent 去留**（C13） | 影响冻结基线是否保留、`chat` 目录是否参与 W3 | 删除 / 移出 / 长期冻结 |
| **W0-6** | **交接门禁口径**：纳入 p50 决策 + 扩大样本量 | 当前 `Decide()` 只看 Errors/Profile/相关性/p95，无 p50、p99、吞吐、并发写入下延迟；且评测集**只有 7 条 query**，其 p95 等于 max，不能支撑尾延迟结论 | 修订 ADR-010 |
| **W0-7** | **调用授权方案**：服务身份认证 + 检索范围 capability | mixin-search **无任何调用方认证**（无 interceptor、无 TLS、`reflection` 开启）；`SearchDocuments` 直接采信调用方提供的 allow-list，这是一个 **capability confusion**。今天因唯一调用方是 gin-backend 且只绑 `127.0.0.1` 而可控，py-agent 直连即与指南 §10「Model 不能授予权限」冲突 | ADR-012 决策节 |

---

## 2. 阶段 1：机械清理（风险＝机，可立即执行）

**目标**：零行为变化地把最难读的地方变可读，为阶段 2/3 腾出空间。

| 编号 | 动作 | 现状 | 目标 | 规模 |
|---|---|---|---|---|
| **W1-1** | 拆 `mixin-search/internal/rag/contract.go` | 1,091 行，含 7 个 RPC + 领域模型 + 授权判定 + 工具 | `service.go`(RPC 实现) / `model.go`(类型) / `authorization.go`(授权与集合工具) / `identity.go`(key 与指纹) | S |
| **W1-2** | 拆 `mixin-search/internal/rag/control_store.go` | 572 行，含模型 + 校验 + 双向映射 | `control_model.go` / `control_validate.go` / `control_mapping.go` | S |
| **W1-3** | 双模型映射加**往返测试** | `capture`/`restore` 逐字段手写 ~150 行，唯一护栏是 `validateControlState` | 新增 `restore(capture(s)) ≡ s` 属性测试，防两侧漂移 | S |
| **W1-4** | 重命名 `internal/rag/p2_2_test.go` | 以项目阶段号命名 | 按行为命名（如 `qdrant_control_filter_test.go`） | S |
| **W1-5** | `cmd/document-search-eval` → `cmd/tools/` | 不在镜像内（Dockerfile 只构建 3 个二进制），但留在 `cmd/` | 与 C12 规则一致 | S |
| **W1-6** | 清理 `STRUCTURE_ASSESSMENT.md` 中的旧路径引用 | 2 处 `cmd/runtimeapitest/main.go` | 加"迁移前时点快照"标注，或更新路径 | S |

**验收**：`go build/vet/test` 全绿；`go list` 依赖图**逐字节不变**（可用改造前后 diff 证明）。

**为什么先做**：W1-1/W1-2 把 1,663 行从 2 个文件变成 7 个文件，是阶段 3 修改控制面并发模型的前提——在 572 行单文件里改锁策略不可审。

---

## 3. 阶段 2：结构解耦（风险＝语）

### W2-1 mixin-search 适配器抽离子包

| 项 | 内容 |
|---|---|
| 依据 | `internal/rag` 单包内同时有领域模型、应用服务、端口、适配器（18 文件）。Go 的包是最小封装边界，同包意味着没有任何机制阻止应用服务直接 new 具体适配器 |
| 动作 | 抽 `internal/rag/adapter/vector/{qdrant,pgvector,memory}`、`internal/rag/adapter/control/{postgres,memory}`，保留 `rag` 为核心包 |
| **明确不做** | 不采用横向 `model/service/store` 切分——那是按技术层切，会改变可见性且易生循环 |
| 前置 | W1-1/W1-2（先拆文件再抽包，避免一次改两件事） |
| 规模 / 风险 | M / 语 |

### W2-2 `document` 领域模型补行为（贫血 → 充血）

| 项 | 内容 |
|---|---|
| 依据 | `domain` 包零内部 import（纯净），但基本只有数据结构；修订号递增、发布状态迁移、摘要截断、dedupe key 全在 `application/command_service.go`（437 行事务脚本） |
| 动作 | 把状态迁移收回聚合：`Document.Activate(versionID, revision)` / `Document.Trash(now)`、`DocumentVersion.Publish()/Supersede()`、`AccessPolicy.Replace(public, revision)` |
| 收益 | `Create`/`Update`/`Trash` 各缩 30–50%；状态机集中一处，可单测 |
| 规模 / 风险 | M / 语（纯重构，现有 14 个测试文件兜底） |

### W2-3 `Repository` 端口按聚合拆分

| 项 | 内容 |
|---|---|
| 依据 | `domain/repository.go` 单个接口 **25 个方法**，横跨 space/member/document/version/policy/grant/projection/outbox 八类概念 + 事务控制 |
| 动作 | 拆 `DocumentRepository` / `SpaceRepository` / `AccessRepository` / `IndexDeliveryRepository`；事务入口改为 `InTransaction(ctx, func(Tx) error)`，`Tx` 暴露各子仓储 |
| **顺带** | 把 `InRepeatableRead`、`ListDocumentsForIndexing` 等为运维用例加入的原语从领域端口移出 |
| 规模 / 风险 | M / 语 |

### W2-4 `common/service/sessioncookie` 去 gin 依赖

| 项 | 内容 |
|---|---|
| 依据 | 它是唯一依赖 gin 的 `common/service` 文件，而约定 §1.3/§2 写的是绝对禁止——**规则与实际不一致**（此前由架构测试冻结为已知例外，该测试已撤销） |
| 动作 | gin 相关部分（`AccessToken`/`RefreshToken`/`SetTokens`/`ClearTokens`/`Resolve*Refresh`/`ValidateCSRF`）迁至 `platform/httpserver/sessioncookie`；纯 crypto（`csrfMAC`/`newCSRFToken`）留在 `common/service` |
| 收益 | 规则与实际一致，`common/service` 可声明为完全 transport 无关 |
| 规模 / 风险 | M / 语 |

### W2-5 统一路由装配

| 项 | 内容 |
|---|---|
| 依据 | `platform/httpserver/router.go` 硬编码 import `modules/auth/api`、`modules/manager/api`，而 `document` 经 `Dependencies` 注入——同一关注点两种机制 |
| 动作 | auth/manager 也改为 `RouteRegistrar` 经组合根注入；移除 `platform/httpserver` 中的模块硬编码 |
| 规模 / 风险 | M / 语 |

### W2-6 组合根与全局态收敛

| 项 | 内容 |
|---|---|
| 依据 | `app/dependencies.go` 四个失败分支重复 `Unregister` 链、启动失败用 `panic`；`PostgreSQLManager`/`RedisManager`/`sessionevent.SetDefaultBus`/`basecache.DefaultRuntime` 为包级可变全局态 |
| 动作 | 结构化 cleanup 栈（LIFO 注册 + `errors.Join`）；连接管理器改为组合根持有并显式注入 |
| 规模 / 风险 | M / 语 |

### W2-7 `common` 反向依赖 `config` 收敛

| 项 | 内容 |
|---|---|
| 依据 | `common/base/logger → config/must`、`common/base/connection/postgresql → config/must`、`common/service/jwt → config`——基础设施反向依赖配置解析 |
| 动作 | 改为构造函数接收配置值，组合根注入 |
| 规模 / 风险 | **L / 语**（`config.CustomConfig()` 全局读取点较多，需逐个改；建议最后做） |

---

## 4. 阶段 3：并发与可扩展性（风险＝行）——**py-agent 接入前置**

这是本次方案中**技术风险最高、但对第二消费者最关键**的一段。

### W3-1 控制面去除全局锁 + 消除每请求全量加载

**现状（实测）**：每个 RPC 开头 `refreshControlStateLocked` 全量 `controlStore.Load()`，`cloneControlState` 再做一次 JSON marshal/unmarshal；所有 RPC 共享一把 `s.mu`。

**为什么必须做**：mixin-search 将同时承载两种互不相关的负载——go-web 的**批量索引写入**（顺序、可延迟、突发）与 py-agent 的**在线检索**（延迟敏感、并发不可预测）。共享一把全局锁意味着前者的突发会直接干扰后者的延迟。

**分三步，每步独立可验收**：

| 步 | 动作 | 收益 | 风险 |
|---|---|---|---|
| **1** | 增加 generation 短路：轻量查询当前 generation，仅当高于本地才全量 Load | 稳态下消除绝大多数全量加载与 JSON 往返 | 低 |
| **2** | 全局 `s.mu` 改为**按文档分片锁**（`documentID` → mutex），仅 operations/pending 集合用小全局锁 | 文档间并行；写入不再阻塞无关检索 | 中 |
| **3** | 控制状态从**单个 JSON 快照**改为**按文档行**存储，配 `SELECT ... FOR UPDATE` 行级 CAS | 彻底移除"整个控制面必须装进内存"的天花板 | 高 |

**第 3 步需要新 ADR**——它改写了 ADR-006 的整快照提交顺序设计。**建议先做 1+2，把 3 留作独立议题**。

**护栏**：`control_store_test.go`（807 行）+ `control_store_integration_test.go`（117 行）+ `verify-control-store.ps1` 已覆盖重启、并发、失败关闭、generation CAS、乱序与墓碑语义——这四者是本项的安全网。

### W3-3 调用授权边界（**A 线第一优先，先于 W3-1**）

| 项 | 内容 |
|---|---|
| 依据 | `newGRPCServer` 仅 `grpc.NewServer(grpc.MaxRecvMsgSize(...))`——**无 interceptor、无 TLS、且 `reflection.Register` 开着**；全生产代码无 `caller/principal/tenant/audience/scope` 概念；`SearchDocuments` 的 OR 授权直接用 `request.AllowedSpaceIDs/AllowedDocumentIDs`，**不校验调用方是否有权声明该范围** |
| 动作 | ① 服务身份认证：索引写 RPC 仅允许 gin-backend Worker；② 检索调用认证：区分 go-web / py-agent / 运维工具；③ **范围 capability**：由 go-web 签发短时、限定 audience/用户/空间/文档范围的 capability，mixin-search 校验 `requested ⊆ granted`（**只允许缩小**）；④ 配额与审计 |
| **明确不做** | 不让 mixin-search 每次查询反向调用 go-web 校验——会新增同步链路，与 W3-2「读路径不应增加依赖」矛盾 |
| 可测性 | 「只允许缩小」是纯集合断言，可在现有 `contract_test.go` 中直接加 subset 测试 |
| 规模 / 风险 | L / 行 |
| 前置 | W0-7 决策 |

### W3-4 蓝绿索引与不中断重建

| 项 | 内容 |
|---|---|
| 依据 | 一旦存在在线消费者，重建不再免费。当前无 generation/alias 切换机制 |
| 动作 | `active generation → 后台构建 next generation → 验证完整性/权限/质量 → 原子切换 collection alias → 保留旧 generation` |
| 收益 | 同时保住「派生数据可重建」与「在线消费者不见空索引」；替代"保留 vs 删除 `document_search_projection`"的伪二选一 |
| 规模 / 风险 | L / 行 |
| 前置 | W3-1 步 1+2 |



| 项 | 内容 |
|---|---|
| 依据 | `SearchDocuments` 是查询，却先 `refreshControlStateLocked`（可能触发 pending 清理与写回），再 `SyncDocumentControls` 把控制面投影写入 Qdrant——查询带写副作用，失败面与延迟都包含写成本 |
| 动作 | 维护"投影 generation"，仅在过期时同步；把投影同步移到后台 reconciler |
| 收益 | 搜索延迟与索引写入解耦，直接改善 py-agent 的在线体验 |
| 规模 / 风险 | M / 行 |
| 前置 | W3-1 第 2 步 |

---

## 5. 阶段 4：遗留模块迁移（风险＝语，可延后）

当前采用**约定冻结**（`DEVELOPMENT_CONVENTIONS.md` §5：遗留目录禁止新增生产文件）——不要求重写，但禁止新增。**曾以 `internal/architecture` 记录 70 个文件的精确基线并做相等断言，该测试已撤销**（会训练团队「顺手更新基线」，且禁止的是文件数变化而非依赖方向错误）。是否解冻取决于阶段 0 的 W0-5。

### W4-1 `auth` / `manager` 解耦 GORM

| 项 | 内容 |
|---|---|
| 依据 | `auth/logic` 与 `manager/logic` 直接依赖 `model/orm/*`、`model/store`、`common/base/connection/postgresql`——这正是 `document` 已消除的耦合，也是两个模块**零单元测试**的根因（无法脱离 PostgreSQL 测试） |
| 动作 | 引入仓储端口 + 适配器，把 `logic` 与 ORM 分离；先做 `refresh`/`logout` 路径（已部分解耦），再 `login`/`register`/`request` |
| 验收 | 每个解耦的用例补 characterization 单元测试 |
| 规模 / 风险 | **L / 语** |

### W4-2 迁移到 Tier A/B 并更新基线

解耦完成后，把模块迁到约定 §1.2 的 Tier B 形态，并**同步更新** `DEVELOPMENT_CONVENTIONS.md` §5 的遗留清单与 §7 状态表。

> 若届时恢复自动化约束，应采用**方向性断言**（禁止 import X、禁止新增顶层目录），而**不是**文件清单快照——理由见约定 §5。

---

## 6. 明确的非目标

| 不做 | 原因 |
|---|---|
| **不合并两个服务** | 拆分由多消费者拓扑倒推，反证检验全部通过 |
| **不重写 auth/manager** | 约定冻结 + 渐进迁移优于大重写；两模块有 1045 行 HTTP 集成套件覆盖真实行为 |
| **不为 chat/aiagent 做结构清理** | 按 W0-5 决定；未决前不投入 |
| **不在交接前删除 `document_search_projection`** | 它是当前唯一的降级路径（P2.4 已验证停机不影响搜索） |
| **不引入 DI 容器 / 新框架** | 现有手工装配的问题（panic、全局态）用结构化 cleanup 即可收敛，引入容器收益不成比例 |
| **不做横向 `model/service/store` 包切分** | 按技术层切分改变可见性、易生循环，且不符合 Go 的包语义 |
| **不做无业务收益的一次性重写** | 与 `PROJECT_STRUCTURE.md` §3 既有原则一致 |

---

## 7. 排序与依赖

```
阶段 0（决策）
  W0-1 多消费者理由 ─┬─> 影响验收标准
  W0-3 pgvector ─────┤
  W0-4 降级策略 ─────┼─> 阻塞 W4（交接）
  W0-6 门禁口径 ─────┘
  W0-5 chat/aiagent ────> 影响 W4-2 基线收缩
        │
        v
阶段 1（机械，可立即开始，不依赖阶段 0）
  W1-1 拆 contract.go ─┬─> 阻塞 W2-1、W3-1
  W1-2 拆 control_store.go ─┘
  W1-3 往返测试 ──────────> 护栏 W2-1、W3-1
        │
        v
阶段 2（结构解耦）          阶段 3（并发，py-agent 前置）
  W2-1 适配器抽包             W3-1 控制面（依赖 W1-1/W1-2）
  W2-2 充血模型               W3-2 读路径副作用（依赖 W3-1 步2）
  W2-3 拆仓储端口
  W2-4 sessioncookie 去 gin
  W2-5 统一路由装配
  W2-6 组合根收敛
  W2-7 common→config（最后）
        │
        v
阶段 4（遗留迁移，依赖 W0-5）
  W4-1 解耦 GORM ──> W4-2 Tier 迁移 + 基线收缩

阶段 5（交接，绑定 py-agent 时序，本方案不展开）
```

**可并行**：阶段 1 与阶段 0 并行；阶段 2 内部各项除 W2-7 外基本独立。

---

## 8. 统一验收门禁

**每个工作包完成后**

```powershell
# gin-backend
cd apps/gin-backend; go build ./...; go vet ./...; go test ./...

# mixin-search
cd apps/mixin-search; go build ./...; go vet ./...; go test ./...; go test ./internal/architecture -v

# 仓库级
git diff --check
```

**涉及运行时行为（风险＝行）或 HTTP/认证的，追加**

```powershell
./deployments/verify.ps1     # 95 项断言 × 2 拓扑 + 三个 PASS 标记
```

**涉及依赖方向（风险＝语）的，追加**

```powershell
go list -f '{{.ImportPath}}|{{range .Imports}}{{.}} {{end}}' ./...   # 与改造前 diff
```

**约束纪律**

- **gin-backend 当前没有自动化架构护栏**（C9 已撤销）。因此「语级」改动必须显式做 `go list` 依赖图 diff——这不是可选项，而是唯一护栏。
- `apps/mixin-search/internal/architecture` 仍然生效，不得为了通过改动而放宽其 3 条规则。
- 若将来恢复 gin-backend 的自动化约束，**只加方向性断言**（禁止 import X、禁止新增顶层目录、必需路径存在），**不加文件清单快照**。新增任何规则都需给出「为何这条规则值得机器强制」。

---

## 9. 建议的起步顺序

**修正说明**：本方案初版把「拆 `contract.go`」列为第 1 项、把工程交付面排在结构改造之后。经核验这是**排序错误**——未入库的 188 项变更、镜像内私钥、零 CI 属于**尾部风险**，一次事故的代价超过全部结构改造的收益。

修正后的顺序：

1. **WA-1 工作树入库 + WA-2 `.dockerignore` + WA-4 release 校验**（半天 + 1 小时）——消除最高尾部风险，成本最低。
2. **WA-5 加 CI**——它是阶段 1–4 全部工作包的**前置**。没有 CI，后面每一条"跑门禁"都只是自觉。
3. **W0-1 / W0-2 / W0-7（ADR-012）**——决定后续所有工作的验收标准是否指向正确目标，并确定调用授权方案。纯文档，可与 1、2 并行。
4. **W1-1 + W1-2 + W1-3**——机械拆文件 + 往返测试，零风险，且是 W3-1 的前置。
5. **W3-3 调用授权**（A 线第一优先）→ **W3-1 步 1+2** → **W3-2** → **W3-4**。
6. **W2-x 结构解耦**（多数可并行）→ **W4 遗留迁移**。

**若只做三件事**：WA-1、WA-2、WA-5。

---

# 第二部分：如何实施

## 10. 实施前置：先把工作树落地（**阻塞项**）

**当前状态**：`git HEAD` = `a8ab94a`（阶段 2 起点），工作树含 **188 项未提交变更**（80 修改 + 5 删除 + 103 未跟踪），涵盖 P2.1–P2.5、缓存加固、C1–C12，以及本方案的三份评估文档。

**为什么这是阻塞项**：在 188 项脏变更之上做结构改造会导致

- 无法区分「改造的效果」与「既有未提交的差异」
- 没有可回滚的基线——`git revert` 无从下手
- 无法判断任何结构变化是哪一个变更引入的

**动作**：按关注点分批提交，然后才开始 W0/W1。

| 批次 | 内容 | 建议 message |
|---|---|---|
| 1 | 三份评估/方案文档（纯文档，零风险） | `docs: 新增结构与拆分评估及改造方案` |
| 2 | P2.1–P2.5 检索链路 + 缓存加固（既有工作，量大） | `feat: 阶段2 检索链路与缓存加固` |
| 3 | C1–C8 身份契约与出入口统一 | `refactor: 统一身份契约与 HTTP 出入口` |
| 4 | C9–C12 架构测试 + `cmd/tools` 归置（**注意：C9 的 gin-backend 部分后续已撤销**） | `build: mixin-search 架构测试与工具归置` |

批次 2 约 40+ 文件，若过大可按 P2.x 再拆。**批次之间必须逐次跑门禁**——每批提交后 `go build/vet/test` 必须全绿；若某批失败，说明该批内部不自洽，应继续拆分而不是硬提。

---

## 11. 工作包执行 SOP

每个 WP 固定走七步，不跳步：

1. **前置**——`git status` 干净；记录 `git rev-parse HEAD`
2. **基线取证**（仅语级/行级）——把改造前的依赖图存盘
   ```powershell
   cd apps/<module>
   go list -f '{{.ImportPath}}|{{range .Imports}}{{.}} {{end}}' ./... > "$env:TEMP\before.txt"
   ```
3. **执行**——只改一个关注点。若发现需要顺带改别的，记下来另开 WP
4. **验证**——按 §8 的风险级别跑对应门禁
5. **对比**（仅语级）——`go list` 输出与 `before.txt` 做 diff
6. **证据**（仅行级）——报告写入 `deployments/test-results/<wp-id>_<timestamp>.md`
7. **提交**——一个 WP 一个提交，message 引用 WP 编号

**三条硬性规则**

- **语级改动必须显式做依赖图 diff**——gin-backend 已无自动化护栏，这是唯一防线（步骤 2/5）
- **不得为通过而放宽 `apps/mixin-search/internal/architecture` 的 3 条规则**
- 机级改动**不得**触碰任何 `_test.go`（W1-3 本身除外）

---

## 12. 门禁自动化：补一个 `verify-fast.ps1`

**现状问题**：仓库**没有任何 CI**（根目录隐藏目录仅 `.agents`/`.git`/`.vscode`；`.github/workflows` 只存在于第三方包 `node_modules/rfdc/` 内）。唯一门禁是手工执行的 `deployments/verify.ps1`，而它会**构建镜像并启动 8 个服务**——对单个 WP 而言过重，实际后果是「验证被跳过」。

**建议**：新增 `deployments/verify-fast.ps1`，不含 Docker：

```powershell
# 要点
# 1. packages/proto/verify-generated.ps1
# 2. 三个 module 逐目录执行 go build / go vet / go test ./...
# 3. mixin-search 的 go test ./internal/architecture -v
# 4. git diff --check
# 5. 输出 VERIFY_FAST=PASS
```

| 脚本 | 何时用 | 耗时 |
|---|---|---|
| `verify-fast.ps1` | 每个 WP、每次提交前 | 秒级 |
| `verify.ps1` | 行级改动、里程碑、阶段收口 | 数分钟（含镜像构建） |

**可选下一步**：加最小 CI（如 `.github/workflows/verify.yml`）跑 `verify-fast.ps1`，让「忘记跑门禁」在机制上不可能。

---

## 13. 回滚策略

| 风险 | 回滚方式 | 备注 |
|---|---|---|
| 机 | `git revert <commit>` | 单提交即可 |
| 语 | `git revert`，随后需**手工核对依赖图**（gin-backend 无自动化护栏） | 见 §14 |
| 行 | 按步骤提交，每步独立可回滚 | **W3-1 的步 1/2/3 必须分三次提交** |
| 行（数据模型变更） | 无法廉价回滚 —— 但控制状态是可重建派生数据，回滚 = 清空 namespace + 从 go-web 事实重放 | 这是 W3-1 步 3 的兜底，也是它敢做的根本原因 |

**W3-1 步 3 的前置**：先验证「从空控制存储全量重建」在当前代码上可用（`P2.3_INDEX_REBUILD_E2E=PASS` 已证明），并把它固化为**回滚演练脚本**，而不是只当验收项。

---

## 14. 约束与遗留清单的维护流程

**遗留模块迁移完成时**

1. 文件迁移完成、测试全绿
2. 更新 `DEVELOPMENT_CONVENTIONS.md` **§5 遗留清单与 §7 状态表**
3. 若迁移使某条约定失效（如 W2-4 让 `common/service` 完全 transport 无关、W2-5 消除 `platform` 的模块硬编码），同步删除对应条目，避免文档留下已不成立的约束
4. `mixin-search` 侧若迁移触及依赖方向，同步更新其 `internal/architecture` 规则

**关于自动化约束的现状与门槛**

- **gin-backend 当前没有自动化架构约束**。曾有的规则表 + 精确基线冻结已撤销，理由见约定 §5：文件清单断言会训练团队「顺手更新基线」，且禁止的是文件数变化而非依赖方向错误。
- **若将来恢复自动化，只加方向性断言**：禁止 import X、禁止新增顶层目录、必需路径存在。**不加文件清单快照。**
- **新增任何规则的门槛**——必须同时满足：
  - (a) 对应**已发生**的真实问题，不是假想
  - (b) 有**明确修复方向**，而不只是「禁止」
  - (c) 不对合法写法产生误报
  - (d) 不会因**合法**的结构调整而失败

---

## 15. 建议的执行批次

| 批次 | 内容 | 依赖 | 产出 |
|---|---|---|---|
| **A0** | **阶段 A**：WA-1 工作树入库 + WA-2 `.dockerignore` + WA-3 私钥挂载 + WA-4 release 校验 + WA-5 CI + WA-6 文档矛盾 | 无 | 尾部风险消除 + CI 生效 |
| **A** | W0-1 / W0-2 / W0-7（ADR-012）——纯文档，可与 A0 并行 | 无 | 决策落档 |
| **B** | W1-1 / W1-2 / W1-3 | A0 | `rag` 包从 2 个大文件变 7 个 |
| **C** | W1-4 / W1-5 / W1-6 | A0 | 机械清理收尾 |
| **D** | **W3-3 调用授权**（A 线第一优先） | A（W0-7） | 可信授权边界 |
| **E** | W3-1 步 1（generation 短路） | B | 稳态下消除全量加载 |
| **F** | W3-1 步 2（分片锁） | E | 文档间并行 |
| **G** | W3-2（读路径副作用分离）→ W3-4（蓝绿索引） | F | 读写解耦 + 不中断重建 |
| **H** | W2-1 … W2-6（结构解耦，多数可并行） | B | 包边界与装配统一 |
| **I** | W0-3/4/5/6 决策 → W4（遗留迁移） | 决策 | 遗留清单更新 |

**前两天的具体清单**

```
Day 1 上午   WA-1 工作树拆批入库；跑 verify.ps1 确认既有工作健康
Day 1 下午   WA-2 .dockerignore + WA-4 release 校验 + WA-6 文档矛盾修复
Day 2        WA-5 加 CI（build/vet/test/mixin-search 架构测试/proto/前端 type-check）
Day 2 尾     W0-1/W0-2/W0-7 起草 ADR-012（可与 Day 2 并行）
```

**批次 B 与 C 之间可插入阶段 0 决策**——它们不依赖工作树落地，可与 A 并行推进（纯文档）。

---

## 16. 实施踩坑清单（本次会话实测）

| 坑 | 表现 | 处理 |
|---|---|---|
| `go.work` 不传递 `./...` | 根目录跑 `go test ./...` 不覆盖三个 module | **逐 module 进目录执行** |
| 集成测试默认跳过 | `control_store_integration_test.go` 等静默 skip | 显式设 `CONTROL_STORE_INTEGRATION=1` / `QDRANT_INTEGRATION=1` / `PGVECTOR_INTEGRATION=1` |
| 受限环境下 `GOCACHE` 不可写 | `go build` 报 `Access is denied`（默认缓存在 `%LOCALAPPDATA%\go-build`） | `$env:GOCACHE` 指向工作区内或临时目录 |
| Go telemetry 写用户目录 | 每条命令附带 `error acquiring upload token` 噪音 | `$env:GOTELEMETRY='off'` |
| 架构测试依赖 CWD | 内部用 `os.Getwd()` 相对定位 module 根 | **必须从对应 module 目录运行** |
| `verify.ps1` 很重 | 构建镜像 + 起 8 服务 + 两轮拓扑 | 只在行级/里程碑跑；日常用 `verify-fast.ps1` |
| CRLF 警告噪音 | `git diff --check` 前大量 LF→CRLF warning | 无害，`git diff --check` 本身仍为 0 |
| `cmd/tools` 移动显示为删除+新增 | 新文件未 `git add` 时 git 不识别为重命名 | 提交时一并 `git add`，git 会自动识别 |

---

## 17. 交接阶段（阶段 5）的前置约束

阶段 5（BM25 读取切换）**不在本方案展开**，其时序绑定 py-agent 接入。但有三条前置必须在阶段 3 完成时就位，否则交接会把一个「零可用性代价的派生服务」直接变成生产单点：

1. **降级路径已决策并验证可切换**（W0-4）
2. **控制面已支持双消费者**（W3-1 + W3-2 完成）
3. **多语料契约已设计**（W0-6 相关）

**在三条就位之前，不应启动交接**——这与 `MIXIN_SEARCH_SPLIT_ASSESSMENT.md` §5 的临界点结论一致。
