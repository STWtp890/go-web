# 交付给 py-agent 任务的 Go 侧接口材料（阶段 A）

> 状态：**部分可接入**。第 1–4、6 节是已实现且已实际验证的接口要求；第 5 节（重建来源接口）**未实现**，第 7 节逐条登记依赖与未实现项。任何第 7 节列出的内容都不得表述为可直接接入的能力。
> 交付版本：**v2**（2026-10-01）。相对 v1（2026-09-30）的变化：`document.v1` 修订了保存用例的重放语义并新增 `applied_version_id`；`packages/serviceauth` 新增边界密钥文件加载器；契约哈希全部重新核对。
> 日期：2026-10-01
> 交付方：go-web 工作区（主 Agent 汇总）
> 相关契约：[服务身份与权限凭证](./SERVICE_IDENTITY_AND_CAPABILITY.md)、[文档服务 v1](./DOCUMENT_SERVICE_V1_CONTRACT.md)、[ADR-017](../adr/017-source-owned-document-and-search-services.md)
> 事实清单与逐条验证依据：[stage-a-qq-search-pyagent-requirements.md](../reports/evidence/phase4/stage-a-qq-search-pyagent-requirements.md)
> 验收反馈修复记录：[fix-acceptance-f01-f07.md](../reports/evidence/phase4/fix-acceptance-f01-f07.md)
> py-agent 本体不在本仓库：以下只声明 Go 侧对输入的**实际反应**，不声明 py-agent 能产出符合要求的载荷。

## 1. 契约版本

基线提交 `33304e498544806636b29d33bb504e7c01a75cfe`，其上叠加未提交的工作区修订（阶段 A/B/C 与验收反馈修复 F01–F07）。契约以文件内容为准，不以下游仓库的提交号为准；请用下表的 SHA-256 核对（**v2 已逐项重新计算**）：

| 契约文件 | SHA-256 | 说明 |
| --- | --- | --- |
| `packages/proto/qqsource/v1/qqsource.proto` | `e20a3f49dfaed00b366b37ff1bd8859d340cee4769f89c3871bd4ce5d0cabac4` | py-agent **提供**的事件契约（`QQSourceService`） |
| `packages/proto/qqsearch/v1/qqsearch.proto` | `b74e6d64f2138a308872dfbbad5dab5aa30f9315e1ebc63fa025bab74aa97f30` | qq-search **提供**的索引与检索契约 |
| `packages/proto/document/v1/document.proto` | `a9a926c1b0acb43ff5bc3248456238eadb8c7bfb501691bf28b2bb325092b30f` | 正式文档命令与详情（QQ 内容晋升时使用）；v2 因保存用例修订而变更 |
| `packages/proto/documentsearch/v1/documentsearch.proto` | `6875db927f6002a748ad9e602714c9e8746b14306a9919d9de0b767fc9525168` | 正式文档检索（QQ 晋升后经此检索） |
| `packages/serviceauth/identity.go` | `509822252ffe4b8a6fa021941b5c7c220cb83b2c71a89ba7b3c7092bebdb8c29` | caller/audience/scope 登记与签发方规则 |
| `packages/serviceauth/credential.go` | `fca00e9c466041204debb84f751db3e90f488d43d82545264b37cb63caa6b224` | 凭证信封、校验与固定测试样例 |
| `packages/serviceauth/boundary.go` | `727a0594a27c31219df1e31970ffa8f74f305d2c680e1ac0dca6de4433797fd5` | 每方法 scope 策略与边界拦截 |
| `packages/serviceauth/transport.go` | `915a746ca644dbe68626c668082d49154bb9c52341f6c0075c29f606f392eadd` | header 读取与错误映射 |
| `packages/serviceauth/keyfile.go` | `6a1ab1dcdef86c8358caf0b2880fb30b83c709040dfd1d3eb4449625543107a5` | v2 新增：边界密钥文件加载（`LoadBoundaryKeyFile`），调用方读取共享密钥的唯一入口 |

重新交付前请用同一命令复核，例如
`Get-FileHash -Algorithm SHA256 packages/proto/document/v1/document.proto`。

生成代码：`packages/gen/qqsource/v1`、`packages/gen/qqsearch/v1`（由 `packages/proto/verify-generated.ps1` 逐文件哈希核对，六份契约全部 PASS）。

**已实现并登记的 RPC 列表**（`qqsearch` audience）：

