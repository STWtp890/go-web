// Command document-service runs the formal document business service.
//
// It is the only writer of formal documents, versions, knowledge spaces,
// membership, group-to-space bindings, resource authorization and the document
// change Outbox. It is not a generic SQL proxy.
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

	"document-service/internal/app"
	"document-service/internal/config"
)

func main() {
	if err := run(); err != nil {
		slog.Error("document service exited with an error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", os.Getenv("DOCUMENT_SERVICE_CONFIG"), "path to the YAML configuration file")
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
		return fmt.Errorf("document-service: listen on %s: %w", loaded.GRPCListenAddress, err)
	}
	httpListener, err := net.Listen("tcp", loaded.HTTPListenAddress)
	if err != nil {
		return fmt.Errorf("document-service: listen on %s: %w", loaded.HTTPListenAddress, err)
	}

	serveErrors := make(chan error, 2)
	go func() {
		logger.Info("document service gRPC boundary listening", "address", grpcListener.Addr().String())
		if err := service.ServeGRPC(grpcListener); err != nil {
			serveErrors <- fmt.Errorf("grpc serve: %w", err)
		}
	}()
	go func() {
		logger.Info("document service probe boundary listening", "address", httpListener.Addr().String())
		if err := service.ServeHTTP(httpListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrors <- fmt.Errorf("http serve: %w", err)
		}
	}()
	if err := service.WaitReady(ctx); err != nil {
		return err
	}
	logger.Info("document service ready")

	select {
	case <-ctx.Done():
		logger.Info("document service shutting down")
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
