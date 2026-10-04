# ADR-016：QQ 身份与知识空间映射

> 状态：**已接受**
> 修订：v2（2026-09-19，按协作评审修订：上下文化解析、撤销生效点、绑定写入口、验收重写）；v2.1（2026-09-19 第二轮评审后接受：判定二态 + 三类服务端标签、标签不变量验收、`Denied` 终止签发链）；v2.2（2026-09-24：Bot 身份命名空间与 P3.4 spacectl 实施约定）；v2.3（2026-09-25：同步账号不可用拒绝原因与现行服务所有权入口）；**v3（2026-09-26，按 ADR-017 的责任划分重写身份与所有权：资源侧事实全部迁入文档服务，取消“QQ 用户 ↔ go-web 用户绑定”作为资源权限前提，明确 Web 与 QQ 身份不以互相绑定为前提）**
> 日期：2026-09-19
> 决策范围：P3.4（QQ 身份与知识空间映射）；P3.6 的接入前置
> 现行服务所有权与实施进度：[ADR-017](./017-source-owned-document-and-search-services.md) 调整服务边界；[当前实施计划](../planning/CURRENT_IMPLEMENTATION_PLAN.md) §0 记录当前任务与验收。本 ADR 的 v2/v2.2 文字保留为历史决策说明，v3 各节取代与 ADR-017 冲突的所有权描述。
> 相关决策：[ADR-003](./003-chat-domain-boundary.md)、[ADR-012](./012-multi-consumer-search-boundary-and-critical-path-shift.md)、[ADR-014](./014-per-corpus-control-plane-isolation.md)、[ADR-015](./015-control-plane-idempotency-ledger-retention.md)；契约 [SERVICE_IDENTITY_AND_CAPABILITY.md](../contracts/SERVICE_IDENTITY_AND_CAPABILITY.md)、[DOCUMENT_SERVICE_V1_CONTRACT.md](../contracts/DOCUMENT_SERVICE_V1_CONTRACT.md)
>
> **v3 阅读指引**：第 1 节已被 §决策 1（v3）取代；第 2 节中 `qq_user_bindings` 已取消，见 §决策 3（v3）与 §数据结构（v3）；第 8 节的解析入口迁入文档服务，判定结构与标签不变量保持不变。

## 背景

生态阶段三要建立 QQ 身份与知识空间的确定性映射（[生态指南](../ECOSYSTEM_EVOLUTION_GUIDE.md) §4、§7、§8）。指南已经把职责划分写死：`py-agent` 判断某个 QQ 身份在当前私聊或群聊中可以执行哪些行为，`go-web` 判断对应用户可以访问哪些空间和文档，**最终有效权限是两者的交集**，且 Model 不能授予权限、扩大身份范围或绕过资源校验。

仓库现状是"地基已有、映射未建"：

- Phase 1 已建 `knowledge_spaces`（`private` / `team`）、`space_members`（含 `member_role` 与 `revoked_at` 软撤销）、数据库级不变量触发器 `validate_space_membership`（每个空间必须有活动 owner 成员；private 空间只允许该 owner），以及 `document_grants.grantee_space_id` 的跨空间授权建模；
- 但 `team` 空间**从未被任何代码使用**，成员表除 owner 行外没有写入路径，私人空间只在文档创建时惰性创建；
- **QQ 身份及其与 go-web 用户的绑定完全不存在**；
- 休眠的 `chat_groups` / `chat_group_members`（`deployments/postgresql/sql/service/chat/schema_init.sql`）属于**站内聊天产品域**，模块未注册路由，不是 QQ 群模型，不能当作现成的群身份使用。

三个必须在编码前定下来的语义是：**绑定关系的事实源与写入口**、**群与空间的关系基数**、**capability 换取入口的位置**。三者一旦落进表结构与契约就具备跨系统契约性，因此先决策后编码。

评审轮补充了三条必须写进决策的事实（本轮已核对）：

