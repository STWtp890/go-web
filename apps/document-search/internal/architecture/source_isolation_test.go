package architecture_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"document-search/internal/config"

	"packages/gen/servicearch"
)

// sourceClientPackage is the document service contract. Exactly one file may
// import it, and that file is the consumer.
const sourceClientPackage = "packages/gen/document/v1"

// TestQueryPathHasNoSourceClient pins the rule that makes this service a search
// service rather than a proxy: the query and rebuild paths must not be able to
// call document-service, go-web or py-agent even by accident.
//
// It is a structural check on the imports of the application package, so it
// fails at review time rather than at 3am when the fact source is down.
func TestQueryPathHasNoSourceClient(t *testing.T) {
	root, err := servicearch.RepositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	applicationDir := filepath.Join(root, "apps", "document-search", "internal", "application")
	imports := parsePackageImports(t, applicationDir)
	if len(imports) == 0 {
		t.Fatalf("no implementation files found under %s", applicationDir)
	}

	for name, imported := range imports {
		for _, path := range imported {
			switch {
			case path == sourceClientPackage && name != "consumer.go":
				t.Errorf("%s imports %s: only the event consumer may hold a document-service client", name, sourceClientPackage)
			case path == "google.golang.org/grpc" && name != "consumer.go":
				t.Errorf("%s imports the gRPC client package: only the event consumer opens a source connection", name)
			}
		}
	}

	// The query implementation itself is pinned to this import surface. A new
	// import here means the query path grew a dependency, and it needs to be an
	// explicit decision rather than a convenient one.
	allowed := map[string]struct{}{
		"context":                        {},
		"fmt":                            {},
		"strings":                        {},
		"packages/gen/documentsearch/v1": {},
		"packages/serviceauth":           {},
	}
	searchImports, ok := imports["search.go"]
	if !ok {
		t.Fatal("the query implementation must live in internal/application/search.go")
	}
	for _, path := range searchImports {
		if _, permitted := allowed[path]; !permitted {
			t.Errorf("search.go imports %q, which is outside the query path's allowed surface %v", path, sortedKeys(allowed))
		}
	}
}

// TestIndexNamespaceIsNotShared asserts the collection namespace recorded by
// this service differs from the other two services' namespaces, so two sources
// can never converge into one physical collection by accident. The alias and
// storage domain themselves are asserted against the database in the
// integration suite.
func TestIndexNamespaceIsNotShared(t *testing.T) {
	cfg := config.Default()
	index, err := cfg.IndexConfig()
	if err != nil {
		t.Fatalf("IndexConfig: %v", err)
	}
	if cfg.Postgres.Schema != "document_search" {
		t.Fatalf("postgres schema = %q, want document_search", cfg.Postgres.Schema)
	}
	if index.CollectionAlias != "go_web_document_v1" {
		t.Fatalf("collection alias = %q, want go_web_document_v1", index.CollectionAlias)
	}
	if index.StorageDomain != "document-search:documents:v1" {
		t.Fatalf("storage domain = %q, want document-search:documents:v1", index.StorageDomain)
	}

	// Reference values from the ownership table in ADR-017 decision 6.
	foreign := []struct {
		owner  string
		alias  string
		domain string
	}{
		{"mixin-search", "go_web_shadow_v1", "postgres:go-web-shadow-v1"},
		{"qq-search messages", "qq_source_messages_v1", "qq-search:messages:v1"},
		{"qq-search files", "qq_source_files_v1", "qq-search:files:v1"},
	}
	for _, other := range foreign {
		if index.CollectionAlias == other.alias {
			t.Errorf("the document search alias collides with the %s alias %q", other.owner, other.alias)
		}
		if index.StorageDomain == other.domain {
			t.Errorf("the document search storage domain collides with the %s domain %q", other.owner, other.domain)
		}
	}
}

// parsePackageImports returns the imports of every non-test Go file in a
// directory, keyed by file name.
func parsePackageImports(t *testing.T, directory string) map[string][]string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read %s: %v", directory, err)
	}
	result := make(map[string][]string, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		paths := make([]string, 0, len(file.Imports))
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquote an import in %s: %v", name, err)
			}
			paths = append(paths, path)
		}
		result[name] = paths
	}
	return result
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