| RPC | scope | `x-resource-capability` | py-agent 用途 |
| --- | --- | --- | --- |
| `IndexQQSourceEvent` | `qq-index-writer` | 不需要 | 推送 QQ 来源事件（唯一写入口） |
| `SearchQQMessages` | `qq-searcher` | **必须** | 消息检索 |
| `SearchQQFiles` | `qq-searcher` | **必须** | 文件检索 |
| `GetQQRecordState` | `qq-searcher` | **必须**（本轮新增） | 单条记录状态（含撤回） |
| `RebuildIndex` | `qq-index-writer` | 不需要 | 重建（`confirm=true` 必填） |
| `GetIndexStatus` | `qq-index-writer` | 不需要 | 两个集合身份与消费进度 |

未在策略表登记的 RPC 一律拒绝（失败关闭）。只有 `grpc.health.v1.Health/Check`、`/Watch` 匿名。

正式文档侧（`document` audience，晋升路径）：`CreateDocument`、`SaveDocument`、`PublishDocument`、`WithdrawVersion`、`ArchiveDocument`、`TrashDocument`、`GetDocument`、`ListDocuments`，以及 `ResolveAccessScope` / `IssueSearchCapability`。

### 1.1 保存用例的重放语义（v2 明确，py-agent 若重试晋升后的编辑需按此实现）

`SaveDocument` 以 `request_id` 幂等，服务端为**每个文档保留全部已提交请求 ID 的持久记录**（不是只记最近一次）：

| 场景 | 结果 |
| --- | --- |
| 同一 `request_id` 重复提交（无论间隔多久、中间是否有其他保存） | `replayed=true`；**不新增版本、不写策略、不写事件、不推进任何 revision** |
| 重放时的响应内容 | `document` 是**当前已提交状态**（head），可能已被后续保存推进；`applied_version_id` 是**首次**尝试创建的那个版本（可能已被 supersede）——两者语义不同，不要假定它们一致 |
| 同一 `request_id` 携带**不同载荷** | `ALREADY_EXISTS`，事务零变更；换内容必须换新的 `request_id` |
| 首次保存 | `replayed=false`，`applied_version_id` = 刚激活的版本，等于 `document.summary.active_version_id` |

载荷指纹覆盖 title、content、content_format 与访问策略的 presence+取值；`expected_aggregate_revision` 不参与指纹（它是并发保护，不是请求内容）。

## 2. 连接要求

| 项 | 值 |
| --- | --- |
| 传输 | 明文 gRPC（当前无 TLS/mTLS），仅绑定回环地址 |
| qq-search gRPC 默认地址 | `127.0.0.1:18083`（`QQ_SEARCH_GRPC_LISTEN_ADDRESS`） |
| qq-search HTTP 探针 | `127.0.0.1:18093`（`QQ_SEARCH_HTTP_LISTEN_ADDRESS`） |
| `GET /healthz` | 200，仅表示进程存活，不查数据库 |
| `GET /readyz` | 数据库 ping（3s）：200 就绪 / 503 `{"status":"unavailable","reason":"database"}` |
| `GET /info` | service / audience / schema / 两个集合别名 |
| gRPC 健康服务 | 启动 `NOT_SERVING`，`WaitReady`（最长 30s）后 `SERVING` |

- **服务端没有 per-RPC deadline、超时、重试或退避**：调用方必须自备 deadline 与重连策略，服务端不代办。
- 错误映射（契约层）：

| 场景 | gRPC 状态 | 调用方处理 |
| --- | --- | --- |
| 缺失 / 格式错误 / 签名无效 / 过期 / audience 不符 / 签发方不符 | `UNAUTHENTICATED` | 不可重试 |
| 缺 scope / 命名空间越界 / 请求范围越界 | `PERMISSION_DENIED` | 不可重试 |
| 服务自身配置无效 | `INTERNAL` | 不是调用方错误 |
| producer 数据非法 | `INVALID_ARGUMENT` | 修数据 |
| 同 revision 不同载荷、`record_id` 跨会话冲突 | `ALREADY_EXISTS` | 重试无用 |
| 数据库暂不可用 | `UNAVAILABLE` | **可用同一 `event_id` 重投** |
| `RebuildIndex` 缺 `confirm=true` | `FAILED_PRECONDITION` | 否 |

## 3. 身份与权限要求

### 3.1 caller / audience / scope 的允许组合

| caller | 可声明主体命名空间 | 说明 |
| --- | --- | --- |
| `py-agent` | `qq:*` | QQ 运行时；QQ 渠道能力与原始事实的唯一来源 |
| `go-web` | `web:*` | Web 账号与 Web 入口 |
| `spacectl` | `web:*` | `go-web` 的运维 CLI，审计上与 `go-web` 区分 |
| `document-service` | 无 | 文档服务自身 |

audience 一服务一值：`document-service`、`document-search`、`qq-search`。

