package domain_test

import (
	"errors"
	"testing"

	"document-service/internal/domain"
)

// The resolution tests pin the two-state decision, the three server generated
// labels and their disjointness. They run without a database: the resolver takes
// facts, not a connection.

func granted(t *testing.T, subjectKey string, private []string, currentTeam string, others []string) domain.Resolution {
	t.Helper()
	resolution, err := domain.GrantedResolution(subjectKey, private, currentTeam, others, nil)
	if err != nil {
		t.Fatalf("GrantedResolution: %v", err)
	}
	if err := resolution.Validate(); err != nil {
		t.Fatalf("granted resolution is not valid: %v", err)
	}
	return resolution
}

func TestGrantedResolutionBuildsADeterministicUnion(t *testing.T) {
	resolution := granted(t, "web:user:1",
		[]string{"private-b", "private-a", "private-a"}, "team-current", []string{"team-z", "team-a"})

	if len(resolution.Private) != 2 || resolution.Private[0] != "private-a" || resolution.Private[1] != "private-b" {
		t.Fatalf("private = %v, want a sorted de-duplicated set", resolution.Private)
	}
	if resolution.CurrentTeam != "team-current" {
		t.Fatalf("current team = %q", resolution.CurrentTeam)
	}
	want := []string{"private-a", "private-b", "team-a", "team-current", "team-z"}
	if len(resolution.MemberSpaceIDs) != len(want) {
		t.Fatalf("member space ids = %v, want %v", resolution.MemberSpaceIDs, want)
	}
	for index, id := range want {
		if resolution.MemberSpaceIDs[index] != id {
			t.Fatalf("member space ids = %v, want %v", resolution.MemberSpaceIDs, want)
		}
	}
}

func TestGrantedResolutionRefusesOverlappingLabels(t *testing.T) {
	if _, err := domain.GrantedResolution("web:user:1", nil, "space-1", []string{"space-1"}, nil); !errors.Is(err, domain.ErrInvariant) {
		t.Fatalf("current team inside other teams: err = %v, want an invariant failure", err)
	}
	if _, err := domain.GrantedResolution("web:user:1", []string{"space-1"}, "space-1", nil, nil); !errors.Is(err, domain.ErrInvariant) {
		t.Fatalf("current team inside private: err = %v, want an invariant failure", err)
	}
	if _, err := domain.GrantedResolution("web:user:1", []string{"space-1"}, "", []string{"space-1"}, nil); !errors.Is(err, domain.ErrInvariant) {
		t.Fatalf("private inside other teams: err = %v, want an invariant failure", err)
	}
}

func TestDeniedResolutionCarriesNoRange(t *testing.T) {
	resolution := domain.DeniedResolution("web:user:1", domain.DeniedGroupUnbound)
	if err := resolution.Validate(); err != nil {
		t.Fatalf("denied resolution is not valid: %v", err)
	}
	if resolution.Granted {
		t.Fatal("a denial must not be granted")
	}
	if resolution.Reason != domain.DeniedGroupUnbound {
		t.Fatalf("reason = %q", resolution.Reason)
	}
	if resolution.CurrentTeam != "" || len(resolution.Private) != 0 ||
		len(resolution.OtherTeams) != 0 || len(resolution.MemberSpaceIDs) != 0 || len(resolution.DocumentIDs) != 0 {
		t.Fatalf("a denial must not carry a range: %+v", resolution)
	}
	if len(resolution.Envelope()) != 0 {
		t.Fatal("a denied resolution has no envelope")
	}
}

func TestValidateRejectsADenialWithoutAReason(t *testing.T) {
	if err := (domain.Resolution{}).Validate(); !errors.Is(err, domain.ErrInvariant) {
		t.Fatalf("err = %v, want an invariant failure", err)
	}
	if err := (domain.Resolution{Reason: domain.DeniedReason("made-up")}).Validate(); !errors.Is(err, domain.ErrInvariant) {
		t.Fatalf("unknown reason err = %v, want an invariant failure", err)
	}
}

func TestValidateRejectsAnEnvelopeThatIsNotTheUnion(t *testing.T) {
	resolution := granted(t, "web:user:1", []string{"private-a"}, "", []string{"team-a"})
	resolution.MemberSpaceIDs = []string{"private-a"}
	if err := resolution.Validate(); !errors.Is(err, domain.ErrInvariant) {
		t.Fatalf("err = %v, want an invariant failure", err)
	}
}

