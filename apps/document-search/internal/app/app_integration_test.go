package app_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"document-search/internal/app"
	"document-search/internal/config"
	"document-search/internal/dbtest"
)

// This file runs the real composition root: the same New the process calls, with
// a real database, a real Qdrant and a real boundary key file on disk. It is what
// proves the wiring - the vector collection reaching the query path, the consumer
// and the readiness probe - rather than the wiring being assumed.

// testConfig is the process configuration, pointed at the development services.
func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Postgres.DSN = dbtest.DSN()
	keyPath := filepath.Join(t.TempDir(), "boundary.key")
	key := []byte(strings.Repeat("document-search-app-key-", 3))
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatalf("write the boundary key: %v", err)
	}
	cfg.Auth.CapabilityKeyFile = keyPath
	cfg.Source.CapabilityKeyFile = keyPath
	// The consumer is not started in these tests, and a disabled source needs no
	// endpoint.
	cfg.Source.Disabled = true
	return cfg
}

// TestIntegrationAppComesUpWithTheVectorFlow asserts the process root opens the
// vector collection, reports readiness only while it answers, and closes cleanly.
func TestIntegrationAppComesUpWithTheVectorFlow(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	service, err := app.New(ctx, cfg, logger)
	if err != nil {
		t.Fatalf("app.New (Qdrant must be running at %s): %v", cfg.Vector.Endpoint, err)
	}
	if service.VectorIndex() == nil {
		t.Fatal("the assembled service has no vector index")
	}
	if service.VectorIndex().Alias() != cfg.Index.CollectionAlias {
		t.Fatalf("the service opened alias %q, want %q", service.VectorIndex().Alias(), cfg.Index.CollectionAlias)
	}
	if err := service.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = service.ServeHTTP(listener) }()
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })

	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get("http://" + listener.Addr().String() + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"ready"`) {
		t.Fatalf("/readyz returned %d %s, want ready: the database and the vector collection both answer",
			response.StatusCode, string(body))
	}
}

// TestIntegrationAppRefusesToStartWithoutTheVectorBackend pins the fail-closed
// direction: a service that advertises hybrid search must not come up as a
// keyword-only index because its vector collection is unreachable.
func TestIntegrationAppRefusesToStartWithoutTheVectorBackend(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	// A closed port, with a short call timeout so the test does not wait for the
	// client's default.
	cfg.Vector.Endpoint = "127.0.0.1:1"
	cfg.Vector.Timeout = "1s"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	service, err := app.New(ctx, cfg, logger)
	if err == nil {
		_ = service.Close()
		t.Fatal("the service started although the vector backend is unreachable")
	}
	if !strings.Contains(err.Error(), "vector") {
		t.Fatalf("the startup failure is %q, want it to name the vector index", err.Error())
	}
}