**签发方规则（本轮修复，属于强制校验）**：capability 的 `issuer` 必须是该 audience 的事实源——
`document-search` 只接受 `document-service` 签发，`qq-search` 只接受 **`py-agent`** 签发；
没有登记签发方的 audience 一律拒绝。签发端（`SealCapability`）与校验端（`OpenCapability`）都执行该规则。
"签名有效"只证明是谁签的，不证明签发方拥有该范围描述的事实。py-agent 因此是 audience=`qq-search` 的唯一合法签发方。

`qq-search` 的调用方允许列表默认 `py-agent` + `document-service`（可配置收窄，不能超出登记集合）；`go-web` 不在默认列表。

### 3.2 凭证信封与固定测试样例

```text
token = base64url(payload_json) "." base64url(HMAC-SHA256(key, base64url(payload_json)))
```

无算法头；唯一可接受的算法是与边界密钥绑定的 HMAC-SHA256。JSON 解析使用 `DisallowUnknownFields`：**未知字段整体拒绝**（静默忽略一个未知声明正是"范围被悄悄扩大"的通过路径）。

固定样例（密钥 = 32 字节 ASCII `k`，`issued_at=1800000000`，`expires_at=1800000120`；来自 `packages/serviceauth` 的 `TestCredentialGoldenVectors`，逐字节断言，任何字段名、顺序或规范化规则的改动都会让该测试失败）：

服务身份断言（`go-web` → `document-service`）：

```text
eyJ2ZXJzaW9uIjoxLCJjYWxsZXIiOiJnby13ZWIiLCJhdWRpZW5jZSI6ImRvY3VtZW50LXNlcnZpY2UiLCJzY29wZXMiOlsiZG9jdW1lbnQucmVhZCIsImRvY3VtZW50LndyaXRlIl0sInN1YmplY3Rfa2V5Ijoid2ViOnVzZXI6NDIiLCJjb252ZXJzYXRpb24iOnsia2luZCI6InByaXZhdGUifSwiYWN0b3IiOiJ3ZWItYXBpIiwicmVxdWVzdF9pZCI6InJlcS00MiIsImlzc3VlZF9hdCI6MTgwMDAwMDAwMCwiZXhwaXJlc19hdCI6MTgwMDAwMDEyMH0.D-gzsOOKQZiPrZH7u2YRhvQNmU2WMkPw8yJ8L8Rpss4
```

资源范围 capability（`document-service` → `document-search`）：

```text
eyJ2ZXJzaW9uIjoxLCJpc3N1ZXIiOiJkb2N1bWVudC1zZXJ2aWNlIiwiYXVkaWVuY2UiOiJkb2N1bWVudC1zZWFyY2giLCJzY29wZXMiOlsiZG9jdW1lbnQtc2VhcmNoZXIiXSwic3ViamVjdF9rZXkiOiJ3ZWI6dXNlcjo0MiIsInByaXZhdGVfc3BhY2VfaWRzIjpbIjExMTExMTExLTExMTEtMTExMS0xMTExLTExMTExMTExMTExMSJdLCJhdXRoZW50aWNhdGVkX3B1YmxpYyI6dHJ1ZSwiaXNzdWVkX2F0IjoxODAwMDAwMDAwLCJleHBpcmVzX2F0IjoxODAwMDAwMTIwfQ.nesRevAEys4X4ESv8cW0eGWleoJOX9A9as8TTV2KqTs
```

py-agent 应能用同一密钥复算出完全相同的两个字符串；对不上说明编码或规范化规则实现不一致。

### 3.3 py-agent 签发的渠道 capability 字段

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `version` | 是 | 固定 `1` |
| `issuer` | 是 | **必须 `py-agent`** |
| `audience` | 是 | `qq-search` |
| `scopes` | 是 | 查询方必须含 `qq-searcher` |
| `subject_key` | 是 | 该范围属于哪个 QQ 主体 |
| `bot_ids` / `conversation_ids` / `external_group_ids` | 否 | 渠道范围；**留空的家族表示不限制该家族** |
| `issued_at` / `expires_at` | 是 | Unix 秒；契约默认有效期 `2m`；服务端只校验 ±30s 时钟容忍，不限制上限 |

范围规则：请求是已授予范围的**子集**（含空集）→ 允许；出现任何未授予标识 → **整体 `PERMISSION_DENIED`**，不静默裁剪。

## 4. QQ 事件要求

### 4.1 事件种类