func TestResolveResourceScopeDeniesUnknownAndInactiveSubjects(t *testing.T) {
	conversation := domain.Conversation{Kind: domain.ConversationPrivate}

	unknown, err := domain.ResolveResourceScope("web:user:1", conversation, "", domain.ScopeFacts{})
	if err != nil {
		t.Fatalf("ResolveResourceScope: %v", err)
	}
	if unknown.Granted || unknown.Reason != domain.DeniedSubjectUnknown {
		t.Fatalf("unknown subject resolved to %+v", unknown)
	}

	inactive, err := domain.ResolveResourceScope("web:user:1", conversation, "",
		domain.ScopeFacts{SubjectSeen: true, SubjectActive: false, PrivateSpaceIDs: []string{"private-a"}})
	if err != nil {
		t.Fatalf("ResolveResourceScope: %v", err)
	}
	if inactive.Granted || inactive.Reason != domain.DeniedSubjectInactive {
		t.Fatalf("inactive subject resolved to %+v", inactive)
	}
	if len(inactive.Private) != 0 {
		t.Fatal("a denial must not leak the spaces the subject had")
	}
}

func TestResolveResourceScopePrivateConversationWithNoMembershipIsGrantedAndEmpty(t *testing.T) {
	// This is the deliberate non-denial: the subject is known and active, it simply
	// has no space yet. An empty envelope means "nothing to search", and only an
	// unknown or deactivated subject is denied.
	resolution, err := domain.ResolveResourceScope("web:user:1",
		domain.Conversation{Kind: domain.ConversationPrivate}, "",
		domain.ScopeFacts{SubjectSeen: true, SubjectActive: true})
	if err != nil {
		t.Fatalf("ResolveResourceScope: %v", err)
	}
	if !resolution.Granted {
		t.Fatalf("a known active subject with no membership must be granted, got %+v", resolution)
	}
	if resolution.CurrentTeam != "" {
		t.Fatalf("a private conversation must never carry a current team, got %q", resolution.CurrentTeam)
	}
	if len(resolution.MemberSpaceIDs) != 0 {
		t.Fatalf("envelope = %v, want empty", resolution.MemberSpaceIDs)
	}
}

func TestResolveResourceScopePrivateConversationLabelsOtherTeams(t *testing.T) {
	resolution, err := domain.ResolveResourceScope("web:user:1",
		domain.Conversation{Kind: domain.ConversationPrivate}, "",
		domain.ScopeFacts{
			SubjectSeen:     true,
			SubjectActive:   true,
			PrivateSpaceIDs: []string{"private-a"},
			TeamSpaceIDs:    []string{"team-a", "team-b"},
		})
	if err != nil {
		t.Fatalf("ResolveResourceScope: %v", err)
	}
	if len(resolution.Private) != 1 || resolution.Private[0] != "private-a" {
		t.Fatalf("private = %v", resolution.Private)
	}
	if resolution.CurrentTeam != "" {
		t.Fatalf("current team = %q, want empty in a private conversation", resolution.CurrentTeam)
	}
	if len(resolution.OtherTeams) != 2 {
		t.Fatalf("other teams = %v, want both team spaces", resolution.OtherTeams)
	}
	for _, id := range resolution.OtherTeams {
		if id == "private-a" {
			t.Fatal("a private space must never appear under a team label")
		}
	}
}

func TestResolveResourceScopeGroupConversation(t *testing.T) {
	conversation := domain.Conversation{Kind: domain.ConversationGroup, ExternalGroupID: "20002"}
	base := domain.ScopeFacts{
		SubjectSeen:        true,
		SubjectActive:      true,
		PrivateSpaceIDs:    []string{"private-a"},
		TeamSpaceIDs:       []string{"team-a", "team-b"},
		ActiveGroupSpaceID: "team-a",
		IsActiveMember:     true,
		GroupSeen:          true,
	}

	grantedScope, err := domain.ResolveResourceScope("qq:user:10001/20002", conversation, "10001", base)
	if err != nil {
		t.Fatalf("ResolveResourceScope: %v", err)
	}
	if !grantedScope.Granted {
		t.Fatalf("expected a grant, got %+v", grantedScope)
	}
	if grantedScope.CurrentTeam != "team-a" {
		t.Fatalf("current team = %q, want team-a", grantedScope.CurrentTeam)
	}
	if len(grantedScope.OtherTeams) != 1 || grantedScope.OtherTeams[0] != "team-b" {
		t.Fatalf("other teams = %v, want [team-b]: the current team is never repeated", grantedScope.OtherTeams)
	}

	// A group whose binding is not active is denied, and the reason distinguishes
	// "never bound" from "binding revoked".
	unbound := base
	unbound.ActiveGroupSpaceID = ""
	unbound.GroupSeen = false
	resolution, err := domain.ResolveResourceScope("qq:user:10001/20002", conversation, "10001", unbound)
	if err != nil {
		t.Fatalf("ResolveResourceScope: %v", err)
	}
	if resolution.Granted || resolution.Reason != domain.DeniedGroupUnbound {
		t.Fatalf("unbound group resolved to %+v", resolution)
	}

	revoked := base
	revoked.ActiveGroupSpaceID = ""
	revoked.GroupSeen = true
	revoked.GroupRevoked = true
	resolution, err = domain.ResolveResourceScope("qq:user:10001/20002", conversation, "10001", revoked)
	if err != nil {
		t.Fatalf("ResolveResourceScope: %v", err)
	}
	if resolution.Granted || resolution.Reason != domain.DeniedGroupBindingRevoked {
		t.Fatalf("revoked binding resolved to %+v", resolution)
	}

	notMember := base
	notMember.IsActiveMember = false
	resolution, err = domain.ResolveResourceScope("qq:user:10001/20002", conversation, "10001", notMember)
	if err != nil {
		t.Fatalf("ResolveResourceScope: %v", err)
	}
	if resolution.Granted || resolution.Reason != domain.DeniedNotSpaceMember {
		t.Fatalf("non member resolved to %+v", resolution)
	}
	if len(resolution.MemberSpaceIDs) != 0 {
		t.Fatal("a denial must not carry the envelope")
	}
}

