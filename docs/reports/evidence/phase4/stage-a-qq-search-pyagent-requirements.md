# Stage A / qq-search 对 py-agent 的接口事实清单

> 日期：2026-09-30
> 负责人：Subagent D（teammate `svc-qq`），共享任务 `task-3` 第二项交付
> 范围：`apps/qq-search`（Go 侧）**当前实际实现**的边界事实
> 阅读约定：§1–§8 都是已经在代码中实现、并在本次验证中实际执行过的行为（除逐条注明"未验证"者）；**§9 是未实现、未定或依赖他人的部分，不得当作可直接接入的能力**。py-agent 本体不在本仓库，其服务端实现本次未验证。
> 证据：`docs/reports/evidence/phase4/stage-a-qq-search.md`

## 0. 角色、方向与调用边（实现事实）

| 项 | 事实 |
| --- | --- |
| py-agent 的角色 | QQ 原始事实（消息、文件、会话、撤回）的唯一写入方；QQ 渠道范围的判定与 capability 签发方 |
| qq-search 的角色 | `qqsource.v1` 事件的消费者（`IndexQQSourceEvent` 推送）；`qqsearch.v1.QQSearchService` 的实现方 |
| 允许的调用方向 | py-agent → qq-search（同步推送 + 同步查询）。**反向禁止**：qq-search 在查询与重建路径上没有 py-agent 客户端、没有出站连接配置，由 `internal/architecture/source_client_test.go` 的结构断言与集成测试固定 |
| 本仓库是否有 py-agent 实现 | 没有。`apps/` 下只有 document-service、document-search、gin-backend、mixin-search、qq-search、simple-frontend；py-agent 侧的服务实现本次**未验证** |

## 1. 实际实现的 RPC 与 scope

来源：`apps/qq-search/internal/interfaces/grpcapi/api.go` 的 `methodScopes`；由 `TestMethodScopesCoverEveryRPCExactlyOnce` 与生成的 RPC 描述符逐条比对。

| RPC | 所需 scope | 额外要求 `x-resource-capability` | 说明 |
| --- | --- | --- | --- |
| `/qqsearch.v1.QQSearchService/IndexQQSourceEvent` | `qq-index-writer` | 否 | 单条事件推送，幂等 |
| `/qqsearch.v1.QQSearchService/SearchQQMessages` | `qq-searcher` | **是** | 消息检索 |
| `/qqsearch.v1.QQSearchService/SearchQQFiles` | `qq-searcher` | **是** | 文件检索 |
| `/qqsearch.v1.QQSearchService/GetQQRecordState` | `qq-searcher` | **是**（本次新增行为） | 单条记录状态；范围外与不存在同形 |
| `/qqsearch.v1.QQSearchService/RebuildIndex` | `qq-index-writer` | 否 | `confirm=true` 必填，否则 `FAILED_PRECONDITION` |
| `/qqsearch.v1.QQSearchService/GetIndexStatus` | `qq-index-writer` | 否 | 两个集合身份与消费进度 |

- 未在策略表登记的 RPC 一律 `PERMISSION_DENIED`（失败关闭）。
- `audience` 固定为 `qq-search`；断言与 capability 的 audience 不符 → `UNAUTHENTICATED`。
- 调用方允许列表（`auth.allowed_callers`）默认 `py-agent` + `document-service`；配置只能填已登记调用方（`py-agent` / `document-service` / `go-web`）。**`go-web` 不在默认列表中**。
- 只有 `grpc.health.v1.Health/Check` 与 `/Watch` 匿名；其余无匿名入口。

## 2. 连接方式与健康检查（实现事实）

| 项 | 事实 |
| --- | --- |
| 传输 | 明文 gRPC（当前无 TLS/mTLS），仅绑定回环地址 |
| 默认 gRPC 地址 | `127.0.0.1:18083`（`QQ_SEARCH_GRPC_LISTEN_ADDRESS`） |
| 默认 HTTP 探针地址 | `127.0.0.1:18093`（`QQ_SEARCH_HTTP_LISTEN_ADDRESS`） |
| `GET /healthz` | 200 `{"status":"ok","service":"qq-search"}`，只表示进程存活，不查数据库 |
| `GET /readyz` | 数据库 ping（3s 超时）：成功 200 `{"status":"ready",...}`；失败 503 `{"status":"unavailable","reason":"database"}` |
| `GET /info` | 返回 service / audience / schema / message_collection / file_collection |
| gRPC 健康服务 | 启动时 `NOT_SERVING`；`WaitReady`（最长 30s、每 250ms ping 数据库）成功后置 `SERVING`；关闭时回到 `NOT_SERVING` |
| 容器探针 | `cmd/qq-search-healthcheck`：`GET /readyz`，默认 `127.0.0.1:18093`，`-timeout 2s`，非 200 退出码 1 |
| 客户端连接管理 | 服务端没有提供客户端库、服务发现或负载均衡配置；py-agent 需自备连接与重连策略 |

