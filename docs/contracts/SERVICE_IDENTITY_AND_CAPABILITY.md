# 服务身份与权限凭证契约

> 状态：已落地（`packages/serviceauth`，2026-09-26 起为 ADR-017 三个服务的共享边界）
> 日期：2026-09-26
> 相关决策：[ADR-017](../adr/017-source-owned-document-and-search-services.md)、[ADR-016](../adr/016-qq-identity-and-knowledge-space-mapping.md) v3
> 适用范围：文档服务、文档检索服务、QQ 检索服务，以及作为调用方的 `go-web` 与 `py-agent`
> 实现：`packages/serviceauth`（`identity.go`、`credential.go`、`boundary.go`、`transport.go`）

## 1. 目的

本契约固定两类凭证的线上格式与校验规则，使三个新服务能用同一套边界机制互相识别，并把“谁在调用”“代表谁”“能访问多大范围”三件事分开表达。

它同时回答一个此前没有定下来的问题：**文档服务如何判定资源权限，而不依赖其他服务的用户表**。答案是文档服务持有自己的资源访问主体登记，调用方只能声明自己命名空间内的主体。

## 2. 调用方、audience 与 scope

**调用方（caller）**是固定字符串，新增调用方是契约变更，不是配置变更：

| caller | 可声明的主体命名空间 | 说明 |
| --- | --- | --- |
| `go-web` | `web:*` | Web 账号与 Web 侧业务入口 |
| `spacectl` | `web:*` | `go-web` 的运维 CLI 入口，审计上与 `go-web` 区分 |
| `py-agent` | `qq:*` | QQ Bot 运行时，持有 QQ 身份与会话事实 |
| `document-service` | 无 | 文档服务自身；作为调用方时不再声明新主体 |

**audience** 按服务划分，一个服务一个值：

| audience | 服务 | 说明 |
| --- | --- | --- |
| `document-service` | 文档服务 | 业务命令与详情读取 |
| `document-search` | 文档检索服务 | 正式文档索引写入与检索 |
| `qq-search` | QQ 检索服务 | QQ 原始内容索引写入与检索 |

**scope** 决定可调用哪些 RPC，不决定数据范围：

| scope | 含义 | 持有者 |
| --- | --- | --- |
| `document.write` | 创建、修改草稿、发布、撤销、归档、删除正式文档 | `go-web`、`spacectl` |
| `document.read` | 文档详情与列表读取 | `go-web`、`py-agent` |
| `space.admin` | 空间、成员、群空间绑定管理 | `go-web`、`spacectl` |
| `access.resolve` | 请求资源范围解析（本身不授予任何资源） | `go-web`、`py-agent` |
| `document-event-reader` | 读取文档服务 Outbox 事件流 | 文档检索服务 |
| `document-index-writer` | 写入与重建正式文档索引 | 文档检索服务的消费身份 |
| `document-searcher` | 查询正式文档索引 | 查询调用方（持文档服务签发的 capability） |
| `qq-index-writer` | 写入与重建 QQ 原始内容索引 | `py-agent` |
| `qq-searcher` | 查询 QQ 原始内容索引 | 查询调用方（持 `py-agent` 签发的 capability） |

未在方法策略表登记的 RPC 一律拒绝（失败关闭）；新增 RPC 必须同时登记策略与 scope。

## 3. 两类凭证

两类凭证共用同一种信封，靠 payload 的字段集合与 audience 区分：

```text
token = base64url(payload_json) "." base64url(HMAC-SHA256(key, base64url(payload_json)))
```

刻意不包含算法头：唯一可接受的算法是与边界密钥绑定的 HMAC-SHA256，调用方没有可协商或可降级的空间。

传输位置：

| 位置 | 携带内容 |
| --- | --- |
| `authorization: Bearer <token>` | **服务身份断言**：调用方证明自己是谁、持有哪些 scope、代表哪个主体与会话 |
| `x-resource-capability: Bearer <token>` | **资源范围 capability**：事实源已经判定并签发的范围 |

两者分开是刻意的：断言说明“谁在问”，capability 说明“问的人已经被授予了什么”。检索服务只依据 capability 授权查询，不接受调用方自述的范围。

### 3.1 服务身份断言 payload

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `version` | int | 固定为 `1`；其他版本按未知声明拒绝 |
| `caller` | string | 必须等于服务端允许列表中的已登记调用方 |
| `audience` | string | 被调服务的 audience；不符按未认证拒绝 |
| `scopes` | string[] | 已登记 scope；未登记的值整体拒绝 |
| `subject_key` | string | 可选；调用方代表的主体，必须落在自己的命名空间内 |
| `conversation` | object | 可选；`{kind: private\|group, external_group_id}`，仅供资源范围解析 |
| `actor` | string | 可选；操作者标识，只用于审计 |
| `request_id` | string | 可选；关联 id，只用于审计与去重提示 |
| `channel` / `bot_id` / `external_user_id` / `external_group_id` | string | 可选；QQ 渠道上下文，供文档服务判定会话 |
| `issued_at` / `expires_at` | int64 | Unix 秒；`expires_at` 必须晚于 `issued_at` |

断言由调用方用边界密钥自签。**它只证明调用方身份，不证明资源权限**：`document.write` 允许发起写入命令，命令能否作用于某个文档仍由文档服务按自己的事实判定。