1. capability 是**离线校验**（对称 HMAC，无自省端点），go-web 侧配置 `token_ttl: 2m`（`apps/gin-backend/configs/config.yaml`、`config.docker.yaml`），mixin-search 侧校验允许 `30s` 时钟偏差（`internal/security/capability.go` 的 `clocks` 默认值）。因此**已签发的 token 不会因绑定撤销而失效**，其残留窗口上界是 `TTL + leeway`；
2. 在文档契约里，**空范围不等于没有访问权**：范围判定是包含，"含空集，即只检索公开文档"（[SERVICE_CALL_CAPABILITY.md](../contracts/SERVICE_CALL_CAPABILITY.md) §5），实现上 `authenticated_public` 恒为 `min_should` 的基础条件（`internal/rag/store_qdrant.go` 的 `qdrantControlFilter`）。所以"没有范围"必须表达为**拒绝**，不能表达为空集合；
3. 资源范围与**会话上下文**相关：同一个用户在不同私聊/群聊里能用的范围不同，而群与空间的绑定（决策 4）只有在解析时被读入才有意义。

## 决策（v2/v2.2 原文，所有权部分由 v3 取代）

### 1. 事实源与职责边界（**已由 v3 取代，见文末 §决策（v3）**）

- QQ 会话、消息、聊天上下文与**渠道权限**：`py-agent` 是事实源；
- go-web 用户、知识空间、空间成员、文档与**资源权限**：`go-web` 是事实源；
- **绑定关系（QQ 身份 ↔ go-web 用户、QQ 群 ↔ 团队空间）由 `go-web` 保存**：它是资源侧的身份映射，资源权限判定必须在 go-web 内闭合，不能依赖外部系统回传；
- `mixin-search` 继续不解释 QQ 身份（沿用 [ADR-014](./014-per-corpus-control-plane-isolation.md) 决策 5 与聊天契约 §2）：它只接收已经是空间/文档标识的 allow-list。

### 2. QQ 身份与用户绑定是可撤销的显式关系

- 同一 QQ 身份**至多有一条活动绑定**；本包的 QQ 身份键是 `(channel, bot_id, external_id)`，用户和群各自有外部 ID 字段；同一 go-web 用户可以绑定多个 QQ 身份（为多渠道预留 `channel` 维度，本包只实现 `qq`）；
- 解绑与改绑都是**追加式操作**：写入新行或标记 `revoked_at`，历史绑定保留用于审计，不做物理删除；
- 每行绑定必须记录 `actor`、`source`（操作来源）、`reason` 与时间，供事后追溯（见决策 9）。

### 3. 私聊映射到该用户的私人空间

- 私聊的资源范围**至少**包含绑定的 go-web 用户的 `private` 空间（复用 Phase 1 的惰性创建与触发器不变量，不新建第二套私人空间概念）；
- **未绑定的 QQ 身份没有任何资源范围**：不自动建账号、不默认可读公开文档。这是 fail closed：由 `py-agent` 引导绑定，而不是由 go-web 猜测身份；
- 该"没有任何范围"在接口上必须是**拒绝**而不是空集合（理由见背景第 2 条）。

### 4. QQ 群与团队空间：1 群 : 1 空间，可改绑，留审计

- 同一时刻：同一 `(channel, bot_id)` 下，一个 QQ 群至多绑定一个 team space，一个 team space 至多被一个 QQ 群绑定；唯一性由数据库约束保证，而不是靠约定；
- 改绑 = 撤销旧绑定 + 新增活动绑定，保留完整历史（支持"空间迁移"与事故追溯）；
- team space 的 owner 是 go-web 用户（创建者），**群标识不进入空间主键**，避免把群固化为空间；同一个用户可以拥有私人空间并加入多个团队空间。

### 5. 群成员变化不自动扩大资源权限（资源侧不变量）

原则之外，本 ADR 固定一条**可测的资源侧不变量**：

> 创建 QQ 用户绑定、创建群空间绑定、处理"群成员加入"事件，**均不得写入 `space_members`**。

群聊资源范围成立必须**同时**满足三个条件：用户绑定活动、群空间绑定活动、用户在该团队空间中具有**活动** `space_members` 记录。理由：否则任何能拉人入群的人都能间接扩大文档访问范围，与"渠道权限无法扩大资源权限"直接冲突。

