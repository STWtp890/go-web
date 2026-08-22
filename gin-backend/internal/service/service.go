package service

import (
	"context"
	"fmt"
	"log/slog"

	"gin-backend/internal/common/base/connection"
	postgresqlconn "gin-backend/internal/common/base/connection/postgresql"
	redisconn "gin-backend/internal/common/base/connection/redis"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/sessioncookie"
	"gin-backend/internal/common/service/sessionevent"
	"gin-backend/internal/config"
	"gin-backend/internal/middleware"
	authapi "gin-backend/internal/service/auth/api"
	"gin-backend/internal/service/chat"
	chatapi "gin-backend/internal/service/chat/api"
	managerapi "gin-backend/internal/service/manager/api"
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
	r.GET("/readyz", ReadinessCheck)

	// API v1 路由组
	v1 := r.Group("/api/v1")
	v1.Use(middleware.TraceID())

	// 公开路由组, 无鉴权
	public := v1.Group("/public")

	// 保护路由组, 需鉴权
	protected := v1.Group("/protected")
	protected.Use(middleware.AuthRequired(conf))
	protected.Use(middleware.CSRFProtection(sessioncookie.UserCSRFCookie))

	// 管理员保护路由组, 需管理员鉴权 (ManagerAuthRequired)
	managerProtected := v1.Group("/protected")
	managerProtected.Use(middleware.ManagerAuthRequired(conf))
	managerProtected.Use(middleware.CSRFProtection(sessioncookie.ManagerCSRFCookie))

	// 各业务路由注册
	authapi.SetRouteGroup(public, protected)
	managerapi.SetRouteGroup(public, managerProtected)
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

// ReadinessCheck 只有全部依赖就绪时才表示可接收流量。
func ReadinessCheck(c *gin.Context) {
	if err := Ready(c.Request.Context()); err != nil {
		slog.Warn("readiness_check_failed", slog.String("error", err.Error()))
		responses.Fail(c, 503, "SERVICE_UNAVAILABLE", "服务暂未就绪")
		return
	}
	responses.OK(c, gin.H{"status": "ready", "service": "gin-backend"})
}

// Ready 校验数据库、Redis 与聊天模块均已可用。
func Ready(ctx context.Context) error {
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
	rc, err := redisconn.RedisManager.Get(connection.ServiceAuth)
	if err != nil {
		return err
	}
	cli, err := rc.GetConn()
	if err != nil {
		return err
	}
	if err := cli.Ping(ctx).Err(); err != nil {
		return err
	}
	if chat.Hub() == nil {
		return fmt.Errorf("chat hub 未初始化")
	}
	return nil
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

	// 注册并初始化 Redis 连接 (token 状态)
	if _, err := redisconn.RedisManager.RegisterAndGet(connection.ServiceAuth, &conf.RedisConfig); err != nil {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth)
		panic(fmt.Sprintf("Redis 连接失败: %v", err))
	}

	// 会话撤销事件使用认证 Redis 的 Pub/Sub 广播。当前仅接入发布端，
	// 业务模块订阅及资源清理由各模块后续实现。
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

	// 注册并初始化 Redis 连接 (实体缓存专用, 与 token 状态连接隔离)
	if _, err := redisconn.RedisManager.RegisterAndGet(connection.ServiceCache, &conf.RedisConfig); err != nil {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth)
		_ = redisconn.RedisManager.Unregister(connection.ServiceAuth)
		panic(fmt.Sprintf("Redis 连接失败: %v", err))
	}

	// 注册并初始化 PostgreSQL 连接 (markdown 文档业务)
	if _, err := postgresqlconn.PostgreSQLManager.RegisterAndGet(connection.ServiceMarkdown, conf.PostgresConfig, conf.LogConfig.Level); err != nil {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth)
		_ = redisconn.RedisManager.Unregister(connection.ServiceAuth)
		panic(fmt.Sprintf("PostgreSQL 连接失败: %v", err))
	}

	// 表结构初始化已移出启动流程, 由部署脚本统一执行 (数据库初始化事实来源):
	//   - 建表: deployments/postgresql/sql/service/{auth,markdown,chat,manager}/schema_init.sql
	//   - 插件: deployments/postgresql/sql/plugin/
	//   - 业务索引/时序配置: deployments/postgresql/sql/service/

	// 业务模块级初始化: chat (WebSocket Hub)
	// 约定: 各业务模块在 internal/service/{name}/init.go 提供 Init(conf) func()
	chatCleanup := chat.Init(conf)

	return func() {
		sessionevent.SetDefaultBus(nil)
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
		if err := redisconn.RedisManager.Unregister(connection.ServiceCache); err != nil {
			slog.Error("关闭缓存 Redis 连接失败", slog.String("error", err.Error()))
		}
	}
}
