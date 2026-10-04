// Package architecture_test enforces the QQ search service's place in the
// source-owned layout.
package architecture_test

import (
	"testing"

	"packages/gen/servicearch"
)

func TestServiceBoundaries(t *testing.T) {
	layout := servicearch.DefaultLayout()
	var service servicearch.Service
	found := false
	for _, candidate := range layout.Services {
		if candidate.Name == "qq-search" {
			service, found = candidate, true
			break
		}
	}
	if !found {
		t.Fatal("qq-search must be registered in the shared layout")
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
