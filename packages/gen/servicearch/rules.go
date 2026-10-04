package servicearch

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Service describes one deployable unit in the source-owned layout.
type Service struct {
	// Name is the human name used in test output.
	Name string
	// ModulePath is the Go module path of the service.
	ModulePath string
	// Directory is the service root, relative to the repository root or absolute.
	Directory string
	// ForeignModules lists module path prefixes this service must never import.
	// It covers every other service in the layout, so a new service is protected
	// by adding its own entry rather than by editing every sibling.
	ForeignModules []string
}

// Layout is the full set of services that must stay mutually independent.
type Layout struct {
	Services []Service
	// BusinessSchemas lists the PostgreSQL schemas owned by services. Cross
	// service table writes are detected by looking for these schema qualifiers
	// inside write statements.
	BusinessSchemas []string
	// WriteAccounts maps a service name to the database role it is allowed to
	// write with. A service's own DDL/SQL must not reference another owner role.
	WriteAccounts map[string]string
}

// DefaultLayout returns the repository's current service layout.
func DefaultLayout() Layout {
	return Layout{
		Services: []Service{
			{Name: "go-web", ModulePath: "gin-backend", Directory: "apps/gin-backend"},
			{Name: "mixin-search", ModulePath: "mixin-search", Directory: "apps/mixin-search"},
			{Name: "document-service", ModulePath: "document-service", Directory: "apps/document-service"},
			{Name: "document-search", ModulePath: "document-search", Directory: "apps/document-search"},
			{Name: "qq-search", ModulePath: "qq-search", Directory: "apps/qq-search"},
		},
		BusinessSchemas: []string{"document_service", "document_search", "qq_search"},
		WriteAccounts: map[string]string{
			"document-service": "document_service_writer",
			"document-search":  "document_search_writer",
			"qq-search":        "qq_search_writer",
		},
	}
}

// TestNoForeignModuleImports asserts that a service never imports another
// service's Go module.
func TestNoForeignModuleImports(t *testing.T, service Service, layout Layout) {
	t.Helper()
	root := requireDirectory(t, service.Directory)
	foreign := make([]string, 0, len(layout.Services))
	for _, candidate := range layout.Services {
		if candidate.ModulePath == service.ModulePath {
			continue
		}
		foreign = append(foreign, candidate.ModulePath)
	}

	walkGoFiles(t, root, func(path string, imports []string) {
		for _, imported := range imports {
			for _, prefix := range foreign {
				if imported == prefix || strings.HasPrefix(imported, prefix+"/") {
					t.Errorf("%s imports %q, which belongs to another service", path, imported)
				}
			}
		}
	})
}

// TestNoCrossServiceTableWrites asserts that a service's Go sources and SQL
// artifacts neither qualify another schema in a write statement nor reference
// another service's writer role.
func TestNoCrossServiceTableWrites(t *testing.T, service Service, layout Layout) {
	t.Helper()
	root := requireDirectory(t, service.Directory)
	ownSchema := ownSchemaFor(service, layout)

	walkTextFiles(t, root, func(path string, content string) {
		for _, candidate := range writeStatement.FindAllStringSubmatch(content, -1) {
			table := candidate[1]
			schema, _, qualified := strings.Cut(table, ".")
			if !qualified {
				continue
			}
			if !containsString(layout.BusinessSchemas, schema) || schema == ownSchema {
				continue
			}
			t.Errorf("%s writes to %s.%s, which belongs to another service", path, schema, table)
		}
		for name, account := range layout.WriteAccounts {
			if name == service.Name {
				continue
			}
			if strings.Contains(content, account) {
				t.Errorf("%s references %q, the write account of %s", path, account, name)
			}
		}
	})
}

var writeStatement = regexp.MustCompile(`(?is)\b(?:insert\s+into|update|delete\s+from|truncate|alter\s+table|create\s+table(?:\s+if\s+not\s+exists)?|drop\s+table(?:\s+if\s+exists)?)\s+([a-zA-Z_][a-zA-Z0-9_$.]*)`)

func ownSchemaFor(service Service, layout Layout) string {
	switch service.Name {
	case "document-service":
		return "document_service"
	case "document-search":
		return "document_search"
	case "qq-search":
		return "qq_search"
	default:
		return ""
	}
}

// TestNoServiceImportsItsOwnCompositionRoot asserts the layered dependency rule
// that domain code never depends on the composition root.
func TestNoServiceImportsItsOwnCompositionRoot(t *testing.T, service Service) {
	t.Helper()
	root := requireDirectory(t, service.Directory)
	internalRoot := filepath.Join(root, "internal")
	if info, err := os.Stat(internalRoot); err != nil || !info.IsDir() {
		t.Fatalf("service %s must keep business code under internal/: %v", service.Name, err)
	}
	walkGoFiles(t, internalRoot, func(path string, imports []string) {
		for _, imported := range imports {
			if imported == service.ModulePath+"/cmd" || strings.HasPrefix(imported, service.ModulePath+"/cmd/") {
				t.Errorf("%s imports the composition root", path)
			}
		}
	})
}

func requireDirectory(t *testing.T, relative string) string {
	t.Helper()
	root, err := RepositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, filepath.FromSlash(relative))
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("service directory must exist: %s: %v", target, err)
	}
	return target
}

// RepositoryRoot locates the repository root by walking up until go.work is
// found. Tests run with the package directory as the working directory, so the
// depth differs per module and must not be hard coded.
func RepositoryRoot() (string, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	current := workingDirectory
	for {
		if _, err := os.Stat(filepath.Join(current, "go.work")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", os.ErrNotExist
		}
		current = parent
	}
}

func walkGoFiles(t *testing.T, root string, visit func(path string, imports []string)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		imports := make([]string, 0, len(file.Imports))
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			imports = append(imports, imported)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		visit(relative, imports)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func walkTextFiles(t *testing.T, root string, visit func(path string, content string)) {
	t.Helper()
	extensions := map[string]struct{}{".go": {}, ".sql": {}, ".yaml": {}, ".yml": {}, ".ps1": {}}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if _, ok := extensions[strings.ToLower(filepath.Ext(path))]; !ok {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		visit(relative, string(content))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