同时区分两个阶段：**P3.4 只能证明资源侧结果**；"退出群后渠道权限拒绝""渠道结论与资源权限求交"需要可信的 `py-agent` 输入，属于 P3.6 的端到端验收（见文末）。

### 6. 有效范围 = 渠道权限 ∩ 资源权限，求交在 go-web 闭合

- `py-agent` 提供：QQ 身份 + 该身份的渠道结论（当前私聊/群聊允许的行为）；
- `go-web` 计算资源侧信封（决策 8），有效范围是两者的交集；
- 沿用既有的包含规则：请求范围必须是已授予范围的子集，出现任何未授予标识**整体拒绝**，不静默裁剪；
- 渠道权限不能扩大资源权限，资源权限不能绕过渠道限制，Model/编排层的输出不参与任何判定；
- **P3.4 不实现交集的服务间调用**（换取入口属 P3.6），本包只保证资源侧信封是确定性、可测、可审计的。

### 7. 撤销的生效点是"下一次解析与下一次签发"，不是已签发的 token

- 解绑、移出空间、改绑、空间迁移在下一次资源范围解析与下一次 capability 签发时**立即生效**，且**不推进索引 generation、不切换 alias**；
- 但已交付给调用方的 token 在 **`TTL + leeway`（当前配置 2m + 30s）** 内仍然有效：离线校验无法撤回；
- 因此本 ADR 明确：**不声称对已签发 token 实现即时撤销**。最大残留窗口与处置方式由 P3.6 二选一并写入契约：① token 不交给 `py-agent`，由 go-web 按调用逐次签发并代理执行；② token 交给 `py-agent`，明确撤销延迟上界为 token TTL 并把 TTL 纳入安全验收。

### 8. 资源范围解析必须带会话上下文（唯一入口）

唯一的范围解析入口接收身份与会话上下文，而不是只接收用户：

```text
ResolveQQResourceScope(
    channel,               // "qq"
    externalUserID,        // QQ 身份
    ConversationContext{   // 当前会话
        Kind:            Private | Group,
        ExternalGroupID: "...",   // 仅群聊
    },
) Resolution
```

`Resolution` 是**判定二态 + 三类服务端标签**，不是裸集合、也不是三态：

```text
Decision:      Granted | Denied{reason}   // 判定只有两个分支
Private:       []spaceID     // Granted 专有：该用户的私人空间（0 或 1）
CurrentTeam:   spaceID | none // Granted 专有：群聊为当前群绑定的空间；私聊恒为 none
OtherTeams:    []spaceID     // Granted 专有：该用户的其他活动团队空间
```

`Denied` 分支**不携带任何范围字段**，也不得降级为空 `Granted`（理由见背景第 2 条）。当前不需要第三种判定状态：非法输入按既有契约返回 `INVALID_ARGUMENT`，依赖不可用返回 `UNAVAILABLE`，两者都不是"范围解析结果"。

确定性规则：

- **私聊**：`Private` = 绑定用户的私人空间；`CurrentTeam` 恒为 none（私聊不得把任何团队标成"当前团队"）；
- **群聊**：`CurrentTeam` = **当前群**的活动绑定空间，且仅当该用户是它的活动成员；未绑定群 → `Denied`；其他群绑定的空间**不得**出现在 `CurrentTeam`；
- 用户属于其他团队空间时，它们只能出现在 `OtherTeams`（**带标签**），供编排层按生态指南 §7 的语义决定是否使用，而不得被当作"当前团队"；
- **标签不变量**：三类标签由服务端生成、互不重叠——`CurrentTeam` 不得同时出现在 `OtherTeams`，`Private` 不得出现在任何团队标签里；绑定目标必须是 `team` 空间，因此 private 空间**不可能**成为 `CurrentTeam`；调用方不能提交标签，也不能通过重新标记改变权限类别；
- `Denied` 的当前拒绝原因：用户未绑定、用户绑定已撤销、go-web 账号被禁用或删除、群未绑定、群绑定已撤销、不是当前空间的活动成员；
- `ListUserSpaces(userID)` 可以存在（供管理界面与对账），**但不得用于签发任何会话的 capability**；
- 多表读取必须使用**单条 JOIN 或一致性读事务**，不得在并发改绑时拼出"旧用户绑定 + 新群绑定"这类混合快照。

