# 阶段 1 破坏性改造实施方向

> 状态：历史决策记录；生产式迁移、兼容和回滚部分已由 [ADR-005](../adr/005-development-baseline-over-production-migration.md) 取代
> 日期：2026-09-11
> 文档职责：保留阶段 1 领域模型审计依据；第 7-9 节不再作为当前实施门禁
> 目标：以长期系统健康性优先，允许内部数据库、Go 包、缓存和未发布 RPC 契约发生不兼容变化。

## 1. 审计结论

当前 Markdown 能力已经具备稳定的 HTTP 行为，但内部模型不适合作为长期知识库内核：

- markdowns 同时承担文档身份、当前版本、权限、列表投影和 BM25 索引载体；
- markdown_contents 只有当前正文，PUT 会覆盖内容，无法追踪版本；
- 删除会软删除元信息并物理删除正文，无法支撑回收站、恢复和索引重建；
- cache:markdown:* 直接缓存旧表形状，领域模型变化会继续放大耦合；
- ServiceMarkdown 同时被搁置的 Chat 存储代码复用，连接所有权不清晰；
- PostgreSQL 初始化脚本只在空数据卷执行，缺少已有数据库的有序升级机制；
- BM25 校验脚本和索引名称硬编码到 markdowns.search_text；
- 当前 mixin-search/v1 只表达单一 space_id，尚不能完整表达公开访问、跨空间授权和独立 ACL 修订；
- 删除 RPC 没有生命周期修订号，必须依靠严格事件顺序才能避免旧删除覆盖新发布。

因此阶段 1 不采用长期双写旧表的方案，而采用一次受控停写窗口完成领域内核切换。

## 2. 方案审计

| 方案 | 优点 | 主要代价 | 结论 |
|---|---|---|---|
| A. 旧表增列并继续演化 | 初期修改少 | 继续混合身份、版本、权限和搜索职责 | 拒绝 |
| B. 新旧表长期双写 | 可逐步切换、停机短 | 双写分叉、回填竞争、回滚方向不清晰 | 拒绝作为主路线 |
| C. 兼容视图与触发器模拟旧表 | 旧代码短期可运行 | 写视图、触发器和 ORM 行为难以推理 | 仅用于临时只读诊断 |
| D. 受控停写、一次迁移、领域内核重写 | 模型干净，不留下双写债务 | 需要维护窗口、完整备份、迁移演练和反向导出 | 采用 |
| E. 全量事件溯源重写 | 审计能力最强 | 对当前规模过度设计 | 暂不采用 |

## 3. 已批准方向

选择方案 D：受控破坏性切换（controlled clean break）。

破坏范围：

- 用新的文档领域表替换 markdowns、markdown_contents；
- 内部 Go 包由 service/markdown、model/orm/markdown 收敛到 document 领域；
- ServiceMarkdown 改为 ServiceDocument，Chat 代码改用独立 ServiceChat；
- 删除 cache:markdown:* 的运行时使用，启用版本化 cache:document:v1:*；
- BM25 从业务主表迁移到独立搜索投影；
- 在正式接入前允许继续修改 mixin-search/v1，补齐 ACL 与生命周期 fencing。

保持兼容的外部行为：

