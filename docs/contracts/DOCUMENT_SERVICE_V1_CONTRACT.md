# 文档服务 v1 契约

> 状态：已落地（`packages/proto/document/v1`、`packages/proto/documentsearch/v1`、`packages/proto/qqsource/v1`、`packages/proto/qqsearch/v1`）
> 日期：2026-09-26
> 相关决策：[ADR-017](../adr/017-source-owned-document-and-search-services.md)、[ADR-016](../adr/016-qq-identity-and-knowledge-space-mapping.md) v3
> 相关契约：[服务身份与权限凭证](./SERVICE_IDENTITY_AND_CAPABILITY.md)、[mixin-search/v1 文档索引契约](./MIXIN_SEARCH_V1_CONTRACT.md)（迁移期并存，目标由文档检索服务取代）
> 唯一负责人清单：[当前实施计划 §0.5](../planning/CURRENT_IMPLEMENTATION_PLAN.md)

## 1. 服务边界

文档服务是正式文档的唯一业务写入方。它**不是**接受表名或 SQL 的通用数据库写入代理：调用方提交业务命令与详情查询，服务负责聚合、版本、生命周期、空间、成员、资源权限、审计与 Outbox。

| 拥有 | 不拥有 |
| --- | --- |
| 正式文档与版本、生命周期 | Web 账号与 Web 界面（`go-web`） |
| 知识空间、空间成员 | QQ Bot 身份、会话事实、渠道权限、QQ 原始内容（`py-agent`） |
| 群空间绑定 | 正式文档索引（文档检索服务） |
| 资源访问主体登记 | QQ 原始内容索引（QQ 检索服务） |
| 资源权限判定与 capability 签发 | 检索索引、collection、alias |
| 审计、文档变更 Outbox | |

## 2. 身份模型

- 文档服务持有 `document_service.access_subjects`。主体键形如 `web:user:42`、`qq:user:10001/20002`。
- 调用方通过 `authorization` 中的服务身份断言提供身份材料（见[服务身份与权限凭证](./SERVICE_IDENTITY_AND_CAPABILITY.md)），可以声明的主体必须落在自己的命名空间内。
- **Web 请求与 QQ 请求分别通过各自的受信入口提供身份材料**：`go-web` 代表 Web 账号，`py-agent` 代表 QQ 身份。文档服务据此判定空间成员关系与文档权限。
- **两类身份不以互相绑定为前提**：不存在“QQ 身份必须绑定 Web 用户才能访问空间”的要求，也不存在对应的绑定表。QQ 主体对空间的访问来自文档服务自己的活动 `space_members` 记录。
- 请求体中的任何身份字段都不被采信；主体只从已校验的断言读取。

## 3. RPC 契约

包名 `document.v1`，服务 `DocumentService`。所有 RPC 都需要服务身份断言；表中 `scope` 列是额外要求。

