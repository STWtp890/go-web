# QQ 主体与团队空间运维

> 状态：**已按 ADR-017 v2 重写**。原文描述的 P3.4 模型（`qq_user_bindings`、把 QQ 身份绑定到 go-web 用户、`gin-backend` 直连数据库的 `spacectl`）已被取代：**不存在 QQ 身份与 Web 账号的绑定表**，空间与成员的管理入口在文档服务侧。
> 相关决策：[ADR-017](../adr/017-source-owned-document-and-search-services.md)、[ADR-016 v3](../adr/016-qq-identity-and-knowledge-space-mapping.md)
> 相关契约：[文档服务 v1](../contracts/DOCUMENT_SERVICE_V1_CONTRACT.md) §4、[服务身份与权限凭证](../contracts/SERVICE_IDENTITY_AND_CAPABILITY.md)

## 1. 现在的模型

- 文档服务持有**自己的资源访问主体登记** `document_service.access_subjects`，主体键是带命名空间的规范字符串：`web:user:42`、`qq:user:10001/20002`。
- Web 请求与 QQ 请求分别通过受信入口提供身份材料；**两类身份不以互相绑定为前提**。QQ 主体可以是团队空间的活动成员而不存在对应 Web 账号；Web 主体也不需要先绑定 QQ 身份。
- QQ 主体对空间的访问一律来自文档服务自己的**活动 `space_members` 记录**（显式授予）。入群不会自动成为空间成员。
- 群与空间的绑定是 `document_service.group_space_bindings`，按 `(channel, bot_id, external_group_id)` 记录，绑定目标必须是 `team` 空间；同一 Bot 下一个空间只能绑定一个群。

## 2. 管理入口

唯一入口是文档服务的运维 CLI（经服务接口写入，不直连数据库）：

```powershell
cd apps/document-service
go run ./cmd/document-service-spacectl `
  -endpoint 127.0.0.1:18081 `
  -capability-key-file ../../deployments/secrets/mixin_search_capability.key `
  -subject web:user:42 -actor admin@example.com -reason "create the team space" `
  create-team -name "Team A"
```

- `-subject` 是断言代表的主体，也是 `CreateTeamSpace` 记录的空间 owner；
- `-actor` 与 `-reason` 必填并进入审计（`document_service.space_audit_events`）；
- 子命令 `add-member` / `revoke-member` / `bind-group` / `revoke-group` / `list-subjects` / `get-space` 同理。

旧 `apps/gin-backend/cmd/tools/spacectl` 与其直连 `public.*` 表的路径已在阶段 C 删除。

## 3. 资源范围解析

`ResolveAccessScope(subject_key, conversation)` 由文档服务实现，返回：

```text
Decision:      Granted | Denied{reason}
Private:       []spaceID       // 该主体拥有的私人空间（0 或 1）
CurrentTeam:   spaceID | none  // 群聊为当前群绑定的空间；私聊恒为 none
OtherTeams:    []spaceID       // 该主体的其他活动团队空间
member_space_ids: 服务端重算的并集
```

- 三类标签只由服务端生成、互不重叠；调用方不能提交或改写标签。
- 群聊的 `CurrentTeam` 必须是**当前群**活动绑定的空间，且主体是它的活动成员；其他群绑定的空间只能出现在 `OtherTeams`。
- 群未绑定 → `Denied{group_unbound}`；绑定已撤销 → `Denied{group_binding_revoked}`；不是活动成员 → `Denied{not_space_member}`。
- `Denied` 不携带任何范围，也不得降级为空 `Granted`——在文档契约里空范围等价于「仅 `authenticated_public`」，降级会让未授权主体读到公开文档。
- **渠道权限不在文档服务**：QQ 渠道结论由 `py-agent` 判定；检索侧最终范围是「文档服务给出的最大资源信封 ∩ 渠道策略 ∩ 调用方请求子集」。

## 4. 检索 capability 的签发

需要检索时由文档服务 `IssueSearchCapability` 解析范围并签发 audience 为 `document-search` 的短时 capability；`Denied` 时不签发任何 token。检索服务离线校验，并且**只接受 `document-service` 签发的该 audience capability**（签发方规则由 `packages/serviceauth` 在签发端与校验端同时强制）。

## 5. 验收

- 端到端：`deployments/verify-source-owned-services.ps1`（含越界请求整体拒绝、跨 audience capability 被拒）。
- 真实 Web 链路：`deployments/verify-stage-a-web.ps1`（含主体隔离与非所有者拒绝）。
- 数据库隔离：`deployments/postgresql/verify-service-isolation.ps1`。