func TestResolveResourceScopeGroupWithoutBotNamespaceIsUnbound(t *testing.T) {
	// A group binding is addressed by (channel, bot_id, external_group_id). Without
	// a Bot namespace there can be no binding, so the group is unbound rather than
	// resolved against some other Bot's space.
	resolution, err := domain.ResolveResourceScope("qq:user:10001/20002",
		domain.Conversation{Kind: domain.ConversationGroup, ExternalGroupID: "20002"}, "",
		domain.ScopeFacts{SubjectSeen: true, SubjectActive: true, ActiveGroupSpaceID: "team-a", IsActiveMember: true})
	if err != nil {
		t.Fatalf("ResolveResourceScope: %v", err)
	}
	if resolution.Granted || resolution.Reason != domain.DeniedGroupUnbound {
		t.Fatalf("resolution = %+v, want group_unbound", resolution)
	}
}

func TestConversationValidate(t *testing.T) {
	if err := (domain.Conversation{Kind: domain.ConversationPrivate}).Validate(); err != nil {
		t.Fatalf("private conversation: %v", err)
	}
	if err := (domain.Conversation{Kind: domain.ConversationPrivate, ExternalGroupID: "1"}).Validate(); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("private conversation with a group id: err = %v", err)
	}
	if err := (domain.Conversation{Kind: domain.ConversationGroup, ExternalGroupID: "20002"}).Validate(); err != nil {
		t.Fatalf("group conversation: %v", err)
	}
	if err := (domain.Conversation{Kind: domain.ConversationGroup}).Validate(); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("group conversation without a group id: err = %v", err)
	}
	if err := (domain.Conversation{Kind: "channel"}).Validate(); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("unknown kind: err = %v", err)
	}
}

func TestMayReadDocument(t *testing.T) {
	cases := []struct {
		name  string
		facts domain.AccessFacts
		want  bool
	}{
		{"owner", domain.AccessFacts{IsOwner: true}, true},
		{"subject grant", domain.AccessFacts{HasSubjectGrant: true}, true},
		{"granted space member", domain.AccessFacts{IsGrantedSpaceMember: true}, true},
		{"owner space member", domain.AccessFacts{IsOwnerSpaceMember: true}, true},
		{"public and registered", domain.AccessFacts{AuthenticatedPublic: true, SubjectRegistered: true, SubjectActive: true}, true},
		{"public but unregistered", domain.AccessFacts{AuthenticatedPublic: true}, false},
		{"public but inactive", domain.AccessFacts{AuthenticatedPublic: true, SubjectRegistered: true, SubjectActive: false}, false},
		{"stranger on a private document", domain.AccessFacts{SubjectRegistered: true, SubjectActive: true}, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := domain.MayReadDocument(testCase.facts); got != testCase.want {
				t.Fatalf("MayReadDocument = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestAccessFactsValidate(t *testing.T) {
	if err := (domain.AccessFacts{}).Validate(); !errors.Is(err, domain.ErrInvariant) {
		t.Fatalf("missing document id: err = %v", err)
	}
	if err := (domain.AccessFacts{DocumentID: "doc-1", LifecycleStatus: "stale"}).Validate(); !errors.Is(err, domain.ErrInvariant) {
		t.Fatalf("unknown lifecycle: err = %v", err)
	}
	if err := (domain.AccessFacts{DocumentID: "doc-1", LifecycleStatus: domain.LifecycleActive}).Validate(); err != nil {
		t.Fatalf("valid facts: %v", err)
	}
}

func TestDocumentFromFactsRoundTripsTheHead(t *testing.T) {
	facts := domain.AccessFacts{
		DocumentID:         "doc-1",
		OwnerSubjectKey:    "web:user:1",
		OwnerSpaceID:       "space-1",
		LifecycleStatus:    domain.LifecycleActive,
		ActiveVersionID:    "version-1",
		ActivationRevision: 3,
		AccessRevision:     2,
		LifecycleRevision:  1,
		AggregateRevision:  4,
	}
	document := domain.DocumentFromFacts(facts)
	if document.DocumentID != "doc-1" || document.AggregateRevision != 4 || document.ActiveVersionID != "version-1" {
		t.Fatalf("DocumentFromFacts = %+v", document)
	}
}
