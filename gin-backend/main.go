package main

import (
	"fmt"
	"log/slog"
	"os"

	"gin-backend/internal/common/base/logger"
	"gin-backend/internal/config"
	"gin-backend/internal/service"

	"github.com/gin-gonic/gin"
)

func main() {
	// 1.1 加载配置
	conf, err := config.Load(configPath())
	if err != nil {
		panic(fmt.Sprintf("加载配置文件失败: %v", err))
	}

	LoadConfig(conf)

	// 1.2 初始化, 例如默认连接 (PostgreSQL/Redis) 等, 生命周期由返回的清理函数维护
	cleanup := service.Init(conf)
	defer cleanup()

	// 2. 初始化路由
	r := gin.New()
	service.Setup(r, conf)

	// 3. 通过 Gin Engine 启动 HTTP 服务
	address := fmt.Sprintf(":%d", conf.ServerConfig.Port)
	slog.Info("HTTP 服务启动", slog.String("address", address))
	if err := r.Run(address); err != nil {
		panic(fmt.Sprintf("HTTP 服务启动失败: %v", err))
	}
}

// configPath 获取配置文件路径
// 优先级: 环境变量 > 默认路径
func configPath() string {
	if p := os.Getenv("GIN_CONFIG_PATH"); p != "" {
		return p
	}
	return "configs/config.yaml"
}

// LoadConfig 加载配置文件并初始化日志、JWT 密钥等
func LoadConfig(conf *config.Config) {
	// 1. 初始化日志
	l := logger.NewLogger(conf.LogConfig)
	slog.SetDefault(l)

	// 2. 重定向 Gin 内部 debug 输出到 slog 的 writer
	gin.DefaultWriter = logger.GinWriter(conf.LogConfig)
	gin.DefaultErrorWriter = logger.GinWriter(conf.LogConfig)

	// 3. 加载 RSA 密钥对(必须在加载配置后, 路由初始化之前)
	err := conf.CustomConfig.JWT.LoadKeys()
	if err != nil {
		panic(fmt.Sprintf("加载 JWT 密钥失败: %v", err))
	}
}