**关于范围宽窄的一处刻意保留**：生态指南 §7 允许私聊访问"其有权加入的团队空间"，也允许群聊使用"私人、当前团队或其他授权团队的知识"（由 Agent 选择、权限系统约束）。因此本 ADR 定义的是**带标签的最大信封**，而"某次回答实际取哪个子集"属于编排策略（P3.6）；若产品决定收紧为"私聊只允许私人空间、群聊只允许当前群绑定空间"，那是策略层的一处开关，不改变本 ADR 的解析结构。

**信封 → capability 的唯一链条（P3.6 实现，本 ADR 固定语义）**：

```text
最大资源信封（go-web 解析，带标签）
  ∩ 可信渠道策略（python-agent 提供结论，go-web 按标签屏蔽当前会话不允许的类别）
  ∩ 调用方请求的子集（Agent/Model 只能提出候选空间 ID）
  = 最终 capability 范围
```

配套的五条硬规则，用来保证与决策 6 的"Model 输出不参与权限判定"完全一致：

1. **标签只由 go-web 生成**：调用方不得提交、改写或重新标记标签，尤其不得把 `OtherTeams` 标成 `CurrentTeam`；
2. **Agent/Model 只能提议空间 ID**，不能提议标签、不能扩大信封；
3. **渠道策略可以按标签裁剪**：若渠道策略禁止群聊使用私人空间，即使 `Private` 在最大信封里，也不得进入最终 capability；
4. **每次签发都重新解析**：不得复用调用方缓存或上次的最大信封；
5. **出现任意越界 ID 时整体拒绝**，不静默裁剪。

### 9. 绑定写入主体与审计（P3.4 只开受信内部入口）

- P3.4 **只提供受信内部写入口**：已认证的 go-web 管理主体（管理员角色或运维工具）才能创建、撤销、改绑；每次写入记录 actor、来源与原因；
- **QQ 身份所有权证明**（如何证明操作者持有该 QQ 号）与**群管理权证明**依赖 `py-agent` 的渠道事实，**推迟到 P3.6**；在此之前禁止把"操作者声称"当作证明，也禁止任何匿名外部入口；
- `py-agent` **不得**直接写绑定表、不得持有 go-web 数据库凭据；它与 go-web 的服务认证方案随换取入口一并在 P3.6 决定（本包不实现服务间认证）；
- 未授权写入必须整体失败，且不改变活动绑定与审计历史。

### 数据结构（P3.4 实施）

| 对象 | 形态 | 关键约束 |
| --- | --- | --- |
| QQ 用户绑定 | `qq_user_bindings`（channel、bot_id、external_user_id、user_id、`revoked_at`、actor/source/reason） | 每个 Bot 侧身份至多一条活动绑定；历史行保留 |
| 群空间绑定 | `group_space_bindings`（channel、bot_id、external_group_id、space_id、`revoked_at`、actor/source/reason） | 活动群与空间分别按 `(channel, bot_id, external_group_id)`、`(channel, bot_id, space_id)` 唯一；目标空间必须是 `team` |
| 审计 | `space_audit_events`（实体、动作、身份键、前后目标、actor/source/reason、时间） | 只追加；与对应绑定或成员变更同事务提交 |
| 团队空间成员 | 复用 `space_members` | 复用既有延迟约束触发器；创建空间与 owner 成员须同一事务 |

## 结果与权衡

