# mixin-search 聊天语料索引契约

> 状态：契约已定型，服务端实现已随 P3.3 交付并归档（验收点 `32f4648`，tag `p3.3-accepted`；证据见 [阶段三证据快照](../reports/evidence/phase3/README.md)）
> 责任边界：§8 是 `py-agent` 接入前后的核对清单（谁负责什么、mixin-search 不做哪些事）
> 日期：2026-09-17
> Proto：`packages/proto/mixin-search/chat/v1/chat.proto`
> 相关决策：[ADR-014](../adr/014-per-corpus-control-plane-isolation.md)、[ADR-012](../adr/012-multi-consumer-search-boundary-and-critical-path-shift.md) 决策 6

## 1. 适用范围

聊天语料使用**独立契约**，服务名为 `mixin_search.chat.v1.ChatIndexService`。它不复用文档契约，也不通过给文档 RPC 增加 `corpus_type` 字段扩展（[ADR-014](../adr/014-per-corpus-control-plane-isolation.md) 决策 3 明确拒绝该做法）。

数据所有权：

- `py-agent` 拥有会话、消息、QQ 身份、渠道权限和原始聊天记录的长期保存；
- `mixin-search` 只拥有可重建的聊天索引状态：块向量、执行快照、撤回墓碑和索引控制状态；
- 本服务**不创建、不编辑、不删除**已归档的聊天记录，也不解释 QQ 身份。

与文档语料的隔离是硬隔离：独立契约、独立控制面实例与 generation、独立持久化状态、独立投影 reconciler、独立索引集合与 alias。共享的是机制（不可变快照、compare-and-swap、后台收敛、capability 边界），不是运行状态。

本节只声明**数据所有权**；三方在判定、执行与举证上的分工见 §8。

## 2. RPC

| RPC | 语义 |
| --- | --- |
| `IndexConversationMessages` | 在指定 `lifecycle_revision` 下幂等写入一批不可变消息；同一 `(conversation_id, message_id)` 不允许不同内容覆盖；同一批次内重复出现同一个 `message_id` 是**非法请求**（`invalid_argument`），不是幂等重放 |
| `ArchiveConversation` | 按 `archive_revision` 将会话标记为**可检索**；仅索引不等于可检索 |
| `UpdateConversationAccess` | 用 `access_revision` 与 `lifecycle_revision` 替换 `granted_scope_ids` 完整访问快照 |
| `RetractMessage` | 按 `retract_revision` 撤回单条消息：它仍被保存、仍属已归档会话，但不再可检索 |
| `DeleteConversation` | 以 `conversation_id + lifecycle_revision` 幂等删除全部派生索引并留下墓碑 |
| `GetConversationIndexState` | 查询会话的存在性、状态、三类修订与计数，用于对账 |
| `SearchChatMessages` | 在已授予范围内检索已归档会话中未被撤回的消息 |

## 3. 三个状态的独立性

这是本契约与文档契约最重要的语义差别，也是必须显式建模的部分：

| 状态 | 归属 | 含义 |
| --- | --- | --- |
| **已保存** | `py-agent` | 消息存在于聊天记录中。本服务**从不参与**，也不影响它 |
| **已索引** | 本服务 | `IndexConversationMessages` 已接受消息文本 |
| **已归档** | 本服务 | `ArchiveConversation` 已把会话标记为可用于检索 |

推论：

- 索引**不等于**可检索：未归档会话中的消息即使已索引也不进入结果集；
- 归档**不产生**正式文档：聊天内容成为文档必须经过 `go-web` 的独立创建、审核与发布流程；
- 撤回**不等于**删除：撤回只让消息离开检索，原始记录仍由 `py-agent` 保存；
- 删除会话**不等于**撤回消息：前者结束整个会话的索引生命周期（墓碑），后者只影响一条消息。

四类修订相互独立，推进一种不得隐式推进或回退另一种：

- `archive_revision`：只排序归档/取消归档；
- `access_revision`：只排序授权范围快照；
- `lifecycle_revision`：排序索引存在性、删除与重新索引周期；
- `retract_revision`：只排序单条消息的撤回。

## 4. 授权

