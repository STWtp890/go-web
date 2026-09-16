// document-index-admin 提供索引失败检查、重放、对账和全量重建入口。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	postgresqlconn "gin-backend/internal/common/base/connection/postgresql"
	"gin-backend/internal/config"
	"gin-backend/internal/modules/document/application"
	"gin-backend/internal/modules/document/infrastructure/mixinsearch"
	documentpostgresql "gin-backend/internal/modules/document/infrastructure/postgresql"

	"github.com/google/uuid"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "document-index-admin 退出: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPathDefault := configPathFromArgs(os.Args[1:])
	conf, err := config.Load(configPathDefault)
	if err != nil {
		return fmt.Errorf("加载配置: %w", err)
	}
	global := flag.NewFlagSet("document-index-admin", flag.ContinueOnError)
	_ = global.String("config", configPathDefault, "gin-backend YAML config path")
	address := global.String("mixin-search-address", envOrDefault("MIXIN_SEARCH_GRPC_ADDRESS", conf.IndexDeliveryConfig.GRPCAddress), "mixin-search gRPC address")
	maxSendBytes := global.Int("max-send-bytes", conf.IndexDeliveryConfig.MaxSendBytes, "maximum gRPC request size")
	if err := global.Parse(os.Args[1:]); err != nil {
		return err
	}
	args := global.Args()
	if len(args) == 0 {
		return errors.New("用法: document-index-admin [全局参数] status|shadow-status|failures|reconcile|rebuild ...")
	}
	db, err := postgresqlconn.NewDB(conf.PostgresConfig, conf.LogConfig.Level)
	if err != nil {
		return err
	}
	defer postgresqlconn.Close(db)
	store, err := documentpostgresql.New(db)
	if err != nil {
		return err
	}
	client, err := mixinsearch.New(*address, *maxSendBytes)
	if err != nil {
		return err
	}
	defer client.Close()
	service, err := application.NewIndexMaintenanceService(store, client)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "status":
		return runStatus(ctx, store, client, conf.IndexDeliveryConfig.Enabled, *address, conf.IndexDeliveryConfig.HealthTimeout, args[1:])
	case "shadow-status":
		return runShadowStatus(ctx, store, args[1:])
	case "failures":
		return runFailures(ctx, service, args[1:])
	case "reconcile":
		return runReconcile(ctx, service, args[1:])
	case "rebuild":
		return runRebuild(ctx, service, args[1:])
	default:
		return fmt.Errorf("未知命令 %q", args[0])
	}
}

func runShadowStatus(ctx context.Context, store *documentpostgresql.Repository, args []string) error {
	flags := flag.NewFlagSet("shadow-status", flag.ContinueOnError)
	source := flags.String("source", "", "optional runtime or evaluation observation source")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *source != "" && *source != "runtime" && *source != "evaluation" {
		return errors.New("shadow-status source must be runtime or evaluation")
	}
	stats, err := store.GetShadowSearchStats(ctx, *source)
	if err != nil {
		return err
	}
	return writeJSON(stats)
}

func runStatus(
	ctx context.Context,
	store *documentpostgresql.Repository,
	client *mixinsearch.Client,
	enabled bool,
	address string,
	defaultTimeout time.Duration,
	args []string,
) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	timeout := flags.Duration("timeout", defaultTimeout, "mixin-search health timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	stats, err := store.GetIndexDeliveryStats(ctx)
	if err != nil {
		return err
	}
	healthCtx, cancel := context.WithTimeout(ctx, *timeout)
	healthErr := client.CheckHealth(healthCtx)
	cancel()
	health := map[string]any{"address": address, "available": healthErr == nil}
	if healthErr != nil {
		health["error"] = healthErr.Error()
	}
	return writeJSON(map[string]any{
		"enabled":     enabled,
		"mixinSearch": health,
		"outbox":      stats,
	})
}

func runFailures(ctx context.Context, service *application.IndexMaintenanceService, args []string) error {
	if len(args) == 0 {
		return errors.New("用法: failures list|replay")
	}
	switch args[0] {
	case "list":
		flags := flag.NewFlagSet("failures list", flag.ContinueOnError)
		limit := flags.Int("limit", 100, "maximum failures")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		events, err := service.Failures(ctx, *limit)
		if err != nil {
			return err
		}
		return writeJSON(events)
	case "replay":
		flags := flag.NewFlagSet("failures replay", flag.ContinueOnError)
		eventID := flags.String("event-id", "", "dead-letter event UUID")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if err := service.Replay(ctx, *eventID); err != nil {
			return err
		}
		fmt.Printf("REQUEUED event_id=%s\n", *eventID)
		return nil
	default:
		return fmt.Errorf("未知 failures 命令 %q", args[0])
	}
}

func runReconcile(ctx context.Context, service *application.IndexMaintenanceService, args []string) error {
	flags := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	limit := flags.Int("limit", 100, "maximum documents scanned")
	if err := flags.Parse(args); err != nil {
		return err
	}
	created, err := service.Reconcile(ctx, *limit)
	if err != nil {
		return err
	}
	fmt.Printf("RECONCILE_ENQUEUED=%d\n", created)
	return nil
}

func runRebuild(ctx context.Context, service *application.IndexMaintenanceService, args []string) error {
	if len(args) == 0 {
		return errors.New("用法: rebuild start|status")
	}
	flags := flag.NewFlagSet("rebuild "+args[0], flag.ContinueOnError)
	runID := flags.String("run-id", "", "rebuild run UUID")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if args[0] == "start" && *runID == "" {
		*runID = uuid.NewString()
	}
	if *runID == "" {
		return errors.New("-run-id is required")
	}
	var (
		run any
		err error
	)
	switch args[0] {
	case "start":
		run, err = service.PrepareRebuild(ctx, *runID)
	case "status":
		run, err = service.RebuildStatus(ctx, *runID)
	default:
		return fmt.Errorf("未知 rebuild 命令 %q", args[0])
	}
	if err != nil {
		return err
	}
	return writeJSON(run)
}

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
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
