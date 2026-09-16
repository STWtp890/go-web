# go-web 结构与复用开发约定

> 状态：**生效中**；新增代码必须遵守，存量遗留模块见第 5 节
> 适用范围：`apps/gin-backend`、`apps/mixin-search`
> 强制方式：`apps/mixin-search/internal/architecture` 以依赖测试强制单向边界；gin-backend 侧**当前无自动化约束**（曾有的测试已移除，见第 5 节），靠 review 与本文约定维持
> 上位文档：[ECOSYSTEM_EVOLUTION_GUIDE.md](../ECOSYSTEM_EVOLUTION_GUIDE.md)；目录职责见 [PROJECT_STRUCTURE.md](./PROJECT_STRUCTURE.md)

---

## 0. 为什么需要本文

本文不是风格偏好，而是对三类已实测混乱的收敛方案。

### 0.1 身份解析分散在四层

同一份 JWT claims 存在 **3 种取用方式**、**6 个文件**读取裸字符串键 `"claims"`：

| 方式 | 位置 | 问题 |
|---|---|---|
| `jwt.ExtractClaims(c)` | `modules/chat/handler/common.go:14`、`websocket.go:27` | 返回未类型化 `*MapClaims` |
| `jwt.Subject(c)` / `jwt.SubjectUint(c)` | `modules/document/interfaces/http/handler.go:252`、`modules/manager/handler/request.go:50,94` | 各自解析，无统一类型 |
| `c.Get("claims")` 裸读 | `common/service/jwt/method.go:20`、`modules/auth/handler/logout.go:18`、`modules/manager/handler/logout.go:20`、`middleware/csrf.go:22` | **魔法字符串跨 3 层重复** |

同时两处业务逻辑层**自行解析 token**：

- `modules/auth/logic/refresh.go:24` → `jwt.ParseTokenClaims(oldToken, ...)`
- `modules/manager/logic/refresh.go:28` → 同上

`common/base/constant/const.go` 已定义 `CtxKeyTraceID` / `CtxKeyUserID` / `CtxKeyClientIP`，但 `"claims"` **没有常量**；且 `CtxKeyUserID`、`CtxKeyClientIP` 定义后从未被使用（各仅 1 处引用，即定义本身）。

### 0.2 两个鉴权中间件 95% 重复

`middleware/auth.go`(64 行) 与 `middleware/manager_auth.go`(70 行) 是逐行复制的同一流程，仅 3 处不同：Cookie 名、会话校验函数、是否要求 `role=manager`：

```
提取 Cookie → ParseToken → 断言 MapClaims → 取 sub/sid
→ 校验会话活跃 → c.Set("claims", claims) → Next
```

### 0.3 出入口不统一

| 问题 | 证据 |
|---|---|
| panic 出口绕过统一信封 | `middleware/recovery.go:25` 用 `c.AbortWithStatusJSON(500, gin.H{...})`，与 `responses.Response` 信封不一致 |
| 错误映射分散在 handler | `document/interfaces/http/handler.go:270` 的 `writeApplicationError` switch；`manager/handler/request.go:71,113` 另有两处 switch |
| 分页计算两份且行为不同 | `document/.../handler.go:242-244` 有除零保护；`manager/handler/request.go:38` 无保护 |

> 说明：`responses` 包本身设计良好且被全部 handler 使用（`OK`/`Created`/`OKWithMeta`/`Fail`/`AbortFail`/`FailAppError`）。问题不在缺少统一出口，而在**存在两条旁路**（recovery、模块自建映射）。

---

## 1. 新增代码落位规则

### 1.1 决策树

```
新增代码属于哪个业务能力？
├─ 能明确归属某个业务域 ──────────────→ internal/modules/<domain>/   （第 1.2 节）
├─ 是跨域技术底座（无业务语义） ────────→ internal/common/base/<concern>/
├─ 是 HTTP/传输层横切关注点 ────────────→ internal/platform/httpserver/<concern>/
├─ 是跨应用稳定契约 ────────────────────→ packages/proto/ + packages/gen/（须经生成脚本）
└─ 不确定归属 ──────────────────────────→ 先确定归属；**禁止**放入 common 或新建 utils
```

