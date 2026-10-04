// Command qq-search runs the raw QQ content retrieval service.
//
// It owns the raw QQ search data only: original chat messages and original files,
// in separate data models, tables and index namespaces. It consumes qqsource.v1
// events from py-agent and never calls back into py-agent while answering a
// query.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"qq-search/internal/app"
	"qq-search/internal/config"
)

func main() {
	if err := run(); err != nil {
		slog.Error("qq search exited with an error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", os.Getenv("QQ_SEARCH_CONFIG"), "path to the YAML configuration file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	loaded, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if err := loaded.Validate(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	service, err := app.New(ctx, loaded, logger)
	if err != nil {
		return err
	}
	defer func() { _ = service.Close() }()

	grpcListener, err := net.Listen("tcp", loaded.GRPCListenAddress)
	if err != nil {
		return fmt.Errorf("qq-search: listen on %s: %w", loaded.GRPCListenAddress, err)
	}
	httpListener, err := net.Listen("tcp", loaded.HTTPListenAddress)
	if err != nil {
		return fmt.Errorf("qq-search: listen on %s: %w", loaded.HTTPListenAddress, err)
	}

	serveErrors := make(chan error, 2)
	go func() {
		logger.Info("qq search gRPC boundary listening", "address", grpcListener.Addr().String())
		if err := service.ServeGRPC(grpcListener); err != nil {
			serveErrors <- fmt.Errorf("grpc serve: %w", err)
		}
	}()
	go func() {
		logger.Info("qq search probe boundary listening", "address", httpListener.Addr().String())
		if err := service.ServeHTTP(httpListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrors <- fmt.Errorf("http serve: %w", err)
		}
	}()

	if err := service.WaitReady(ctx); err != nil {
		return err
	}
	logger.Info("qq search ready")

	select {
	case <-ctx.Done():
		logger.Info("qq search shutting down")
	case err := <-serveErrors:
		stop()
		shutdownTimeout, _ := loaded.Shutdown()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = service.Shutdown(shutdownCtx)
		return err
	}

	shutdownTimeout, err := loaded.Shutdown()
	if err != nil {
		return err
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return service.Shutdown(shutdownCtx)
}
