// Package testsupport assembles the document service against a real PostgreSQL
// database for integration tests.
//
// It deliberately fails the test instead of skipping when the database cannot be
// reached: a skipped integration test is indistinguishable from a passing one in a
// test summary, and this service's whole contract is about what actually commits.
//
// Every helper hands out identifiers with a unique per-test prefix, so two agents
// or two test runs sharing the development database never collide.
package testsupport

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"document-service/internal/application"
	"document-service/internal/config"
	"document-service/internal/infrastructure/postgres"

	"packages/serviceauth"

	"github.com/google/uuid"
)

// DefaultTestDSN is the development database the three services share. It is used
// when DOCUMENT_SERVICE_TEST_DSN is not set.
const DefaultTestDSN = "postgres://document_service_writer:document_service@127.0.0.1:15432/gin_demo?sslmode=disable"

// connectTimeout bounds every harness connection attempt.
const connectTimeout = 20 * time.Second

// TestDSN returns the database the integration tests must use.
func TestDSN() string {
	if dsn := strings.TrimSpace(os.Getenv("DOCUMENT_SERVICE_TEST_DSN")); dsn != "" {
		return dsn
	}
	return DefaultTestDSN
}

// BoundaryKey is the shared boundary key material used by the tests. It is longer
// than the 32 byte minimum so the codec accepts it.
func BoundaryKey() []byte {
	return bytes.Repeat([]byte("document-service-integration-key"), 2)
}

// TestConfig builds a valid configuration that points at the test database.
func TestConfig(t testing.TB, key []byte) config.Config {
	t.Helper()
	keyFile := filepath.Join(t.TempDir(), "boundary.key")
	if err := os.WriteFile(keyFile, key, 0o600); err != nil {
		t.Fatalf("write boundary key: %v", err)
	}
	cfg := config.Default()
	cfg.Postgres.DSN = TestDSN()
	cfg.Postgres.Schema = "document_service"
	cfg.Auth.CapabilityKeyFile = keyFile
	cfg.Auth.CapabilityTTL = "2m"
	cfg.Outbox.MaxBatchSize = 200
	if err := cfg.Validate(); err != nil {
		t.Fatalf("test configuration is invalid: %v", err)
	}
	return cfg
}

