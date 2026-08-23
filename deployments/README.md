# gin-backend 依赖部署

当前后端运行时依赖如下：

| 依赖 | 作用 | 本地编排 |
| --- | --- | --- |
| PostgreSQL | 用户、Markdown、聊天、管理员审批与搜索/时序数据 | [`../docker-compose.yaml`](../docker-compose.yaml) |
| Redis | 会话 SID、缓存、Token 状态与会话撤销广播 | [`../docker-compose.yaml`](../docker-compose.yaml) |

本地开发通过根目录编排启动完整服务栈：

```bash
docker compose -f docker-compose.yaml up -d --build --wait
```

浏览器访问 `http://localhost:15173`。Compose 内的后端使用 `postgres`、`redis` 服务名连接依赖；宿主机仍可通过 `127.0.0.1:15432` 与 `127.0.0.1:16379` 连接数据库和 Redis。

尚未将对象存储、消息队列等写入部署清单，因为当前后端尚未实际依赖它们。

## 构建、部署与验证链路

在 Windows PowerShell 中从仓库根目录执行：

```powershell
.\deployments\verify.ps1
```

脚本按以下顺序执行，任一步失败都会停止：

1. `docker compose config --quiet`：验证 Compose 文件可解析。
2. `go test ./...`：验证后端编译与单元测试。
3. `npm run type-check`、`npm run build`：验证 Vue/TypeScript 与生产构建。
4. `docker compose up -d --build --wait`：构建镜像并等待四个服务运行或健康。
5. 请求前端首页、`/healthz` 与 `/readyz`：验证浏览器入口、Nginx 反向代理及后端依赖就绪。

已确认镜像无需重建时可以执行：

```powershell
.\deployments\verify.ps1 -SkipImageBuild
```

脚本默认保留已验证的服务。停止服务但保留数据卷：

```bash
docker compose -f docker-compose.yaml down
```

查看运行状态和日志：

```bash
docker compose -f docker-compose.yaml ps
docker compose -f docker-compose.yaml logs --tail=100 gin-backend simple-frontend
```