- 聊天**没有** `authenticated_public` 对应物：检索必须命中已授予范围，两个 allow-list 都为空时返回空结果，不存在"默认公开"；
- `granted_scope_ids` 是完整快照而不是补丁：遗漏某个范围即表示撤销；
- 调用方必须携带 capability，角色为 `chat-index-writer`（写）、`chat-searcher`（检索）或 `chat-ops`（只读运维）：这三个角色与文档的 `index-writer` / `searcher` / `ops` 不通用；聊天语料的 audience 是 `mixin-search-chat`（文档语料是 `mixin-search`），角色与 audience 绑定，因此**文档侧凭证以及"聊天角色 + 文档 audience"这类混合凭证都在校验阶段被拒绝**，不会走到权限判定；服务端启动时也拒绝两个语料共用同一个 audience；
- 范围判定沿用文档契约的**包含**规则：请求范围必须是已授予范围的子集，越界整体拒绝，不做静默裁剪。

## 5. 引用形式

检索命中返回 `conversation_id + message_id`、`owner_scope_id`、`sender_id`、`sent_at_unix_ms`、位置、摘要、`content_sha256`，以及融合分数 `rrf_score` 与两条召回路径各自的 `dense_rank` / `sparse_rank`；不回传文档语义字段（文档、版本、活动版本、生命周期状态）。两个 rank 字段由契约声明，就必须是真实值：底层混合召回本来就算它们，适配层只是把它们传出来，不能让调用方把"恒为 0"读成"每条都是最优"。`dense_rank` / `sparse_rank` 表达的是"本次查询里该消息在该路召回中的名次"，不表达任何跨语料的排序或全局得分。

因此联合回答可以同时使用两类结果，并在结果层各自标注来源；控制面**不合并**语料，底层也不会出现一个无法区分来源的结果集合。

## 6. 实现与验证边界

- 契约形状由 `apps/mixin-search/internal/architecture/contracts_test.go` 强制：两个契约的 RPC 集合互不重叠，任一契约不得出现属于另一语料的字段名，也不得增加 `corpus_type` 一类的语料选择器；
- 生成代码一致性由 `packages/proto/verify-generated.ps1` 与 CI 的 `generated-proto` 作业强制；
- 角色策略由 `internal/transport/grpc/auth.go` 的方法表强制：聊天方法只接受 `chat-index-writer` / `chat-searcher` / `chat-ops`，未登记的方法一律拒绝；`internal/transport/grpc` 的测试断言两个语料的角色集合不相交且每个 RPC 都有策略；
- 控制面在 `internal/chat`，与文档语料不共享快照、generation、持久化表或 reconciler；跨语料隔离由 `internal/chat/isolation_test.go` 与 `internal/architecture/dependencies_test.go` 的可执行断言保证；
- 服务端、独立集合与不中断重建的验收条件见 [ADR-014](../adr/014-per-corpus-control-plane-isolation.md) 的"验证"一节；本契约不重复排期。

## 7. 容量

聊天控制快照是"整份复制 + 整份序列化 + 单行 CAS"的写入模型，因此容量上限属于契约语义：超过上限的写入是**明确拒绝**，不是静默降级。两条硬限制：

- 单 corpus 已索引消息数（`-chat-max-messages`）；
- 单 corpus 控制快照编码字节数（`-chat-max-snapshot-bytes`）。

两条限量由部署配置给出（0 = 不限）。**当前根 Compose 已启用 A 档**（30,000 条 / 24 MiB），因此该本地基线的容量是受控的；其他部署必须显式传入。

**系统不承诺一定容纳 30,000 条消息。** 有效容量由消息数量上限与序列化快照字节上限中**先到达者**决定：在当前代表性画像下，无 metadata 约 29.5k、32 B metadata 约 27k、64 B metadata 约 26k。因此 `-chat-max-messages` 的定性是**防止异常状态无限增长的第二道保险，不是产品容量指标**；主要约束是字节上限。

检查发生在写入之前：消息数是精确检查，快照字节数按**已持久化的快照**判定（读到别的实例写入的更大快照也会被看见），因此最多滞后一次写入——这是刻意的取舍，避免为了精确判定把整份候选快照再编码一遍。达到快照上限后**所有**写入路径（索引、归档、访问快照、撤回、删除）都被拒绝，读取不受影响。

### 7.1 metadata 预算

