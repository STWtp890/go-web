package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mixin-search/internal/security"
	grpcadapter "mixin-search/internal/transport/grpc"
	mixinsearchv1 "packages/gen/mixin-search/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"
)

// testBoundaryKey is a fixed 32-byte key so tests exercise the real minimum
// length instead of a convenient short string.
const testBoundaryKey = "0123456789abcdef0123456789abcdef"

type testRAGService struct {
	mixinsearchv1.UnimplementedRAGServiceServer
}

func TestGRPCServerReportsHealth(t *testing.T) {
	t.Parallel()

	verifier, err := security.NewVerifier([]byte(testBoundaryKey), "go-web", "mixin-search")
	if err != nil {
		t.Fatalf("build verifier: %v", err)
	}
	authenticator, err := grpcadapter.NewAuthenticator(grpcadapter.AuthConfig{Verifier: verifier})
	if err != nil {
		t.Fatalf("build authenticator: %v", err)
	}

	listener := bufconn.Listen(1024 * 1024)
	server := newGRPCServer(1024*1024, &testRAGService{}, authenticator, true)
	go func() {
		if err := server.Serve(listener); err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	connection, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	client := healthpb.NewHealthClient(connection)
	for _, service := range []string{"", mixinsearchv1.RAGService_ServiceDesc.ServiceName} {
		response, err := client.Check(ctx, &healthpb.HealthCheckRequest{Service: service})
		if err != nil {
			t.Fatalf("check health for %q: %v", service, err)
		}
		if response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
			t.Fatalf(
				"health status for %q = %s, want SERVING",
				service,
				response.GetStatus(),
			)
		}
	}
}

func TestProtectedRPCWithoutCapabilityIsRejected(t *testing.T) {
	t.Parallel()

	verifier, err := security.NewVerifier([]byte(testBoundaryKey), "go-web", "mixin-search")
	if err != nil {
		t.Fatalf("build verifier: %v", err)
	}
	authenticator, err := grpcadapter.NewAuthenticator(grpcadapter.AuthConfig{Verifier: verifier})
	if err != nil {
		t.Fatalf("build authenticator: %v", err)
	}

	listener := bufconn.Listen(1024 * 1024)
	server := newGRPCServer(1024*1024, &testRAGService{}, authenticator, false)
	go func() {
		if err := server.Serve(listener); err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	connection, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	// Health is deliberately outside the capability policy so container probes
	// do not need credentials; the business RPCs are not.
	_, err = mixinsearchv1.NewRAGServiceClient(connection).SearchDocuments(
		context.Background(),
		&mixinsearchv1.SearchDocumentsRequest{Query: "boundary"},
	)
	if err == nil {
		t.Fatal("SearchDocuments without a capability succeeded, want rejection")
	}
	if !strings.Contains(err.Error(), "capability") {
		t.Fatalf("SearchDocuments error = %v, want a capability rejection", err)
	}
}

func TestLoadBoundaryKey(t *testing.T) {
	t.Parallel()

	t.Run("missing source is fatal", func(t *testing.T) {
		t.Parallel()
		if _, err := loadBoundaryKey("", ""); err == nil {
			t.Fatal("loadBoundaryKey with no source succeeded, want error")
		}
	})

	t.Run("short inline key is rejected", func(t *testing.T) {
		t.Parallel()
		if _, err := loadBoundaryKey("", "too-short"); err == nil {
			t.Fatal("loadBoundaryKey with a short key succeeded, want error")
		}
	})

	t.Run("inline key is accepted", func(t *testing.T) {
		t.Parallel()
		key, err := loadBoundaryKey("", testBoundaryKey)
		if err != nil {
			t.Fatalf("loadBoundaryKey(inline): %v", err)
		}
		if string(key) != testBoundaryKey {
			t.Fatalf("loadBoundaryKey(inline) = %q", key)
		}
	})

	t.Run("file key wins and is trimmed", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "capability.key")
		if err := os.WriteFile(path, []byte("  "+testBoundaryKey+"\n"), 0o600); err != nil {
			t.Fatalf("write key file: %v", err)
		}
		key, err := loadBoundaryKey(path, testBoundaryKey+"-inline-ignored-padding")
		if err != nil {
			t.Fatalf("loadBoundaryKey(file): %v", err)
		}
		if string(key) != testBoundaryKey {
			t.Fatalf("loadBoundaryKey(file) = %q, want the trimmed file contents", key)
		}
	})

	t.Run("unreadable file is fatal", func(t *testing.T) {
		t.Parallel()
		_, err := loadBoundaryKey(filepath.Join(t.TempDir(), "absent.key"), testBoundaryKey)
		if err == nil {
			t.Fatal("loadBoundaryKey with a missing file succeeded, want error")
		}
	})
}

func TestOpenControlStoreMemory(t *testing.T) {
	store, closeStore, err := openControlStore(context.Background(), " memory ", "", "", false)
	if err != nil {
		t.Fatalf("open memory control store: %v", err)
	}
	if store == nil {
		t.Fatal("memory control store is nil")
	}
	closeStore()
}

func TestOpenControlStoreRejectsUnknownBackend(t *testing.T) {
	store, closeStore, err := openControlStore(context.Background(), "unknown", "", "", false)
	if err == nil {
		closeStore()
		t.Fatalf("open unknown control store = %T, want error", store)
	}
}

func TestControlDefaultsUseEnvironment(t *testing.T) {
	t.Setenv("CONTROL_STORE", "memory")
	t.Setenv("CONTROL_DATABASE_DSN", "postgres://control.example/control")
	t.Setenv("CONTROL_STORE_NAMESPACE", "test-namespace")
	t.Setenv("CONTROL_STORE_BOOTSTRAP", "true")

	if got := defaultControlStore(); got != "memory" {
		t.Fatalf("default control store = %q, want memory", got)
	}
	if got := defaultControlDSN(); got != "postgres://control.example/control" {
		t.Fatalf("default control DSN = %q", got)
	}
	if got := defaultControlNamespace(); got != "test-namespace" {
		t.Fatalf("default control namespace = %q", got)
	}
	if !defaultControlBootstrap() {
		t.Fatal("default control bootstrap = false, want true")
	}
}
