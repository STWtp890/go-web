package domain

import (
	"fmt"
	"time"
)

// The functions in this file are the revision state machine of a formal
// document. They are pure: they take the current aggregate head, apply one
// business transition and return the new head. Keeping them free of SQL is what
// makes "which revision moves on which command" testable without a database,
// and it gives the persistence layer exactly one place to read the new fencing
// values from.
//
// Invariants every transition keeps:
//   - activation_revision, access_revision, lifecycle_revision and
//     aggregate_revision only ever move forward;
//   - aggregate_revision advances on every command that changes anything the
//     search index can observe, so a consumer can fence stale events;
//   - a lifecycle that does not allow the transition is a precondition failure,
//     never a silent no-op.

// CreateDocumentState returns the aggregate head of a document created right
// now: the first version is published and active immediately, so activation,
// access and aggregate all start at revision 1.
func CreateDocumentState(documentID, ownerSubjectKey, ownerSpaceID string, now time.Time) Document {
	return Document{
		DocumentID:         documentID,
		OwnerSubjectKey:    ownerSubjectKey,
		OwnerSpaceID:       ownerSpaceID,
		LifecycleStatus:    LifecycleActive,
		ActivationRevision: 1,
		AccessRevision:     1,
		LifecycleRevision:  0,
		AggregateRevision:  1,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

// CheckExpectedRevision implements the optimistic concurrency rule shared by the
// mutating commands: a non-zero expected revision must equal the current
// aggregate revision.
func CheckExpectedRevision(document Document, expected uint64) error {
	if expected == 0 {
		return nil
	}
	if uint64(document.AggregateRevision) != expected {
		return fmt.Errorf("%w: aggregate revision is %d but the request expected %d",
			ErrPrecondition, document.AggregateRevision, expected)
	}
	return nil
}

// NextVersionRevision returns the revision number for a version appended after
// the given latest revision. A missing latest revision is revision 0, so the
// first appended version is 1.
func NextVersionRevision(latest int64) (int64, error) {
	if latest < 0 {
		return 0, fmt.Errorf("%w: latest version revision %d is negative", ErrInvariant, latest)
	}
	return latest + 1, nil
}

// AppendDraftVersion applies "append an unpublished version" to the aggregate
// head. Publishing is a separate command, so the active version is untouched.
func AppendDraftVersion(document Document, now time.Time) (Document, error) {
	if document.LifecycleStatus != LifecycleActive {
		return document, fmt.Errorf("%w: document %s is %s and cannot take a new draft",
			ErrPrecondition, document.DocumentID, document.LifecycleStatus)
	}
	document.AggregateRevision++
	document.UpdatedAt = now
	return document, nil
}

// ChangeAccess applies an authenticated_public change. It reports whether the
// flag actually changed: an access revision only moves when the access decision
// moves, so an identical value does not inflate the fencing values.
func ChangeAccess(document Document, authenticatedPublic bool, current AuthenticatedPublicState, now time.Time) (Document, bool, error) {
	if document.LifecycleStatus != LifecycleActive {
		return document, false, fmt.Errorf("%w: document %s is %s and its access cannot change",
			ErrPrecondition, document.DocumentID, document.LifecycleStatus)
	}
	if current.Set && current.Value == authenticatedPublic {
		return document, false, nil
	}
	document.AccessRevision++
	document.AggregateRevision++
	document.UpdatedAt = now
	return document, true, nil
}

// AuthenticatedPublicState is the current access flag together with whether a
// policy row exists at all.
type AuthenticatedPublicState struct {
	Value bool
	Set   bool
}

// ValidatePublishable rejects a version whose publication status forbids
// activation. A withdrawn version is retired for good: republishing it would
// resurrect content the owner explicitly retired.
func ValidatePublishable(version Version) error {
	switch version.PublicationStatus {
	case PublicationDraft, PublicationPublished, PublicationSuperseded:
		return nil
	case PublicationWithdrawn:
		return fmt.Errorf("%w: version %s is withdrawn and cannot be published again",
			ErrPrecondition, version.VersionID)
	default:
		return fmt.Errorf("%w: version %s has unknown publication status %q",
			ErrInvariant, version.VersionID, version.PublicationStatus)
	}
}

// ActivateVersion makes a version the active one. The caller has already checked
// that the version belongs to the document and may be published.
func ActivateVersion(document Document, versionID string, now time.Time) (Document, error) {
	if versionID == "" {
		return document, fmt.Errorf("%w: a version id is required to activate a version", ErrInvalidInput)
	}
	if document.LifecycleStatus != LifecycleActive {
		return document, fmt.Errorf("%w: document %s is %s and cannot publish",
			ErrPrecondition, document.DocumentID, document.LifecycleStatus)
	}
	document.ActiveVersionID = versionID
	document.ActivationRevision++
	document.AggregateRevision++
	document.UpdatedAt = now
	return document, nil
}

// WithdrawVersion applies the retirement of one published version. It reports
// whether the withdrawn version was the active one, because that decides whether
// the index must be told to delete the document or to re-read its state.
func WithdrawVersion(document Document, target Version, now time.Time) (Document, bool, error) {
	if document.LifecycleStatus != LifecycleActive {
		return document, false, fmt.Errorf("%w: document %s is %s and cannot withdraw a version",
			ErrPrecondition, document.DocumentID, document.LifecycleStatus)
	}
	if target.PublicationStatus != PublicationPublished {
		return document, false, fmt.Errorf("%w: version %s is %s, only a published version can be withdrawn",
			ErrPrecondition, target.VersionID, target.PublicationStatus)
	}
	wasActive := document.ActiveVersionID == target.VersionID
	if wasActive {
		document.ActiveVersionID = ""
		document.ActivationRevision++
	}
	document.AggregateRevision++
	document.UpdatedAt = now
	return document, wasActive, nil
}

// Archive moves an active document out of the searchable set while keeping it.
func Archive(document Document, now time.Time) (Document, error) {
	if document.LifecycleStatus != LifecycleActive {
		return document, fmt.Errorf("%w: document %s is already %s",
			ErrPrecondition, document.DocumentID, document.LifecycleStatus)
	}
	document.LifecycleStatus = LifecycleArchived
	document.LifecycleRevision++
	document.AggregateRevision++
	document.UpdatedAt = now
	return document, nil
}

// Trash moves a document to the trashed lifecycle. Both an active and an
// archived document may be trashed; trashing a trashed document is a
// precondition failure rather than a second revision bump.
func Trash(document Document, now time.Time) (Document, error) {
	if document.LifecycleStatus == LifecycleTrashed {
		return document, fmt.Errorf("%w: document %s is already trashed", ErrPrecondition, document.DocumentID)
	}
	document.LifecycleStatus = LifecycleTrashed
	document.LifecycleRevision++
	document.AggregateRevision++
	document.UpdatedAt = now
	trashedAt := now
	document.TrashedAt = &trashedAt
	return document, nil
}
