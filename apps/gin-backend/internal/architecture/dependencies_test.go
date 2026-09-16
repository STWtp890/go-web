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
			name: "document application depends on domain only",
			root: []string{"modules", "document", "application"},
			forbidden: func(path string) bool {
				return path != modulePath+"modules/document/domain" &&
					(strings.HasPrefix(path, modulePath) ||
						path == "github.com/gin-gonic/gin" || strings.HasPrefix(path, "gorm.io/"))
			},
			reason: "document/application may depend on document/domain only among project packages",
		},
		{
			name: "document interfaces do not own persistence",
			root: []string{"modules", "document", "interfaces"},
			forbidden: func(path string) bool {
				return strings.HasPrefix(path, modulePath+"modules/document/infrastructure") ||
					strings.HasPrefix(path, modulePath+"model/orm") ||
					path == modulePath+"model/store" ||
					strings.HasPrefix(path, "gorm.io/")
			},
			reason: "document/interfaces may adapt application/domain but must not own persistence",
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
