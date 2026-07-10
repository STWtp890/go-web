# 缓存的一致性问题

## 使用 zero model 缓存的优化查询
go-zero `sqlc.CachedConn` 自动缓存 `FindOne` 结果，对于任意手写的 `queryPage()` 不走 model 缓存。对高频列表查询添加 Redis 缓存层（带 TTL 失效）

## 解决方案

采用短 TTL + 自然过期策略解决list一致性问题，对于公告/文档列表的查询上，30 秒延迟是可以接受的。

且无需在 create/update/delete 中写失效逻辑，若后续需要更强的一致性，只需将 TTL 调小或在 mutation 中调用。