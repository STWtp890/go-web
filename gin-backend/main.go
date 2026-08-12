package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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

	// ...

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// 998. 启动 HTTP 服务
	go StartServer(r, conf, quit)

	// 999. 阻塞主 goroutine，等待信号
	<-quit
	slog.Info("服务正在关闭...")
	os.Exit(0)

}

// configPath 获取配置文件路径
// 优先级: 环境变量 > 默认路径
func configPath() string {
	if p := os.Getenv("GIN_CONFIG_PATH"); p != "" {
		return p
	}
	return "./config.yaml"
}

// LoadConfig 加载配置文件并初始化日志、JWT 密钥等
func LoadConfig(conf *config.Config) {
	// 1.2.1 初始化日志
	l := logger.NewLogger(conf.LogConfig)
	slog.SetDefault(l)

	// 1.2.2 重定向 Gin 内部 debug 输出到 slog 的 writer
	gin.DefaultWriter = logger.GinWriter(conf.LogConfig)
	gin.DefaultErrorWriter = logger.GinWriter(conf.LogConfig)

	// 1.3 加载 RSA 密钥对(必须在加载配置后, 路由初始化之前)
	err := conf.CustomConfig.JWT.LoadKeys()
	if err != nil {
		panic(fmt.Sprintf("加载 JWT 密钥失败: %v", err))
	}
}

// StartServer 启动 HTTP 服务
// :Param
// - `r` gin.Engine 实例
// - `conf` 配置文件
func StartServer(r *gin.Engine, conf *config.Config, quit <-chan os.Signal) {
	err := r.RunTLS(fmt.Sprintf(":%d", conf.ServerConfig.Port), conf.TLSConfig.CertFile, conf.TLSConfig.KeyFile)
	if err != nil {
		slog.Error(
			"服务启动失败",
			slog.String("error", err.Error()),
		)
	}
}
