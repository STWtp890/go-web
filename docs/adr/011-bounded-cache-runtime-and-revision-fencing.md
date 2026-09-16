# ADR-011：有界实体缓存运行时与 revision fencing

> 状态：已接受
> 日期：2026-09-15

## 背景

gin-backend 原有实体缓存分别在 `model/store` 和 document 基础设施中实例化内存回退，`MemCache` 没有容量上限或主动清理；每个 `EntityCache` 还各自持有 `singleflight.Group`。Redis 客户端本身由连接注册表共享，但内存预算、防击穿范围和统计口径被实例边界切开。

原有 `Evict` 还会吞掉两级删除错误。更重要的是，Cache-Aside 的“提交后删除”无法阻止提交前已经开始的读在删除之后回填旧值。singleflight 只能合并同向读，不能为读写提供新鲜度仲裁。

JWT 会话状态是另一条安全边界。实际实现依赖 Redis 原子脚本并在 Redis 故障时失败关闭，但一个未使用的旧文件仍声称存在应用内回退，造成语义冲突。

## 决策

### 1. 进程内只使用一个缓存运行时

`DefaultRuntime` 统一持有一个 RedisCache 壳、一个进程级 singleflight 和两个有界内存分区：

- entities：最多 10,000 条、32 MiB；
- documents：最多 2,048 条、64 MiB。

内存分区使用 TTL + LRU，按条目数和估算字节数双重限制。每分钟输出不含业务 key 的命中、未命中、故障、回源、共享、回填错误、删除错误、条目数、字节数、过期与淘汰统计；读取统计时同时清理过期项。

所有未命中统一返回 `ErrMiss`，调用方使用 `errors.Is` 判断。实体构造只通过运行时入口完成，User 的 ID/邮箱路径以及 Manager 的 ID/用户名路径分别共享同一 EntityCache。

### 2. 可变实体以数据库 revision 建键

users 与 managers 增加从 1 开始的 `cache_revision bigint`。PostgreSQL BEFORE UPDATE 触发器在缓存相关字段、软删除字段或 revision 本身被更新时强制按旧值加一，因此不依赖秒级 `updated_at`，同秒多次写也可区分。

每次 User/Manager 读取先直查 PostgreSQL 的 `(id, cache_revision)` head，再访问：

- `cache:user:v2:<id>:revision:<revision>`；
- `cache:manager:v2:<id>:revision:<revision>`。

缓存回源查询同时约束 id 与 revision。若 head 与实体查询之间发生更新，查询不会把新实体写入旧键，而是重新读取 head，最多尝试三次。旧请求即使在 Evict 后延迟回填，也只污染已不可达的旧版本键，最终由 TTL/LRU 回收。

document 继续使用已经包含活动版本和授权/生命周期 revision 的版本键，不改动其正确性模型。

### 3. Evict 只负责回收，JWT 保持失败关闭

Evict 同时尝试 Redis 与内存删除，使用 `errors.Join` 返回全部错误并增加统计；提交后的业务调用记录结构化错误。它不再承担新鲜度保证。

删除未使用的 JWT 黑名单/白名单旧 API 及其“内存回退”注释。现有会话 SID/refresh rotation 继续只依赖 Redis 原子操作；Redis 不可用时返回错误，不在进程内复制会话权威状态。

## 结果与权衡

- 内存回退的最坏占用和淘汰行为有明确上限，新增 EntityCache 不再隐式创建新 map 或新 singleflight；
- 多进程下的延迟旧回填不会覆盖新 revision 的读取；
- User/Manager 的每次缓存读取增加一次轻量 PostgreSQL head 查询，缓存只覆盖完整实体反序列化与较重读取；
- 旧版本 Redis key 不主动全量扫描，依赖 TTL 到期；
- JWT 与普通实体缓存保持不同故障语义：实体可降级到有界内存，会话状态失败关闭。

## 验证

- 缓存单测覆盖统一 miss 哨兵、TTL、LRU、条目/字节上限、共享 singleflight、Evict 聚合错误和旧版本延迟回填隔离；
- store 单测固定 User/Manager revision key 格式；
- `cache_revision_verify.sql` 验证字段、触发器及两次立即更新从 revision 1 单调增长到 3；
- gin-backend 执行 `go test ./...` 与 `go vet ./...`；根验收从空数据卷再次执行 schema、API、故障与恢复链路。
