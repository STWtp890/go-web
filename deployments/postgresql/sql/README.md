# SQL 目录约定

- `plugin/`：PostgreSQL 扩展安装与验证；不包含业务表、索引或数据。
- `service/{auth,markdown,chat,manager}/`：各业务的表、索引、时序配置和种子数据。

空数据库由 `docker-entrypoint-initdb.d/00-init.sh` 按依赖顺序执行。可靠投递与管理员申请唯一性
已融合到 `service/chat/schema_init.sql`、`service/manager/schema_init.sql`；已有数据库需按这两份文件中新增 DDL
手动补齐，不能重跑全量初始化。

聊天应用层 ACK 模型升级时，已有数据库需单独执行：

```bash
psql -U postgres -d gin_demo -f service/chat/migrate_pending_delivery_ack.sql
```

该迁移删除已经 ACK 的旧 delivery，并把 `chat_message_deliveries` 收敛为只保存
pending delivery 的短期可靠投递表；它不包含已读回执语义。
