package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"

	"mixin-search/internal/rag"
	"mixin-search/internal/security"
	grpcadapter "mixin-search/internal/transport/grpc"
	mixinsearchv1 "packages/gen/mixin-search/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

const localEmbeddingDimensions = 64

func main() {
	address := flag.String("grpc-address", "127.0.0.1:9090", "gRPC listen address")
	backend := flag.String("store", "memory", "vector store: memory, qdrant, or pgvector")
	qdrantHost := flag.String("qdrant-host", "localhost", "Qdrant gRPC host")
	qdrantPort := flag.Int("qdrant-port", 6334, "Qdrant gRPC port")
	qdrantCollection := flag.String("qdrant-collection", "rag_chunks", "Qdrant collection")
	qdrantTLS := flag.Bool("qdrant-tls", false, "connect to Qdrant using TLS")
	pgDSN := flag.String("pg-dsn", defaultPGDSN(), "PostgreSQL connection string")
	controlBackend := flag.String("control-store", defaultControlStore(), "control store: memory or postgres")
	controlDSN := flag.String("control-dsn", defaultControlDSN(), "control PostgreSQL connection string")
	controlNamespace := flag.String("control-namespace", defaultControlNamespace(), "control state namespace")
	controlBootstrap := flag.Bool("control-bootstrap", defaultControlBootstrap(), "initialize a missing control namespace once")
	maxReceiveBytes := flag.Int("max-receive-bytes", 16<<20, "maximum gRPC request size")
	capabilityKeyPath := flag.String(
		"capability-key-file",
		os.Getenv("MIXIN_SEARCH_CAPABILITY_KEY_FILE"),
		"file holding the shared boundary key for caller capabilities (required)",
	)
	capabilityKey := flag.String("capability-key", "", "boundary key inline; prefer -capability-key-file outside tests")
	capabilityIssuer := flag.String(
		"capability-issuer",
		envOrDefault("MIXIN_SEARCH_CAPABILITY_ISSUER", "go-web"),
		"accepted capability issuer",
	)
	capabilityAudience := flag.String(
		"capability-audience",
		envOrDefault("MIXIN_SEARCH_CAPABILITY_AUDIENCE", "mixin-search"),
		"accepted capability audience",
	)
	callerRate := flag.Float64(
		"caller-rate-per-second",
		envFloatOrDefault("MIXIN_SEARCH_CALLER_RATE_PER_SECOND", 200),
		"per-caller request budget per second; 0 disables throttling",
	)
	callerBurst := flag.Int(
		"caller-burst",
		envIntOrDefault("MIXIN_SEARCH_CALLER_BURST", 400),
		"per-caller burst budget; 0 disables throttling",
	)
	enableReflection := flag.Bool(
		"enable-reflection",
		envBoolOrDefault("MIXIN_SEARCH_ENABLE_REFLECTION", true),
		"register gRPC reflection; a development aid that must be off when the port is exposed",
	)
	flag.Parse()

	if *maxReceiveBytes <= 0 {
		log.Fatal("max-receive-bytes must be greater than zero")
	}

	boundaryKey, err := loadBoundaryKey(*capabilityKeyPath, *capabilityKey)
	if err != nil {
		log.Fatalf("load capability boundary key: %v", err)
	}
	verifier, err := security.NewVerifier(boundaryKey, *capabilityIssuer, *capabilityAudience)
	if err != nil {
		log.Fatalf("build capability verifier: %v", err)
	}
	limiter := security.NewRateLimiter(*callerRate, *callerBurst)
	authenticator, err := grpcadapter.NewAuthenticator(grpcadapter.AuthConfig{
		Verifier: verifier,
		Limiter:  limiter,
		Audit:    security.SlogAuditSink(slog.Default()),
	})
	if err != nil {
		log.Fatalf("build boundary authenticator: %v", err)
	}

	ctx := context.Background()

	store, err := openStore(ctx, *backend, *qdrantHost, *qdrantPort, *qdrantCollection, *qdrantTLS, *pgDSN)
	if err != nil {
		log.Fatal(err)
	}
	service, err := rag.NewServiceWithStore(ctx, store)
	if err != nil {
		_ = store.Close()
		log.Fatal(err)
	}
	controlStore, closeControlStore, err := openControlStore(ctx, *controlBackend, *controlDSN, *controlNamespace, *controlBootstrap)
	if err != nil {
		_ = service.Close()
		log.Fatal(err)
	}
	contractService, err := rag.NewDocumentIndexServiceWithControlStore(ctx, service, controlStore)
	if err != nil {
		closeControlStore()
		_ = service.Close()
		log.Fatal(err)
	}
	defer closeControlStore()
	defer func() {
		if closeErr := service.Close(); closeErr != nil {
			log.Printf("close vector store: %v", closeErr)
		}
	}()
	handler, err := grpcadapter.NewServer(contractService)
	if err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		log.Fatalf("listen on %s: %v", *address, err)
	}
	defer listener.Close()

	server := newGRPCServer(*maxReceiveBytes, handler, authenticator, *enableReflection)

	log.Printf(
		"RAG gRPC server listening on %s (store=%s control_store=%s issuer=%s audience=%s throttling=%t reflection=%t)",
		listener.Addr(),
		strings.ToLower(*backend),
		strings.ToLower(*controlBackend),
		*capabilityIssuer,
		*capabilityAudience,
		limiter.Enabled(),
		*enableReflection,
	)
	if err := server.Serve(listener); err != nil {
		log.Fatal(err)
	}
}