- **收益**：资源权限始终在 go-web 内闭合；QQ 侧变化不需要索引重建；绑定表带渠道维度，未来加渠道是加行而不是改模型；1 群 : 1 空间的约束把"空间迁移"变成一次可审计的改绑；上下文解析让"当前团队"成为可归因的标签，而不是调用方的自称。
- **代价**：两套事实源必须在编排层求交，`py-agent` 必须传递渠道结论而不能只传 QQ 号；"入群不等于获得资源"需要显式授予，带来运营成本（用最小权限换取）；**撤销存在 `TTL + leeway` 的残留窗口**，P3.6 必须二选一处理；P3.4 期间所有绑定写入都依赖受信操作者，自助绑定要等 P3.6。
- **明确不做**：capability 换取端点与服务间认证（P3.6）；聊天记录域、知识晋升与共享记录（阶段四）；多渠道适配器（只保留 `channel` 扩展点）；不把休眠的 `chat_groups` 当成 QQ 群模型；不新建第二套空间/成员体系。

## 验证：P3.4 的最低验收条件

1. **私聊信封**：绑定用户的私聊解析 `Private` 为其私人空间，`CurrentTeam` 为空；其他团队空间只能以 `OtherTeams` 出现，不得被标成当前团队。
2. **群聊信封**：群聊解析的 `CurrentTeam` 必须是**当前群**活动绑定的那个空间，且该用户是它的活动成员；其他群绑定的空间不得出现在 `CurrentTeam`；未绑定群 → `Denied`。
3. **资源侧不变量**：创建用户绑定、创建群空间绑定、处理入群事实都**不会**新增 `space_members` 行（断言表行数不变）。
4. **拒绝而非空范围**：未绑定用户、未绑定群、已撤销绑定、go-web 账号被禁用或删除、非活动成员都返回 `Denied`；`Denied` 不得被折叠为空集合——空集合在文档契约里等价于"仅 `authenticated_public`"，因此未绑定身份**不能**取得公开文档、私人空间、团队空间或任何文档级范围。
5. **撤销生效点**：解绑、成员撤销、群改绑之后，**下一次**解析立即得到新结果，且 generation 与 alias 不变（无索引重建）。
6. **包含规则**：请求空间必须是本次上下文解析结果的子集，越界整体拒绝，不静默裁剪。
7. **并发与一致性**：用户绑定、群绑定、空间反向绑定的活动唯一性在并发事务下成立；改绑提交前读旧值、提交后读新值，不出现混合快照；历史行可审计。
8. **写入授权**：未授权主体不能创建、撤销或改绑关系；失败写入不改变活动绑定与审计历史。
9. **标签不变量**：`Private` / `CurrentTeam` / `OtherTeams` 由服务端生成且互不重叠——`CurrentTeam` 不得同时出现在 `OtherTeams`，`Private` 不出现在团队标签中，private 空间不能成为 `CurrentTeam`；调用方无法通过提交或重新标记标签改变权限类别（越权提交被整体拒绝）。

## P3.6 再验收（不属于 P3.4）

- **范围链**固定为 `最大资源信封 ∩ 可信渠道策略 ∩ 调用方请求子集 = capability 范围`；
- 渠道允许、资源拒绝；资源允许、渠道拒绝（两类反例）；
- **`Denied` 的最终处置**：任意 `Denied` 解析结果都**不得签发 capability、不得降级为空范围、不得发起 `mixin-search` 调用**，对应审计记录必须保留稳定的拒绝原因（否则适配层仍可能把 `Denied` 错误映射成"仅公开文档"检索）；
- capability 签发、TTL 与撤销残留窗口的处置结论；
- `py-agent` 不能自行扩大空间 allow-list，也不能复用缓存的最大信封；
- 双端 golden vector 与审计字段（含"代表哪个最终用户"）。

## 复审条件

- 出现同一自然人跨渠道（QQ 之外的渠道）统一身份的产品需求；
- 需要把群成员自动同步为空间成员（策略变更，需重新论证与最小权限的关系）；
- 产品决定收紧"私聊/群聊可用的最大信封"（策略变更，需同步生态指南 §7）；
- P3.6 的换取协议要求 go-web 暴露范围解析的其它形态（批量解析或预计算）；
- 出现跨 go-web 实例并发写入绑定的部署形态（需要分布式一致性讨论）。

## P3.4 实施约定（v2.2）

