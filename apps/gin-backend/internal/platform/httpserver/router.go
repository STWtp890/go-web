// Package httpserver 负责 Gin 引擎、全局中间件、探针和业务路由装配。
package httpserver

import (
	"context"
	"log/slog"
	"net/http"

	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/sessioncookie"
	"gin-backend/internal/config"
	authapi "gin-backend/internal/modules/auth/api"
	managerapi "gin-backend/internal/modules/manager/api"
	"gin-backend/internal/platform/httpserver/middleware"

	"github.com/gin-gonic/gin"
)

// RouteRegistrar 由需要挂载 HTTP 路由的业务模块实现。
type RouteRegistrar interface {
	RegisterRoutes(public, protected *gin.RouterGroup)
}

// Dependencies 是 HTTP 传输层需要的最小依赖集合。
type Dependencies struct {
	DocumentRoutes RouteRegistrar
	Ready          func(context.Context) error
}

// New 创建配置完成但尚未监听端口的 Gin 引擎。
func New(conf *config.Config, dependencies Dependencies) *gin.Engine {
	gin.SetMode(conf.ServerConfig.Mode)

	engine := gin.New()
	engine.Use(middleware.GinLogger())
	engine.Use(middleware.GinRecovery())
	engine.Use(middleware.CORS(conf))

	engine.GET("/healthz", healthCheck)
	engine.GET("/readyz", readinessCheck(dependencies.Ready))

	v1 := engine.Group("/api/v1")
	v1.Use(middleware.TraceID())

	public := v1.Group("/public")

	protected := v1.Group("/protected")
	protected.Use(middleware.AuthRequired(conf))
	protected.Use(middleware.CSRFProtection(sessioncookie.UserCSRFCookie))

	managerProtected := v1.Group("/protected")
	managerProtected.Use(middleware.ManagerAuthRequired(conf))
	managerProtected.Use(middleware.CSRFProtection(sessioncookie.ManagerCSRFCookie))

	authapi.RegisterRoutes(public, protected)
	managerapi.RegisterRoutes(public, managerProtected)
	if dependencies.DocumentRoutes != nil {
		dependencies.DocumentRoutes.RegisterRoutes(public, protected)
	}
	return engine
}

func healthCheck(c *gin.Context) {
	responses.OK(c, gin.H{"status": "ok", "service": "gin-backend"})
}

func readinessCheck(ready func(context.Context) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		if ready == nil {
			responses.Fail(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "服务暂未就绪")
			return
		}
		if err := ready(c.Request.Context()); err != nil {
			slog.Warn("readiness_check_failed", slog.String("error", err.Error()))
			responses.Fail(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "服务暂未就绪")
			return
		}
		responses.OK(c, gin.H{"status": "ready", "service": "gin-backend"})
	}
}
