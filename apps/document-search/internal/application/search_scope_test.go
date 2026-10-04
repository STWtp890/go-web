package application

import (
	"testing"

	"packages/serviceauth"
)

// These tests pin the rule the integration suite then proves against the real
// database: the capability is the authorization ceiling, the request is the
// actual filter. They need no database.

const (
	spaceA = "11111111-1111-4111-8111-111111111111"
	spaceB = "22222222-2222-4222-8222-222222222222"
	docA   = "33333333-3333-4333-8333-333333333333"
	docB   = "44444444-4444-4444-8444-444444444444"
)

func scopeCapability(public bool, spaces, documents []string) *serviceauth.CapabilityClaims {
	return &serviceauth.CapabilityClaims{
		Version:             1,
		Issuer:              serviceauth.CallerDocumentService,
		Audience:            serviceauth.AudienceDocumentSearch,
		Scopes:              []string{string(serviceauth.ScopeDocumentSearcher)},
		SubjectKey:          "web:user:scope",
		AllowedSpaceIDs:     spaces,
		AllowedDocumentIDs:  documents,
		AuthenticatedPublic: public,
	}
}

func resolvedScope(capability *serviceauth.CapabilityClaims, requestedSpaces, requestedDocuments []string, ownedOnly bool) searchScope {
	grantedSpaces := canonicalIDs(grantedSpaceIDs(capability))
	grantedDocuments := canonicalIDs(capability.AllowedDocumentIDs)
	return effectiveSearchScope(capability, grantedSpaces, grantedDocuments,
		canonicalIDs(requestedSpaces), canonicalIDs(requestedDocuments), ownedOnly)
}

// A named family becomes the actual filter set: the previous implementation
// validated the request and then filtered by the full grant anyway, so naming
// space A still returned everything else that was granted.
func TestEffectiveSearchScopeNamedRangeReplacesTheGrant(t *testing.T) {
	capability := scopeCapability(true, []string{spaceA, spaceB}, []string{docA})

	cases := []struct {
		name       string
		spaces     []string
		documents  []string
		wantSpaces []string
		wantDocs   []string
		wantPublic bool
	}{
		{"nothing named keeps the whole granted envelope", nil, nil, []string{spaceA, spaceB}, []string{docA}, true},
		{"naming a space drops the other families and the public floor", []string{spaceA}, nil, []string{spaceA}, nil, false},
		{"naming a document drops the granted spaces", nil, []string{docA}, nil, []string{docA}, false},
		{"naming both keeps exactly both", []string{spaceB}, []string{docA}, []string{spaceB}, []string{docA}, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			scope := resolvedScope(capability, testCase.spaces, testCase.documents, false)
			assertIDs(t, "spaces", scope.SpaceIDs, testCase.wantSpaces)
			assertIDs(t, "documents", scope.DocumentIDs, testCase.wantDocs)
			if scope.IncludePublic != testCase.wantPublic {
				t.Fatalf("include_public = %v, want %v", scope.IncludePublic, testCase.wantPublic)
			}
			if scope.OwnedBySubject {
				t.Fatal("the owner filter was enabled by a request that did not ask for it")
			}
		})
	}
}

// The scope never hands SQL a nil family: an empty family must filter
// everything out rather than degrade into "no condition".
func TestEffectiveSearchScopeNeverReturnsNilFamilies(t *testing.T) {
	scope := resolvedScope(scopeCapability(false, nil, nil), nil, nil, false)
	if scope.SpaceIDs == nil || scope.DocumentIDs == nil {
		t.Fatalf("families must be empty, not nil: spaces=%v documents=%v", scope.SpaceIDs, scope.DocumentIDs)
	}
	// An empty capability is the "authenticated-public only" envelope, which is
	// why an unnarrowed request keeps the floor even when the capability's own
	// flag is false. The integration suite pins the same behaviour.
	if !scope.IncludePublic {
		t.Fatal("an unnarrowed request turned the authenticated public floor off")
	}
	named := resolvedScope(scopeCapability(true, []string{spaceA}, nil), []string{spaceA}, nil, false)
	if named.DocumentIDs == nil {
		t.Fatal("the unnamed document family must be empty, not nil")
	}
	if named.IncludePublic {
		t.Fatal("a named range kept the authenticated public floor on")
	}
}

// Only identifiers that can match an indexed row reach SQL. Dropping a malformed
// value narrows the filter, which is the fail-closed direction.
func TestEffectiveSearchScopeDropsNonUUIDIdentifiers(t *testing.T) {
	scope := resolvedScope(
		scopeCapability(false, []string{spaceA, "not-a-uuid"}, []string{"also-not-a-uuid"}),
		nil, nil, false)
	assertIDs(t, "spaces", scope.SpaceIDs, []string{spaceA})
	if len(scope.DocumentIDs) != 0 {
		t.Fatalf("documents = %v, want the malformed grant dropped", scope.DocumentIDs)
	}
}

// owned_by_subject_only reads its subject from the validated capability and
// never from the request; a capability that names no subject yields an empty
// owner, which the query path answers with an empty result.
func TestEffectiveSearchScopeOwnerComesFromTheCapability(t *testing.T) {
	capability := scopeCapability(false, []string{spaceA}, nil)
	scope := resolvedScope(capability, nil, nil, true)
	if !scope.OwnedBySubject {
		t.Fatal("owned_by_subject_only did not enable the owner filter")
	}
	if scope.OwnerSubjectKey != "web:user:scope" {
		t.Fatalf("owner subject = %q, want the capability subject", scope.OwnerSubjectKey)
	}

	anonymous := scopeCapability(false, []string{spaceA}, nil)
	anonymous.SubjectKey = "   "
	scope = resolvedScope(anonymous, nil, nil, true)
	if !scope.OwnedBySubject || scope.OwnerSubjectKey != "" {
		t.Fatalf("a subjectless capability produced owner=%v key=%q, want an empty key that fails closed",
			scope.OwnedBySubject, scope.OwnerSubjectKey)
	}
}

func assertIDs(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
}
