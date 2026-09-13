# ============================================================
# Dockerfile — gin-backend PostgreSQL 镜像
# 内置扩展: TimescaleDB (时序) + ParadeDB pg_search (BM25 全文检索)
#
# 基础: 官方 postgres:17-bookworm (Debian 12, 自带 PGDG apt 源)
#
# 选型说明 (2026-08, 依据官方文档实测):
#   - pg_search 0.25+ 仅提供 Debian/Ubuntu 预编译 .deb (bookworm/trixie/noble/...),
#     无 Alpine/jammy 变体 → 基础镜像必须为 Debian/Ubuntu 系
#   - timescale 官方镜像: 标准版基于 Alpine (无 apt), HA 版基于 Ubuntu jammy
#     (pg_search 无 jammy 包, 实测 404) → 两者均不适用,
#     故基于 postgres:17-bookworm 自行 apt 装配
#   - 扩展来源: timescaledb (packagecloud Debian) + pg_search (本地校验的 GitHub Release .deb)
#   - pg_search 上游要求 pgvector 作为运行时依赖，镜像中显式固定并安装；
#     go-web 不创建 vector 列、向量索引或向量检索接口。
#   - shared_preload_libraries 需含 timescaledb,pg_search (见 ../../docker-compose.yaml)
# ============================================================
FROM postgres:17-bookworm

ARG TIMESCALEDB_VERSION=2.30.0~debian12-1711
ARG PG_SEARCH_SHA256=f9f4cccbd5c19b8181c04bfb410bf64f76cac621fff62ca2e9086f220de1020a
ARG PGVECTOR_VERSION=0.8.6-1.pgdg12+1

# 1. 基础工具 + timescale apt 源 (packagecloud, Debian bookworm)
USER root
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl ca-certificates gnupg lsb-release \
    && echo "deb [signed-by=/usr/share/keyrings/timescaledb.gpg] https://packagecloud.io/timescale/timescaledb/debian/ $(lsb_release -c -s) main" \
         > /etc/apt/sources.list.d/timescaledb.list \
    && curl -fsSL https://packagecloud.io/timescale/timescaledb/gpgkey \
         | gpg --dearmor -o /usr/share/keyrings/timescaledb.gpg \
    && apt-get update

# 2. 固定并安装 timescaledb、pgvector，再安装 pg_search。
COPY vendor/postgresql-17-pg-search_0.25.2-1PARADEDB-bookworm_amd64.deb /tmp/pg_search.deb
RUN apt-get install -y --no-install-recommends \
        "timescaledb-2-loader-postgresql-17=${TIMESCALEDB_VERSION}" \
        "timescaledb-2-postgresql-17=${TIMESCALEDB_VERSION}" \
        "postgresql-17-pgvector=${PGVECTOR_VERSION}" \
    && echo "${PG_SEARCH_SHA256}  /tmp/pg_search.deb" | sha256sum -c - \
    && apt-get install -y --no-install-recommends /tmp/pg_search.deb \
    && rm -f /tmp/pg_search.deb \
    && rm -rf /var/lib/apt/lists/*

USER postgres
