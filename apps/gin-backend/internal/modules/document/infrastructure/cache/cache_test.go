package cache

import (
	"strings"
	"testing"

	"gin-backend/internal/modules/document/domain"
)

func TestViewKeyContainsIdentityAndFencingRevisions(t *testing.T) {
	head := domain.DocumentHead{
		DocumentID: "document-id", ActiveVersionID: "version-id",
		ActivationRevision: 2, AccessRevision: 3, LifecycleRevision: 4,
	}
	key := ViewKey(head)
	for _, expected := range []string{"document-id", "version-id", "activation:2", "access:3", "lifecycle:4"} {
		if !strings.Contains(key, expected) {
			t.Fatalf("ViewKey() = %q, missing %q", key, expected)
		}
	}

	head.AccessRevision++
	if next := ViewKey(head); next == key {
		t.Fatalf("ViewKey() did not change after access revision advanced: %q", next)
	}
}
