package domain

import (
	"fmt"
	"sort"
	"strings"
)

// ConversationKind is the session context a trusted entry point proved: a
// private session or a group session. The resource envelope depends on it,
// because "the current team" only exists in a group session.
type ConversationKind string

const (
	ConversationPrivate ConversationKind = "private"
	ConversationGroup   ConversationKind = "group"
)

// Conversation is the validated session context used for scope resolution.
type Conversation struct {
	Kind            ConversationKind
	ExternalGroupID string
}

// Validate rejects a session context the service cannot resolve. Malformed
// context is INVALID_ARGUMENT, not a denial: a denial is a resource decision
// about a well formed question (ADR-016 v3 section 4).
func (conversation Conversation) Validate() error {
	switch conversation.Kind {
	case ConversationPrivate:
		if strings.TrimSpace(conversation.ExternalGroupID) != "" {
			return fmt.Errorf("%w: a private conversation must not carry a group id", ErrInvalidInput)
		}
		return nil
	case ConversationGroup:
		if !ValidExternalID(conversation.ExternalGroupID) {
			return fmt.Errorf("%w: a group conversation requires a numeric group id", ErrInvalidInput)
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown conversation kind %q", ErrInvalidInput, conversation.Kind)
	}
}

// IsGroup reports whether the conversation is a group session.
func (conversation Conversation) IsGroup() bool {
	return conversation.Kind == ConversationGroup
}

// GroupID returns the trimmed external group id (empty for a private session).
func (conversation Conversation) GroupID() string {
	return strings.TrimSpace(conversation.ExternalGroupID)
}

// DeniedReason is the stable vocabulary of resource denials. The set is fixed by
// the document contract: an adapter must be able to tell "no range" from "not
// allowed", so a denial always carries a reason it can log.
type DeniedReason string

const (
	// DeniedSubjectUnknown means the subject was never registered.
	DeniedSubjectUnknown DeniedReason = "subject_unknown"
	// DeniedSubjectRevoked is reserved. The registry models deactivation, not
	// revocation of an individual subject key, so no resolution produces it; it
	// stays in the vocabulary because the contract publishes it.
	DeniedSubjectRevoked DeniedReason = "subject_revoked"
	// DeniedSubjectInactive means the subject exists but is deactivated.
	DeniedSubjectInactive DeniedReason = "subject_inactive"
	// DeniedGroupUnbound means the current group has no active binding.
	DeniedGroupUnbound DeniedReason = "group_unbound"
	// DeniedGroupBindingRevoked means the current group had a binding that was
	// revoked and has not been rebound.
	DeniedGroupBindingRevoked DeniedReason = "group_binding_revoked"
	// DeniedNotSpaceMember means the group is bound but the subject is not an
	// active member of the bound space.
	DeniedNotSpaceMember DeniedReason = "not_space_member"
	// DeniedSessionContextInvalid is reserved: malformed session context is
	// reported as INVALID_ARGUMENT instead of as a resource decision, so this
	// reason is never produced by the resolver.
	DeniedSessionContextInvalid DeniedReason = "session_context_invalid"
)

// KnownDeniedReason reports whether a reason is part of the published
// vocabulary.
func KnownDeniedReason(reason DeniedReason) bool {
	switch reason {
	case DeniedSubjectUnknown, DeniedSubjectRevoked, DeniedSubjectInactive,
		DeniedGroupUnbound, DeniedGroupBindingRevoked, DeniedNotSpaceMember,
		DeniedSessionContextInvalid:
		return true
	default:
		return false
	}
}

// Resolution is the resource half of a scope decision: a two-state decision plus
// three server-generated, non-overlapping space labels.
//
// A denied resolution carries no range at all. It must never be flattened into
// an empty granted envelope, because in the document contract an empty range
// means "authenticated_public only" - that would hand an unauthorized subject the
// public corpus.
type Resolution struct {
	Granted    bool
	Reason     DeniedReason
	SubjectKey string

	Private     []string
	CurrentTeam string
	OtherTeams  []string

	// MemberSpaceIDs is the deterministic union of the three labelled families,
	// recomputed server side. It is never accepted as input from a caller.
	MemberSpaceIDs []string

	// DocumentIDs are the document level grants of the subject. They are not part
	// of the published resolution shape (a caller submits labels, never document
	// ids) but a capability must be able to carry them.
	DocumentIDs []string
}

// DeniedResolution builds a denial. It deliberately drops every range field.
func DeniedResolution(subjectKey string, reason DeniedReason) Resolution {
	return Resolution{Granted: false, Reason: reason, SubjectKey: strings.TrimSpace(subjectKey)}
}

// GrantedResolution builds a granted envelope and enforces the label invariants
// while doing so. A private space can never be the current team (group bindings
// may only target team spaces, which the database trigger also enforces), and a
// space id can never appear under two labels: an id under two labels would let a
// caller re-label its way to a wider range.
func GrantedResolution(subjectKey string, privateSpaceIDs []string, currentTeamSpaceID string, otherTeamSpaceIDs []string, documentIDs []string) (Resolution, error) {
	private := canonicalIDs(privateSpaceIDs)
	others := canonicalIDs(otherTeamSpaceIDs)
	current := strings.TrimSpace(currentTeamSpaceID)

	seen := make(map[string]string, len(private)+len(others)+1)
	for _, id := range private {
		seen[id] = "private"
	}
	if current != "" {
		if previous, duplicate := seen[current]; duplicate {
			return Resolution{}, fmt.Errorf("%w: space %s is labelled both %s and current_team",
				ErrInvariant, current, previous)
		}
		seen[current] = "current_team"
	}
	for _, id := range others {
		if previous, duplicate := seen[id]; duplicate {
			return Resolution{}, fmt.Errorf("%w: space %s is labelled both %s and other_teams",
				ErrInvariant, id, previous)
		}
		seen[id] = "other_teams"
	}

	members := make([]string, 0, len(seen))
	for id := range seen {
		members = append(members, id)
	}
	sort.Strings(members)

	return Resolution{
		Granted:        true,
		SubjectKey:     strings.TrimSpace(subjectKey),
		Private:        private,
		CurrentTeam:    current,
		OtherTeams:     others,
		MemberSpaceIDs: members,
		DocumentIDs:    canonicalIDs(documentIDs),
	}, nil
}

// Validate re-checks the label invariants of a resolution. It is the assertion
// the resolver runs before answering and the one the unit tests call directly.
func (resolution Resolution) Validate() error {
	if !resolution.Granted {
		if resolution.Reason == "" {
			return fmt.Errorf("%w: a denied resolution must carry a reason", ErrInvariant)
		}
		if !KnownDeniedReason(resolution.Reason) {
			return fmt.Errorf("%w: unknown denial reason %q", ErrInvariant, resolution.Reason)
		}
		if len(resolution.Private) > 0 || resolution.CurrentTeam != "" ||
			len(resolution.OtherTeams) > 0 || len(resolution.MemberSpaceIDs) > 0 ||
			len(resolution.DocumentIDs) > 0 {
			return fmt.Errorf("%w: a denied resolution must not carry a range", ErrInvariant)
		}
		return nil
	}

	rebuild, err := GrantedResolution(resolution.SubjectKey, resolution.Private, resolution.CurrentTeam, resolution.OtherTeams, resolution.DocumentIDs)
	if err != nil {
		return err
	}
	if !equalIDs(rebuild.MemberSpaceIDs, resolution.MemberSpaceIDs) {
		return fmt.Errorf("%w: member_space_ids %v is not the union of the three labels %v",
			ErrInvariant, resolution.MemberSpaceIDs, rebuild.MemberSpaceIDs)
	}
	return nil
}

// Envelope returns the server-recomputed union as a sorted, de-duplicated set.
// It is the maximum range a capability may be minted from.
func (resolution Resolution) Envelope() []string {
	if !resolution.Granted {
		return nil
	}
	return canonicalIDs(resolution.MemberSpaceIDs)
}

// CurrentTeamSet reports whether a group conversation resolved to a current
// team.
func (resolution Resolution) CurrentTeamSet() bool {
	return resolution.Granted && resolution.CurrentTeam != ""
}

func canonicalIDs(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, duplicate := seen[trimmed]; duplicate {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	if len(result) == 0 {
		return nil
	}
	sort.Strings(result)
	return result
}

func equalIDs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
