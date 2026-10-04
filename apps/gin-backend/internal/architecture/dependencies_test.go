// Package architecture_test 固化 gin-backend 的依赖方向。
//
// 范围说明：本测试**只做方向性 import 断言**——「某个目录不得 import 某类包」。
// 它刻意**不包含遗留文件的精确清单快照**，因为那种断言：
//  1. 会因任何合法改动（迁移一个文件、补一个测试）而失败，长期会训练团队
//     「顺手更新基线」，使信号衰减；
//  2. 约束的是文件数量，而不是真正要防的依赖方向错误。
//
// 背景与取舍见 docs/architecture/DEVELOPMENT_CONVENTIONS.md 第 5 节。
// 新增规则的门槛：对应已发生的真实问题、有明确修复方向、不对合法写法产生误报。
//
// 结构说明（ADR-017 阶段 C 之后的当前结构）：Web 文档面只由
// modules/document/interfaces/sourceowned 经
// modules/document/infrastructure/{documentservice,documentsearch} 访问来源专属服务；
// document/domain 仍是这些适配器共用的领域类型。逐个列出的规则只覆盖**当前存在**
// 的目录，因此本文件不再为已删除的旧 application/PostgreSQL 实现保留断言。
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

type importRule struct {
	name      string
	root      []string
	forbidden func(string) bool
	reason    string
}

// usesLocalPersistence 判定一个 import 路径是否把本进程连回本地持久化。
// ADR-017 要求 go-web 只通过版本化契约访问来源专属服务，因此远程客户端目录
// 不得出现 GORM、本地模型或数据库连接。
func usesLocalPersistence(path string) bool {
	return strings.HasPrefix(path, modulePath+"model/") ||
		strings.HasPrefix(path, modulePath+"common/base/connection") ||
		strings.HasPrefix(path, modulePath+"modules/document/infrastructure/postgresql") ||
		strings.HasPrefix(path, "gorm.io/")
}