- QQ Bot 的用户与群身份均按 `(channel, bot_id, external_id)` 区分。相同数字 ID 在不同 Bot 下属于不同身份；QQ OAuth 等其他提供方必须使用独立提供方/实例命名空间。
- P3.4 的受信写入口是 `apps/gin-backend/cmd/tools/spacectl/`。不新增绑定 HTTP 接口。命令将 `source` 固定为 `spacectl`，操作者必须提供 `actor` 与 `reason`；业务校验、写入及审计由 `space/application` 与 PostgreSQL 仓储负责。
- 用户绑定、群绑定、撤销和改绑的活动记录及审计事件在同一事务提交。改绑关闭旧行并新增活动行，失败时完整回滚；QQ 绑定或群绑定不写入 `space_members`。
- `ResolveQQResourceScope` 只接收 Bot/QQ 身份与会话上下文，由 go-web 查出 go-web 用户，不接受调用方提供 `user_id`。`Denied` 不含范围；`Granted` 的 `Private`、`CurrentTeam`、`OtherTeams` 标签只由服务端生成。
- 请求子集校验对完整资源信封执行：出现任一越界 ID 或服务端标签重叠时整体拒绝。P3.6 再按可信会话策略限制首发私聊使用 `Private`、群聊使用 `CurrentTeam`；不把该策略固化进 P3.4 解析器。
- P3.4 的表结构进入 `deployments/postgresql/sql/service/document/schema_init.sql`；该脚本用于全新开发数据库初始化，仓库当前不维护数据库升级迁移 runner。

## 决策（v3，2026-09-26）

本节按 [ADR-017](./017-source-owned-document-and-search-services.md) 的责任划分重写身份与所有权。v2/v2.2 中与之冲突的表述（尤其是 `qq_user_bindings` 与 `go-web` 持有资源侧解析）不再有效；判定结构、标签不变量与验收语义继续沿用。

### 1（v3）. 事实源与职责边界

- **QQ Bot 身份命名空间、QQ 用户与群的会话事实、渠道权限、QQ 原始消息/文件/撤回事实**：`py-agent` 是事实源。它只证明“这个身份在当前会话中是什么、渠道允许什么”，不判定资源权限。
- **正式文档、版本、知识空间、空间成员、群空间绑定、资源权限、审计、文档 Outbox、资源访问主体登记**：**文档服务**是事实源。资源权限判定在文档服务内闭合，不依赖其他系统回传。
- **Web 账号与 Web 界面**：`go-web` 是事实源。它是 Web 侧业务入口，通过文档服务接口操作正式文档与空间，不直接读写文档服务业务表。
- **索引与控制状态**：文档检索服务与 QQ 检索服务各自拥有自己来源的派生索引与控制表；它们不解释身份，只在查询前校验已签发的范围 capability。
- `mixin-search` 在迁移期内仍是既有文档/聊天语料的实现，但不再是两个来源的目标归属地（见 ADR-017 决策 5）。

### 2（v3）. 资源访问主体，以及为什么不再需要 QQ↔Web 用户绑定

文档服务拥有 `document_service.access_subjects`：主体键是带命名空间的规范字符串。

```text
web:user:<web user id>              // go-web 账号，由 go-web 断言
qq:user:<bot_id>/<external_user_id> // QQ 身份，按 Bot 隔离，由 py-agent 断言
```

- **两类身份不以互相绑定为前提**：QQ 主体可以是团队空间的活动成员而不存在对应 Web 账号；Web 主体也不需要先绑定 QQ 身份才能操作文档。ADR-017 的责任划分要求资源权限判定只使用文档服务自己的事实，绑定关系会把输入放回另一个服务。
- 每个请求只能声明自己命名空间内的主体（`go-web` → `web:*`，`py-agent` → `qq:*`）；跨命名空间声明在校验阶段整体拒绝。
- 主体由文档服务登记：受信入口首次声明某主体时，文档服务在一次事务中登记主体（`subject_type`、`origin`、`display_name`）并写入审计（动作 `register`）。登记只表示“这个主体出现过”，**不产生任何空间成员或文档权限**。
- 主体可以停用（`active = false`）；停用后解析返回 `Denied{subject_inactive}`。
- **取消 `qq_user_bindings`**：v2 用“QQ 身份绑定到 go-web 用户”推出私聊的私人空间。v3 不再把资源权限建立在跨系统绑定上。QQ 主体对空间的访问一律来自文档服务自己的活动 `space_members` 记录，即显式授予。
- 因此私聊的资源信封是“该主体已是活动成员的空间”，而不是“某个 Web 用户的私人空间”。这不是放宽限制：主体仍是活动成员才进信封，`Denied` 仍然不降级为空范围。

