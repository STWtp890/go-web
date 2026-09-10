// Package app 负责 gin-backend 的配置、依赖和 HTTP 服务生命周期装配。
package app

import (
	"fmt"
	"log/slog"
	"os"

	"gin-backend/internal/common/base/logger"
	"gin-backend/internal/config"

	"github.com/gin-gonic/gin"
)

const defaultConfigPath = "configs/config.yaml"

// Run 装配运行时依赖并启动 HTTP 服务。
func Run() error {
	conf, err := config.Load(configPath())
	if err != nil {
		return fmt.Errorf("加载配置文件失败: %w", err)
	}

	if err := initializeRuntime(conf); err != nil {
		return err
	}

	cleanup := initDependencies(conf)
	defer cleanup()

	engine := gin.New()
	setupRoutes(engine, conf)

	address := fmt.Sprintf(":%d", conf.ServerConfig.Port)
	slog.Info("HTTP 服务启动", slog.String("address", address))
	if err := engine.Run(address); err != nil {
		return fmt.Errorf("HTTP 服务启动失败: %w", err)
	}
	return nil
}

// configPath 按“环境变量优先、默认路径兜底”的顺序获取配置文件路径。
func configPath() string {
	if path := os.Getenv("GIN_CONFIG_PATH"); path != "" {
		return path
	}
	return defaultConfigPath
}

// initializeRuntime 初始化日志输出和 JWT 密钥等进程级配置。
func initializeRuntime(conf *config.Config) error {
	slog.SetDefault(logger.NewLogger(conf.LogConfig))
	gin.DefaultWriter = logger.GinWriter(conf.LogConfig)
	gin.DefaultErrorWriter = logger.GinWriter(conf.LogConfig)

	if err := conf.CustomConfig.JWT.LoadKeys(); err != nil {
		return fmt.Errorf("加载 JWT 密钥失败: %w", err)
	}
	return nil
}
