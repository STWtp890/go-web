# Redis 部署

此 Redis 实例是 `gin-backend` 的运行时依赖，承担：

- 普通用户和管理员的 SID 会话及 Refresh Token 哈希；
- Access Token 黑名单的兼容数据；
- 实体缓存；
- `session.revoked` 的 Pub/Sub 广播。

## 本地启动

在本目录执行：

```bash
docker compose -f ../../docket-compose.yaml up -d redis
docker compose -f ../../docket-compose.yaml ps redis
docker compose -f ../../docket-compose.yaml exec redis redis-cli ping
```

默认仅映射到 `127.0.0.1:16379`，与 `gin-backend/configs/config.yaml` 相匹配：

```yaml
redis:
  host: 127.0.0.1
  port: 16379
  password: ""
  db: 0
```

为使 Docker 转发的本机连接可用，本地配置关闭了 Redis protected mode，且未设置密码；安全边界由仅回环的端口映射提供。

停止容器但保留数据：

```bash
docker compose -f ../../docket-compose.yaml stop redis
```

如需删除本 Redis 的命名数据卷：

```bash
docker compose -f ../../docket-compose.yaml rm -sfv redis
```

## 生产要求

该 Compose 文件是本机开发配置，不能直接暴露到公网。生产环境应：

- 仅允许后端服务的私有网络访问 Redis；不发布 `6379` 宿主机端口。
- 启用 `requirepass` 或 Redis ACL，并将相同凭证通过密钥管理/环境变量写入应用 `redis.password`。
- 保持 AOF 或使用受管 Redis 的持久化与备份；会话数据丢失会导致用户需要重新登录。
- 保持 `maxmemory-policy noeviction`，并为内存使用率与连接失败设置监控告警。
- 如会话撤销清理需要可靠补偿，另行引入 Redis Streams/Outbox；Redis Pub/Sub 只提供实时通知。
