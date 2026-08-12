package middleware

import (
	"time"

	"github.com/gin-contrib/cors"

	"gin-backend/internal/config"

	"github.com/gin-gonic/gin"
)

// CORS 返回 CORS 中间件
// NOTE: 该中间件的配置与实现处于开发中阶段
func CORS(c *config.Config) gin.HandlerFunc {
	cfg := c.CustomConfig.CORS

	return cors.New(cors.Config{
		AllowOrigins:     cfg.AllowOrigins,
		AllowMethods:     cfg.AllowMethods,
		AllowHeaders:     cfg.AllowHeaders,
		ExposeHeaders:    cfg.ExposeHeaders,
		AllowCredentials: cfg.AllowCredentials,
		MaxAge:           time.Duration(cfg.MaxAge),
	})
}