### 3（v3）. 群与空间绑定属于文档服务

- `document_service.group_space_bindings` 是群↔空间的唯一事实源；绑定写入通过文档服务的 `BindGroupSpace` / `RevokeGroupSpace` 用例，不再由 `go-web` 直接写表。
- 基数与唯一性不变：同一 `(channel, bot_id)` 下，一个 QQ 群至多绑定一个 team space，一个 team space 至多被一个 QQ 群绑定；唯一性由数据库部分唯一索引保证。改绑 = 撤销旧绑定 + 新增活动绑定，历史行保留。
- 绑定目标必须是 `team` 空间，由数据库触发器与用例共同保证；private 空间不可能成为 `CurrentTeam`。
- `actor` / `source` / `reason` 必填并进入审计；`source` 取值来自调用方与入口（`document-service-api`、`spacectl`、`go-web-api`），不再固定为 `spacectl`。

### 4（v3）. 资源范围解析的入口与判定

唯一入口是文档服务的 `ResolveAccessScope`。它只接收受信入口已经证明的主体键与会话上下文：

```text
ResolveAccessScope(
    subject_key,           // 受信入口证明：web:user:42 或 qq:user:10001/20002
    ConversationContext{ Kind: Private | Group, ExternalGroupID },
) Resolution
```

`Resolution` 结构、`Granted`/`Denied` 二态、`Private`/`CurrentTeam`/`OtherTeams` 三类服务端标签、标签不重叠、越界整体拒绝、空范围等价“仅 `authenticated_public`”的语义**与 v2 完全一致**。变化只有两处：

1. 判定的输入是文档服务自己的主体与空间事实，不再有跨服务的用户绑定查询；
2. 私聊的 `Private` 是“该主体拥有的私人空间（若已创建）”，`OtherTeams` 是“该主体的其他活动团队空间”；群聊的 `CurrentTeam` 仍是当前群的活动绑定空间且主体是它的活动成员。

`Denied` 原因集合更新为：`subject_unknown`、`subject_revoked`、`subject_inactive`、`group_unbound`、`group_binding_revoked`、`not_space_member`、`session_context_invalid`。非法输入仍按既有契约返回 `INVALID_ARGUMENT`，依赖不可用返回 `UNAVAILABLE`，两者都不是“范围解析结果”。

### 5（v3）. 渠道权限 ∩ 资源权限

不变：`最大资源信封（文档服务解析，带标签） ∩ 可信渠道策略（py-agent 结论，调用方按标签裁剪） ∩ 调用方请求子集 = 最终 capability 范围`。

变化：交集的计算者是**发起该次会话的调用方**（`py-agent` 代表 QQ 会话，`go-web` 代表 Web 请求），不再由 `go-web` 独占。文档服务不解释渠道策略；检索服务不解释任何权限，只做包含校验。

### 6（v3）. 撤销的生效点

不变：解绑、移出空间、改绑在下一次解析与下一次签发立即生效，且不推进索引 generation、不切换 alias；已签发的范围 capability 在 `TTL + leeway` 内仍有效，ADR-017 的 capability 默认 `2m` 有效期与 `30s` 时钟偏差沿用。不声称对已签发 capability 实现即时撤销。

### 7（v3）. 写入主体与审计