## 3. 超时与错误映射（实现事实）

- **服务端没有 per-RPC deadline、超时、重试或退避配置**（未实现，见 §9）。调用方必须自己设置 deadline 与重试策略。
- 凭据失败映射（`packages/serviceauth` 契约 + 实现）：

| 场景 | gRPC 状态 | 调用方处理 |
| --- | --- | --- |
| 缺失 / 格式错误 / 签名无效 / 过期 / audience 不符 | `UNAUTHENTICATED` | 不可重试（先修凭据） |
| 缺所需 scope / 命名空间越界 / 请求范围越界 | `PERMISSION_DENIED` | 不可重试（先改授权范围） |
| 服务自身配置无效（密钥缺失等） | `INTERNAL` | 不是调用方错误 |

- 业务错误码（`apps/qq-search` 实际返回）：

| 场景 | 状态 | 是否可重试 |
| --- | --- | --- |
| producer 数据非法（未知 kind、必填缺失、超长、时间戳非 RFC3339、负数 revision/sequence/size） | `INVALID_ARGUMENT` | 否，需修数据 |
| 同 revision 不同载荷；`record_id` 已绑定其他会话/Bot | `ALREADY_EXISTS` | 否，需 producer 修正 |
| 事务 begin/commit 失败、数据库暂不可用 | `UNAVAILABLE` | 是（同一 `event_id` 重投安全） |
| 数据库语句失败、连接池不可用、索引集合基线缺失 | `INTERNAL` | 取决于原因 |
| `RebuildIndex` 未带 `confirm=true` | `FAILED_PRECONDITION` | 否 |
| 检索/状态查询无命中 | 不是错误：空 hits / `exists=false` | — |

## 4. 身份断言与 capability 的字段与校验规则（实现事实）

### 4.1 传输位置

| Header | 内容 | 作用 |
| --- | --- | --- |
| `authorization: Bearer <assertion>` | 服务身份断言 | 谁在调用 |
| `x-resource-capability: Bearer <capability>` | 资源范围 capability | 已被授予什么；**查询类 RPC 必须恰好一个值** |

两者分离是刻意的：检索服务只依据 capability 授权，不接受调用方自述范围。

### 4.2 服务身份断言 payload（`serviceauth.AssertionClaims`）

字段：`version`(=1)、`caller`、`audience`、`scopes[]`、`subject_key?`、`conversation?{kind,external_group_id}`、`actor?`、`request_id?`、`channel?`、`bot_id?`、`external_user_id?`、`external_group_id?`、`issued_at`、`expires_at`。

校验（任一失败即拒绝）：

1. 签名 HMAC-SHA256（常量时间比较），信封 `base64url(payload).base64url(mac)`，无算法头；
2. JSON **未知字段整体拒绝**（`DisallowUnknownFields`）；
3. `version == 1`；
4. `caller` 已登记且在服务允许列表内；
5. `audience == "qq-search"`；
6. 每个 scope 已在共享 registry 登记；
7. `subject_key` 非空时其 origin 段必须等于调用方命名空间（`py-agent` → `qq`）；
8. `expires_at > issued_at`，且 `now ≤ expires_at + 30s`、`issued_at ≤ now + 30s`。

### 4.3 资源范围 capability payload（`serviceauth.CapabilityClaims`）

qq-search **实际读取**的字段：`version`、`issuer`、`audience`、`scopes[]`、`subject_key`、`bot_ids[]`、`conversation_ids[]`、`external_group_ids[]`、`issued_at`、`expires_at`。
`private_space_ids` / `current_team_space_id` / `other_team_space_ids` / `allowed_space_ids` / `allowed_document_ids` / `authenticated_public` 属于文档范围，qq-search **不读、不判定**。

校验：

1. 签名、无未知字段、`version == 1`（同上）；
2. `issuer` 是已登记身份（注意：见 §9.3，**未强制必须是 `py-agent`**）；
3. `audience == "qq-search"`；
4. 每个 scope 已登记；查询类 RPC 必须含 `qq-searcher`；
5. 时间窗口同上（+30s 时钟容忍）；服务端不限制有效期上限，契约默认 2m 由签发方给出；
6. 三族标识统一规范化：去空白、去重、排序后按集合比较（顺序与重复不改变判定）。