// OpenPool connects to the test database. A failure is fatal, never a skip.
func OpenPool(t testing.TB, cfg config.Config) *postgres.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	pool, err := postgres.Open(ctx, cfg.Postgres)
	if err != nil {
		t.Fatalf("open the document service test database (%s): %v\n"+
			"set DOCUMENT_SERVICE_TEST_DSN to point at a running PostgreSQL", cfg.Postgres.DSN, err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

// NewService assembles the business boundary over the real database.
func NewService(t testing.TB, cfg config.Config, pool *postgres.Pool, options ...application.Option) *application.Service {
	t.Helper()
	service, err := application.New(application.Dependencies{
		Config: cfg,
		Pool:   pool,
		Principal: func(ctx context.Context) (*serviceauth.Principal, error) {
			principal, ok := serviceauth.PrincipalFrom(ctx)
			if !ok {
				return nil, errors.New("testsupport: no principal in context")
			}
			return principal, nil
		},
		Options: options,
	})
	if err != nil {
		t.Fatalf("assemble the document service: %v", err)
	}
	return service
}

// Session pairs a service with the identity a call is made as.
type Session struct {
	Service   *application.Service
	Principal *serviceauth.Principal
	Ctx       context.Context
}

// As returns a session that calls the service as the given principal.
func As(service *application.Service, principal *serviceauth.Principal) Session {
	return Session{
		Service:   service,
		Principal: principal,
		Ctx:       serviceauth.WithPrincipal(context.Background(), principal),
	}
}

// IDs hands out unique, per-test identifiers.
type IDs struct {
	prefix   string
	actor    string
	subjects map[string]struct{}
	seq      *Sequence
}

// NewIDs creates a fresh identifier set. The prefix is hex so it can prefix a UUID
// that the database accepts, and the actor is unique so audit rows can be cleaned
// up by actor.
func NewIDs() *IDs {
	raw := strings.ReplaceAll(uuid.NewString(), "-", "")
	prefix := raw[:8]
	return &IDs{
		prefix:   prefix,
		actor:    "integration-" + prefix,
		subjects: make(map[string]struct{}),
		seq:      NewSequence(prefix),
	}
}

// Prefix is the per-test hex prefix.
func (ids *IDs) Prefix() string { return ids.prefix }

// Actor is the audit actor every call of this test uses.
func (ids *IDs) Actor() string { return ids.actor }

// Subject builds a Web subject key such as "web:user:a1b2c3d4-owner".
func (ids *IDs) Subject(name string) string {
	key := "web:user:" + ids.prefix + "-" + name
	ids.subjects[key] = struct{}{}
	return key
}

// QQSubject builds a Bot scoped QQ subject key.
func (ids *IDs) QQSubject(botID, externalUserID string) string {
	key, err := serviceauth.QQSubjectKey(botID, externalUserID)
	if err != nil {
		panic(fmt.Sprintf("testsupport: build QQ subject key: %v", err))
	}
	ids.subjects[key] = struct{}{}
	return key
}

// RequestID builds a unique idempotency key.
func (ids *IDs) RequestID(name string) string { return ids.prefix + "-" + name }

// NumericID builds a unique numeric identifier for a Bot or a QQ group.
func (ids *IDs) NumericID() string {
	generated := uuid.New()
	return "9" + fmt.Sprintf("%d", binary.BigEndian.Uint32(generated[0:4]))
}

// Sequence generates deterministic, valid UUIDs so a test can address the rows a
// transaction created. The real service uses random UUIDs.
type Sequence struct {
	prefix  string
	counter int64
}

// NewSequence builds a UUID sequence whose first value is At(1).
func NewSequence(prefix string) *Sequence {
	return &Sequence{prefix: prefix}
}

// Next produces the next identifier.
func (sequence *Sequence) Next() string {
	sequence.counter++
	return sequence.at(sequence.counter)
}

// At produces the identifier of a given position without advancing the sequence.
func (sequence *Sequence) At(position int64) string { return sequence.at(position) }

func (sequence *Sequence) at(position int64) string {
	return fmt.Sprintf("%s-%04x-4%03x-8%03x-%012x",
		sequence.prefix, position, position&0xfff, position&0xfff, position)
}

// Options returns the seams a deterministic test needs.
func (sequence *Sequence) Options() []application.Option {
	return []application.Option{application.WithIDGenerator(sequence.Next)}
}

// Principal builds the verified identity a trusted entry point would have proven.
func (ids *IDs) Principal(caller serviceauth.Caller, subjectKey string, scopes ...serviceauth.Scope) *serviceauth.Principal {
	names := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		names = append(names, string(scope))
	}
	return &serviceauth.Principal{
		Caller:     caller,
		Audience:   serviceauth.AudienceDocumentService,
		Scopes:     names,
		SubjectKey: subjectKey,
		Actor:      ids.actor,
	}
}

// WebWriter is a go-web caller holding every scope the document commands need.
func (ids *IDs) WebWriter(subjectKey string) *serviceauth.Principal {
	return ids.Principal(serviceauth.CallerGoWeb, subjectKey,
		serviceauth.ScopeDocumentWrite, serviceauth.ScopeDocumentRead,
		serviceauth.ScopeSpaceAdmin, serviceauth.ScopeAccessResolve)
}

// SpaceAdmin is a go-web caller that may only administer spaces.
func (ids *IDs) SpaceAdmin(subjectKey string) *serviceauth.Principal {
	return ids.Principal(serviceauth.CallerGoWeb, subjectKey,
		serviceauth.ScopeSpaceAdmin, serviceauth.ScopeDocumentRead)
}

// QQAgent is a py-agent caller, which may resolve scopes and read documents for a
// QQ subject but never write one.
func (ids *IDs) QQAgent(subjectKey string) *serviceauth.Principal {
	return ids.Principal(serviceauth.CallerPyAgent, subjectKey,
		serviceauth.ScopeAccessResolve, serviceauth.ScopeDocumentRead)
}

// Cleanup removes the rows a test created.
//
// Order matters and is dictated by the schema's own invariants:
//  1. bindings and audit rows first, because audit rows reference a space with ON
//     DELETE RESTRICT;
//  2. the team spaces next, which cascades their membership rows. Deleting the
//     membership rows directly would trip the deferred "every space has an active
//     owner" trigger, because that trigger fires on space_members and the space
//     would still be there without its owner;
//  3. registered subjects last, and only when nothing references them. A subject
//     that owns a private space cannot be deleted, and neither can the documents
//     and Outbox rows that reference it: the Outbox is append-only by design and
//     document rows are RESTRICT-referenced from it. Those rows stay behind on
//     purpose, and the per-test prefixes keep them from colliding with anybody
//     else's test.
func (ids *IDs) Cleanup(t testing.TB, pool *postgres.Pool) {
	t.Helper()
	if pool == nil || pool.Pgx() == nil {
		t.Fatalf("testsupport: Cleanup needs an open pool")
	}
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	conn := pool.Pgx()
	prefix := "web:user:" + ids.prefix + "%"

	statements := []struct {
		sql  string
		args []any
	}{
		{"DELETE FROM document_service.group_space_bindings WHERE actor = $1", []any{ids.actor}},
		{"DELETE FROM document_service.space_audit_events WHERE actor = $1", []any{ids.actor}},
		{
			"DELETE FROM document_service.knowledge_spaces WHERE space_type = 'team' AND owner_subject_key LIKE $1",
			[]any{prefix},
		},
	}
	for _, statement := range statements {
		if _, err := conn.Exec(ctx, statement.sql, statement.args...); err != nil {
			// Cleanup is best effort: a row another row still references (a document
			// owning a private space, for instance) is expected to stay.
			t.Logf("cleanup %q: %v", statement.sql, err)
		}
	}

	// A subject may only be deleted once nothing references it; the guard makes the
	// attempt a no-op instead of a foreign key error.
	const deleteSubjectSQL = `
DELETE FROM document_service.access_subjects s
WHERE s.subject_key = $1
  AND NOT EXISTS (SELECT 1 FROM document_service.knowledge_spaces k WHERE k.owner_subject_key = s.subject_key)
  AND NOT EXISTS (SELECT 1 FROM document_service.documents d WHERE d.owner_subject_key = s.subject_key)
  AND NOT EXISTS (SELECT 1 FROM document_service.document_versions v WHERE v.created_by_subject_key = s.subject_key)
  AND NOT EXISTS (SELECT 1 FROM document_service.space_members m WHERE m.subject_key = s.subject_key)
  AND NOT EXISTS (SELECT 1 FROM document_service.document_grants g WHERE g.grantee_subject_key = s.subject_key)`
	for subjectKey := range ids.subjects {
		if _, err := conn.Exec(ctx, deleteSubjectSQL, subjectKey); err != nil {
			t.Logf("cleanup subject %s: %v", subjectKey, err)
		}
	}
}

// Count runs a scalar count query and fails the test when it cannot. A nil pool is
// a test wiring defect: it must fail the test rather than panic, because a panic
// aborts the whole package and hides every other failure.
func Count(t testing.TB, pool *postgres.Pool, query string, args ...any) int64 {
	t.Helper()
	if pool == nil || pool.Pgx() == nil {
		t.Fatalf("testsupport: Count needs an open pool (query %q)", query)
	}
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	var value int64
	if err := pool.Pgx().QueryRow(ctx, query, args...).Scan(&value); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return value
}

// Exec runs a statement and fails the test when it cannot.
func Exec(t testing.TB, pool *postgres.Pool, query string, args ...any) {
	t.Helper()
	if pool == nil || pool.Pgx() == nil {
		t.Fatalf("testsupport: Exec needs an open pool (query %q)", query)
	}
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	if _, err := pool.Pgx().Exec(ctx, query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
