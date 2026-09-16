package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"mixin-search/internal/rag"
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
	flag.Parse()

	if *maxReceiveBytes <= 0 {
		log.Fatal("max-receive-bytes must be greater than zero")
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

	server := newGRPCServer(*maxReceiveBytes, handler)

	log.Printf(
		"RAG gRPC server listening on %s (store=%s control_store=%s)",
		listener.Addr(),
		strings.ToLower(*backend),
		strings.ToLower(*controlBackend),
	)
	if err := server.Serve(listener); err != nil {
		log.Fatal(err)
	}
}

func newGRPCServer(maxReceiveBytes int, handler mixinsearchv1.RAGServiceServer) *grpc.Server {
	server := grpc.NewServer(grpc.MaxRecvMsgSize(maxReceiveBytes))
	mixinsearchv1.RegisterRAGServiceServer(server, handler)

	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(server, healthServer)
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus(
		mixinsearchv1.RAGService_ServiceDesc.ServiceName,
		healthpb.HealthCheckResponse_SERVING,
	)

	reflection.Register(server)
	return server
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
