// automigrate.go — AutoMigrate 迁移逻辑
package main

import (
	"log/slog"

	"gin-backend/internal/common/connection"
	postgresqlconn "gin-backend/internal/common/connection/postgresql"
	"gin-backend/internal/config"
	authmodel "gin-backend/internal/orm/auth"
	chatmodel "gin-backend/internal/orm/chat"
	markdownmodel "gin-backend/internal/orm/markdown"
)

// migrateAuth 迁移 auth 业务表 (PostgreSQL users)
// :Param
// - `conf` 应用配置 (读取 PostgresConfig)
// :Return
// - `error` 如果连接或迁移失败, 返回错误信息
func migrateAuth(conf *config.Config) error {
	slog.Info("auth 业务迁移开始", slog.String("target", "postgres:users"))
	db, err := postgresqlconn.PostgreSQLManager.RegisterAndGet(connection.ServiceAuth, conf.PostgresConfig, conf.LogConfig.Level)
	if err != nil {
		return err
	}
	defer func() {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth)
	}()

	return postgresqlconn.AutoMigrate(db, &authmodel.User{})
}

// migrateMarkdown 迁移 markdown 业务表 (PostgreSQL markdowns / markdown_contents)
// 可选创建 pg_search BM25 全文检索索引 (依赖 pg_search 扩展, 见 ../deployments/postgresql/sql/pg_search_setup.sql)
// :Param
// - `conf` 应用配置 (读取 PostgresConfig)
// - `withIndex` 是否同时创建 pg_search BM25 索引
// :Return
// - `error` 如果连接或迁移失败, 返回错误信息
func migrateMarkdown(conf *config.Config, withIndex bool) error {
	slog.Info("markdown 业务迁移开始", slog.String("target", "postgres:markdowns,markdown_contents"), slog.Bool("with_index", withIndex))
	db, err := postgresqlconn.PostgreSQLManager.RegisterAndGet(connection.ServiceMarkdown, conf.PostgresConfig, conf.LogConfig.Level)
	if err != nil {
		return err
	}
	defer func() {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceMarkdown)
	}()

	if err := postgresqlconn.AutoMigrate(db, &markdownmodel.Markdown{}, &markdownmodel.Content{}); err != nil {
		return err
	}

	if withIndex {
		slog.Info("创建 pg_search BM25 全文检索索引 (幂等)")
		if err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_markdowns_paradedb
			ON markdowns USING bm25 (markdown_id, (search_text::pdb.jieba))
			WITH (key_field = 'markdown_id')`).Error; err != nil {
			return err
		}
	}
	return nil
}

// migrateChat 迁移 chat 业务表 (PostgreSQL chat_groups / chat_group_members / chat_messages)
// chat 与 markdown 同库 (复用 ServiceMarkdown 连接)
// :Param
// - `conf` 应用配置 (读取 PostgresConfig)
// :Return
// - `error` 如果连接或迁移失败, 返回错误信息
func migrateChat(conf *config.Config) error {
	slog.Info("chat 业务迁移开始", slog.String("target", "postgres:chat_groups,chat_group_members,chat_messages"))
	db, err := postgresqlconn.PostgreSQLManager.RegisterAndGet(connection.ServiceMarkdown, conf.PostgresConfig, conf.LogConfig.Level)
	if err != nil {
		return err
	}
	defer func() {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceMarkdown)
	}()

	return postgresqlconn.AutoMigrate(db,
		&chatmodel.Group{},
		&chatmodel.GroupMember{},
		&chatmodel.Message{},
	)
}
