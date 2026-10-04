package grpcapi

// Unit tests for the channel inclusion rule. They need no server and no
// database: the rule is the whole point of the transport boundary and it is
// exported precisely so it can be tested on its own.

import (
	"strings"
	"testing"

	qqsearchv1 "packages/gen/qqsearch/v1"
	"packages/serviceauth"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCheckChannelInclusionOnlyNarrows(t *testing.T) {
	claims := &serviceauth.CapabilityClaims{
		BotIDs:           []string{"10001"},
		ConversationIDs:  []string{"qq:10001:group:999"},
		ExternalGroupIDs: []string{"999"},
	}
	cases := []struct {
		name      string
		requested *qqsearchv1.QQChannelScope
		allowed   bool
	}{
		{
			name:      "no requested scope means the whole grant",
			requested: nil,
			allowed:   true,
		},
		{
			name:      "an empty requested scope means the whole grant",
			requested: &qqsearchv1.QQChannelScope{},
			allowed:   true,
		},
		{
			name:      "a subset is allowed",
			requested: &qqsearchv1.QQChannelScope{BotIds: []string{"10001"}},
			allowed:   true,
		},
		{
			name: "the exact grant is allowed",
			requested: &qqsearchv1.QQChannelScope{
				BotIds:           []string{"10001"},
				ConversationIds:  []string{"qq:10001:group:999"},
				ExternalGroupIds: []string{"999"},
			},
			allowed: true,
		},
		{
			name:      "another bot is refused",
			requested: &qqsearchv1.QQChannelScope{BotIds: []string{"10002"}},
			allowed:   false,
		},
		{
			name:      "another conversation is refused",
			requested: &qqsearchv1.QQChannelScope{ConversationIds: []string{"qq:10001:group:1000"}},
			allowed:   false,
		},
		{
			name:      "another group is refused",
			requested: &qqsearchv1.QQChannelScope{ExternalGroupIds: []string{"1000"}},
			allowed:   false,
		},
		{
			name: "one out-of-scope identifier rejects the whole request",
			requested: &qqsearchv1.QQChannelScope{
				ConversationIds: []string{"qq:10001:group:999", "qq:10001:group:1000"},
			},
			allowed: false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := CheckChannelInclusion(claims, testCase.requested)
			if testCase.allowed {
				if err != nil {
					t.Fatalf("CheckChannelInclusion returned %v, want the request to be allowed", err)
				}
				return
			}
			if err == nil {
				t.Fatal("an out-of-scope request must be refused, not trimmed")
			}
			if got := status.Code(serviceauth.GRPCStatus(err)); got != codes.PermissionDenied {
				t.Fatalf("status = %s, want PermissionDenied", got)
			}
		})
	}
}

func TestCheckChannelInclusionRefusesWithoutACapability(t *testing.T) {
	if err := CheckChannelInclusion(nil, nil); err == nil {
		t.Fatal("a query without a capability must be refused even with an empty request scope")
	}
}

func TestMethodScopesCoverEveryRPCExactlyOnce(t *testing.T) {
	service := qqsearchv1.QQSearchService_ServiceDesc
	if len(service.Methods) == 0 {
		t.Fatal("the generated service descriptor has no methods")
	}
	if len(methodScopes) != len(service.Methods) {
		t.Fatalf("the policy table has %d entries for %d RPCs: an unpoliced RPC would fail open",
			len(methodScopes), len(service.Methods))
	}
	for _, method := range service.Methods {
		fullMethod := "/" + service.ServiceName + "/" + method.MethodName
		scopes, known := methodScopes[fullMethod]
		if !known {
			t.Errorf("RPC %s has no authorization policy", fullMethod)
			continue
		}
		if len(scopes) == 0 {
			t.Errorf("RPC %s has an empty policy", fullMethod)
		}
	}

	writerOnly := map[string]bool{
		"/qqsearch.v1.QQSearchService/IndexQQSourceEvent": true,
		"/qqsearch.v1.QQSearchService/RebuildIndex":       true,
		"/qqsearch.v1.QQSearchService/GetIndexStatus":     true,
	}
	searcherOnly := map[string]bool{
		"/qqsearch.v1.QQSearchService/SearchQQMessages": true,
		"/qqsearch.v1.QQSearchService/SearchQQFiles":    true,
		"/qqsearch.v1.QQSearchService/GetQQRecordState": true,
	}
	// Every qq-searcher RPC reads the channel capability from
	// x-resource-capability and enforces the inclusion rule on the scope in its
	// request. GetQQRecordState belongs to this set: it resolves the capability
	// exactly like the two searches, which is what keeps a record outside the
	// grant indistinguishable from a record that does not exist.
	capabilityQueryMethods := map[string]bool{
		"/qqsearch.v1.QQSearchService/SearchQQMessages": true,
		"/qqsearch.v1.QQSearchService/SearchQQFiles":    true,
		"/qqsearch.v1.QQSearchService/GetQQRecordState": true,
	}
	for fullMethod, scopes := range methodScopes {
		switch {
		case writerOnly[fullMethod]:
			if len(scopes) != 1 || scopes[0] != serviceauth.ScopeQQIndexWriter {
				t.Errorf("%s requires %v, want only %q", fullMethod, scopes, serviceauth.ScopeQQIndexWriter)
			}
			if capabilityQueryMethods[fullMethod] {
				t.Errorf("%s is also registered as a capability query method", fullMethod)
			}
		case searcherOnly[fullMethod]:
			if len(scopes) != 1 || scopes[0] != serviceauth.ScopeQQSearcher {
				t.Errorf("%s requires %v, want only %q", fullMethod, scopes, serviceauth.ScopeQQSearcher)
			}
			if !capabilityQueryMethods[fullMethod] {
				t.Errorf("%s requires a channel capability but is not in the capability query table: "+
					"a query RPC that skips the inclusion rule can probe outside the grant", fullMethod)
			}
		default:
			t.Errorf("unexpected method in the policy table: %s", fullMethod)
		}
	}
	if len(capabilityQueryMethods) != len(searcherOnly) {
		t.Errorf("the capability query table has %d methods for %d qq-searcher RPCs",
			len(capabilityQueryMethods), len(searcherOnly))
	}

	// The two QQ scopes are distinct strings, so neither credential can do the
	// other's job.
	if serviceauth.ScopeQQIndexWriter == serviceauth.ScopeQQSearcher {
		t.Fatal("the writer and searcher scopes are the same value")
	}
	if !serviceauth.KnownScope(serviceauth.ScopeQQIndexWriter) || !serviceauth.KnownScope(serviceauth.ScopeQQSearcher) {
		t.Fatal("the QQ scopes must be registered in the shared scope registry")
	}
	if strings.Contains(string(serviceauth.ScopeQQSearcher), "document") {
		t.Fatal("the QQ searcher scope must not be a document scope")
	}
}