### 1.2 新业务域的标准形态

**Tier A（完整形态）** —— 域内存在聚合不变量、多个聚合或跨聚合用例时使用：

```text
internal/modules/<domain>/
├── domain/                    # 聚合、值对象、领域错误、命令/查询模型、端口
│   ├── model.go               # 聚合与值对象（仅 stdlib）
│   ├── errors.go              # 领域哨兵错误
│   ├── repository.go          # 按聚合拆分的仓储端口（禁止单一宽接口）
│   └── <port>.go              # 其他出站端口（每端口一文件）
├── application/               # 用例编排：仅依赖本域 domain + stdlib
│   └── <usecase>_service.go
├── interfaces/
│   └── http/                  # Gin 适配器：DTO、绑定、错误映射、路由注册
│       ├── handler.go
│       └── errors.go          # 本域唯一的 哨兵错误 → AppError 映射
└── infrastructure/            # 端口实现：仅依赖本域 domain + 驱动/SDK
    ├── postgresql/
    ├── cache/
    └── <remote>/              # 跨服务 RPC 客户端适配器
```

**Tier B（精简形态）** —— 纯委托型 CRUD、无领域不变量时使用：

```text
internal/modules/<domain>/
├── domain/                    # 模型 + 端口 + 领域错误
├── interfaces/http/
└── infrastructure/
```

Tier B **不建 `application/`**，`interfaces/http` 可直接调用 `domain` 端口。一旦出现「一个用例需要编排 3 个以上端口调用」或「跨聚合事务」，必须升级为 Tier A。

> 现状参照：`modules/document` 是 Tier A 的已验证样板 —— 其 `domain` 包**零内部 import**，`application` **只依赖 `domain`**，三个 `infrastructure/*` 适配器**各自只依赖 `domain`**，均由 `go list` 编译级依赖图确认。

### 1.3 新组件落位表

| 组件类型 | 落位 | 约束 |
|---|---|---|
| 领域模型 / 值对象 | `modules/<d>/domain/` | 仅 stdlib；禁止 Gin/GORM/RPC |
| 用例编排 | `modules/<d>/application/` | 仅本域 `domain` + stdlib |
| 出站端口 | `modules/<d>/domain/<port>.go` | 按能力拆分的小接口，单文件单端口 |
| 端口实现 | `modules/<d>/infrastructure/<tech>/` | 仅本域 `domain` + 驱动 |
| HTTP 适配器 | `modules/<d>/interfaces/http/` | 可依赖本域 `application`/`domain` + `platform` 契约 |
| 跨服务 RPC 契约 | `packages/proto` → `packages/gen` | 必须经生成脚本，禁止手改生成物 |
| 跨服务 RPC 客户端 | `modules/<d>/infrastructure/<remote>/` | 作为端口实现，禁止在 `application` 内直接调用 |
| 技术底座（日志/连接/缓存/错误码/响应信封） | `internal/common/base/<concern>/` | **无业务语义**；不得依赖任何 `modules/*` |
| 认证会话服务（token 签发与校验、会话存储、会话事件） | `internal/common/service/<concern>/` | 不得依赖任何 `modules/*`；原则上不得感知 Gin。迁移期仅 `sessioncookie/cookie.go` 为已冻结例外，禁止新增，迁移完成后删除该例外 |
| HTTP 横切（中间件、路由装配、身份上下文） | `internal/platform/httpserver/` | 不承载业务规则；不得直接 import `modules/*/api` |
| 可执行架构约束 | `apps/mixin-search/internal/architecture/` | 仅测试，无生产代码；gin-backend 侧当前无此类测试（见第 5 节） |
| 运维/评估/调试入口 | `cmd/<name>/` | 见 1.4 |

### 1.4 禁止事项