### 4.4 范围规则（只允许缩小）

- 请求 `scope` 的每个家族独立判定：**留空 = 使用授予范围在该家族的全部**；非空 = 必须是授予集合的子集。
- 出现任何未授予标识 → **整个请求 `PERMISSION_DENIED`**，不静默裁剪（裁剪会让调用方探测无权读取的标识）。
- 三族全部为空的授予范围 = "没有会话"：检索返回空、记录状态返回 `exists=false`，**绝不退化为全部会话**。
- 同一套规则同时作用于 `SearchQQMessages`、`SearchQQFiles`、`GetQQRecordState`（本次修复后一致）。

## 5. QQ 事件种类与记录 / 事件 / 修订号规则（实现事实）

### 5.1 事件种类（`IndexQQSourceEvent.kind` 字符串）

| kind | 载荷 | 说明 |
| --- | --- | --- |
| `message_upsert` | message_text / sender_external_user_id / sent_at / platform_sequence | 消息写入或更新 |
| `file_upsert` | file_name / mime_type / size_bytes / content_sha256 / storage_ref / uploader_external_user_id / uploaded_at | 文件写入或更新 |
| `message_recalled` | recalled_at / operator_external_user_id | 撤回消息（行保留） |
| `file_recalled` | recalled_at / operator_external_user_id | 撤回文件（行保留） |

**没有 delete / deleted 种类**：撤回是追加事实，原始行永不删除。

### 5.2 身份字段（服务端强校验，producer 必须满足）

| 字段 | 规则 |
| --- | --- |
| `channel` | 必须 `qq`（trim + 小写后比较） |
| `bot_id` | 非空，≤64 字符 |
| `conversation_kind` | `private` 或 `group` |
| `conversation_id` | 必须形如 `qq:<bot_id>:<private\|group>:<local_id>`：4 段、第 1 段 `qq`、第 2 段 == `bot_id`、第 3 段 == `conversation_kind`、第 4 段非空；≤192 字符。服务端**原样使用**该字符串，不重新推导 |
| `external_group_id` | group 会话必填且必须等于 `conversation_id` 第 4 段（≤64）；private 会话必须为空 |
| `external_user_id` | private 会话非空时必须等于第 4 段（≤64） |
| `record_id` | 非空，≤192 字符；**是两张表各自的主键，因此在同一记录类型内必须跨 Bot 唯一**；被别的会话/Bot 声明同一 id → `ALREADY_EXISTS` |
| 其他列限制 | `sender_external_user_id` / `uploader_external_user_id` ≤64；`file_name` ≤512；`mime_type` ≤128；`storage_ref` ≤512；`content_sha256` ≤64；`size_bytes ≥ 0`；`platform_sequence ≥ 0` |
| 字段分离 | 消息发送者只从 `sender_external_user_id` 读，文件上传者只从 `uploader_external_user_id` 读；服务端绝不用 `external_user_id` 或 `sender_*` 去填 uploader |

### 5.3 事件号、修订号、序号、时间

| 字段 | 规则 |
| --- | --- |
| `event_id` | 非空、≤256 字符、对 qq-search 不透明（不必是 UUID）。服务端用固定命名空间 + SHA-256 派生自己的账本 uuid（RFC4122 v5 布局），因此同一 `event_id` 永远映射到同一账本行。py-agent 侧实际换算（`internal/ingress/pyagent/converter.go` 记录并在测试中使用）：消息 = `hex(sha256("<conversation_id>:message:<message_id>"))`；文件 = `hex(sha256("file:<conversation_id>:<record_id>:<revision>"))`；撤回 = `hex(sha256("recall:<message\|file>:<conversation_id>:<record_id>:<revision>"))` |
| `record_revision` | 必填，`1..MaxInt64`（0 或负值 `INVALID_ARGUMENT`）；同一 `(record_kind, record_id)` 只有**严格更大**的修订号才会被应用；账本唯一约束 `(record_kind, record_id, record_revision)` |
| `sequence` | ≥0，消费者游标提示；本地游标只前进（`GREATEST`），乱序到达不会回退 |
| `occurred_at` / `sent_at` / `uploaded_at` / `recalled_at` | RFC3339（可含纳秒）字符串，空 = 未提供，非法 = `INVALID_ARGUMENT`；upsert 的 `sent_at` / `uploaded_at` 用 `COALESCE` 写入（省略不抹掉已有值）；撤回时间空则回退 `occurred_at`，再回退写入时刻 |

