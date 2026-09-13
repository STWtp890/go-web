package app

import (
	"context"
	"fmt"
	"log/slog"

	"gin-backend/internal/common/base/connection"
	postgresqlconn "gin-backend/internal/common/base/connection/postgresql"
	redisconn "gin-backend/internal/common/base/connection/redis"
	"gin-backend/internal/common/service/sessionevent"
	"gin-backend/internal/config"
	"gin-backend/internal/modules/document/application"
	documentcache "gin-backend/internal/modules/document/infrastructure/cache"
	documentpostgresql "gin-backend/internal/modules/document/infrastructure/postgresql"
	documenthttp "gin-backend/internal/modules/document/interfaces/http"
)

type runtimeDependencies struct {
	documentHTTP *documenthttp.Handler
}

// ready 校验当前已接入的数据库与 Redis 基础设施均可用。
func (dependencies *runtimeDependencies) ready(ctx context.Context) error {
	pg, err := postgresqlconn.PostgreSQLManager.Get(connection.ServiceAuth)
	if err != nil {
		return err
	}
	db, err := pg.GetConn()
	if err != nil {
		return err
	}
	if err := postgresqlconn.HealthCheck(db); err != nil {
		return err
	}

	documentPostgreSQL, err := postgresqlconn.PostgreSQLManager.Get(connection.ServiceDocument)
	if err != nil {
		return err
	}
	documentDB, err := documentPostgreSQL.GetConn()
	if err != nil {
		return err
	}
	if err := postgresqlconn.HealthCheck(documentDB); err != nil {
		return err
	}

	rc, err := redisconn.RedisManager.Get(connection.ServiceAuth)
	if err != nil {
		return err
	}
	client, err := rc.GetConn()
	if err != nil {
		return err
	}
	return client.Ping(ctx).Err()
}

// initDependencies 初始化全局基础设施和业务模块，并返回依赖集合与统一清理函数。
func initDependencies(conf *config.Config) (*runtimeDependencies, func()) {
	if _, err := postgresqlconn.PostgreSQLManager.RegisterAndGet(connection.ServiceAuth, conf.PostgresConfig, conf.LogConfig.Level); err != nil {
		panic(fmt.Sprintf("数据库连接失败: %v", err))
	}

	if _, err := redisconn.RedisManager.RegisterAndGet(connection.ServiceAuth, &conf.RedisConfig); err != nil {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth)
		panic(fmt.Sprintf("Redis 连接失败: %v", err))
	}

	authRedisConn, err := redisconn.RedisManager.Get(connection.ServiceAuth)
	if err != nil {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth)
		_ = redisconn.RedisManager.Unregister(connection.ServiceAuth)
		panic(fmt.Sprintf("获取认证 Redis 连接失败: %v", err))
	}
	authRedisClient, err := authRedisConn.GetConn()
	if err != nil {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth)
		_ = redisconn.RedisManager.Unregister(connection.ServiceAuth)
		panic(fmt.Sprintf("获取认证 Redis 客户端失败: %v", err))
	}
	sessionBus, err := sessionevent.NewRedisPubSubBus(authRedisClient)
	if err != nil {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth)
		_ = redisconn.RedisManager.Unregister(connection.ServiceAuth)
		panic(fmt.Sprintf("初始化会话事件总线失败: %v", err))
	}
	sessionevent.SetDefaultBus(sessionBus)

	if _, err := redisconn.RedisManager.RegisterAndGet(connection.ServiceCache, &conf.RedisConfig); err != nil {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth)
		_ = redisconn.RedisManager.Unregister(connection.ServiceAuth)
		panic(fmt.Sprintf("Redis 连接失败: %v", err))
	}

	documentDB, err := postgresqlconn.PostgreSQLManager.RegisterAndGet(connection.ServiceDocument, conf.PostgresConfig, conf.LogConfig.Level)
	if err != nil {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth)
		_ = redisconn.RedisManager.Unregister(connection.ServiceAuth)
		_ = redisconn.RedisManager.Unregister(connection.ServiceCache)
		panic(fmt.Sprintf("PostgreSQL 连接失败: %v", err))
	}

	documentRepository, err := documentpostgresql.New(documentDB)
	if err != nil {
		panic(fmt.Sprintf("初始化文档仓储失败: %v", err))
	}
	documentCommands, err := application.NewCommandService(documentRepository)
	if err != nil {
		panic(fmt.Sprintf("初始化文档写服务失败: %v", err))
	}
	documentQueries, err := application.NewQueryService(documentRepository, documentcache.New(0))
	if err != nil {
		panic(fmt.Sprintf("初始化文档查询服务失败: %v", err))
	}
	documentHandler, err := documenthttp.New(documentCommands, documentQueries)
	if err != nil {
		panic(fmt.Sprintf("初始化文档 HTTP 适配器失败: %v", err))
	}

	// 数据库结构由 PostgreSQL 空卷初始化脚本建立，不在服务启动时迁移。
	// Chat 包使用独立 ServiceChat 名称，但当前不注册连接或路由。
	dependencies := &runtimeDependencies{documentHTTP: documentHandler}
	return dependencies, func() {
		sessionevent.SetDefaultBus(nil)
		if err := postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceDocument); err != nil {
			slog.Error("关闭文档 PostgreSQL 连接失败", slog.String("error", err.Error()))
		}
		if err := postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth); err != nil {
			slog.Error("关闭数据库连接失败", slog.String("error", err.Error()))
		}
		if err := redisconn.RedisManager.Unregister(connection.ServiceAuth); err != nil {
			slog.Error("关闭 Redis 连接失败", slog.String("error", err.Error()))
		}
		if err := redisconn.RedisManager.Unregister(connection.ServiceCache); err != nil {
			slog.Error("关闭缓存 Redis 连接失败", slog.String("error", err.Error()))
		}
	}
}