1. **禁止**新建 `internal/utils`、`internal/helper`、`internal/common/utils` 等无归属包。工具函数归属其唯一使用方所在的域；确被多域稳定复用时，提取为 `common/base/<具体能力>` 并在本文登记。
2. **禁止**在 `domain` 层出现 Gin、GORM、`net/http`、任何 RPC/Protobuf 类型。
3. **禁止**在 `interfaces/http` 中出现 `gorm.io/*` 或具体仓储实现 import。
4. **禁止**业务模块依赖 `internal/app`（组合根）。
5. **禁止**在 `platform/httpserver` 中硬编码 `modules/*/api` 路由注册 —— 一律经组合根注入 `RouteRegistrar`。
6. **禁止**在 `cmd/*` 中承载业务规则：`cmd` 只做参数解析、组合根调用、生命周期。
7. **禁止**测试工具与产品入口混放：`runtimeapitest`、`pemgenerator` 这类非产品二进制必须放在 `cmd/tools/<name>/`（已完成，见第 7 节）。

---

## 2. 依赖方向规则

| 位置 | 可以依赖 | 禁止依赖 |
|---|---|---|
| `modules/<d>/domain` | Go 标准库 | 一切 `gin-backend/internal/*`、Gin、GORM、Protobuf |
| `modules/<d>/application` | 本域 `domain`、标准库 | Gin、GORM、具体数据库、`interfaces`、`infrastructure`、组合根 |
| `modules/<d>/interfaces` | 本域 `application`/`domain`、`platform/httpserver` 契约、`common/base/responses`、`common/base/errors` | 具体仓储实现、旧 `model/*` |
| `modules/<d>/infrastructure` | 本域 `domain`、驱动/SDK | `application`、`interfaces`、组合根 |
| `platform/httpserver` | Gin、`middleware`、`RouteRegistrar` 接口、`common/base/*` | 领域规则、具体仓储、`modules/*/api` |
| `common/base/*` | 标准库、驱动 | 任何 `modules/*` |
| `common/service/*` | 标准库、驱动、`common/base/*`；迁移期仅 `sessioncookie/cookie.go` 可依赖 Gin | 任何 `modules/*`；除该冻结例外外的 Gin 依赖 |
| `app` | 各层公开构造函数 | 领域规则、持久化细节 |

**mixin-search 附加规则**：

- `internal/rag` **禁止** import `internal/transport/*` 与 `packages/gen/*`（Protobuf 类型不得进入核心包）。
- `internal/transport/*` 只做协议转换与错误码映射，**禁止**承载业务语义。
- 依赖方向固定为 `document_pipeline → internal/rag → internal/transport/* → cmd/*`，禁止反向与环。

---

## 3. 身份与上下文契约

这是消除第 0.1、0.2 节混乱的核心机制。

### 3.1 唯一身份类型

中间件解析后注入 `platform/httpserver/identity.Principal`，业务层只读取该类型：

```go
package identity

type Kind string
const (
    KindUser    Kind = "user"
    KindManager Kind = "manager"
)

// Principal 是中间件解析并校验后的可信身份。
// 只有持有者能构造，业务层无法伪造。
type Principal struct {
    Kind      Kind
    Subject   string // JWT sub 原文
    UserID    int64  // Subject 的数值形式；无法解析时为 0
    SessionID string // sid
}
```

### 3.2 取用方式（唯一）

| 场景 | API |
|---|---|
| 中间件注入 | `identity.Set(c, principal)` |
| HTTP handler 取用 | `identity.FromGin(c) (Principal, bool)` |
| 需要用户 ID | `identity.UserID(c) (int64, bool)` |
| 需要会话 ID | `identity.SessionID(c) (string, bool)` |
| 非 Gin 层传递 | `identity.With(ctx, p)` / `identity.From(ctx)` |

上下文键使用**私有类型**（`type ctxKey struct{}`），禁止字符串键。

### 3.3 鉴权中间件唯一实现

两个中间件合并为一个工厂，差异只经选项表达：