| RPC | scope | 语义 |
| --- | --- | --- |
| `CreateDocument` | `document.write` | 为断言中的主体创建正式文档。首次创建时惰性建立该主体的 private 空间与 owner 成员。可带 `source`（来源系统、Bot/会话、原始记录 ID）。`authenticated_public` 在此确定。 |
| `UpdateDraft` | `document.write` | 追加一个新版本（`draft`，不成为活动版本、不写 Outbox）。`expected_aggregate_revision` 非 0 时做乐观并发校验。**不改变访问策略**：请求一旦带上 `authenticated_public`（presence）即以 `INVALID_ARGUMENT` 拒绝，而不是静默忽略——正文编辑不得顺手撤销或扩大访问。 |
| `SaveDocument` | `document.write` | “修改并保存”用例（见 §3.1）：一次事务完成所有权检查、预期修订号检查、新版本写入、活动版本切换、**可选**访问策略修改与 Outbox 事件写入，返回切换后的活动版本详情。 |
| `PublishDocument` | `document.write` | 把指定版本置为 `published` 并成为活动版本；旧活动版本变为 `superseded`。同一事务写入 Outbox `upsert` 事件。 |
| `WithdrawVersion` | `document.write` | 把版本置为 `withdrawn`；若是活动版本则不活动，并写入 `delete` 事件。 |
| `ArchiveDocument` | `document.write` | 生命周期转为 `archived`；索引侧按 `delete` 事件撤销。 |
| `TrashDocument` | `document.write` | 生命周期转为 `trashed`；写入 `delete` 事件。 |
| `GetDocument` | `document.read` | 文档详情：摘要、活动版本正文、公共访问标志、授予的空间、来源关系。 |
| `ListDocuments` | `document.read` | 按主体/空间/生命周期列出文档摘要；`authenticated_public_only` 是过滤条件，不是授权。摘要带**真实公开状态**（读策略行，而不是由发布状态推断），响应带 `total_count`。 |
| `CreateTeamSpace` | `space.admin` | 创建 team 空间与 owner 成员（同一事务）。 |
| `GetSpace` | `document.read` | 空间详情、成员与群绑定。 |
| `ListSubjects` | `document.read` | 读取文档服务自己的主体登记。 |
| `GrantSpaceMember` | `space.admin` | 新增或恢复活动成员。**入群事件不得调用它。** |
| `RevokeSpaceMember` | `space.admin` | 关闭活动成员；owner 不可撤销。 |
| `BindGroupSpace` | `space.admin` | 绑定 QQ 群到 team 空间。 |
| `RevokeGroupSpace` | `space.admin` | 关闭活动群绑定。 |
| `ResolveAccessScope` | `access.resolve` | 资源范围解析（见 §4）。 |
| `IssueSearchCapability` | `access.resolve` | 解析范围并在 `Granted` 时签发 audience 为 `document-search` 的资源范围 capability（见 §4.1）。 |
| `ListDocumentEvents` | `document-event-reader` | 文档变更事件流（见 §5）。**不要求资源主体**：这是服务到服务的全量读取，签名主体是“要代表谁”，对全量流没有意义。scope 由传输边界与用例双重校验。 |

### 3.1 保存用例（`SaveDocument`）

“修改并保存”是一个用例，不是“先 `UpdateDraft` 再 `PublishDocument`”的两次调用。它必须在**一个事务**内完成：

1. 锁定文档行；
2. 所有权检查（只有所有者可保存，非所有者 `PERMISSION_DENIED`）；
3. 预期修订号检查（`expected_aggregate_revision` 非 0 时）；
4. 写入新版本；
5. 切换活动版本（旧活动版本转 `superseded`，新版本转 `published` 并成为活动版本）；
6. 请求显式给出 `authenticated_public`（presence 决定，不是取 bool 值）时，在同一事务内修改访问策略并推进 `access_revision`；
7. 写入一条 `upsert` Outbox 事件。

任一步失败，整个事务回滚：不留下版本、不留下策略变更、不留下事件。

- **复用同一条版本切换规则**：`SaveDocument` 与 `PublishDocument` 必须共用同一段活动版本切换与事件写入逻辑，不允许出现两套。
- **返回新活动版本详情**：响应中的 `document.summary.active_version_id` 与 `document.version` 就是刚激活的版本，因此保存后立即读取详情即可拿到新正文。
- **幂等**：以 `request_id` 去重。同一主体对同一文档重复提交同一 `request_id` 时，返回首次尝试的结果并置 `replayed=true`，不新增版本、不新增事件。重复投递不得产生第二份业务结果。
- **访问策略**：正文保存不改变策略；显式修改策略时，详情、列表摘要与事件三处必须一致（事件携带修改后的完整访问快照）。
- 与 `UpdateDraft` 的分工：草稿是暂存，不成为活动版本、不产生事件；保存是让编辑生效。

### 3.2 错误语义

| 场景 | gRPC 状态 |
| --- | --- |
| 缺少/无效断言、audience 不符、过期 | `UNAUTHENTICATED` |
| scope 不足、主体命名空间越界 | `PERMISSION_DENIED` |
| 主体不是文档所有者且无文档级权限 | `PERMISSION_DENIED`（`document.read` 的详情读取按资源判定，不以“看不到”伪装成 `NOT_FOUND`） |
| 文档或空间不存在 | `NOT_FOUND` |
| 乐观并发校验失败、生命周期不允许该操作 | `FAILED_PRECONDITION` |
| 输入非法（空标题、非法 UUID、非法主体键、非法会话上下文） | `INVALID_ARGUMENT` |
| 数据库不可用 | `UNAVAILABLE` |

## 4. 资源范围解析

```text
ResolveAccessScope(subject_key, conversation)
  -> Decision: Granted | Denied{reason}
     Private:     []spaceID
     CurrentTeam: spaceID | none
     OtherTeams:  []spaceID
     member_space_ids: 服务端重算的并集
```