### 3.2 资源范围 capability payload

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `version` | int | 固定为 `1` |
| `issuer` | string | 签发方：正式文档范围是 `document-service`，QQ 渠道范围是 `py-agent` |
| `audience` | string | 被调检索服务 |
| `scopes` | string[] | `document-searcher` 或 `qq-searcher` |
| `subject_key` | string | 该范围属于哪个主体；范围与主体一起传递，不能拆分重放 |
| `private_space_ids` / `current_team_space_id` / `other_team_space_ids` | string[] / string | 文档服务解析出的三类标签（与 ADR-016 v3 §决策 4 一致） |
| `allowed_space_ids` / `allowed_document_ids` | string[] | 文档级显式授予范围 |
| `authenticated_public` | bool | 是否允许命中 `authenticated_public` 文档 |
| `bot_ids` / `conversation_ids` / `external_group_ids` | string[] | QQ 渠道范围（`py-agent` 签发） |
| `issued_at` / `expires_at` | int64 | Unix 秒；默认有效期 `2m` |

**`Denied` 不签发 capability。** 解析结果是 `Denied` 时不得签发、不得降级为空范围、不得发起检索调用；`Denied` 不携带任何范围字段。

### 3.3 密钥

- 边界密钥至少 32 字节，由 `deployments/bootstrap.ps1` 生成到 `deployments/secrets/mixin_search_capability.key`；开发环境各服务共用同一份边界密钥，生产部署应按服务拆分密钥，本契约不改变信封格式。
- 密钥不入库、不入镜像（`.gitignore` / `.dockerignore` 的 `*.key`），由 Compose 只读挂载。
- 缺少密钥或密钥过短时服务**拒绝启动**，不退化为接受匿名调用。
- 真实终端用户不持有边界密钥：他们只使用短时 capability。

## 4. 校验规则

服务端按顺序执行，任一步失败即拒绝：

1. `authorization` 恰好一个值且使用 `Bearer` 方案；
2. 签名与边界密钥匹配（常量时间比较）；
3. payload 可解析、**无未知字段**、`version` 受支持、必填声明非空、`expires_at > issued_at`；
4. `audience` 等于本服务的 audience；
5. `caller`（断言）或 `issuer`（capability）已登记；
6. **capability 的 `issuer` 必须是该 audience 的事实源**：`document-search` 只接受 `document-service` 签发，`qq-search` 只接受 `py-agent` 签发；没有登记签发方的 audience 一律拒绝，不退化为"任何已登记调用方都可签发"。签名有效只证明是谁签的，不证明签发方拥有该范围所描述的事实，因此签发与校验两端都执行这条规则（`SealCapability` 同样拒绝错误组合）；
7. 每个 scope 已登记；
8. `subject_key` 非空时必须落在调用方自己的命名空间内，否则整体拒绝；
9. 当前时间不超过 `expires_at + 30s`，且 `issued_at` 不晚于当前时间 `+ 30s`；
10. 该方法的策略要求的 scope 全部持有；
11. 查询类 RPC：`x-resource-capability` 恰好一个值、可校验、`subject_key` 非空、scope 匹配；
12. 请求范围是已授予范围的**子集**，否则整体拒绝。

`packages/serviceauth` 刻意对未知字段严格拒绝：静默忽略一个未知声明，正是“被悄悄扩大的范围”得以通过的路径。

失败映射：

| 场景 | gRPC 状态 | HTTP 状态 |
| --- | --- | --- |
| 缺失、格式错误、签名无效、过期、audience 不符 | `UNAUTHENTICATED` | 401 |
| 缺少所需 scope、命名空间越界、请求范围越界 | `PERMISSION_DENIED` | 403 |
| 服务自身配置无效（密钥缺失等） | `INTERNAL` | 500 |

`UNAUTHENTICATED` 与 `PERMISSION_DENIED` 对调用方都是**不可重试**；`INTERNAL` 表示服务配置问题，不应被当作调用方错误处理。

## 5. 范围规则：只允许缩小

- 请求范围是已授予范围的子集（含空集，即只检索 `authenticated_public`）→ 允许；
- 请求中出现任何未授予的空间、文档或会话 → **整体拒绝**；
- 不允许“静默裁剪后按子集检索”：那会让调用方探测它无权读取的标识符；
- 标识符在签发与校验两侧都规范化（去空白、去重、排序），因此集合顺序或重复项不改变判定。

## 6. 文档服务的可信入口

- 文档服务只接受 `go-web`、`py-agent`、`document-service`、`spacectl` 四类调用方；允许列表可由配置收窄，不能超出登记集合。
- 调用方只能声明自己命名空间内的主体。文档服务不接受、也不信任请求体中的任何身份字段；主体只从已校验的断言读取。
- 主体首次出现时由文档服务登记并写审计；登记不产生任何空间成员或文档权限。
- 探针方法（`grpc.health.v1.Health/Check`、`/Watch`）刻意不要求凭据，且只暴露状态；除此之外没有匿名入口，`reflection` 默认关闭。

## 7. 事件传递

- 文档服务把业务变更与事件写入同一事务；事件流为 `ListDocumentEvents`，要求 `document-event-reader` scope，按 `sequence` 游标读取，消费者自持游标并容忍重复投递。
- QQ 来源事件由 `py-agent` 通过 `qq-search` 的 `IndexQQSourceEvent` 推送，要求 `qq-index-writer` scope。
- 事件契约与凭证契约正交：事件内容由 `packages/proto` 的版本化消息定义，凭证只决定“谁可以投递、谁可以读取”。

## 8. 尚未建立的部分

- **按服务拆分的独立密钥**：当前开发基线各服务共用一份边界密钥，生产部署应拆分并按服务轮换；
- **传输加密**：仍为明文 gRPC，仅绑定回环地址；mTLS 属于在线暴露前的加固项；
- **按主体/会话的配额**：当前没有按主体的限流，只有按调用方的进程内约束（`mixin-search` 侧既有实现保留）；
- **QQ 身份所有权证明**（如何证明操作者持有该 QQ 号）仍属后续接入工作。
