// Package architecture_test 固化 gin-backend 的模块边界和依赖方向。
package architecture_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "gin-backend/internal/"

func TestDocumentModuleDependencyDirection(t *testing.T) {
	root := backendInternalRoot(t)
	documentRoot := filepath.Join(root, "modules", "document")

	for _, layer := range []string{"domain", "application", "interfaces", "infrastructure"} {
		if info, err := os.Stat(filepath.Join(documentRoot, layer)); err != nil || !info.IsDir() {
			t.Fatalf("document layer %q must exist: %v", layer, err)
		}
	}

	assertImports(t, filepath.Join(documentRoot, "domain"), func(path string) bool {
		return strings.HasPrefix(path, modulePath) || path == "github.com/gin-gonic/gin" || strings.HasPrefix(path, "gorm.io/")
	}, "domain must not depend on application, infrastructure, transport, or persistence packages")

	assertImports(t, filepath.Join(documentRoot, "application"), func(path string) bool {
		return path != modulePath+"modules/document/domain" && (strings.HasPrefix(path, modulePath) || path == "github.com/gin-gonic/gin" || strings.HasPrefix(path, "gorm.io/"))
	}, "application may depend on document/domain only; infrastructure and transport are wired by app")

	assertImports(t, filepath.Join(documentRoot, "interfaces"), func(path string) bool {
		return strings.HasPrefix(path, modulePath+"modules/document/infrastructure") ||
			strings.HasPrefix(path, modulePath+"modules/markdown") ||
			strings.HasPrefix(path, modulePath+"model/orm/markdown") ||
			path == modulePath+"model/store"
	}, "interfaces may depend on document application/domain and transport frameworks, but not persistence implementations or legacy storage")

	assertImports(t, filepath.Join(documentRoot, "infrastructure"), func(path string) bool {
		return strings.HasPrefix(path, modulePath+"modules/document/application") || strings.Contains(path, "/interfaces/")
	}, "infrastructure may implement domain ports but must not depend on application or interfaces")
}

func TestHTTPPlatformOwnsGinAssembly(t *testing.T) {
	root := backendInternalRoot(t)
	for _, directory := range []string{
		filepath.Join(root, "platform", "httpserver"),
		filepath.Join(root, "platform", "httpserver", "middleware"),
	} {
		info, err := os.Stat(directory)
		if err != nil || !info.IsDir() {
			t.Fatalf("HTTP platform directory %q must exist: %v", directory, err)
		}
	}

	for _, obsoletePath := range []string{
		filepath.Join(root, "middleware"),
		filepath.Join(root, "app", "routes.go"),
	} {
		if _, err := os.Stat(obsoletePath); !os.IsNotExist(err) {
			t.Fatalf("obsolete Gin assembly path must stay removed: %s: %v", obsoletePath, err)
		}
	}
}

func TestBackendHasNoObsoleteServiceImports(t *testing.T) {
	root := backendInternalRoot(t)
	if _, err := os.Stat(filepath.Join(root, "service")); !os.IsNotExist(err) {
		t.Fatalf("obsolete internal/service directory must stay removed: %v", err)
	}
	assertImports(t, root, func(path string) bool {
		return strings.HasPrefix(path, modulePath+"service/") ||
			strings.HasPrefix(path, modulePath+"application/document") ||
			strings.HasPrefix(path, modulePath+"domain/document") ||
			strings.HasPrefix(path, modulePath+"infra/postgresql/document")
	}, "imports must use the domain-oriented internal/modules layout")
}

func TestDocumentModuleDoesNotDependOnLegacyMarkdownStorage(t *testing.T) {
	root := filepath.Join(backendInternalRoot(t), "modules", "document")
	assertImports(t, root, func(path string) bool {
		return strings.HasPrefix(path, modulePath+"modules/markdown") ||
			strings.HasPrefix(path, modulePath+"model/orm/markdown") ||
			path == modulePath+"model/store"
	}, "document is the replacement domain and must not depend on legacy Markdown storage")
}

func TestBusinessModulesDoNotDependOnCompositionRoot(t *testing.T) {
	root := filepath.Join(backendInternalRoot(t), "modules")
	assertImports(t, root, func(path string) bool {
		return path == modulePath+"app" || strings.HasPrefix(path, modulePath+"app/")
	}, "business modules must not depend on the app composition root")
}

func backendInternalRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(workingDirectory, ".."))
}

func assertImports(t *testing.T, root string, forbidden func(string) bool, rule string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if forbidden(importPath) {
				position := spec.Pos()
				t.Errorf("%s imports forbidden package %q at token position %d: %s", path, importPath, position, rule)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
