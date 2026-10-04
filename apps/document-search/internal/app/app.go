// Package app is the document search composition root: configuration wiring,
// database connectivity, the event consumer and the process lifecycle. It
// contains no business rules.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"document-search/internal/application"
	"document-search/internal/config"
	"document-search/internal/infrastructure/postgres"
	"document-search/internal/interfaces/grpcapi"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
)

// Service is the assembled document search process.
type Service struct {
	config     config.Config
	logger     *slog.Logger
	pool       *postgres.Pool
	vectors    application.VectorIndex
	grpcServer *grpc.Server
	httpServer *http.Server
	health     *health.Server
	consumer   *application.Consumer
}

// New opens the database, opens the vector collection of the active index
// generation, validates the boundary key and assembles the service.
//
// The vector collection is opened here rather than lazily on the first query:
// a service that answers searches while its vector half is unreachable would
// report a total that does not describe the index it claims to maintain.
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
	vectors, err := application.NewVectorIndex(ctx, cfg, pool)
	if err != nil {
		_ = pool.Close()
		return nil, err
	}
	api, err := grpcapi.New(cfg, pool, boundaryKey, logger, vectors)
	if err != nil {
		closeVectorIndex(vectors)
		_ = pool.Close()
		return nil, err
	}
	consumer, err := application.NewConsumer(application.ConsumerDependencies{
		Config:      cfg,
		Pool:        pool,
		Logger:      logger,
		VectorIndex: vectors,
	})
	if err != nil {
		closeVectorIndex(vectors)
		_ = pool.Close()
		return nil, err
	}

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(api.UnaryInterceptor()),
		grpc.StreamInterceptor(api.StreamInterceptor()),
	)
	api.Register(grpcServer)
	healthServer := health.NewServer()
	healthv1.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", healthv1.HealthCheckResponse_NOT_SERVING)

	probeMux := http.NewServeMux()
	probeMux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"status":"ok","service":"document-search"}`))
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
		// The vector collection is part of the index this service serves, so a
		// service that cannot reach it is not ready to answer: the keyword half
		// alone would answer with a total the hybrid query does not produce.
		if vectors != nil {
			if err := vectors.Health(probeCtx); err != nil {
				logger.Warn("document-search vector backend is unavailable", "error", err.Error())
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusServiceUnavailable)
				_, _ = writer.Write([]byte(`{"status":"unavailable","reason":"vector-index"}`))
				return
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"status":"ready","service":"document-search"}`))
	})
	probeMux.HandleFunc("/info", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(writer, `{"service":"document-search","audience":%q,"schema":%q,"collection_alias":%q}`,
			cfg.Auth.Audience, cfg.Postgres.Schema, cfg.Index.CollectionAlias)
	})

	return &Service{
		config:     cfg,
		logger:     logger,
		pool:       pool,
		vectors:    vectors,
		consumer:   consumer,
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

// closeVectorIndex releases the vector collection when assembly fails partway.
func closeVectorIndex(vectors application.VectorIndex) {
	if vectors != nil {
		_ = vectors.Close()
	}
}

// Pool exposes the database pool to tests.
func (service *Service) Pool() *postgres.Pool { return service.pool }

// VectorIndex exposes the opened vector collection to tests and operators.
func (service *Service) VectorIndex() application.VectorIndex { return service.vectors }

// ServeGRPC serves the gRPC boundary.
func (service *Service) ServeGRPC(listener net.Listener) error {
	return service.grpcServer.Serve(listener)
}

// ServeHTTP serves the probe boundary.
func (service *Service) ServeHTTP(listener net.Listener) error {
	return service.httpServer.Serve(listener)
}

// Consume follows the document service Outbox until the context is cancelled.
func (service *Service) Consume(ctx context.Context) error {
	if service.consumer == nil {
		return errors.New("document-search: event consumer is not configured")
	}
	return service.consumer.Run(ctx)
}

// WaitReady blocks until the database answers, so the process only reports ready
// once it can serve business calls.
func (service *Service) WaitReady(ctx context.Context) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := service.pool.Ping(ctx); err == nil {
			service.health.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("document-search: database is not reachable")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Shutdown stops the boundaries and closes the database pool and the vector
// collection.
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
	vectorErr := closeVectorIndexError(service.vectors)
	poolErr := service.pool.Close()
	return errors.Join(httpErr, vectorErr, poolErr)
}

// Close releases resources without a graceful drain.
func (service *Service) Close() error {
	if service == nil {
		return nil
	}
	service.grpcServer.Stop()
	vectorErr := closeVectorIndexError(service.vectors)
	return errors.Join(vectorErr, service.pool.Close())
}

// closeVectorIndexError closes the vector collection and reports a real failure.
func closeVectorIndexError(vectors application.VectorIndex) error {
	if vectors == nil {
		return nil
	}
	return vectors.Close()
}