- 绑定、成员与空间的写入主体是**受信服务身份**：文档服务只接受持有边界密钥的已登记调用方，且要求对应 scope（`space.admin`）。每次写入记录 `actor`、`source`、`reason`、`request_id` 与 `occurred_at`。
- `spacectl` 仍是运维入口，但它改为调用文档服务接口（scope `space.admin`），不再直接写表，也不再是唯一的写入来源。
- 未授权写入整体失败，且不改变活动绑定、成员与审计历史。
- **QQ 身份所有权证明**（如何证明操作者持有该 QQ 号）与群管理权证明仍依赖 `py-agent` 的渠道事实，属于后续接入工作；在此之前禁止把“操作者声称”当作证明，也禁止任何匿名外部入口。

### 数据结构（v3）

| 对象 | 形态 | 关键约束 |
| --- | --- | --- |
| 资源访问主体 | `document_service.access_subjects`（`subject_key`、`subject_type`、`origin`、`display_name`、`active`） | 主体键形如 `web:user:42`；`subject_key LIKE origin \|\| ':%'` 保证前缀与 origin 一致 |
| 知识空间 | `document_service.knowledge_spaces`（`owner_subject_key`、`space_type`、`name`） | 每个主体至多一个 private 空间 |
| 空间成员 | `document_service.space_members`（`space_id`、`subject_key`、`member_role`、`revoked_at`） | 每个空间必须有一个活动 owner 成员；private 空间只能有 owner；由延迟约束触发器保证 |
| 群空间绑定 | `document_service.group_space_bindings`（`channel`、`bot_id`、`external_group_id`、`space_id`、`revoked_at`、actor/source/reason） | 活动群与空间分别按 `(channel, bot_id, external_group_id)`、`(channel, bot_id, space_id)` 唯一；目标空间必须是 `team` |
| 审计 | `document_service.space_audit_events`（实体、动作、主体键、身份键、前后目标、actor/source/reason/request_id、时间） | 只追加；与对应主体、绑定或成员变更同事务提交 |
| ~~QQ 用户绑定~~ | **已取消** | ADR-017 不再要求资源权限依赖跨系统用户绑定 |

### 验收（v3，取代 v2 的最低验收条件）

1. **主体登记**：受信入口首次声明主体时登记成功并写审计；跨命名空间声明（`py-agent` 声明 `web:*`，或反之）整体拒绝；未持有 boundary key 的调用被拒绝。
2. **私聊信封**：主体的私人空间出现在 `Private`；`CurrentTeam` 为空；其他团队空间只以 `OtherTeams` 出现。
3. **群聊信封**：`CurrentTeam` 必须是当前群活动绑定的空间且主体是它的活动成员；未绑定群 `Denied{group_unbound}`；其他群绑定空间不得出现在 `CurrentTeam`。
4. **资源侧不变量**：登记主体、创建群空间绑定、处理入群事实都**不会**新增 `space_members` 行（断言表行数不变）。
5. **拒绝而非空范围**：未知或停用主体返回 `Denied`，且 `Denied` 不携带范围；空信封只能来自“主体有效但当前没有活动成员空间”，不得折叠为拒绝以外的语义。
6. **撤销生效点**：成员撤销、群改绑之后**下一次**解析立即得到新结果，且 generation 与 alias 不变。
7. **包含规则**：请求空间必须是本次上下文解析结果的子集，越界整体拒绝，不静默裁剪。
8. **并发与一致性**：主体登记、群绑定的活动唯一性在并发事务下成立；改绑提交前读旧值、提交后读新值，不出现混合快照。
9. **标签不变量**：三类标签由服务端生成且互不重叠；调用方无法通过提交或重新标记改变权限类别。
10. **事务性**：绑定、成员、主体登记与审计在同一事务提交；任一步失败时全量回滚，审计不残留。

### 复审条件（v3 补充）

- 出现“同一自然人在 QQ 与 Web 必须共享资源权限”的产品需求（那会重新引入跨系统身份绑定，需要重新论证最小权限）；
- 需要允许一个 QQ 主体同时绑定多个群空间（当前 1 群 : 1 空间不变）；
- 需要把群成员自动同步为空间成员（策略变更）。