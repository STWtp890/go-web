# gin-backend 依赖部署

当前后端运行时依赖如下：

| 依赖 | 作用 | 本地编排 |
| --- | --- | --- |
| PostgreSQL | 用户、Markdown、聊天、管理员审批与搜索/时序数据 | [`postgresql/docker-compose.yaml`](postgresql/docker-compose.yaml) |
| Redis | 会话 SID、缓存、Token 状态与会话撤销广播 | [`redis/docker-compose.yaml`](redis/docker-compose.yaml) |

本地开发可分别启动：

```bash
cd deployments/postgresql && docker compose up -d --build
cd ../redis && docker compose up -d
```

应用默认连接 `127.0.0.1:15432` 与 `127.0.0.1:16379`。容器化部署 `gin-backend` 本身时，应把数据库和 Redis 地址改为对应 Compose 服务名或私有网络 DNS 名称，而非 `127.0.0.1`。

尚未将对象存储、消息队列等写入部署清单，因为当前后端尚未实际依赖它们。