```go
// platform/httpserver/middleware/auth.go
func AuthRequired(opts ...AuthOption) gin.HandlerFunc          // 用户面
func ManagerAuthRequired(opts ...AuthOption) gin.HandlerFunc  // 管理面
```

共同流程（单一实现，不再复制）：提取 Cookie → `jwt.ParseToken` → 断言 claims → 取 sub/sid → 校验会话活跃 → 构造 `Principal` → `identity.Set` → `Next`。

### 3.4 禁止事项

1. **禁止**业务层（`application` / `domain` / `logic`）解析或验签 token。token 验签只允许出现在三处：`common/service/jwt`（token 服务）、`common/service/sessioncookie`（凭据解析）、`platform/httpserver`（中间件）。
2. **禁止**读取裸字符串键 `c.Get("claims")`。该键在迁移完成后不再写入。
3. **禁止**新增 `jwt.ExtractClaims` / `jwt.Subject` / `jwt.SubjectUint` 的调用；这三个函数已从 `common/service/jwt` 删除。
4. `common/service/jwt` **禁止** import gin：gin 相关的取用封装一律放 `platform/httpserver/identity`。
5. 刷新流程必须经 `sessioncookie.ResolveUserRefresh` / `ResolveManagerRefresh` 一次取得 `RefreshCredential`（含类型化 `jwt.RefreshIdentity`），**禁止**由 handler 或 logic 自行解析 refresh token。
6. 凭据签发结果必须使用 `jwt.TokenPair` 等具名类型，**禁止**用 `map[string]string` 传递 token（键名拼写错误会静默变成空字符串）。

---

## 4. HTTP 出入口契约

### 4.1 唯一出口

所有 HTTP 响应必须经 `common/base/responses`：

| 场景 | 函数 |
|---|---|
| 成功 200 | `responses.OK(c, data)` |
| 成功 201 | `responses.Created(c, data)` |
| 成功带分页 | `responses.OKWithMeta(c, data, meta)` |
| 失败 | `responses.Fail(c, status, code, message)` |
| 中间件失败（必须终止链） | `responses.AbortFail(c, status, code, message)` |
| 由 error 推导 | `responses.FailAppError(c, err)` |

**禁止**在 `responses` 包之外直接调用 `c.JSON` / `c.AbortWithStatusJSON` / `c.String`。当前已无违例（`middleware/recovery.go` 已在 C8 修正）。

### 4.2 错误映射的唯一位置

- 领域层：定义哨兵错误（`var ErrXxx = errors.New(...)`）。
- 应用层：把哨兵错误翻译为业务语义错误。
- **接口层**：每个域恰有一个 `<domain>/interfaces/http/errors.go`，把本域错误映射为 `*apperrors.AppError`；handler 内**只调用 `responses.FailAppError`**。
- **禁止**在 handler 函数体内出现 `switch { case errors.Is(err, ...) }` 形式的映射。

### 4.3 分页统一

分页元信息只经 `responses.NewPageMeta(page, perPage, total)` 构造，内部统一处理 `perPage <= 0` 与 `total = 0`。**禁止**在 handler 内手写 `totalPages` 除法。

---

## 5. 存量模块：基线冻结

`modules/auth`、`modules/manager`、`modules/chat`、`internal/model` 采用 `api/handler/logic/types` 遗留布局。**不要求立即重写**，但按以下约定冻结：

1. 遗留目录内**禁止新增生产文件**。允许补充 `_test.go` characterization 测试；新产品功能必须新建 Tier A/B 模块。
2. 遗留模块内的 Bug 修复允许，但必须登记在第 7 节迁移待办中。
3. `internal/model` 与 `internal/common` 不接收新领域逻辑。
4. 每个遗留模块迁移完成时，在本文更新状态表与遗留清单。
5. 遗留模块的生产文件清单记录在 [PROJECT_STRUCTURE.md](./PROJECT_STRUCTURE.md)，并在 code review 中核对；**不设精确清单自动化测试**。