## 6. 重复 / 乱序 / 冲突 / 重试行为（实现事实 + 集成测试）

| 场景 | 结果 |
| --- | --- |
| 同一 `event_id` 重投（含并发重投） | `applied=false`，`reason="duplicate"`，写路径不重复 |
| 同一 revision、同一载荷、不同 `event_id` | `applied=false`，`reason="duplicate_revision"`（幂等回放） |
| 到达的 revision 小于已应用修订号 | `applied=false`，`reason="stale_revision"`（低修订 upsert 不会复活已撤回记录） |
| 同一 revision 但载荷不同 | `ALREADY_EXISTS`，绝不覆盖 |
| `record_id` 已绑定另一个会话/Bot | `ALREADY_EXISTS` |
| 更高 revision 的 upsert | 应用，并清除撤回状态（更高修订 = 更新的来源事实） |
| 撤回先于对应 upsert 到达 | 先建 `recalled` 行（无内容）；后续低修订 upsert 被账本栅栏挡住 |
| 无 delete 种类 | 撤回不可逆 |

重试建议（由上述实现推导，服务端不代办）：

- `UNAVAILABLE` → 可用同一 `event_id` 重投；
- `ALREADY_EXISTS` → 重试无用，需 producer 修正 revision 或 record_id 归属；
- `UNAUTHENTICATED` / `PERMISSION_DENIED` → 不可重试；
- 同一记录的事件按记录串行化（服务端使用记录级 advisory lock），跨记录可并发。

## 7. Bot 与会话范围要求（py-agent 的义务）

- 渠道权限判定只属于 py-agent；qq-search 不做渠道判定，只执行"请求 ⊆ capability"。
- capability 的三族标识应表达"该主体当前会话可访问的 Bot / 会话 / 外部群"；**留空的家族 = 不限制该家族**，因此只签发 `conversation_ids` 而不签发 `bot_ids` 时，授权完全由会话 id 决定。
- 查询方（py-agent）应先用请求中的会话上下文裁剪 capability，再发起查询；qq-search 侧再次执行同一包含规则。
- 检索结果只包含 `status='indexed'` 的行；撤回记录不出现在检索里，但可在授权范围内通过记录状态查询看到 `RECALLED`。
- 记录状态查询的完整行为（本次修复后）：
  - 范围内存在 → `exists=true` + 状态（`INDEXED` / `RECALLED`）；
  - 范围外、记录不存在、`kind` 与实际模型不匹配 → 全部 `exists=false` 且无 `state`，三者同形；
  - 空授权范围 → `exists=false`（不访问数据库）；
  - 请求显式指名越界标识 → `PERMISSION_DENIED`。

## 8. 重建来源接口与分页 / 游标规则（事实）

- **qq-search 不实现流式拉取**：没有 `ListQQSourceEvents` 客户端、没有 py-agent 地址配置、没有使用 `after_sequence` / `batch_size` / `follow`。`packages/proto/qqsource/v1` 里的 `ListQQSourceEvents` 是 py-agent 侧的服务定义，qq-search 当前**不消费**。
- `RebuildIndex(confirm=true)` 只用本地数据：`qq_search.qq_applied_events` 账本 + 本地行；按 `(record_kind, record_id)` 取最新事件重派生 `status` / `record_revision`；撤回在最新修订时永远胜出（含 2026-09-30 加入的并发修订号栅栏，见证据 §5）；`records_failed` 统计"有行无账本"和"有账本无行"两类不可恢复记录。
- 游标：本地 `qq_search.consumer_state.stream='qq-source-events'.last_sequence` 由推送事件的 `sequence` 推进，只增不减；`GetIndexStatus` 会把它作为 `last_sequence` 返回（这是 qq-search 侧唯一暴露该游标的接口，需要 `qq-index-writer`）。**py-agent 侧如何用该游标续传未定义**（见 §9.1）。
- 批量：配置项 `index.max_batch_size`（默认 500）只做配置校验，**没有任何代码使用**（未实现）。
- 失败处理：单条事件失败不影响其他事件；重建是"两个语料同一事务，要么都重建要么都不重建"；重建会写 `qq_search.rebuild_runs` 行（`running` → `succeeded`/`failed`）。

## 9. 已知限制、未实现与依赖（**不得当作可直接接入的能力**）