消息 metadata 存在控制快照内部，因此它是**容量输入而不是装饰**：实测 metadata 的键与值每增加 32 字节，30k 消息的快照约增加 1.83 MB（约 61 字节/消息，含 JSON 结构开销）。四条限制同时生效，全部以 **UTF-8 字节**计——只数字符数或只数条目都会放过单个超长值：

| 限制 | 默认值 | 配置项 |
| --- | ---: | --- |
| 每条消息的 metadata 条目数 | 8 | `-chat-max-metadata-entries` |
| 单个 key 的 UTF-8 字节数 | 32 | `-chat-max-metadata-key-bytes` |
| 单个 value 的 UTF-8 字节数 | 64 | `-chat-max-metadata-value-bytes` |
| 所有 key 与 value 的 UTF-8 总字节数 | 64 | `-chat-max-metadata-total-bytes` |

默认值由容量分解画像推导（见 [画像证据](../reports/evidence/phase3/p33-chat-capacity-profile_20260919.md) 的"metadata 分解"表）。配置项为 0 表示"使用文档默认值"，**不表示不限**。

### 7.2 错误语义

| 场景 | gRPC 状态 |
| --- | --- |
| 单条消息 metadata 违反预算 | `INVALID_ARGUMENT`（请求不符合契约，与语料是否已满无关） |
| 语料达到消息数或快照容量上限 | `RESOURCE_EXHAUSTED`（资源条件，与"修订过期"这类 `FAILED_PRECONDITION` 区分开） |

**整批校验先于任何写入**：metadata 预算、消息数上限与快照上限都在创建写入意图与写向量**之前**校验；被拒请求零部分提交（generation、控制状态与向量均不变），由 `TestMetadataBudgetIsEnforcedBeforeAnyWrite` 与 `TestSnapshotCeilingRefusesEveryMutationNotJustIndexing` 断言。

### 7.3 账本保留

幂等账本的清理与"重放保证的有效窗口"由 [ADR-015](../adr/015-control-plane-idempotency-ledger-retention.md) 定义：**窗口内**重放同一 `operation_id` 返回首次响应、改绑被拒；**窗口外**不承诺返回首次响应，也不承诺检测改绑，但**不重复写入**仍由状态本身保证（向量键含 `operation_id`、消息内容不可变、修订号幂等）。窗口外的降级是精确的：如果某 `operation_id` 在窗口后的**第一次**到达携带了不同载荷，它会被当作新操作接受；一旦该 id 被重新接受，它会被重新记录，此后改绑检测恢复。

**保留期是"时间窗口或条数上限任一先触发"**，不是无条件的时间保证：峰值写入速率下，条数上限可能先到，未满时间窗口的记录会被清掉。两条限量都受同一个 24 MiB 快照预算约束——账本与消息共用这份预算，而一条 receipt 携带该批每条消息的状态，因此"按峰值反推条数"之外还必须按**字节预算**核对（30k 消息约占 11.2 MiB，留给账本约 12.8 MiB，据此取 30,000 条；ADR-015 决策 3 初稿建议的 100,000 条实测达 63.8 MiB，与字节上限互不相容）。清理机制已实现（`-chat-operation-retention`、`-chat-operation-max-entries`），A 档取 7 天 / 30,000 条；`30,000` 同样是**上限而不是保证**，重放保证的实际窗口取决于操作速率、批大小与操作类型。

## 8. 责任边界：mixin-search 与 py-agent 各自负责什么

本节是 `py-agent` 正式接入（P3.6）前后的核对清单。它不改变任何 RPC 语义，只把"谁负责"写清楚，避免实现服务端时把消费者侧的职责一并实现。

### 8.1 判定责任：谁说了算

