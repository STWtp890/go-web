# gin-backend 依赖部署

当前后端运行时依赖如下：

| 依赖 | 作用 | 本地编排 |
| --- | --- | --- |
| PostgreSQL | 用户、Markdown、聊天、管理员审批与搜索/时序数据 | [`../docket-compose.yaml`](../docket-compose.yaml) |
| Redis | 会话 SID、缓存、Token 状态与会话撤销广播 | [`../docket-compose.yaml`](../docket-compose.yaml) |

本地开发通过根目录编排启动完整服务栈：

```bash
docker compose -f docket-compose.yaml up -d --build
```

浏览器访问 `http://localhost:15173`。Compose 内的后端使用 `postgres`、`redis` 服务名连接依赖；宿主机仍可通过 `127.0.0.1:15432` 与 `127.0.0.1:16379` 连接数据库和 Redis。

尚未将对象存储、消息队列等写入部署清单，因为当前后端尚未实际依赖它们。
