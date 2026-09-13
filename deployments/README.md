# 本地依赖与验证

当前后端运行时依赖 PostgreSQL 和 Redis；根目录 Compose 负责构建并启动 PostgreSQL、Redis、gin-backend 与 simple-frontend。Chat/ WebSocket schema 和源码保留，但服务、连接、路由和前端入口均未注册。mixin-search 不在根 Compose 中，也不接收正式 RPC 流量。

| 依赖 | 当前作用 | 宿主机端口 |
| --- | --- | --- |
| PostgreSQL | 用户、管理员、版本化文档、BM25 投影；保留 Chat 表 | `127.0.0.1:15432` |
| Redis | 会话 SID、缓存、Token 状态与会话撤销广播 | `127.0.0.1:16379` |

## 开发启动

```bash
docker compose -f docker-compose.yaml up -d --build --wait
```

浏览器访问 `http://localhost:15173`。数据库结构仅在全新数据卷上由 `postgresql/entryscript/00-init.sh` 确定性创建；本仓库处于可丢弃数据的开发期，不维护已有数据库升级、迁移账本、备份恢复或旧 schema 兼容。结构改变后应删除开发卷并重新启动：

```bash
docker compose -f docker-compose.yaml down --volumes
docker compose -f docker-compose.yaml up -d --build --wait
```

## 合并验证

在 Windows PowerShell 中从仓库根目录执行：

```powershell
.\deployments\verify.ps1
```

脚本只使用一个随机命名、一次性的 Compose 环境，依次完成：

1. Compose 与 `mixin-search/v1` 生成代码检查；
2. Vue/TypeScript 构建；
3. 从空卷构建并启动完整栈；
4. PostgreSQL 目标 schema、BM25-only 边界和管理员测试种子；
5. 三个 Go module 的 `go test ./...` 与 `go vet ./...`；
6. 认证、Documents、管理员、Nginx 代理、旧 Markdown 404 和 Chat 未注册的运行时 API 回归；
7. HTTP 健康检查，并默认销毁容器和测试数据卷。

镜像已确认无需重建时可使用 `-SkipImageBuild`；排查失败并希望保留临时环境时可使用 `-KeepEnvironment`。

只验证 PostgreSQL 镜像和空库基线：

```powershell
.\deployments\postgresql\verify-image.ps1 -UseCachedBase
```

此处的验证结论仅覆盖本地开发构建和功能正确性，不代表生产容量、升级或灾备能力。