规则见 [ADR-016 v3 §决策 4](../adr/016-qq-identity-and-knowledge-space-mapping.md)。要点：

- 三类标签只由服务端生成、互不重叠；调用方不能提交或改写标签。
- 群聊的 `CurrentTeam` 必须是**当前群**活动绑定的空间且主体是它的活动成员；其他群绑定的空间只能出现在 `OtherTeams`。
- `Denied` 不携带任何范围，也不得降级为空 `Granted`。
- 未绑定群、已撤销群绑定、非活动成员、未知或停用主体都是 `Denied`。
- 撤销在下一次解析立即生效，不推进索引 generation、不切换 alias。

`Denied` 的原因集合：`subject_unknown`、`subject_revoked`、`subject_inactive`、`group_unbound`、`group_binding_revoked`、`not_space_member`、`session_context_invalid`。

### 4.1 资源范围 capability 的签发

`IssueSearchCapability` 是 capability 的唯一签发入口：事实源同时是签发方，检索服务只做离线校验，因此不需要信任调用方对自身范围的描述。

- `Granted` → 返回 `capability`（`issuer=document-service`、`audience=document-search`、`scope=document-searcher`）与解析出的标签。
- `Denied` → 返回判定与原因，**capability 为空**；`Denied` 绝不降级为空范围。
- 可选的 `requested_space_ids` / `requested_document_ids` 必须是解析结果的子集，否则整体 `PERMISSION_DENIED`，不静默裁剪。
- 有效期取 `auth.capability_ttl`（默认 2 分钟）。

### 4.2 详情读取的判定

`GetDocument` 按四个独立条件之一放行：主体是文档所有者、存在活动的主体级文档授予、主体是 owner 空间或某个被授予空间的活动成员、文档 `authenticated_public` 且请求主体已在文档服务登记且处于活动状态。

"已登记"是必要条件而不是自动结果：登记只发生在受信入口首次代表该主体发起命令或管理操作时，只读解析不会顺手登记任何人。因此公开文档对所有已登记主体可读，对未知主体仍然 `PERMISSION_DENIED`。

拒绝与不存在刻意区分：不存在返回 `NOT_FOUND`，无权读取返回 `PERMISSION_DENIED`，契约不允许把后者伪装成前者。

## 5. 文档变更事件（Outbox）

- 事件与业务变更**在同一事务**提交；写入结果不以检索服务当次可用为条件。
- 事件表 `document_service.document_events`，`sequence bigserial` 为游标；表为 append-only（触发器拒绝 UPDATE/DELETE）。
- 事件种类：`upsert`（新增/更新可索引版本）、`access_changed`（访问范围变化）、`delete`（撤销/归档/删除）。
- 事件载荷是 `document.v1.DocumentEventEnvelope` 的序列化结果，包含：`sequence`、`event_id`、`kind`、`document_id`、`version_id`、四个 revision、生命周期与发布状态、`owner_subject_key`、`owner_space_id`、`authenticated_public`、`allowed_space_ids`、标题/摘要/正文/格式/`content_sha256`、`index_profile`、`occurred_at`、`created_at`、`source`。
- 消费方语义：按 `sequence` 游标读取；重复投递必须幂等（按 `event_id` 去重）；乱序到达时低 revision 不得覆盖高 revision；`delete` 之后不得被旧事件复活。
- 重建：消费者用自己保存的已应用事件重放，**不在查询时回调文档服务**。

## 6. 检索边界

- 正式文档索引由**文档检索服务**拥有（`documentsearch.v1`），只消费本文档服务的事件。
- 查询前，查询方先调用 `ResolveAccessScope` 取得范围；需要检索时由文档服务签发 audience 为 `document-search` 的资源范围 capability，检索服务离线校验并执行包含规则。
- 检索服务不回调文档服务取得正文或权限。查询方需要完整正文时直接调用 `GetDocument`。

### 6.1 查询语义（`SearchDocuments`）

```text
SearchDocuments(query, allowed_space_ids, allowed_document_ids, page, page_size, owned_by_subject_only)
```

**授权范围与实际过滤范围必须一致**，这是本节的要点：

