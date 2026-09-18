package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"mixin-search/internal/chat"
	"mixin-search/internal/chatindex"
	"mixin-search/internal/rag"
	"mixin-search/internal/security"
	grpcadapter "mixin-search/internal/transport/grpc"
	mixinsearchchatv1 "packages/gen/mixin-search/chat/v1"
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
		envOrDefault("MIXIN_SEARCH_CAPABILITY_AUDIENCE", security.AudienceDocuments),
		"accepted capability audience for the document corpus",
	)
	chatCapabilityAudience := flag.String(
		"chat-capability-audience",
		envOrDefault("MIXIN_SEARCH_CHAT_CAPABILITY_AUDIENCE", security.AudienceChat),
		"accepted capability audience for the chat corpus; must differ from the document audience",
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
	// Chat corpus. Disabled by default: a deployment that does not consume chat
	// must not grow a second control plane and collection by accident. The
	// collection and namespace are deliberately separate names from the document
	// ones, because the isolation is structural.
	chatEnabled := flag.Bool(
		"chat-enabled",
		envBoolOrDefault("MIXIN_SEARCH_CHAT_ENABLED", false),
		"serve the chat corpus on its own collection, control namespace and reconciler",
	)
	chatCollection := flag.String(
		"chat-collection",
		envOrDefault("MIXIN_SEARCH_CHAT_COLLECTION", "go_web_chat_v1"),
		"chat vector collection; must differ from the document collection",
	)
	chatControlNamespace := flag.String(
		"chat-control-namespace",
		envOrDefault("MIXIN_SEARCH_CHAT_CONTROL_NAMESPACE", "chat-v1"),
		"chat control namespace in the chat control table",
	)
	// Capacity limits are configuration, not constants: the write path rewrites
	// the whole snapshot, so the limit depends on the deployment. Both default to
	// 0 (disabled) until the measured limits are confirmed; the measurements and
	// the proposed values are in docs/planning/CURRENT_IMPLEMENTATION_PLAN.md.
	chatMaxMessages := flag.Int(
		"chat-max-messages",
		envIntOrDefault("MIXIN_SEARCH_CHAT_MAX_MESSAGES", 0),
		"hard limit on indexed chat messages per corpus; 0 disables the limit",
	)
	chatMaxSnapshotBytes := flag.Int64(
		"chat-max-snapshot-bytes",
		envInt64OrDefault("MIXIN_SEARCH_CHAT_MAX_SNAPSHOT_BYTES", 0),
		"hard limit on the encoded chat control snapshot in bytes; 0 disables the limit",
	)
	// Ledger retention (ADR-015): an operation is remembered for the retention
	// window, with a count ceiling as a backstop. Both default to 0, which keeps
	// every entry, until the confirmed limits land.
	chatOperationRetention := flag.Duration(
		"chat-operation-retention",
		envDurationOrDefault("MIXIN_SEARCH_CHAT_OPERATION_RETENTION", 0),
		"how long the chat idempotency ledger keeps an operation; 0 keeps every entry",
	)
	chatOperationMaxEntries := flag.Int(
		"chat-operation-max-entries",
		envIntOrDefault("MIXIN_SEARCH_CHAT_OPERATION_MAX_ENTRIES", 0),
		"ceiling on chat idempotency ledger entries, oldest dropped first; 0 disables the ceiling",
	)
	flag.Parse()

	if *maxReceiveBytes <= 0 {
		log.Fatal("max-receive-bytes must be greater than zero")
	}

	boundaryKey, err := loadBoundaryKey(*capabilityKeyPath, *capabilityKey)
	if err != nil {
		log.Fatalf("load capability boundary key: %v", err)
	}
	if err := validateCorpusIsolation(*qdrantCollection, *chatCollection, *chatEnabled); err != nil {
		log.Fatalf("corpus isolation: %v", err)
	}
	if *chatEnabled {
		if err := validateChatVectorBackend(*backend); err != nil {
			log.Fatalf("chat vector backend: %v", err)
		}
	}
	if err := validateCapabilityAudiences(*capabilityAudience, *chatCapabilityAudience); err != nil {
		log.Fatalf("capability audiences: %v", err)
	}
	verifier, err := security.NewVerifier(boundaryKey, *capabilityIssuer, *capabilityAudience)
	if err != nil {
		log.Fatalf("build capability verifier: %v", err)
	}
	// The chat corpus gets its own verifier with its own audience. The two are
	// never interchangeable: a chat capability fails the document audience check
	// before any role is considered, and the reverse.
	chatVerifier, err := security.NewVerifier(boundaryKey, *capabilityIssuer, *chatCapabilityAudience)
	if err != nil {
		log.Fatalf("build chat capability verifier: %v", err)
	}
	limiter := security.NewRateLimiter(*callerRate, *callerBurst)
	authenticator, err := grpcadapter.NewAuthenticator(grpcadapter.AuthConfig{
		Verifier:     verifier,
		ChatVerifier: chatVerifier,
		Limiter:      limiter,
		Audit:        security.SlogAuditSink(slog.Default()),
	})
	if err != nil {
		log.Fatalf("build boundary authenticator: %v", err)
	}

	ctx := context.Background()

	store, err := openStore(ctx, *backend, *qdrantHost, *qdrantPort, *qdrantCollection, *qdrantTLS, *pgDSN)
	if err != nil {
		log.Fatal(err)
	}
	logStoreAlias(ctx, "document", store)
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

	// The vector-store projection is converged in the background so search
	// requests neither hold the global writer lock nor perform a projection write
	// of their own in the steady state.
	reconcilerCtx, stopProjectionReconciler := context.WithCancel(context.Background())
	defer stopProjectionReconciler()
	contractService.StartProjectionReconciler(reconcilerCtx)

	// The chat corpus is a second, independent control plane with its own
	// collection, its own control namespace and its own reconciler. It is built
	// only when enabled, so a deployment that does not consume chat does not grow
	// a second corpus by accident.
	var chatServer *grpcadapter.ChatServer
	if *chatEnabled {
		chatStore, err := openStore(ctx, *backend, *qdrantHost, *qdrantPort, *chatCollection, *qdrantTLS, *pgDSN)
		if err != nil {
			log.Fatalf("open chat vector store: %v", err)
		}
		logStoreAlias(ctx, "chat", chatStore)
		chatControlStore, closeChatControlStore, err := openChatControlStore(
			ctx, *controlBackend, *controlDSN, *chatControlNamespace, *controlBootstrap,
		)
		if err != nil {
			_ = chatStore.Close()
			log.Fatalf("open chat control store: %v", err)
		}
		defer closeChatControlStore()

		corpus, err := chatindex.New(ctx, chatindex.Config{
			VectorStore:         chatStore,
			ControlStore:        chatControlStore,
			StorageDomain:       *chatCollection,
			MaxMessages:         *chatMaxMessages,
			MaxSnapshotBytes:    *chatMaxSnapshotBytes,
			OperationRetention:  *chatOperationRetention,
			MaxOperationEntries: *chatOperationMaxEntries,
		})
		if err != nil {
			_ = chatStore.Close()
			log.Fatalf("build chat corpus: %v", err)
		}
		defer func() {
			if closeErr := corpus.Close(); closeErr != nil {
				log.Printf("close chat vector store: %v", closeErr)
			}
		}()
		corpus.StartProjectionReconciler(reconcilerCtx)
		chatServer, err = grpcadapter.NewChatServer(corpus.Service())
		if err != nil {
			log.Fatal(err)
		}
	}

	handler, err := grpcadapter.NewServer(contractService)
	if err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		log.Fatalf("listen on %s: %v", *address, err)
	}
	defer listener.Close()

	server := newGRPCServer(*maxReceiveBytes, handler, chatServer, authenticator, *enableReflection)

	log.Printf(
		"RAG gRPC server listening on %s (store=%s control_store=%s chat=%t chat_collection=%s issuer=%s audience=%s chat_audience=%s throttling=%t reflection=%t)",
		listener.Addr(),
		strings.ToLower(*backend),
		strings.ToLower(*controlBackend),
		*chatEnabled,
		*chatCollection,
		*capabilityIssuer,
		*capabilityAudience,
		*chatCapabilityAudience,
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
	chatHandler *grpcadapter.ChatServer,
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
	if chatHandler != nil {
		mixinsearchchatv1.RegisterChatIndexServiceServer(server, chatHandler)
		healthServer.SetServingStatus(
			mixinsearchchatv1.ChatIndexService_ServiceDesc.ServiceName,
			healthpb.HealthCheckResponse_SERVING,
		)
	}

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
			Generation: rag.DefaultQdrantGeneration,
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

// openChatControlStore mirrors openControlStore for the chat corpus. The two are
// separate functions on purpose: their store types differ, so a caller cannot
// accidentally hand one corpus's control state to the other.
func openChatControlStore(
	ctx context.Context,
	backend string,
	dsn string,
	namespace string,
	bootstrap bool,
) (chat.ControlStore, func(), error) {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "memory":
		return chat.NewMemoryControlStore(), func() {}, nil
	case "postgres":
		store, err := chat.NewPostgresControlStore(ctx, chat.PostgresControlStoreConfig{
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

// validateCorpusIsolation refuses a configuration in which the two corpora would
// share a collection. Collection and alias names are how isolation is enforced,
// so a collision is a configuration error rather than something to discover at
// query time.
func validateCorpusIsolation(documentCollection, chatCollection string, chatEnabled bool) error {
	documentCollection = strings.TrimSpace(documentCollection)
	chatCollection = strings.TrimSpace(chatCollection)
	if documentCollection == "" || chatCollection == "" {
		return errors.New("both collection names are required")
	}
	if chatEnabled && documentCollection == chatCollection {
		return fmt.Errorf("chat and document corpora must not share the collection %q", chatCollection)
	}
	if chatEnabled {
		// The configured names are aliases, and each alias is created over
		// <name>_<generation>. An alias that equals the other corpus's physical
		// collection would make startup fail on a name clash at best, and could
		// point one corpus at the other's data at worst.
		documentPhysical := documentCollection + "_" + rag.DefaultQdrantGeneration
		chatPhysical := chatCollection + "_" + rag.DefaultQdrantGeneration
		if chatCollection == documentPhysical || documentCollection == chatPhysical {
			return fmt.Errorf(
				"the two corpora must not use each other's physical collection as an alias (%q, %q)",
				documentCollection, chatCollection,
			)
		}
	}
	return nil
}

// logStoreAlias records which physical collection a corpus's alias resolved to.
//
// The alias is what every caller uses; the physical name is the only place a
// generation is visible, and the container gate asserts the mapping directly, so
// startup states it once instead of leaving it to be inferred.
func logStoreAlias(ctx context.Context, corpus string, store rag.VectorStore) {
	aliased, ok := store.(rag.AliasedVectorStore)
	if !ok {
		log.Printf("%s corpus uses store %T (no alias support)", corpus, store)
		return
	}
	physical, err := aliased.PhysicalCollection(ctx)
	if err != nil {
		log.Printf("%s corpus alias %q could not be resolved: %v", corpus, aliased.Alias(), err)
		return
	}
	log.Printf("%s corpus alias %q -> physical collection %q", corpus, aliased.Alias(), physical)
}

// validateCapabilityAudiences refuses a configuration in which both corpora
// accept the same audience.
//
// Audience is the first of the two locks between the corpora (the role sets are
// the second). Sharing one audience would make the separation depend on the role
// table alone and would let an operator believe the corpora are separated by
// credentials when they are not, so it is a startup error rather than a warning.
func validateCapabilityAudiences(documentAudience, chatAudience string) error {
	documentAudience = strings.TrimSpace(documentAudience)
	chatAudience = strings.TrimSpace(chatAudience)
	if documentAudience == "" || chatAudience == "" {
		return errors.New("both capability audiences are required")
	}
	if documentAudience == chatAudience {
		return fmt.Errorf("the two corpora must not share the capability audience %q", chatAudience)
	}
	return nil
}

// validateChatVectorBackend refuses to serve the chat corpus on a backend that
// cannot give it a collection of its own.
//
// The chat corpus's isolation is structural: it owns a collection, and the
// collection name is what the two corpora never share. `pgvector` keeps every
// corpus in one table (`rag_chunks`), so the collection flag is not even a
// parameter there - both corpora would read and write the same rows, share one
// index, and share one rebuild and delete path. Refusing is the honest outcome:
// the alternative is a second corpus that only looks isolated.
func validateChatVectorBackend(backend string) error {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "qdrant", "memory":
		// qdrant: one collection per corpus. memory: one store instance per
		// corpus, which the composition root builds separately.
		return nil
	case "pgvector":
		return errors.New(
			"the chat corpus needs its own vector collection, and the pgvector backend stores every corpus in one table; run it with -store qdrant",
		)
	default:
		return fmt.Errorf("unknown vector store %q", backend)
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

func envDurationOrDefault(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		log.Fatalf("%s must be a duration such as 168h: %v", name, err)
	}
	return parsed
}

func envInt64OrDefault(name string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
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
