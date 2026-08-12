// automigrate — 独立编译的数据库表结构迁移工具
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"gin-backend/internal/common/base/logger"
	"gin-backend/internal/config"
)

func main() {
	var (
		configPath string
		only       string
		withIndex  bool
	)
	flag.StringVar(&configPath, "config", defaultConfigPath(), "配置文件路径 (默认: 环境变量 GIN_CONFIG_PATH 或 ./configs/config.yaml)")
	flag.StringVar(&only, "only", "all", "仅迁移指定业务: all | auth | markdown | chat")
	flag.BoolVar(&withIndex, "index", false, "markdown 迁移时同时创建 pg_search BM25 全文检索索引 (需已安装扩展)")
	flag.Parse()

	// 1. 加载配置 (与主服务一致, 内含 ConfigCheck)
	conf, err := config.Load(configPath)
	if err != nil {
		fatalf("加载配置失败: %v", err)
	}
	slog.SetDefault(logger.NewLogger(conf.LogConfig))

	// 2. 按目标执行业务迁移 (具体逻辑见 automigrate.go)
	migrateAll := only == "all"
	if only == "auth" || migrateAll {
		if err := migrateAuth(conf); err != nil {
			fatalf("auth 迁移失败: %v", err)
		}
	}
	if only == "markdown" || migrateAll {
		if err := migrateMarkdown(conf, withIndex); err != nil {
			fatalf("markdown 迁移失败: %v", err)
		}
	}
	if only == "chat" || migrateAll {
		if err := migrateChat(conf); err != nil {
			fatalf("chat 迁移失败: %v", err)
		}
	}
	if !migrateAll && only != "auth" && only != "markdown" && only != "chat" {
		slog.Error("未知的 -only 值 (可选 all|auth|markdown|chat)", slog.String("value", only))
		flag.Usage()
		os.Exit(2)
	}

	slog.Info("迁移完成")
}

// defaultConfigPath 默认配置路径: 环境变量 GIN_CONFIG_PATH > ./configs/config.yaml
func defaultConfigPath() string {
	if p := os.Getenv("GIN_CONFIG_PATH"); p != "" {
		return p
	}
	return "./configs/config.yaml"
}

// fatalf 打印错误并退出
func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[automigrate] 错误: "+format+"\n", args...)
	os.Exit(1)
}