1. **未实现 py-agent 事件流的拉取/分页/续传**：`ListQQSourceEvents`、`after_sequence` 语义、`batch_size`、`follow` 在 qq-search 侧全部未实现；当前只有 push 入口 `IndexQQSourceEvent`。若 py-agent 要实现该流，游标含义、批量大小、断线续传与重复投递语义需要单独定义并落到契约。
2. **未实现服务端超时/重试/退避/限流**：没有 per-RPC deadline，也没有按主体或会话的配额（`CURRENT_IMPLEMENTATION_PLAN.md` §0.8 已登记）。
3. **capability 的 issuer 未限定为 `py-agent`**：`packages/serviceauth.OpenCapability` 只要求 issuer 是已登记身份；qq-search 未额外校验"QQ 渠道 capability 必须由 py-agent 签发"。当前只要持有共享边界密钥，其他已登记身份也能签发 audience=`qq-search`、scope=`qq-searcher` 的 capability。这是契约（§3.2）与实现的差异，修复需要改 `packages/serviceauth` 或 qq-search 边界，本任务未做。
4. **capability 的 `subject_key` 未强制非空**：契约 §4 第 10 步要求查询类 RPC 的 capability `subject_key` 非空，实现未强制；也没有校验 QQ 主体键的规范形态（只比较 origin 段 `qq`，`qq:10001:user:20003` 与 `QQSubjectKey()` 产出的 `qq:user:10001/20003` 都会被接受）。主体键的确切格式属未定项，需要 py-agent 与契约固定。
5. **capability 有效期上限未强制**：服务端只校验 `+30s` 时钟容忍窗口，签发方给多长就接受多长（契约默认 2m）。
6. **共享边界密钥**：开发基线所有服务共用 `deployments/secrets/mixin_search_capability.key`，未按服务拆分与轮换；密钥至少 32 字节，缺失或过短时服务拒绝启动。
7. **明文 gRPC、仅回环**：无 TLS/mTLS，上线前需加固。
8. **账本无保留策略与容量上限**：`qq_applied_events` 持续增长，且是重建的唯一依据。
9. **账本无载荷**：行丢失时无法从本地恢复，只能计入 `records_failed`。
10. **`record_id` 唯一性义务在 py-agent**：它同时是 `qq_messages` / `qq_files` 的主键，必须在同一记录类型内跨 Bot 唯一；线上身份仍是 `(channel, bot_id, conversation_id, record_id)`。
11. **索引实现与登记不一致**：`qq_search.index_collections` 登记 Qdrant alias `qq_source_messages_v1` / `qq_source_files_v1`，但当前检索实际走 PostgreSQL tsvector + trigram，Qdrant 集合未创建。
12. **gRPC reflection 实际不可用**：配置项 `auth.enable_reflection`（默认 false）存在，但组合根没有读取它，也没有注册 reflection 服务。
13. **身份所有权证明未实现**（如何证明操作者持有该 QQ 号），属后续工作。
14. **未验证项**：py-agent 本体不在本仓库；本清单只验证了 qq-search 侧对这些输入的反应，未验证 py-agent 能否产出符合 §5 的载荷。

## 10. 本清单的验证依据

| 内容 | 依据 |
| --- | --- |
| RPC/scope/允许调用方 | `apps/qq-search/internal/interfaces/grpcapi/api.go`、`TestMethodScopesCoverEveryRPCExactlyOnce` |
| 凭据与范围规则 | `packages/serviceauth/{boundary,credential,identity,transport}.go`、`internal/interfaces/grpcapi/inclusion_test.go` |
| 事件字段与生命周期 | `internal/application/{model,event,messages,files}.go`、`internal/ingress/pyagent/converter.go` 及其单元测试 |
| 检索与记录状态范围 | `internal/application/{search,messages,files}.go`、`TestIntegrationChannelScopeLimitsResults`、`TestIntegrationGetRecordState*`、`TestIntegrationTransportRecordStateUsesTheChannelCapability` |
| 重建与游标 | `internal/application/{rebuild,status}.go`、`TestIntegrationRebuildRestoresTheDerivedIndexFromLocalData`、`TestIntegrationRebuildCannotResurrectARecordRecalledWhileItRuns` |
| 健康检查与探针 | `internal/app/app.go`、`cmd/qq-search-healthcheck/main.go` |
| 运行结果 | `docs/reports/evidence/phase4/stage-a-qq-search.md`（`go build/vet/gofmt/test` 与 `-run Integration -v` 的退出码与输出） |
