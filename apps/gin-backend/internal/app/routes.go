package app

import (
	"context"
	"fmt"
	"log/slog"

	"gin-backend/internal/common/base/connection"
	postgresqlconn "gin-backend/internal/common/base/connection/postgresql"
	redisconn "gin-backend/internal/common/base/connection/redis"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/sessioncookie"
	"gin-backend/internal/config"
	"gin-backend/internal/middleware"
	authapi "gin-backend/internal/service/auth/api"
	"gin-backend/internal/service/chat"
	chatapi "gin-backend/internal/service/chat/api"
	managerapi "gin-backend/internal/service/manager/api"
	markdownapi "gin-backend/internal/service/markdown/api"

	"github.com/gin-gonic/gin"
)

// setupRoutes 安装全局中间件、探针和各业务模块路由。
func setupRoutes(engine *gin.Engine, conf *config.Config) {
	gin.SetMode(conf.ServerConfig.Mode)

	engine.Use(middleware.GinLogger())
	engine.Use(middleware.GinRecovery())
	engine.Use(middleware.CORS(conf))

	engine.GET("/healthz", healthCheck)
	engine.GET("/readyz", readinessCheck)

	v1 := engine.Group("/api/v1")
	v1.Use(middleware.TraceID())

	public := v1.Group("/public")

	protected := v1.Group("/protected")
	protected.Use(middleware.AuthRequired(conf))
	protected.Use(middleware.CSRFProtection(sessioncookie.UserCSRFCookie))

	managerProtected := v1.Group("/protected")
	managerProtected.Use(middleware.ManagerAuthRequired(conf))
	managerProtected.Use(middleware.CSRFProtection(sessioncookie.ManagerCSRFCookie))

	authapi.SetRouteGroup(public, protected)
	managerapi.SetRouteGroup(public, managerProtected)
	chatapi.SetRouteGroup(protected)
	markdownapi.SetRouteGroup(public, protected)
}

func healthCheck(c *gin.Context) {
	responses.OK(c, gin.H{
		"status":  "ok",
		"service": "gin-backend",
	})
}

func readinessCheck(c *gin.Context) {
	if err := ready(c.Request.Context()); err != nil {
		slog.Warn("readiness_check_failed", slog.String("error", err.Error()))
		responses.Fail(c, 503, "SERVICE_UNAVAILABLE", "服务暂未就绪")
		return
	}
	responses.OK(c, gin.H{"status": "ready", "service": "gin-backend"})
}

// ready 校验数据库、Redis 与聊天模块均已可用。
func ready(ctx context.Context) error {
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
	client, err := rc.GetConn()
	if err != nil {
		return err
	}
	if err := client.Ping(ctx).Err(); err != nil {
		return err
	}
	if chat.Service() == nil {
		return fmt.Errorf("chat service 未初始化")
	}
	return nil
}
