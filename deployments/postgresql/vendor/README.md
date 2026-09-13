# PostgreSQL 离线构建资产

`deployments/postgresql/Dockerfile` 从本目录复制 ParadeDB `pg_search` 安装包，避免 Docker build 依赖 GitHub Release 的实时可用性。

当前必须提供：

```text
postgresql-17-pg-search_0.25.2-1PARADEDB-bookworm_amd64.deb
size: 67182912 bytes
sha256: f9f4cccbd5c19b8181c04bfb410bf64f76cac621fff62ca2e9086f220de1020a
```

`.deb` 不进入 Git；本地、CI 或内部制品仓库必须在构建前把经过 SHA-256 校验的文件放入本目录。