> **为什么不设「精确清单冻结」**：gin-backend 曾以 `internal/architecture` 记录 5 组共 70 个遗留文件的精确基线并做相等断言。该方式**已移除**，原因有二：
> 1. 任何**合法**改动（迁移一个文件、加一个测试）都要同步改测试，长期会训练团队「顺手更新基线」，使信号衰减；
> 2. 它禁止的是「文件数变化」，而不是「依赖方向错误」——真正要防的是后者。
>
> 约束改由 review 与本节约定维持。若将来恢复自动化，应优先采用**方向性断言**（禁止 import X、禁止新增顶层目录、必需路径存在），而非文件清单快照。

**`modules/chat` 与 `modules/aiagent`（合计 39 文件 / 3,211 行）已确认生产入口不可达**（对全部 6 个入口求 `go list -deps` 并集验证）。在给出产品去留结论前同样适用本节冻结约定；结论为保留时才纳入迁移计划。

---

## 6. mixin-search 约定

1. 依赖方向见第 2 节，且由 `internal/architecture` 的依赖测试强制（C10 已落地）。
2. `internal/rag` 内部按**同包多文件**拆分职责，**不改变包边界**（低风险、零依赖图影响）：
   - 目标拆分：`contract.go`(1091) → `service.go` + `model.go` + `authorization.go` + `identity.go`；`control_store.go`(572) → `control_model.go` + `control_validate.go` + `control_io.go`。
3. 当且仅当出现真实复用或循环风险时，才把具体后端抽为子包；抽取方向依据真实依赖图决定，**禁止**预设横向 `model/service/store` 切分。
4. `document_pipeline` 的公开范围待产品确认；确认前**禁止**扩大其导出 API。
5. 测试文件按**行为**命名，禁止使用阶段号（如 `p2_2_test.go`）。

---

## 7. 落地状态与待办

| 编号 | 事项 | 状态 |
|---|---|---|
| C1 | 本文档建立并纳入 `docs/README.md` 索引 | 完成 |
| C2 | `identity.Principal` + 取用 API | **完成** |
| C3 | 合并 `AuthRequired` / `ManagerAuthRequired` 为单一实现 | **完成**（`manager_auth.go` 已删除） |
| C4 | 迁移 5 处裸 `c.Get("claims")` 与 4 处 `jwt.Subject*` 调用 | **完成**（现存 0 处） |
| C5 | 统一 `application`/`logic` 层 token 解析（refresh 流程） | **完成** |
| C6 | `responses.NewPageMeta` 统一分页 | **完成** |
| C7 | 各域 `interfaces/http/errors.go` 唯一错误映射 | **完成** |
| C8 | `middleware/recovery.go` 改走 `responses.AbortFail` | **完成** |
| C9 | `internal/architecture` 升级为规则表 + 遗留基线冻结 | **已撤销**（实施后移除，不采用精确清单冻结；见第 5 节与 7.3） |
| C10 | mixin-search 架构测试 | **完成** |
| C11 | `common/service/jwt` 去除 gin 依赖 | **完成** |
| C12 | `cmd/tools/` 归置非产品二进制 | **完成** |
| C13 | `chat`/`aiagent` 产品去留决策 | **待产品决策** |

### 7.1 C2–C4、C11 实测结果

`go build ./...` / `go vet ./...` / `go test ./...` 全部通过（`apps/gin-backend`）。当时 `go test ./internal/architecture` 的 5 项约束亦全部 PASS；该测试此后已移除（见 C9）。

| 验证项 | 结果 |
|---|---|
| 剩余裸 `"claims"` 字符串键 | **0 处**（原 6 处） |
| 剩余 `jwt.ExtractClaims` / `jwt.Subject` / `jwt.SubjectUint` 调用 | **0 处**（原 7 处，函数已删除） |
| `common/service/jwt` 的 gin 依赖 | **已移除** |
| 鉴权实现 | 1 个共享 `authenticate(policy)` + 2 个薄入口（原 2 份 95% 重复实现） |
| `identity` 取用点 | 17 处，覆盖 auth / manager / document / chat 四个模块 |