| kind | 载荷 |
| --- | --- |
| `message_upsert` | `message_text` / `sender_external_user_id` / `sent_at` / `platform_sequence` |
| `file_upsert` | `file_name` / `mime_type` / `size_bytes` / `content_sha256` / `storage_ref` / `uploader_external_user_id` / `uploaded_at` |
| `message_recalled` | `recalled_at` / `operator_external_user_id` |
| `file_recalled` | `recalled_at` / `operator_external_user_id` |

**没有 delete / deleted 种类**：撤回是追加事实，原始行永不删除，撤回不可逆。若 py-agent 需要"删除"语义，属于契约修订，须先提出。

### 4.2 记录 ID、事件 ID、修订号规则

| 字段 | 规则 |
| --- | --- |
| `event_id` | 非空、≤256 字符、对 qq-search 不透明（不必是 UUID）。服务端用固定命名空间 + SHA-256 派生账本 uuid，同一 `event_id` 永远映射到同一账本行，因此重投安全 |
| `record_revision` | 必填，`1..MaxInt64`；只有**严格更大**的修订号才会被应用 |
| `sequence` | ≥0，消费游标提示；本地游标只前进，乱序不回退 |
| `record_id` | 非空、≤192 字符；**是消息表与文件表各自的主键，因此在同一记录类型内必须跨 Bot 唯一**。线上身份仍是 `(channel, bot_id, conversation_id, record_id)` |
| `conversation_id` | 必须形如 `qq:<bot_id>:<private\|group>:<local_id>`（4 段，服务端原样使用，不重新推导）；≤192 字符 |
| `external_group_id` | group 会话必填且等于第 4 段；private 会话必须为空 |
| 时间字段 | RFC3339（可含纳秒）字符串；空 = 未提供，非法 = `INVALID_ARGUMENT`；upsert 的 `sent_at` / `uploaded_at` 用 `COALESCE` 写入（省略不抹掉已有值） |
| 字段分离 | 消息发送者只从 `sender_external_user_id` 读，文件上传者只从 `uploader_external_user_id` 读；服务端不会用别的字段去填 |

### 4.3 重复、乱序、冲突与重试

| 场景 | 结果 |
| --- | --- |
| 同一 `event_id` 重投（含并发） | `applied=false`，`reason="duplicate"` |
| 同一 revision、同一载荷、不同 `event_id` | `applied=false`，`reason="duplicate_revision"` |
| revision 低于已应用值 | `applied=false`，`reason="stale_revision"` |
| 同一 revision 但载荷不同 | `ALREADY_EXISTS`，绝不覆盖 |
| `record_id` 已绑定另一会话/Bot | `ALREADY_EXISTS` |
| 更高 revision 的 upsert | 应用，并清除撤回状态 |
| 撤回先于对应 upsert 到达 | 先建 `recalled` 行，后续低修订 upsert 被账本栅栏挡住 |
| `UNAVAILABLE` | 可用同一 `event_id` 重投 |

同一记录的事件由服务端按记录串行化（记录级 advisory lock），跨记录可并发。

### 4.4 Bot 与会话范围

- 渠道权限判定只属于 py-agent；qq-search 只执行"请求 ⊆ capability"。
- 查询方应先用请求中的会话上下文裁剪 capability，再发起查询；qq-search 侧再次执行同一包含规则。
- 检索结果只含 `status='indexed'` 的行；撤回记录不出现在检索里，但可在授权范围内通过 `GetQQRecordState` 看到 `RECALLED`。
- `GetQQRecordState` 的完整行为：范围内存在 → `exists=true` + 状态；**范围外 / 不存在 / kind 与实际模型不匹配 → 全部 `exists=false` 且无 `state`（三者同形，不能用它探测其他 Bot 或会话）**；空授权范围 → `exists=false`；请求显式指名越界标识 → `PERMISSION_DENIED`。

## 5. 重建要求

**当前不具备从 py-agent 重建的能力，登记为依赖，不得表述为可直接接入。**

- qq-search **不实现流式拉取**：没有 `ListQQSourceEvents` 客户端、没有 py-agent 地址配置，未使用 `after_sequence` / `batch_size` / `follow`。`qqsource.v1.QQSourceService/ListQQSourceEvents` 目前**没有消费者**。
- `RebuildIndex(confirm=true)` 只重放**本地**数据：`qq_search.qq_applied_events` 账本 + 本地行；按 `(record_kind, record_id)` 取最新事件重派生状态；`records_failed` 统计"有行无账本"和"有账本无行"两类不可恢复记录。
- 架构测试 `apps/qq-search/internal/architecture/source_client_test.go` **结构性禁止** qq-search 生产代码出现任何 py-agent 客户端（`grpc.Dial` / `grpc.NewClient` / `QQSourceServiceClient`）。因此"从事实源重建"不是接线问题，而是需要一次有意的架构决策：定义拉取方向、游标语义、批量与断线续传后，再修改该测试与契约。
- 本地游标 `qq_search.consumer_state.stream='qq-source-events'.last_sequence` 只由推送事件的 `sequence` 推进，可通过 `GetIndexStatus` 读到（需 `qq-index-writer`）；**py-agent 如何用它续传未定义**。

