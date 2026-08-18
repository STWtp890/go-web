-- plugin/timescaledb_setup.sql — TimescaleDB 扩展安装
-- 前置：镜像中已安装 TimescaleDB，且 shared_preload_libraries 包含 timescaledb。
CREATE EXTENSION IF NOT EXISTS timescaledb;