`auth/logic` 与 `manager/logic` 的 `LogoutLogic` 签名由 `(ctx, jwtlib.MapClaims)` 改为 `(ctx, subject, sessionID string)`，**logic 层不再依赖 JWT 库类型**。

### 7.2 C5–C8 实测结果

同样以 `go build ./...` / `go vet ./...` / `go test ./...` 全部通过验证。

| 验证项 | 结果 |
|---|---|
| token 验签发生位置 | 仅 `common/service/jwt`(2 文件)、`common/service/sessioncookie`、`platform/httpserver/middleware` —— **logic / application / domain 中 0 处** |
| `*map[string]string` 形式的 token 返回 | **0 处**（改为 `jwt.TokenPair{AccessToken, RefreshToken}`） |
| `ValidateRefreshCSRF` | 已移除，由 `ResolveUserRefresh` / `ResolveManagerRefresh` 取代 |
| `totalPages` 手写除法 | **仅存在于 `responses.NewPageMeta` 内部**（原 2 份实现合一） |
| `responses` 包外的裸 gin 出口 | **0 处**（原 `recovery.go` 1 处） |
| HTTP 错误映射 switch | 收敛到 3 个 `errors.go`：`auth/handler/errors.go`、`manager/handler/errors.go`、`document/interfaces/http/errors.go` |

行为兼容性说明：重构前，**格式非法或过期的 refresh token 会因 `ValidateRefreshCSRF` 先解析失败而返回 403**；重构后返回 401。`cmd/tools/runtimeapitest` 中全部 refresh 失败断言（L404/L410/L805/L836）期望的均为 401，因此该变化与既有契约一致，且语义更正确（401 = 未认证）。

### 7.3 C9–C10 实施结果

**C9 已撤销。** gin-backend 的 `internal/architecture`（8 条依赖规则 + 5 组遗留文件基线 + 2 条 import 冻结）在实施后被**移除**——不采用「精确文件清单冻结」方式，理由见第 5 节。以下条目仅作历史记录：

- ~~gin-backend 架构测试改为规则表，覆盖 document 四层正向依赖、业务模块禁止依赖组合根、`common/base`/`common/service` 禁止依赖业务模块，以及 `common/service/jwt` 禁止依赖 Gin。~~
- ~~`common/service/sessioncookie/cookie.go` 作为唯一依赖 Gin 的 service 文件被精确冻结；新增 Gin 依赖会失败。~~（该事实仍成立，但**不再由测试强制**；迁移目标见第 5 节）
- ~~auth、manager、chat、aiagent 与 `internal/model` 的生产 Go 文件清单被冻结。~~（冻结约定保留在本节与第 5 节，**不再由测试强制**）
- ~~`platform/httpserver` 的 auth/api 与 manager/api 两条遗留业务 import 被精确冻结。~~（同上；统一路由注入仍是待办）

**C10 保持生效**：mixin-search 新增 `internal/architecture`，强制 `document_pipeline → internal/rag → internal/transport` 的单向边界，并禁止 Protobuf 生成类型进入 rag 核心包。

### 7.4 C12 实施结果

- `pemgenerator` 与 `runtimeapitest` 已迁移至 `cmd/tools/`，与 `server`、索引 Worker/Admin 和评估入口等产品进程分离。
- 根验收脚本、应用 README 与结构文档已统一使用新路径。曾用于「要求新目录存在并禁止旧目录恢复」的架构测试已随 C9 一并移除（见 7.3），当前靠 review 维持。



---

## 8. 验收方式

在 `apps/gin-backend` 执行：

```powershell
go build ./...
go test ./...
go vet ./...
```

在 `apps/mixin-search` 执行：

```powershell
go test ./internal/architecture   # 结构与依赖边界
go build ./...
go test ./...
go vet ./...
```

涉及 HTTP 行为或认证的改动，还必须从仓库根目录执行 `./deployments/verify.ps1`（其 `cmd/tools/runtimeapitest` 覆盖 auth/manager/document 共 95 项断言，并在 mixin-search 正常与停机两种拓扑下各跑一轮）。