- capability 给出的是**授权上限**；请求中某一族非空时，该族的实际过滤集合就是请求给出的集合（已校验为授权范围的子集），而不是完整的授权集合。“只查获准空间 A”就只返回空间 A 的结果，其他空间的文档——包括其他空间的公开文档——都不得出现。
- `authenticated_public` 兜底是授权信封的一部分，只在整个请求都没有收窄（两族都为空）时生效；一旦调用方点名了空间或文档，返回范围就是被点名的集合。
- 请求中出现任何未授予的标识 → **整体拒绝**，不静默裁剪。
- `owned_by_subject_only` 把结果限制为 capability 主体自己拥有的文档；主体从已校验的 capability 读取，调用方不能提交或改写。该开关下其他主体的公开文档与私有文档都不得返回。
- **命中与总数使用完全相同的条件**：`total` 是真实匹配数，不是本页条数；`page` 从 1 起，`page_size` 为每页条数，各页互不重叠、不遗漏。
- 结果仍只包含活动版本、`active` 生命周期、`published` 发布状态；已删除、已撤销、非活动版本不进入结果。
- 命中返回 Web 展示需要的真实字段：`owner_subject_key`、`authenticated_public`、`created_at`、`updated_at`（在消费事件时写入索引投影，查询时不回源）。

#### 6.1.1 混合检索下的 `score` 与 `total`

检索服务同时维护关键词索引与向量集合（`go_web_document_v1`），查询由两条召回臂融合：

- **`score` 是融合分数**（倒数排名融合，RRF `k=60`），不是某一臂的原始相似度。它只保证**同一查询内**的排序稳定（融合分降序、`document_id` 升序），**跨查询、跨部署没有可比性**，调用方不得把它当作阈值或质量指标。
- **`total` 是融合答案集的真实大小**：范围内关键词匹配数，加上向量臂额外召回并通过同一授权/生命周期过滤的文档数。命中与总数来自同一组候选集合，分页互不重叠、不遗漏；`page`/`page_size` 语义不变。
- **向量臂的召回族是调用方点名或获授权的空间与文档**。`authenticated_public` 是授权**下限**而不是召回族：它完整参与**过滤**（每条召回都必须通过同一 SQL 范围条件），但不把整个部署的公开语料拉进候选。因此「公开文档对空信封可见」的行为由关键词臂承载，不变。
- 向量后端未启用或不可用时**不静默降级**：检索服务启动即要求活动 generation 的集合可用，`total` 退化为关键词匹配数只在显式配置 `vector.enabled=false` 时发生。

### 6.2 QQ 记录状态查询（`qqsearch.v1.GetQQRecordState`）

`GetQQRecordState` 与两个检索 RPC 使用同一套渠道范围规则：从 capability 取授权范围，请求中的范围只能收窄、越界整体拒绝，实际查询按 Bot、会话、群过滤。

- 范围外的记录与不存在的记录返回**一致结果**（`exists=false`），因此该接口不能被用来探测其他 Bot 或会话是否存在某个记录。
- 空授权范围同样返回 `exists=false`，不得退化为“全部会话”。
- 范围内的撤回状态仍可查询（`exists=true` 且状态为 `RECALLED`）。
- 消息与文件分别按各自表的列执行同一规则，不共用查询。

## 7. QQ 原始内容与晋升

- QQ 原始消息、文件、会话与撤回事实由 `py-agent` 拥有（`qqsource.v1`），其索引由 **QQ 检索服务**拥有（`qqsearch.v1`）；消息与文件使用独立模型、表与 collection。
- QQ 内容晋升为正式文档时，`py-agent` 调用 `CreateDocument` 并带 `source`（`origin=qq`、`bot_id`、`conversation_id`、`source_record_id`）。文档服务保存来源关系；此后由正式文档的版本与发布规则管理，并进入文档检索服务。
- 来源记录 ID 只用于幂等提交与来源追踪，**不能替代文档 ID**。
- QQ 原始内容不因被保存、索引或与某个空间相关而自动成为该空间的正式文档。

## 8. 事务与一致性要求

- 空间与活动 owner 成员必须同一事务建立；数据库延迟约束触发器保证“每个空间有活动 owner”“private 空间只有 owner”。
- 主体登记、绑定、成员变更与对应审计在同一事务提交；失败整体回滚，不残留审计。
- 版本正文不可变（触发器拒绝改写已写入的版本内容）。
- 活动版本外键为延迟约束，指向的版本必须已发布。
- 文档服务的业务表只由 `document_service_writer` 写入；其他服务没有写权限（由 `deployments/postgresql/verify-service-isolation.ps1` 实测）。
