package service

import (
	"fmt"
	"log/slog"

	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/connection"
	postgresqlconn "gin-backend/internal/common/connection/postgresql"
	redisconn "gin-backend/internal/common/connection/redis"
	"gin-backend/internal/config"
	"gin-backend/internal/middleware"
	authapi "gin-backend/internal/service/auth/api"
	"gin-backend/internal/service/chat"
	chatapi "gin-backend/internal/service/chat/api"
	markdownapi "gin-backend/internal/service/markdown/api"

	"github.com/gin-gonic/gin"
)

// Setup 注册所有路由
func Setup(r *gin.Engine, conf *config.Config) *gin.Engine {
	// 设置运行模式
	gin.SetMode(conf.ServerConfig.Mode)

	// 全局中间件
	r.Use(middleware.GinLogger())   // 请求日志 (slog)
	r.Use(middleware.GinRecovery()) // panic 恢复 (slog)
	r.Use(middleware.CORS(conf))    // 跨域

	// 健康检查（无需鉴权）
	r.GET("/healthz", HealthCheck)

	// API v1 路由组
	v1 := r.Group("/api/v1")
	v1.Use(middleware.TraceID())

	// 公开路由组, 无鉴权
	public := v1.Group("/public")

	// 保护路由组, 需鉴权
	protected := v1.Group("/protected")
	protected.Use(middleware.AuthRequired(conf))

	// 各业务路由注册
	authapi.SetRouteGroup(public, protected)
	chatapi.SetRouteGroup(protected)
	markdownapi.SetRouteGroup(public, protected)

	return r
}

// HealthCheck 健康检查
func HealthCheck(c *gin.Context) {
	responses.OK(c, gin.H{
		"status":  "ok",
		"service": "gin-backend",
	})
}

// Init 封装了各种业务级初始化操作
// :Param
// - `conf` 配置文件
// :Return
// - `func()` 清理函数, 由调用方 (main) 在 defer 中调用, 用于释放资源
func Init(conf *config.Config) func() {
	// 注册并初始化 PostgreSQL 连接 (auth 业务: ServiceAuth)
	// 替换原 MySQL: auth/markdown/chat 统一使用 PostgreSQL (配置走 postgres 段)
	if _, err := postgresqlconn.PostgreSQLManager.RegisterAndGet(connection.ServiceAuth, conf.PostgresConfig, conf.LogConfig.Level); err != nil {
		panic(fmt.Sprintf("数据库连接失败: %v", err))
	}

	// 注册并初始化 Redis 连接
	if _, err := redisconn.RedisManager.RegisterAndGet(connection.ServiceAuth, &conf.RedisConfig); err != nil {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth)
		panic(fmt.Sprintf("Redis 连接失败: %v", err))
	}

	// 注册并初始化 PostgreSQL 连接 (markdown 文档业务)
	if _, err := postgresqlconn.PostgreSQLManager.RegisterAndGet(connection.ServiceMarkdown, conf.PostgresConfig, conf.LogConfig.Level); err != nil {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth)
		_ = redisconn.RedisManager.Unregister(connection.ServiceAuth)
		panic(fmt.Sprintf("PostgreSQL 连接失败: %v", err))
	}

	// 表结构迁移 (AutoMigrate + pg_search BM25 索引) 已移出启动流程,
	// 由独立工具统一执行: go run utils/automigrate/main.go
	// (依赖 pg_search 扩展, 见 ../deployments/postgresql/sql/pg_search_setup.sql)

	// 业务模块级初始化: chat (WebSocket/SSE Hub)
	// 约定: 各业务模块在 internal/service/{name}/init.go 提供 Init(conf) func()
	chatCleanup := chat.Init(conf)

	return func() {
		if chatCleanup != nil { // 无清理需求时业务模块返回 nil
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
	}
}
