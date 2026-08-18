package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	defer signal.Stop(quit)

	server := NewHTTPServer(r, conf)
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()

	select {
	case sig := <-quit:
		slog.Info("服务正在关闭...", slog.String("signal", sig.String()))
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("服务启动失败", slog.String("error", err.Error()))
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		slog.Error("服务优雅关闭失败", slog.String("error", err.Error()))
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

// NewHTTPServer 创建由反向代理终止 TLS 的 HTTP 服务。
func NewHTTPServer(r *gin.Engine, conf *config.Config) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf(":%d", conf.ServerConfig.Port),
		Handler:           r,
		ReadHeaderTimeout: minDuration(conf.ServerConfig.ReadTimeout, 10*time.Second),
		ReadTimeout:       conf.ServerConfig.ReadTimeout,
		// SSE/WebSocket 是长连接，http.Server 的全局 WriteTimeout 会在配置时间后强制断流。
		// 单次 API 写入由反向代理超时策略保护，流连接由各自的心跳和断线检测维护。
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
