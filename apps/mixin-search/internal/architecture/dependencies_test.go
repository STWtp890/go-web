// Package architecture_test turns mixin-search's current dependency direction into an executable boundary.
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

type importRule struct {
	name      string
	root      string
	forbidden func(string) bool
	reason    string
}

func TestDependencyRules(t *testing.T) {
	moduleRoot := mixinModuleRoot(t)
	internalRoot := filepath.Join(moduleRoot, "internal")
	rules := []importRule{
		{
			name: "document pipeline is independent",
			root: filepath.Join(moduleRoot, "document_pipeline"),
			forbidden: func(path string) bool {
				return strings.HasPrefix(path, "mixin-search/internal/") || strings.HasPrefix(path, "packages/gen/")
			},
			reason: "document_pipeline is the dependency root and must not import application or generated protocol packages",
		},
		{
			name: "rag core does not depend on transport or generated protocol",
			root: filepath.Join(internalRoot, "rag"),
			forbidden: func(path string) bool {
				return strings.HasPrefix(path, "mixin-search/internal/transport/") ||
					strings.HasPrefix(path, "mixin-search/cmd/") || strings.HasPrefix(path, "packages/gen/")
			},
			reason: "internal/rag must stay independent of transport, commands, and Protobuf DTOs",
		},
		{
			name: "transport maps protocol and verifies capabilities only",
			root: filepath.Join(internalRoot, "transport"),
			forbidden: func(path string) bool {
				if strings.HasPrefix(path, "mixin-search/cmd/") || path == "mixin-search/document_pipeline" {
					return true
				}
				// This rule is about layers, not about which corpus an adapter
				// belongs to: both corpus adapters live in this one package, so the
				// rule cannot tell them apart. What keeps a chat adapter from
				// reaching the document corpus is that they are separate types with
				// separate ports (see ADR-014 and internal/chat's own rule), not
				// this import check. Do not read the whitelist below as an
				// isolation guarantee.
				allowed := func(candidate string) bool {
					return candidate == "mixin-search/internal/rag" ||
						strings.HasPrefix(candidate, "mixin-search/internal/rag/") ||
						candidate == "mixin-search/internal/chat" ||
						strings.HasPrefix(candidate, "mixin-search/internal/chat/") ||
						candidate == "mixin-search/internal/security" ||
						strings.HasPrefix(candidate, "mixin-search/internal/security/")
				}
				return strings.HasPrefix(path, "mixin-search/internal/") && !allowed(path)
			},
			reason: "transport may map protocol types to a corpus control plane and verify capabilities only, and must not depend on commands or concrete lower layers",
		},
		{
			name: "chatindex is only a corpus adapter",
			root: filepath.Join(internalRoot, "chatindex"),
			forbidden: func(path string) bool {
				if strings.HasPrefix(path, "mixin-search/cmd/") ||
					strings.HasPrefix(path, "mixin-search/internal/transport/") {
					return true
				}
				// It is the seam between the two corpora on purpose: it may build a
				// chat corpus over the shared vector core and register a pipeline
				// through the shared pipeline contract, and it must not reach
				// anything above that seam.
				allowed := func(candidate string) bool {
					return candidate == "mixin-search/document_pipeline" ||
						strings.HasPrefix(candidate, "mixin-search/document_pipeline/") ||
						candidate == "mixin-search/internal/chat" ||
						strings.HasPrefix(candidate, "mixin-search/internal/chat/") ||
						candidate == "mixin-search/internal/rag" ||
						strings.HasPrefix(candidate, "mixin-search/internal/rag/") ||
						candidate == "mixin-search/internal/controlplane" ||
						strings.HasPrefix(candidate, "mixin-search/internal/controlplane/")
				}
				return strings.HasPrefix(path, "mixin-search/internal/") && !allowed(path)
			},
			reason: "internal/chatindex is the only package that may see both corpora, and it must stay below transport and commands",
		},
		{
			name: "controlplane stays a mechanism",
			root: filepath.Join(internalRoot, "controlplane"),
			forbidden: func(path string) bool {
				return strings.HasPrefix(path, "mixin-search/")
			},
			reason: "internal/controlplane is shared by every corpus, so it must not depend on any corpus, transport or command",
		},
		{
			name: "chat control plane is independent of the document corpus",
			root: filepath.Join(internalRoot, "chat"),
			forbidden: func(path string) bool {
				if strings.HasPrefix(path, "mixin-search/cmd/") ||
					strings.HasPrefix(path, "mixin-search/internal/transport/") {
					return true
				}
				// ADR-014: the two corpora may share the publication and projection
				// mechanism, never each other's state or service. Chat reaches a
				// vector store through its own ports, which the composition root
				// satisfies, so it never needs this import.
				return strings.HasPrefix(path, "mixin-search/internal/rag") ||
					strings.HasPrefix(path, "mixin-search/internal/chat/")
			},
			reason: "the chat corpus must not depend on the document corpus, transport or commands",
		},
	}

	for _, rule := range rules {
		rule := rule
		t.Run(rule.name, func(t *testing.T) {
			assertImports(t, rule.root, rule.forbidden, rule.reason)
		})
	}
}

func mixinModuleRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(workingDirectory, "..", ".."))
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
				t.Errorf("%s imports forbidden package %q: %s", path, importPath, rule)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
