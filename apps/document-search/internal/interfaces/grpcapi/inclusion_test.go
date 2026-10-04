package grpcapi

import (
	"errors"
	"testing"

	documentsearchv1 "packages/gen/documentsearch/v1"
	"packages/serviceauth"
)

// These tests cover the authorization rules the transport enforces before any
// business call: which RPCs are registered at all, and the "only narrow"
// inclusion rule. They need no database and no server.

func rangeClaims() *serviceauth.CapabilityClaims {
	return &serviceauth.CapabilityClaims{
		Version:            1,
		Issuer:             serviceauth.CallerDocumentService,
		Audience:           serviceauth.AudienceDocumentSearch,
		Scopes:             []string{string(serviceauth.ScopeDocumentSearcher)},
		SubjectKey:         "web:user:42",
		PrivateSpaceIDs:    []string{"space-private"},
		CurrentTeamSpaceID: "space-current",
		OtherTeamSpaceIDs:  []string{"space-other"},
		AllowedSpaceIDs:    []string{"space-shared"},
		AllowedDocumentIDs: []string{"document-a"},
	}
}

func TestCheckScopeInclusionAcceptsSubsets(t *testing.T) {
	claims := rangeClaims()
	cases := []struct {
		name      string
		spaces    []string
		documents []string
	}{
		{"empty request narrows to the authenticated public floor", nil, nil},
		{"one labelled family", []string{"space-private"}, nil},
		{"the current team space", []string{"space-current"}, nil},
		{"another team space", []string{"space-other"}, nil},
		{"the shared space", []string{"space-shared"}, nil},
		{"every granted space", []string{"space-private", "space-current", "space-other", "space-shared"}, nil},
		{"duplicates and ordering do not matter", []string{"space-shared", "space-shared", "space-private"}, nil},
		{"a granted document", nil, []string{"document-a"}},
		{"spaces and documents together", []string{"space-shared"}, []string{"document-a"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := CheckScopeInclusion(claims, testCase.spaces, testCase.documents); err != nil {
				t.Fatalf("an in-grant request was rejected: %v", err)
			}
		})
	}
}

func TestCheckScopeInclusionRejectsOutOfRangeAsAWhole(t *testing.T) {
	claims := rangeClaims()
	cases := []struct {
		name      string
		spaces    []string
		documents []string
	}{
		{"an unknown space", []string{"space-unknown"}, nil},
		{"an unknown space beside a granted one", []string{"space-private", "space-unknown"}, nil},
		{"an unknown document", nil, []string{"document-b"}},
		{"an unknown document beside a granted one", nil, []string{"document-a", "document-b"}},
		{"both families out of range", []string{"space-unknown"}, []string{"document-b"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := CheckScopeInclusion(claims, testCase.spaces, testCase.documents)
			if err == nil {
				t.Fatal("an out-of-grant request was accepted")
			}
			if !errors.Is(err, serviceauth.ErrScopeNotGranted) {
				t.Fatalf("error = %v, want ErrScopeNotGranted", err)
			}
			if code := serviceauth.HTTPStatus(err); code != 403 {
				t.Fatalf("HTTP status = %d, want 403", code)
			}
		})
	}
	if err := CheckScopeInclusion(nil, nil, nil); !errors.Is(err, serviceauth.ErrSubjectNotAllowed) {
		t.Fatalf("a missing capability returned %v, want ErrSubjectNotAllowed", err)
	}
}

func TestEffectiveScopeComesFromTheCapabilityOnly(t *testing.T) {
	claims := rangeClaims()
	spaces, documents, public := effectiveScope(claims)
	want := []string{"space-current", "space-other", "space-private", "space-shared"}
	if len(spaces) != len(want) {
		t.Fatalf("spaces = %v, want %v", spaces, want)
	}
	for index := range want {
		if spaces[index] != want[index] {
			t.Fatalf("spaces = %v, want the canonical union %v", spaces, want)
		}
	}
	if len(documents) != 1 || documents[0] != "document-a" {
		t.Fatalf("documents = %v, want [document-a]", documents)
	}
	if public {
		t.Fatal("the authenticated public flag was invented from a capability that does not carry it")
	}

	empty := &serviceauth.CapabilityClaims{Version: 1, SubjectKey: "web:user:42"}
	spaces, documents, public = effectiveScope(empty)
	if len(spaces) != 0 || len(documents) != 0 || public {
		t.Fatalf("an empty capability produced range %v/%v/%v, want nothing", spaces, documents, public)
	}
	spaces, documents, public = effectiveScope(nil)
	if len(spaces) != 0 || len(documents) != 0 || public {
		t.Fatalf("a missing capability produced range %v/%v/%v, want nothing", spaces, documents, public)
	}

	publicClaims := rangeClaims()
	publicClaims.AuthenticatedPublic = true
	if _, _, public = effectiveScope(publicClaims); !public {
		t.Fatal("the authenticated public flag was dropped")
	}
}

// TestEveryRPCHasAPolicy guards the fail-closed property of the policy table: a
// method that is served without a registered policy would be an anonymous entry
// point into the index.
func TestEveryRPCHasAPolicy(t *testing.T) {
	for _, method := range documentsearchv1.DocumentSearchService_ServiceDesc.Methods {
		fullMethod := "/documentsearch.v1.DocumentSearchService/" + method.MethodName
		scopes, registered := methodScopes[fullMethod]
		if !registered {
			t.Errorf("%s has no entry in the method policy table", fullMethod)
			continue
		}
		if len(scopes) == 0 {
			t.Errorf("%s is registered with no required scope", fullMethod)
		}
	}
	for fullMethod := range methodScopes {
		found := false
		for _, method := range documentsearchv1.DocumentSearchService_ServiceDesc.Methods {
			if fullMethod == "/documentsearch.v1.DocumentSearchService/"+method.MethodName {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the policy table registers %s, which the service does not serve", fullMethod)
		}
	}
}
