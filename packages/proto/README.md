# RPC 协议包

此目录集中维护 monorepo 内跨应用使用的 RPC 协议，是协议定义的事实源。

- `mixin-search/v1`：版本化文档索引、激活、完整访问快照更新、删除、状态查询和授权范围搜索契约；
- `packages/gen` 集中保存由协议生成的共享 Go 代码；
- 业务实现、运行时配置和应用私有类型不进入此目录；
- 协议变更需要保持明确版本，并由所有消费方共同验证。

协议集中管理不改变应用边界：`apps/mixin-search` 负责实现检索服务，各应用通过 `packages/gen` 中的生成代码消费协议。

## mixin-search/v1 P1.5 定型语义

P1.5 直接更新尚未接入正式流量的 v1 契约，不保留旧字段或旧生成代码兼容：

- 新增 `UpdateDocumentAccess`；
- `IndexDocumentVersion`、状态响应和块元数据统一使用 `owner_space_id`，不再使用含义模糊的 `space_id`；
- `activation_revision`、`access_revision`、`lifecycle_revision` 分别保护内容激活、访问快照和删除/重新发布周期；
- `IndexDocumentVersion` 与 `ActivateDocumentVersion` 携带 lifecycle fence，新建文档初值允许为 `0`；
- `UpdateDocumentAccess` 携带 access/lifecycle revision、`authenticated_public` 和完整 `granted_space_ids` 快照；
- 两个删除 RPC 携带 lifecycle revision；`DeleteDocument` 按 `document_id + lifecycle_revision` 幂等并保留墓碑和修订高水位；
- 搜索按公开访问、允许空间与 `{owner_space_id} ∪ granted_space_ids` 相交、`allowed_document_ids` 显式命中执行 OR 授权；两个 allow-list 可以为空，此时只返回公开文档；
- 低修订返回 `FAILED_PRECONDITION`，同修订同 payload 幂等，同修订不同 payload 冲突；删除后的旧事件不能复活文档，更高 lifecycle 下先索引再以不回退的 activation revision 激活才能重新发布。

这些字段表达由 `go-web` 决定的事实和权限快照；检索服务不解释用户或空间成员身份。P1.5 只验证协议和内存/测试适配器，不接入正式 RPC、Outbox 消费/重放或 Qdrant 生产级 ACL 下推。

## 生成一致性

修改 `mixin-search/v1/mixin-search.proto` 后必须重新生成 Go 代码，并运行 `packages/proto/verify-generated.ps1`。

该检查会在临时目录重新生成代码并逐文件比较 SHA-256，发现协议与提交的生成物不一致时直接失败。