| 事项 | 责任方 | mixin-search 的角色 |
| --- | --- | --- |
| 会话、消息、QQ 身份、渠道权限、原始聊天记录的长期保存 | `py-agent` | **不参与**：连"消息已保存"这一态都不感知（§3 三态中的"已保存"由它独占） |
| 用户、知识空间、空间成员、绑定关系、文档治理 | `go-web` | **不参与**：不解释用户、角色、成员或 QQ 身份 |
| 渠道权限（该 QQ 身份在当前会话能做什么） | `py-agent` | 不判定 |
| 资源权限（该用户可访问哪些空间与文档） | `go-web`（[ADR-016](../adr/016-qq-identity-and-knowledge-space-mapping.md)） | 只执行 `requested ⊆ granted` 的包含校验，**不产生**授权结论 |
| 撤回、归档、删除的**意图** | `py-agent` | 执行：三态状态机与修订围栏（迟到修订被拒） |
| 检索执行与候选级过滤 | `mixin-search` | 唯一执行方（范围、活动版本、墓碑、storage domain） |
| 跨语料结果融合与最终回答 | 编排方（`py-agent`） | 只按语料分别返回结果，不融合（[ADR-014](../adr/014-per-corpus-control-plane-isolation.md) 决策 3） |
| capability 签发 | `go-web` | 只**校验**（发行方与 audience 绑定，密钥不得下发给 `py-agent`） |

**关键含义**：`mixin-search` 收到的 allow-list 是**调用方声明的事实**，它无法验证声明是否真实。因此权限判定的防线在 `go-web`（资源）与 `py-agent`（渠道），`mixin-search` 的包含校验是"执行侧不越界"，不是授权判定。任何"让 mixin-search 自己判断该不该给"的设计都是越界。

### 8.2 mixin-search 明确不做（实现红线）

- 不保存、不返回、不修补原始聊天记录；不实现会话时间线；
- 不解释 QQ 身份，不做身份绑定、不做空间/成员管理（这些是 go-web 的事实源）；
- 不做渠道权限判定，不感知"用户是否还在群里"；
- 不做知识晋升（聊天内容转正式文档必须走 `go-web` 的生命周期）；
- 不融合两语料结果，不生成回答，不调用任何模型；
- 不签发 capability，也不持有签发密钥的下发权；
- **没有出站依赖**：不回调 `py-agent`、不订阅其队列、不主动拉取数据；所有数据由调用方推入；
- 不"理解"业务：不做消息去重语义推断、不猜测缺失消息、不代调用方重试；
- 不把超限请求裁剪成"能装下的子集"（`INVALID_ARGUMENT` / `RESOURCE_EXHAUSTED` 都是整体拒绝，零部分提交）。

### 8.3 py-agent 必须自行负责的输入义务

| 义务 | 说明 |
| --- | --- |
| 稳定的 `operation_id` | 重试必须复用同一个 id；窗口内重放返回首次响应，窗口外不再承诺响应复现（[ADR-015](../adr/015-control-plane-idempotency-ledger-retention.md)） |
| 消息不可变 | 同一 `(conversation_id, message_id)` 不得换内容；内容变更须走"撤回 + 新消息" |
| 修订号单调 | 四类修订（lifecycle / archive / access / retract）由它推进，服务端只拒绝迟到修订 |
| 批次与 metadata 合规 | 遵守 §7.1 预算与批大小约定：**生产端合规 + 服务端拒绝**，服务端不做裁剪 |
| 错误处置 | `UNAUTHENTICATED` / `PERMISSION_DENIED` / `INVALID_ARGUMENT` / `FAILED_PRECONDITION` **不可重试**（后者先对账）；`UNAVAILABLE` 可重试；`RESOURCE_EXHAUSTED` 需区分容量耗尽与限流并退避、告警，**不得无限重试** |
| 不持有边界密钥 | 只能使用 `go-web` 签发的短时 capability（[SERVICE_CALL_CAPABILITY.md](./SERVICE_CALL_CAPABILITY.md) §4） |
| 不自行扩大范围 | 请求范围必须来自 `go-web` 的资源范围解析结果；不得缓存旧信封用于后续签发（ADR-016 决策 8） |
| 对账 | 用 `GetConversationIndexState` 比较自己的记录与服务端已索引状态；"已保存"计数不在服务端，无法在此对账 |

### 8.4 当前开工状态（2026-09-19）

- 正在推进的 **P3.4 只在 `go-web` 侧**（QQ 身份与知识空间映射），**不改动 `mixin-search`**；本包也不实现 capability 换取入口（属 P3.6）；
- 聊天语料目前**没有生产写入者**（只有测试铸造 chat token）：容量、隔离与 alias 结论都是在无真实流量下取得的，真实负载下的行为要到 P3.6 才被验证；实现服务端时不要把"测试通过"当作"真实接入已就绪"。
