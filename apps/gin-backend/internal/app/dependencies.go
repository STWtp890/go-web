package app

import (
	"fmt"
	"log/slog"

	"gin-backend/internal/common/base/connection"
	postgresqlconn "gin-backend/internal/common/base/connection/postgresql"
	redisconn "gin-backend/internal/common/base/connection/redis"
	"gin-backend/internal/common/service/sessionevent"
	"gin-backend/internal/config"
	"gin-backend/internal/service/chat"
)

// initDependencies 初始化全局基础设施和业务模块，并返回统一清理函数。
func initDependencies(conf *config.Config) func() {
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

	if _, err := postgresqlconn.PostgreSQLManager.RegisterAndGet(connection.ServiceMarkdown, conf.PostgresConfig, conf.LogConfig.Level); err != nil {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth)
		_ = redisconn.RedisManager.Unregister(connection.ServiceAuth)
		panic(fmt.Sprintf("PostgreSQL 连接失败: %v", err))
	}

	// 数据库结构由 deployments/postgresql 下的部署脚本管理，不在服务启动时迁移。
	chatCleanup := chat.Init(conf)

	return func() {
		sessionevent.SetDefaultBus(nil)
		if chatCleanup != nil {
			chatCleanup()
		}
		if err := postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceMarkdown); err != nil {
			slog.Error("关闭 PostgreSQL 连接失败", slog.String("error", err.Error()))
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
