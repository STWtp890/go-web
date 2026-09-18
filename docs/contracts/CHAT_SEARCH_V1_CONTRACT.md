# mixin-search 聊天语料索引契约

> 状态：契约已定型；服务端实现属于 P3.3 实施包，进度见 [当前实施计划](../planning/CURRENT_IMPLEMENTATION_PLAN.md)
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
- 调用方必须携带 capability，角色为 `chat-index-writer`（写）或 `chat-searcher`（检索）：这两个角色与文档的 `index-writer` / `searcher` 不通用，因此文档侧凭证无法调用聊天 RPC，反之亦然；
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
