package domain

import (
	"fmt"
	"strings"
	"time"
)

// ScopeFacts is everything the resource scope resolver reads. The decision itself
// is made by ResolveResourceScope below, so the mapping from facts to
// granted/denied is testable without a database.
type ScopeFacts struct {
	SubjectSeen   bool
	SubjectActive bool

	// ActiveGroupSpaceID is empty when the current group has no active binding.
	ActiveGroupSpaceID string
	// GroupSeen reports that the group had a binding at some point.
	GroupSeen bool
	// GroupRevoked reports that the newest binding for the group is revoked.
	GroupRevoked bool
	// IsActiveMember reports whether the subject is an active member of the space
	// the current group is bound to.
	IsActiveMember bool

	PrivateSpaceIDs []string
	TeamSpaceIDs    []string
	// DocumentIDs are the document level grants the subject holds.
	DocumentIDs []string
}

// ResolveResourceScope applies the resolution rules of ADR-016 v3 section 4 to a
// set of facts.
//
// The shape of the answer is deliberate:
//   - a denial carries no range at all, because in the document contract an empty
//     range means "authenticated_public only" - flattening a denial into an empty
//     grant would hand the caller the public corpus;
//   - a private conversation with a valid subject and no memberships is *granted*
//     with an empty envelope. That is "this session currently has nothing to
//     search", not a denial: the subject is known and active, it simply has no
//     space yet. Only an unknown or deactivated subject is denied.
//   - the three labels are generated here and never overlap.
func ResolveResourceScope(subjectKey string, conversation Conversation, botID string, facts ScopeFacts) (Resolution, error) {
	if !facts.SubjectSeen {
		return DeniedResolution(subjectKey, DeniedSubjectUnknown), nil
	}
	if !facts.SubjectActive {
		return DeniedResolution(subjectKey, DeniedSubjectInactive), nil
	}

	if !conversation.IsGroup() {
		return GrantedResolution(subjectKey, facts.PrivateSpaceIDs, "", facts.TeamSpaceIDs, facts.DocumentIDs)
	}

	// A group conversation whose Bot namespace cannot be established has no
	// binding by construction: a group binding is addressed by
	// (channel, bot_id, external_group_id), so an unqualified group id cannot
	// match one. Fail closed with the documented reason.
	if strings.TrimSpace(botID) == "" || facts.ActiveGroupSpaceID == "" {
		if facts.GroupSeen && facts.GroupRevoked {
			return DeniedResolution(subjectKey, DeniedGroupBindingRevoked), nil
		}
		return DeniedResolution(subjectKey, DeniedGroupUnbound), nil
	}
	if !facts.IsActiveMember {
		return DeniedResolution(subjectKey, DeniedNotSpaceMember), nil
	}

	// The current group's space is the only current team; every other team space
	// the subject belongs to stays labelled as an other team.
	others := removeID(facts.TeamSpaceIDs, facts.ActiveGroupSpaceID)
	return GrantedResolution(subjectKey, facts.PrivateSpaceIDs, facts.ActiveGroupSpaceID, others, facts.DocumentIDs)
}

// AccessFacts is the raw material of the document read decision.
type AccessFacts struct {
	IsOwner              bool
	HasSubjectGrant      bool
	IsGrantedSpaceMember bool
	IsOwnerSpaceMember   bool
	AuthenticatedPublic  bool
	SubjectRegistered    bool
	SubjectActive        bool

	DocumentID         string
	OwnerSubjectKey    string
	OwnerSpaceID       string
	LifecycleStatus    string
	ActiveVersionID    string
	ActivationRevision int64
	AccessRevision     int64
	LifecycleRevision  int64
	AggregateRevision  int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
	TrashedAt          *time.Time
}

// MayReadDocument is the document detail read decision of contract section 3.1.
// Four independent ways in: ownership, an explicit subject grant, active
// membership of the owner space or of a granted space, and authenticated_public
// for a registered active subject.
//
// A caller that satisfies none of them is refused with PERMISSION_DENIED. The
// document is not hidden behind NOT_FOUND, because the contract requires the
// difference between "does not exist" and "not allowed" to stay visible.
func MayReadDocument(facts AccessFacts) bool {
	switch {
	case facts.IsOwner, facts.HasSubjectGrant, facts.IsGrantedSpaceMember, facts.IsOwnerSpaceMember:
		return true
	case facts.AuthenticatedPublic && facts.SubjectRegistered && facts.SubjectActive:
		return true
	default:
		return false
	}
}

// DocumentFromFacts rebuilds the aggregate head from the read-decision row, so a
// detail read does not need a second query.
func DocumentFromFacts(facts AccessFacts) Document {
	return Document{
		DocumentID:         facts.DocumentID,
		OwnerSubjectKey:    facts.OwnerSubjectKey,
		OwnerSpaceID:       facts.OwnerSpaceID,
		LifecycleStatus:    facts.LifecycleStatus,
		ActiveVersionID:    facts.ActiveVersionID,
		ActivationRevision: facts.ActivationRevision,
		AccessRevision:     facts.AccessRevision,
		LifecycleRevision:  facts.LifecycleRevision,
		AggregateRevision:  facts.AggregateRevision,
		CreatedAt:          facts.CreatedAt,
		UpdatedAt:          facts.UpdatedAt,
		TrashedAt:          facts.TrashedAt,
	}
}

// Validate checks that the facts describe a readable head.
func (facts AccessFacts) Validate() error {
	if facts.DocumentID == "" {
		return fmt.Errorf("%w: access facts without a document id", ErrInvariant)
	}
	if facts.LifecycleStatus != LifecycleActive &&
		facts.LifecycleStatus != LifecycleArchived &&
		facts.LifecycleStatus != LifecycleTrashed {
		return fmt.Errorf("%w: unknown lifecycle status %q", ErrInvariant, facts.LifecycleStatus)
	}
	return nil
}

func removeID(values []string, remove string) []string {
	if remove == "" || len(values) == 0 {
		return canonicalIDs(values)
	}
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == remove {
			continue
		}
		filtered = append(filtered, value)
	}
	return canonicalIDs(filtered)
}
