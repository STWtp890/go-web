// Package app is the document service composition root: it owns configuration
// wiring, database connectivity and the process lifecycle. It contains no
// business rules.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"document-service/internal/config"
	"document-service/internal/infrastructure/postgres"
	"document-service/internal/interfaces/grpcapi"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Service is the assembled document service process.
type Service struct {
	config     config.Config
	logger     *slog.Logger
	pool       *postgres.Pool
	grpcServer *grpc.Server
	httpServer *http.Server
	health     *health.Server
}

// New opens the database, validates the boundary key and assembles the service.
// Every failure here is fatal: the service never starts in a degraded mode.
func New(ctx context.Context, cfg config.Config, logger *slog.Logger) (*Service, error) {
	if logger == nil {
		logger = slog.Default()
	}
	boundaryKey, err := cfg.BoundaryKey()
	if err != nil {
		return nil, err
	}

	pool, err := postgres.Open(ctx, cfg.Postgres)
	if err != nil {
		return nil, err
	}

	api, err := grpcapi.New(cfg, pool, boundaryKey, logger)
	if err != nil {
		_ = pool.Close()
		return nil, err
	}
	// The interceptors are installed on the server so the authentication boundary
	// cannot be bypassed by registering the service twice.
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(api.UnaryInterceptor()),
		grpc.StreamInterceptor(api.StreamInterceptor()),
	)
	api.Register(grpcServer)

	healthServer := health.NewServer()
	healthv1.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", healthv1.HealthCheckResponse_NOT_SERVING)
	// Reflection stays off by default: it would let any caller enumerate the
	// administrative surface of the fact source.
	if cfg.Auth.EnableReflection {
		reflection.Register(grpcServer)
	}

	probeMux := http.NewServeMux()
	probeMux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"status":"ok","service":"document-service"}`))
	})
	probeMux.HandleFunc("/readyz", func(writer http.ResponseWriter, request *http.Request) {
		probeCtx, cancel := context.WithTimeout(request.Context(), 3*time.Second)
		defer cancel()
		if err := pool.Ping(probeCtx); err != nil {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"status":"unavailable","reason":"database"}`))
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"status":"ready","service":"document-service"}`))
	})
	probeMux.HandleFunc("/info", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(writer, `{"service":"document-service","audience":%q,"schema":%q}`, cfg.Auth.Audience, cfg.Postgres.Schema)
	})

	return &Service{
		config:     cfg,
		logger:     logger,
		pool:       pool,
		grpcServer: grpcServer,
		health:     healthServer,
		httpServer: &http.Server{
			Handler:           probeMux,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
	}, nil
}

// Pool exposes the database pool to the composition root and tests.
func (service *Service) Pool() *postgres.Pool { return service.pool }

// ServeGRPC serves the gRPC boundary until the listener is closed.
func (service *Service) ServeGRPC(listener net.Listener) error {
	return service.grpcServer.Serve(listener)
}

// ServeHTTP serves the probe and tooling boundary.
func (service *Service) ServeHTTP(listener net.Listener) error {
	return service.httpServer.Serve(listener)
}

// WaitReady blocks until the database answers, so the process only reports ready
// once it can actually serve business calls.
func (service *Service) WaitReady(ctx context.Context) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := service.pool.Ping(ctx); err == nil {
			service.health.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("document-service: database is not reachable")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Shutdown stops both boundaries and closes the database pool.
func (service *Service) Shutdown(ctx context.Context) error {
	service.health.SetServingStatus("", healthv1.HealthCheckResponse_NOT_SERVING)
	done := make(chan struct{})
	go func() {
		service.grpcServer.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		service.grpcServer.Stop()
	}
	httpErr := service.httpServer.Shutdown(ctx)
	poolErr := service.pool.Close()
	return errors.Join(httpErr, poolErr)
}

// Close releases resources without a graceful drain. It exists so New failures
// and tests can clean up deterministically.
func (service *Service) Close() error {
	if service == nil {
		return nil
	}
	service.grpcServer.Stop()
	return service.pool.Close()
}
