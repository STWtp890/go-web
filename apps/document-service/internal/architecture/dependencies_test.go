// Package architecture_test enforces the document service's place in the
// source-owned layout.
//
// The rules are shared with the sibling services through packages/gen/servicearch
// so there is one definition of "crossing a service boundary" in the repository.
// They are import-graph and SQL-text assertions, never file inventories: a rule
// that fails on any legitimate refactor trains people to update the baseline
// instead of the design.
package architecture_test

import (
	"testing"

	"packages/gen/servicearch"
)

func TestServiceBoundaries(t *testing.T) {
	layout := servicearch.DefaultLayout()
	service, ok := findService(layout, "document-service")
	if !ok {
		t.Fatal("document-service must be registered in the shared layout")
	}

	t.Run("no foreign module imports", func(t *testing.T) {
		servicearch.TestNoForeignModuleImports(t, service, layout)
	})
	t.Run("no cross service table writes", func(t *testing.T) {
		servicearch.TestNoCrossServiceTableWrites(t, service, layout)
	})
	t.Run("no composition root imports from business code", func(t *testing.T) {
		servicearch.TestNoServiceImportsItsOwnCompositionRoot(t, service)
	})
}

func findService(layout servicearch.Layout, name string) (servicearch.Service, bool) {
	for _, service := range layout.Services {
		if service.Name == name {
			return service, true
		}
	}
	return servicearch.Service{}, false
}
