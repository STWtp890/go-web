// document-index-worker 独立投递已提交的文档索引事件。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	postgresqlconn "gin-backend/internal/common/base/connection/postgresql"
	"gin-backend/internal/config"
	"gin-backend/internal/modules/document/application"
	"gin-backend/internal/modules/document/infrastructure/mixinsearch"
	documentpostgresql "gin-backend/internal/modules/document/infrastructure/postgresql"

	"github.com/google/uuid"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "document-index-worker 退出: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := configPathFromArgs(os.Args[1:])
	conf, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("加载配置: %w", err)
	}
	delivery := conf.IndexDeliveryConfig
	_ = flag.String("config", configPath, "gin-backend YAML config path")
	enabled := flag.Bool("enabled", delivery.Enabled, "enable document index delivery consumption")
	address := flag.String("mixin-search-address", envOrDefault("MIXIN_SEARCH_GRPC_ADDRESS", delivery.GRPCAddress), "mixin-search gRPC address")
	workerID := flag.String("worker-id", defaultWorkerID(), "stable worker process identifier")
	batchSize := flag.Int("batch-size", delivery.BatchSize, "maximum events claimed per poll")
	concurrency := flag.Int("concurrency", delivery.Concurrency, "maximum concurrent documents")
	pollInterval := flag.Duration("poll-interval", delivery.PollInterval, "outbox poll interval")
	leaseDuration := flag.Duration("lease-duration", delivery.LeaseDuration, "event processing lease")
	indexTimeout := flag.Duration("index-timeout", delivery.IndexTimeout, "IndexDocumentVersion deadline")
	controlTimeout := flag.Duration("control-timeout", delivery.ControlTimeout, "control RPC deadline")
	baseBackoff := flag.Duration("base-backoff", delivery.BaseBackoff, "minimum retry backoff")
	maxBackoff := flag.Duration("max-backoff", delivery.MaxBackoff, "maximum retry backoff")
	maxSendBytes := flag.Int("max-send-bytes", delivery.MaxSendBytes, "maximum gRPC request size")
	callerID := flag.String("caller-id", conf.MixinSearchSecurity.IndexCallerID, "mixin-search capability caller identity")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if !*enabled {
		slog.Info("文档索引投递已禁用，Outbox 事件将保留", slog.String("config", configPath))
		<-ctx.Done()
		return nil
	}
	db, err := postgresqlconn.NewDB(conf.PostgresConfig, conf.LogConfig.Level)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := postgresqlconn.Close(db); closeErr != nil {
			slog.Error("关闭文档数据库失败", slog.String("error", closeErr.Error()))
		}
	}()
	store, err := documentpostgresql.New(db)
	if err != nil {
		return err
	}
	issuer, err := mixinsearch.NewCapabilityIssuerFromConfig(
		conf.MixinSearchSecurity.CapabilityKeyPath,
		conf.MixinSearchSecurity.Issuer,
		*callerID,
		conf.MixinSearchSecurity.Audience,
		conf.MixinSearchSecurity.TokenTTL,
	)
	if err != nil {
		return err
	}
	client, err := mixinsearch.New(*address, *maxSendBytes, issuer)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := client.Close(); closeErr != nil {
			slog.Error("关闭 mixin-search 连接失败", slog.String("error", closeErr.Error()))
		}
	}()
	worker, err := application.NewIndexWorker(store, client, *workerID, application.IndexWorkerConfig{
		BatchSize: *batchSize, Concurrency: *concurrency, PollInterval: *pollInterval,
		LeaseDuration: *leaseDuration, IndexTimeout: *indexTimeout, ControlTimeout: *controlTimeout,
		BaseBackoff: *baseBackoff, MaxBackoff: *maxBackoff,
	})
	if err != nil {
		return err
	}

	slog.Info("文档索引投递 Worker 已启动", slog.String("worker_id", *workerID), slog.String("mixin_search", *address))
	return worker.Run(ctx)
}

func configPathFromArgs(args []string) string {
	path := envOrDefault("GIN_CONFIG_PATH", "configs/config.yaml")
	for index, argument := range args {
		if (argument == "-config" || argument == "--config") && index+1 < len(args) {
			return args[index+1]
		}
		if strings.HasPrefix(argument, "-config=") {
			return strings.TrimPrefix(argument, "-config=")
		}
		if strings.HasPrefix(argument, "--config=") {
			return strings.TrimPrefix(argument, "--config=")
		}
	}
	return path
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func defaultWorkerID() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown-host"
	}
	return fmt.Sprintf("%s-%d-%s", hostname, os.Getpid(), uuid.NewString())
}