- /api/v1/protected/markdown/* 路径、请求和响应字段暂不改名；
- markdownId 继续等于新的 document_id；
- public/private、作者权限、分页、错误码和 CSRF 行为保持；
- Chat/WS 继续保持 404；
- 阶段 1 不向 mixin-search 发送真实 RPC。

## 4. 目标数据模型

### 4.1 业务事实表

| 表 | 责任与关键约束 |
|---|---|
| knowledge_spaces | 空间身份；private/team 类型；私人空间对所有者唯一 |
| space_members | (space_id, user_id) 唯一；owner/admin/member；支持撤销 |
| documents | 文档身份、所有者、归属空间、生命周期、活动版本及三类修订号 |
| document_versions | 不可变标题、摘要、正文、格式、SHA-256；(document_id, revision) 唯一 |
| document_access_policies | 当前 private/authenticated_public 策略及策略修订号 |
| document_grants | 对用户或空间的显式、可撤销授权 |
| document_search_projection | 当前有效版本的 BM25 读模型，不承担事实源职责 |
| index_outbox | 文档事务内的待处理事件；支持幂等、顺序、租约和重试 |
| document_index_states | 各版本及 ACL 在检索服务中的期望状态与实际状态 |

document_assets、document_relations 预留标识规则，但不进入阶段 1 第一批运行路径。

### 4.2 状态分离

文档治理状态：

~~~text
active -> archived -> trashed
archived -> active
trashed -> active
~~~

版本发布状态：

~~~text
draft -> published -> superseded
draft -> withdrawn
~~~

索引状态：

~~~text
pending -> indexing -> indexed -> active
                    \-> failed
active -> delete_pending -> deleted
~~~

版本发布与索引处理不得共用一个状态字段。Web 发布事务成功后文档即可读取；索引延迟或失败只影响检索新鲜度，不能回滚正式文档事实。

### 4.3 修订号

- activation_revision：切换有效内容版本时递增；
- access_revision：公开性或授权集合变化时递增；
- lifecycle_revision：归档、回收、恢复和永久删除时递增；
- aggregate_revision：同一文档内严格递增，形成 Outbox 事件顺序；
- 修订号只由 go-web 在持有文档行锁的事务中分配。

## 5. 权限与检索契约调整

文档始终归属一个真实空间，不采用把所有公开文档移动到虚拟公共空间的方案。公开性和持续共享属于访问策略，不改变文档归属。

在 mixin-search/v1 尚未正式接入前完成以下破坏性修订：

1. IndexDocumentVersion 将单一 space_id 明确为 owner_space_id；
2. 增加 UpdateDocumentAccess，携带 access_revision、authenticated_public 和授权空间快照；
3. SearchDocuments 使用 OR 授权：公开可读、允许空间相交或 allowed_document_ids 显式命中；
4. 生命周期删除请求携带 lifecycle_revision，旧修订不得覆盖更新状态；
5. 内容激活、ACL 更新和删除分别使用独立 fencing revision；
6. mixin-search 只执行 go-web 给出的访问快照，不解释用户或成员身份。

## 6. 原子写事务

创建：

~~~text
BEGIN
ensure private space
insert documents and revision=1
set active version and activation_revision=1
insert access policy
upsert BM25 search projection
insert version_published outbox
COMMIT
~~~

更新不再覆盖正文：

~~~text
BEGIN
select document for update
insert immutable revision+1
mark previous version superseded
switch active version and increment activation_revision
update access policy/revision when required
replace BM25 projection
insert ordered outbox events
COMMIT
~~~

删除：

~~~text
BEGIN
select document for update
set lifecycle_status=trashed
increment lifecycle_revision
remove BM25 projection
insert document_trashed outbox
COMMIT
~~~

回收站删除不物理删除版本正文。永久清除必须是独立管理动作，并在索引删除确认后执行。

## 7. 迁移与切换

### 7.1 迁移基础设施

- 建立 schema_migrations(version, checksum, applied_at)；
- 使用 PostgreSQL advisory lock 禁止并发迁移；
- 每个迁移在独立事务中执行；
- 校验已执行脚本 checksum，发现漂移立即失败；
- 空库初始化和已有数据库升级使用同一组迁移。

### 7.2 迁移演练

1. 从当前备份恢复临时实例；
2. 审计非法 UUID/作者、孤立正文、重复 ID、空正文和软删除残留；
3. 执行迁移；
4. 比较行数、权限、正文 SHA-256、时间字段和 BM25 查询样本；
5. 执行反向导出，确认能够恢复旧表形状；
6. 完整运行 HTTP 回归。

### 7.3 正式切换

1. 启用维护模式并停止文档写入；
2. 创建物理备份和逻辑备份；
3. 将旧表重命名为 legacy_markdowns_phase1、legacy_markdown_contents_phase1；
4. 建立新领域表并迁移全部有效数据和必要删除记录；
5. 构建 document_search_projection 及 BM25 索引；
6. 部署只读写新领域表的后端；
7. 清理旧缓存命名空间；
8. 执行数据库校验、HTTP 回归和回滚演练；
9. 解除维护模式。

旧表在一个稳定发布窗口内只读保留；窗口结束且反向导出再次通过后再物理删除。禁止正式运行时长期双写。

## 8. 回滚

正式切换前必须交付并验证：

- 数据库备份恢复命令；
- document -> legacy markdown 反向导出程序；
- 新写入文档投影回旧表的确定性规则；
- BM25 旧索引重建脚本；
- 缓存命名空间切换/清理脚本；
- 回滚后的 HTTP、权限、搜索和重复删除回归。

切换后如果已经产生新版本，不能只恢复切换前快照；必须先反向导出当前活动版本，避免丢失窗口内写入。

## 8.1 P1.3 实施审计结论

P1.3 已完成受控切换：HTTP 外部契约保持不变，运行时命令、查询和缓存全部归属 document 领域，旧 Markdown Go 运行实现退出。为避免在切换窗口把搜索降级为非 BM25，`document_search_projection` 的 BM25 索引作为 P1.3 前置条件随 0010 迁移交付；P1.4 聚焦索引所有权收口、样本基线和旧索引清理。

`markdowns`、`markdown_contents` 与 `idx_markdowns_paradedb` 当前只承担 P1.6 回滚窗口资产职责，不参与正式运行时读写。该保留不构成双写。

## 9. 实施包

| 实施包 | 内容 | 完成门槛 |
|---|---|---|
| P1.0 | 迁移器、备份、前置审计、反向导出骨架 | 临时库往返迁移无数据损失 |
| P1.1 | 新领域表、约束、ORM 与 Repository | PostgreSQL 集成测试通过 |
| P1.2 | 创建/更新/删除事务重写 | 版本、投影、Outbox 原子一致 |
| P1.3 | 查询、缓存和旧 HTTP 适配 | 当前 HTTP 回归全部通过 |
| P1.4 | BM25 搜索投影与校验脚本 | 搜索样本和权限基线通过 |
| P1.5 | mixin-search/v1 ACL/fencing 修订 | 生成一致性和适配层测试通过 |
| P1.6 | 演练、正式切换、回滚验证 | 切换与回滚记录完整 |

## 10. 阶段 1 验收

- 运行时代码不再读写 markdowns、markdown_contents；
- 不再依赖旧 markdownmodel、store.Markdown、cache:markdown:*；
- Chat 不复用文档连接；
- 更新产生不可变新版本，不覆盖历史正文；
- 文档、版本、策略、搜索投影和 Outbox 在一个事务中一致；
- 数据库约束阻止活动版本跨文档、重复修订和非法成员关系；
- BM25 只索引 document_search_projection；
- 当前 Markdown HTTP 契约和前端行为保持兼容；
- 迁移与反向迁移均经过真实 PostgreSQL 验证；
- Chat/WS 仍为 404；
- 阶段 1 只允许 Outbox 积压，不发送真实 RPC；
- 旧表只在回滚窗口内存在，窗口结束后必须删除。