func TestDependencyRules(t *testing.T) {
	internalRoot := backendInternalRoot(t)

	rules := []importRule{
		{
			name: "document domain is pure",
			root: []string{"modules", "document", "domain"},
			forbidden: func(path string) bool {
				return strings.HasPrefix(path, modulePath) ||
					path == "github.com/gin-gonic/gin" || strings.HasPrefix(path, "gorm.io/")
			},
			reason: "document/domain may depend on the standard library only",
		},
		{
			name: "document interfaces do not own persistence",
			root: []string{"modules", "document", "interfaces"},
			forbidden: func(path string) bool {
				// The source-owned adapter under interfaces/sourceowned is an
				// exception by design: it adapts *remote service clients* rather
				// than a local repository, and ADR-017 requires the Web surface to
				// reach the document service instead of a document table.
				if strings.HasPrefix(path, modulePath+"modules/document/infrastructure/documentservice") ||
					strings.HasPrefix(path, modulePath+"modules/document/infrastructure/documentsearch") {
					return false
				}
				return strings.HasPrefix(path, modulePath+"modules/document/infrastructure") ||
					strings.HasPrefix(path, modulePath+"model/orm") ||
					path == modulePath+"model/store" ||
					strings.HasPrefix(path, modulePath+"common/base/connection") ||
					strings.HasPrefix(path, "gorm.io/")
			},
			reason: "document/interfaces may adapt remote services but must not own local persistence",
		},
		{
			name: "document infrastructure points inward",
			root: []string{"modules", "document", "infrastructure"},
			forbidden: func(path string) bool {
				return strings.HasPrefix(path, modulePath+"modules/document/application") ||
					strings.Contains(path, "/interfaces/") ||
					path == "github.com/gin-gonic/gin"
			},
			reason: "document/infrastructure implements domain ports and must not depend on application/interfaces",
		},
		{
			// ADR-017: the Web document surface reaches document-service through a
			// generated contract over gRPC. A client that grows a local model, a
			// GORM handle or a database connection would put document-service
			// business tables back inside go-web.
			name:      "document-service client stays a transport adapter",
			root:      []string{"modules", "document", "infrastructure", "documentservice"},
			forbidden: usesLocalPersistence,
			reason:    "the document-service gRPC client must not own local models, GORM mappings or database connections",
		},
		{
			// Same boundary for the search service: it answers with a capability,
			// not with a projection go-web maintains locally.
			name:      "document-search client stays a transport adapter",
			root:      []string{"modules", "document", "infrastructure", "documentsearch"},
			forbidden: usesLocalPersistence,
			reason:    "the document-search gRPC client must not own local models, GORM mappings or database connections",
		},
		{
			name: "business modules do not depend on composition root",
			root: []string{"modules"},
			forbidden: func(path string) bool {
				return path == modulePath+"app" || strings.HasPrefix(path, modulePath+"app/")
			},
			reason: "business modules must not depend on the app composition root",
		},
		{
			name: "common base has no business dependencies",
			root: []string{"common", "base"},
			forbidden: func(path string) bool {
				return strings.HasPrefix(path, modulePath+"modules/")
			},
			reason: "common/base is a technical foundation and must not depend on business modules",
		},
		{
			name: "common services have no business dependencies",
			root: []string{"common", "service"},
			forbidden: func(path string) bool {
				return strings.HasPrefix(path, modulePath+"modules/")
			},
			reason: "common/service must not depend on business modules",
		},
		{
			name: "jwt service is transport independent",
			root: []string{"common", "service", "jwt"},
			forbidden: func(path string) bool {
				return path == "github.com/gin-gonic/gin"
			},
			reason: "common/service/jwt must stay gin independent; identity reading lives in platform/httpserver/identity",
		},
		{
			// ADR-017 阶段 C/F06：旧的 mixin-search 文档检索客户端、它的 capability
			// 机制与死端口已从 go-web 删除，通用边界密钥加载移到 packages/serviceauth。
			// Web 文档面只经 document-service 与 document-search，因此任何模块重新引入
			// mixin-search 客户端（自建包或 packages/gen/mixin-search 契约）都意味着
			// 旧调用路径正在回到 Web 文档业务里。
			name: "no module reaches the legacy mixin-search retrieval client",
			root: []string{},
			forbidden: func(path string) bool {
				return strings.Contains(path, "mixinsearch") ||
					path == "packages/gen/mixin-search" ||
					strings.HasPrefix(path, "packages/gen/mixin-search/")
			},
			reason: "the legacy mixin-search document client was removed in ADR-017 stage C; go-web reaches document-service and document-search only",
		},
		{
			// ADR-017: go-web talks to the source-owned services over versioned
			// contracts and generated clients only. Importing one of their Go
			// modules would put another service's business types, or worse its
			// persistence, inside the Web application.
			name: "gin-backend never imports a source-owned service module",
			root: []string{},
			forbidden: func(path string) bool {
				for _, service := range []string{"document-service", "document-search", "qq-search"} {
					if path == service || strings.HasPrefix(path, service+"/") {
						return true
					}
				}
				return false
			},
			reason: "go-web must reach document-service, document-search and qq-search through packages/gen contracts, never through their modules",
		},
	}

	for _, rule := range rules {
		rule := rule
		t.Run(rule.name, func(t *testing.T) {
			target := filepath.Join(append([]string{internalRoot}, rule.root...)...)
			assertNoForbiddenImports(t, target, rule.forbidden, rule.reason)
		})
	}
}

func backendInternalRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// 本包位于 internal/architecture，因此 internal 根是它的上一级。
	return filepath.Clean(filepath.Join(workingDirectory, ".."))
}

func assertNoForbiddenImports(t *testing.T, root string, forbidden func(string) bool, rule string) {
	t.Helper()
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Fatalf("rule target directory must exist: %s: %v", root, err)
	}

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