// newGRPCServer assembles the gRPC server. The authenticator is mandatory: a
// server without it would accept any caller that can reach the port.
func newGRPCServer(
	maxReceiveBytes int,
	handler mixinsearchv1.RAGServiceServer,
	authenticator *grpcadapter.Authenticator,
	enableReflection bool,
) *grpc.Server {
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(maxReceiveBytes),
		grpc.UnaryInterceptor(authenticator.UnaryInterceptor),
	)
	mixinsearchv1.RegisterRAGServiceServer(server, handler)

	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(server, healthServer)
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus(
		mixinsearchv1.RAGService_ServiceDesc.ServiceName,
		healthpb.HealthCheckResponse_SERVING,
	)

	// Reflection lets anyone who reaches the port enumerate the API, so it is a
	// development aid rather than a product capability. Health stays registered
	// either way because container probes carry no caller credential.
	if enableReflection {
		reflection.Register(server)
	}
	return server
}

// loadBoundaryKey resolves the shared key from a file first and from an inline
// value second. A missing or too-short key is fatal: starting without it would
// produce a service that cannot authenticate anyone.
func loadBoundaryKey(path, inline string) ([]byte, error) {
	path = strings.TrimSpace(path)
	inline = strings.TrimSpace(inline)
	switch {
	case path != "":
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		key := []byte(strings.TrimSpace(string(contents)))
		if err := security.ValidateKey(key); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return key, nil
	case inline != "":
		key := []byte(inline)
		if err := security.ValidateKey(key); err != nil {
			return nil, err
		}
		return key, nil
	default:
		return nil, fmt.Errorf(
			"%w: set -capability-key-file (or MIXIN_SEARCH_CAPABILITY_KEY_FILE) to the shared boundary key",
			security.ErrInvalidKey,
		)
	}
}
func openStore(
	ctx context.Context,
	backend string,
	qdrantHost string,
	qdrantPort int,
	qdrantCollection string,
	qdrantTLS bool,
	pgDSN string,
) (rag.VectorStore, error) {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "memory":
		return rag.NewMemoryStore(), nil
	case "qdrant":
		return rag.NewQdrantStore(ctx, rag.QdrantConfig{
			Host:       qdrantHost,
			Port:       qdrantPort,
			APIKey:     os.Getenv("QDRANT_API_KEY"),
			UseTLS:     qdrantTLS,
			Collection: qdrantCollection,
			Dimensions: localEmbeddingDimensions,
		})
	case "pgvector":
		return rag.NewPGVectorStore(ctx, rag.PGVectorConfig{
			DSN:        pgDSN,
			Dimensions: localEmbeddingDimensions,
		})
	default:
		return nil, fmt.Errorf("unknown vector store %q", backend)
	}
}

func openControlStore(
	ctx context.Context,
	backend string,
	dsn string,
	namespace string,
	bootstrap bool,
) (rag.ControlStore, func(), error) {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "memory":
		return rag.NewMemoryControlStore(), func() {}, nil
	case "postgres":
		store, err := rag.NewPostgresControlStore(ctx, rag.PostgresControlStoreConfig{
			DSN:       dsn,
			Namespace: namespace,
			Bootstrap: bootstrap,
		})
		if err != nil {
			return nil, func() {}, err
		}
		return store, store.Close, nil
	default:
		return nil, func() {}, fmt.Errorf("unknown control store %q", backend)
	}
}

func defaultPGDSN() string {
	if value := os.Getenv("PGVECTOR_DSN"); value != "" {
		return value
	}
	return "postgres://rag:rag@localhost:5432/rag?sslmode=disable"
}

func defaultControlStore() string {
	if value := strings.TrimSpace(os.Getenv("CONTROL_STORE")); value != "" {
		return value
	}
	return "postgres"
}

func defaultControlDSN() string {
	if value := strings.TrimSpace(os.Getenv("CONTROL_DATABASE_DSN")); value != "" {
		return value
	}
	return "postgres://mixin_control:mixin_control@localhost:55432/mixin_control?sslmode=disable"
}

func defaultControlNamespace() string {
	if value := strings.TrimSpace(os.Getenv("CONTROL_STORE_NAMESPACE")); value != "" {
		return value
	}
	return "default"
}

func defaultControlBootstrap() bool {
	value := strings.TrimSpace(os.Getenv("CONTROL_STORE_BOOTSTRAP"))
	return value == "1" || strings.EqualFold(value, "true")
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envIntOrDefault(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		log.Fatalf("%s must be an integer: %v", name, err)
	}
	return parsed
}

func envFloatOrDefault(name string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		log.Fatalf("%s must be a number: %v", name, err)
	}
	return parsed
}

func envBoolOrDefault(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value == "1" || strings.EqualFold(value, "true")
}
