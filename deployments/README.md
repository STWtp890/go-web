# 本地依赖与验证

根目录 Compose 默认构建并启动 PostgreSQL、Redis、gin-backend、simple-frontend、控制 PostgreSQL、Qdrant、mixin-search 与 document-index-worker。前四者承载正式 Web/Documents/BM25 链路，后四者承载可重建的影子索引和查询链路；mixin-search 故障不改变 gin-backend `/readyz` 或正式搜索结果。Chat/WebSocket schema 和源码保留，但服务、连接、路由和前端入口均未注册。

| 依赖 | 当前作用 | 宿主机端口 |
| --- | --- | --- |
| PostgreSQL | 用户、管理员、版本化文档、BM25 投影；保留 Chat 表 | `127.0.0.1:15432` |
| Redis | 会话 SID、缓存、Token 状态与会话撤销广播 | `127.0.0.1:16379` |

## 前置（全新克隆只需一次）

以下两类产物**刻意不入库**，全新克隆后直接 `docker compose up --build` 会失败：

| 缺失项 | 后果 |
| --- | --- |
| `apps/gin-backend/configs/rsa_private.pem` / `rsa_public.pem` | gin-backend 启动时 `LoadKeys` 失败。密钥不入镜像，由 Compose 只读挂载提供 |
| `deployments/postgresql/vendor/*.deb` | postgres 镜像构建失败（ParadeDB `pg_search` 离线安装包，约 64 MB） |

执行一次引导脚本即可补齐（会生成密钥，并在校验 SHA-256 后下载 `.deb`）：

```powershell
.\deployments\bootstrap.ps1
```

## 开发启动

```bash
docker compose -f docker-compose.yaml up -d --build --wait
```

> 构建镜像时 Go 模块代理默认取 `proxy.golang.org`。若该地址不可达（例如国内网络），
> 会出现 `go mod download` 校验模块失败。`docker-compose.yaml` 已把宿主机 `GOPROXY`
> 作为 build arg 传入，因此先设置环境变量再启动即可：
>
> ```powershell
> $env:GOPROXY = 'https://goproxy.cn,direct'
> ```

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
4. PostgreSQL 目标 schema、BM25-only 边界、User/Manager cache revision fencing 和管理员测试种子；
5. 三个 Go module 的 `go test ./...` 与 `go vet ./...`；
6. 认证、Documents、管理员、Nginx 代理、旧 Markdown 404 和 Chat 未注册的运行时 API 回归；
7. 等待影子索引收敛，使用七类固定样本生成 BM25/mixin-search Recall@K、MRR、nDCG、延迟和正确性报告；
8. 停止 mixin-search，确认第二轮 95 项 API 回归仍通过、影子失败被记录，再恢复服务并自动排空 Outbox；
9. HTTP 健康检查，输出 P2.5_SHADOW_QUERY_EVALUATION=PASS，并默认销毁容器和测试数据卷。

成功运行会在 test-results 生成 document-search-evaluation-<run-id>.json/.md。当前 local-hash-v1 只用于确定性评估，因此报告即使数值门禁通过也会给出 KEEP_BM25；正式读取不会由脚本自动切换。

### 验收产物的保留策略

一次完整验收会新增 6 个文件（3 个报告族 × json + md）。为避免无限增长，`test-results/` **每个报告族只保留最近 3 次运行**：

```powershell
.\deployments\prune-test-results.ps1 -DryRun   # 先看将删除什么
.\deployments\prune-test-results.ps1           # 执行
```

更早的产物可从 git 历史取回。该脚本只识别 `<族>_<YYYYMMDD>_<HHMMSS>.<ext>`（以及早期的 `<族>-<YYYYMMDD>_<HHMMSS>.<ext>`）命名；遇到其他命名会跳过并打印警告。

镜像已确认无需重建时可使用 `-SkipImageBuild`；排查失败并希望保留临时环境时可使用 `-KeepEnvironment`。

只验证 PostgreSQL 镜像和空库基线：

```powershell
.\deployments\postgresql\verify-image.ps1 -UseCachedBase
```

此处的验证结论仅覆盖本地开发构建和功能正确性，不代表生产容量、升级或灾备能力。