需要 py-agent 反馈的接口问题（当前无答案）：

1. 是否需要"从来源重建"？若需要，拉取方向是由 py-agent 提供 `ListQQSourceEvents`，还是由 py-agent 在重建时重推全部事件？
2. 游标的稳定性与分页边界：`sequence` 是否在 py-agent 侧全局单调、可持久化、跨重启稳定？
3. 缺失记录如何处理：来源已删除而索引仍有行时，期望的收敛结果是什么？
4. 是否需要 delete/deleted 事件种类（当前契约没有）？

## 6. Go 侧验证证据

| 内容 | 结果 |
| --- | --- |
| `apps/qq-search`：`go build ./...` / `go vet ./...` / `gofmt -l .` | 退出码 0 / 0 / 0 行输出 |
| `apps/qq-search`：`go test ./... -count=1 -run Integration -v` | PASS 17 / FAIL 0 / SKIP 0，全部真实 PostgreSQL |
| 同一套件连续重跑 | 12/12 通过（修复了并行测试包共享全局计数的偶发失败） |
| `packages/serviceauth`：`go test ./...` | 通过（含签发方规则与固定样例断言） |
| 契约生成物：`packages/proto/verify-generated.ps1` | 六份契约全部 PASS |
| 文档链接：`docs/check-doc-links.ps1` | `DOC_LINKS=PASS`（broken=0） |
| 实际请求与结果 | 见 `docs/reports/evidence/phase4/stage-a-qq-search.md`（逐条验收项映射） |

本轮修复的两个既有缺陷（供 py-agent 参考其行为影响）：

1. `GetQQRecordState` 原先不读渠道 capability：持有 `qq-searcher` 的调用方可探测授权会话之外的记录是否存在。现已与两个检索 RPC 使用同一条授权链。
2. 重建丢失更新竞态：并发撤回在 `RebuildIndex` 取快照后提交时会被写回 `indexed` 旧修订，复活已撤回记录（修复前并行运行 5/8 失败）。已加 `record_revision` 栅栏并附确定性回归测试。

## 7. 依赖与当前状态登记

| # | 项 | 状态 | 影响 |
| --- | --- | --- | --- |
| 1 | 从 py-agent 拉取/分页/续传事件 | **未实现** | 只能推送；见 §5 |
| 2 | delete/deleted 事件种类 | **契约未定义** | 撤回不可逆，删除语义缺失 |
| 3 | capability `subject_key` 未强制非空 | **契约与实现不一致**（契约 §4 第 11 步要求查询类 RPC 非空，实现未强制） | 主体键格式亦未固定：`qq:10001:user:20003` 与 `qq:user:10001/20003` 都会被接受 |
| 4 | capability 有效期上限 | **未强制** | 签发方给多长就接受多长（契约默认 2m） |
| 5 | 服务端 per-RPC deadline / 配额 / 限流 | **未实现** | 调用方自备 |
| 6 | 账本保留策略与容量上限 | **未实现** | `qq_applied_events` 持续增长，且是重建唯一依据 |
| 7 | 账本无载荷 | **已知限制** | 行丢失无法本地恢复，只能计 `records_failed` |
| 8 | Qdrant 集合未创建 | **登记与实现不一致** | `index_collections` 登记了 alias，实际检索走 PostgreSQL tsvector |
| 9 | 共享边界密钥（开发基线） | **未拆分** | 生产应按服务拆分与轮换，信封格式不变 |
| 10 | 明文 gRPC、仅回环 | **待加固** | 上线前需 mTLS |
| 11 | gRPC reflection | **未注册** | 配置项存在但组合根未读取 |
| 12 | QQ 身份所有权证明 | **未实现** | 如何证明操作者持有该 QQ 号属后续工作 |
| 13 | py-agent 本体 | **不在本仓库** | 以上均为 Go 侧对输入的反应，未验证 py-agent 能否产出符合要求的载荷 |

## 8. 变更流程

契约修订由 **go-web 工作区**完成：py-agent 任务提出需求 → 本工作区修订 `packages/proto`、生成物与 Go 实现 → 交付新版本（新的文件哈希 + 新的固定样例）。py-agent 侧不要直接改 `packages/proto` 或 `packages/gen`。
