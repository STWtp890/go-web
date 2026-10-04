package domain_test

import (
	"errors"
	"testing"
	"time"

	"document-service/internal/domain"
)

// These tests pin the revision state machine. They are pure: no database, no
// clock, no identifiers. Every command's effect on the four fencing revisions is
// asserted here, so the SQL layer only has to store what the state machine
// produced.

var testNow = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

func activeDocument() domain.Document {
	return domain.CreateDocumentState("doc-1", "web:user:1", "space-1", testNow)
}

func publishedVersion(documentID string) domain.Version {
	return domain.Version{
		VersionID:         "version-1",
		DocumentID:        documentID,
		Revision:          1,
		PublicationStatus: domain.PublicationPublished,
	}
}

func TestCreateDocumentStateStartsEveryRevisionAtOne(t *testing.T) {
	document := activeDocument()

	if document.LifecycleStatus != domain.LifecycleActive {
		t.Fatalf("lifecycle = %q, want active", document.LifecycleStatus)
	}
	if document.ActivationRevision != 1 || document.AccessRevision != 1 || document.AggregateRevision != 1 {
		t.Fatalf("revisions = (%d,%d,%d), want (1,1,1)",
			document.ActivationRevision, document.AccessRevision, document.AggregateRevision)
	}
	if document.LifecycleRevision != 0 {
		t.Fatalf("lifecycle revision = %d, want 0 before any lifecycle change", document.LifecycleRevision)
	}
	if document.ActiveVersionID != "" {
		t.Fatal("a created state must not carry an active version before the version row exists")
	}
}

func TestAppendDraftVersionBumpsOnlyAggregate(t *testing.T) {
	document, err := domain.AppendDraftVersion(activeDocument(), testNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("AppendDraftVersion: %v", err)
	}
	if document.AggregateRevision != 2 {
		t.Fatalf("aggregate = %d, want 2", document.AggregateRevision)
	}
	if document.ActivationRevision != 1 || document.AccessRevision != 1 || document.LifecycleRevision != 0 {
		t.Fatalf("a draft must not move activation, access or lifecycle: (%d,%d,%d)",
			document.ActivationRevision, document.AccessRevision, document.LifecycleRevision)
	}
}

func TestAppendDraftVersionRefusesNonActiveLifecycle(t *testing.T) {
	document := activeDocument()
	document.LifecycleStatus = domain.LifecycleTrashed

	if _, err := domain.AppendDraftVersion(document, testNow); !errors.Is(err, domain.ErrPrecondition) {
		t.Fatalf("err = %v, want a precondition failure", err)
	}
}

func TestChangeAccessOnlyMovesRevisionsWhenTheFlagMoves(t *testing.T) {
	document := activeDocument()
	state := domain.AuthenticatedPublicState{Value: false, Set: true}

	unchanged, changed, err := domain.ChangeAccess(document, false, state, testNow)
	if err != nil {
		t.Fatalf("ChangeAccess: %v", err)
	}
	if changed {
		t.Fatal("an identical access flag must not report a change")
	}
	if unchanged.AggregateRevision != 1 || unchanged.AccessRevision != 1 {
		t.Fatalf("revisions moved without an access change: (%d,%d)",
			unchanged.AggregateRevision, unchanged.AccessRevision)
	}

	changedDocument, changed, err := domain.ChangeAccess(document, true, state, testNow)
	if err != nil {
		t.Fatalf("ChangeAccess: %v", err)
	}
	if !changed {
		t.Fatal("a flipped access flag must report a change")
	}
	if changedDocument.AccessRevision != 2 || changedDocument.AggregateRevision != 2 {
		t.Fatalf("revisions = (%d,%d), want (2,2)",
			changedDocument.AccessRevision, changedDocument.AggregateRevision)
	}

	// A document without a policy row is treated as "not set", so asking for the
	// default false is still a change that has to be written.
	unset, changed, err := domain.ChangeAccess(document, false, domain.AuthenticatedPublicState{}, testNow)
	if err != nil {
		t.Fatalf("ChangeAccess: %v", err)
	}
	if !changed {
		t.Fatal("a missing policy row must count as a change")
	}
	if unset.AccessRevision != 2 || unset.AggregateRevision != 2 {
		t.Fatalf("revisions = (%d,%d), want (2,2)", unset.AccessRevision, unset.AggregateRevision)
	}
}

func TestActivateVersionBumpsActivationAndAggregate(t *testing.T) {
	document, err := domain.ActivateVersion(activeDocument(), "version-2", testNow)
	if err != nil {
		t.Fatalf("ActivateVersion: %v", err)
	}
	if document.ActiveVersionID != "version-2" {
		t.Fatalf("active version = %q, want version-2", document.ActiveVersionID)
	}
	if document.ActivationRevision != 2 || document.AggregateRevision != 2 {
		t.Fatalf("revisions = (%d,%d), want (2,2)", document.ActivationRevision, document.AggregateRevision)
	}
	if document.AccessRevision != 1 {
		t.Fatalf("access revision = %d, want 1: publishing is not an access change", document.AccessRevision)
	}
}

func TestActivateVersionRequiresAVersionID(t *testing.T) {
	if _, err := domain.ActivateVersion(activeDocument(), "", testNow); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v, want invalid input", err)
	}
}

func TestWithdrawActiveVersionClearsItAndBumpsActivation(t *testing.T) {
	document := activeDocument()
	document.ActiveVersionID = "version-1"
	target := publishedVersion(document.DocumentID)

	updated, wasActive, err := domain.WithdrawVersion(document, target, testNow)
	if err != nil {
		t.Fatalf("WithdrawVersion: %v", err)
	}
	if !wasActive {
		t.Fatal("withdrawing the active version must be reported as such")
	}
	if updated.ActiveVersionID != "" {
		t.Fatalf("active version = %q, want empty", updated.ActiveVersionID)
	}
	if updated.ActivationRevision != 2 || updated.AggregateRevision != 2 {
		t.Fatalf("revisions = (%d,%d), want (2,2)", updated.ActivationRevision, updated.AggregateRevision)
	}
}

func TestWithdrawSupersededVersionOnlyBumpsAggregate(t *testing.T) {
	document := activeDocument()
	document.ActiveVersionID = "version-2"
	target := publishedVersion(document.DocumentID)
	target.VersionID = "version-1"

	updated, wasActive, err := domain.WithdrawVersion(document, target, testNow)
	if err != nil {
		t.Fatalf("WithdrawVersion: %v", err)
	}
	if wasActive {
		t.Fatal("withdrawing a non-active version must not be reported as removing the active one")
	}
	if updated.ActiveVersionID != "version-2" {
		t.Fatalf("active version = %q, want version-2", updated.ActiveVersionID)
	}
	if updated.ActivationRevision != 1 || updated.AggregateRevision != 2 {
		t.Fatalf("revisions = (%d,%d), want (1,2)", updated.ActivationRevision, updated.AggregateRevision)
	}
}

func TestWithdrawRefusesADraftOrWithdrawnVersion(t *testing.T) {
	document := activeDocument()
	document.ActiveVersionID = "version-1"
	for _, status := range []string{domain.PublicationDraft, domain.PublicationWithdrawn} {
		target := publishedVersion(document.DocumentID)
		target.PublicationStatus = status
		if _, _, err := domain.WithdrawVersion(document, target, testNow); !errors.Is(err, domain.ErrPrecondition) {
			t.Fatalf("status %q: err = %v, want a precondition failure", status, err)
		}
	}
}

func TestValidatePublishableRejectsWithdrawnVersions(t *testing.T) {
	withdrawn := publishedVersion("doc-1")
	withdrawn.PublicationStatus = domain.PublicationWithdrawn
	if err := domain.ValidatePublishable(withdrawn); !errors.Is(err, domain.ErrPrecondition) {
		t.Fatalf("err = %v, want a precondition failure", err)
	}

	// Re-publishing a superseded version is a deliberate rollback and stays
	// allowed; re-publishing the already active version is handled by the caller as
	// a no-op.
	for _, status := range []string{domain.PublicationDraft, domain.PublicationPublished, domain.PublicationSuperseded} {
		candidate := publishedVersion("doc-1")
		candidate.PublicationStatus = status
		if err := domain.ValidatePublishable(candidate); err != nil {
			t.Fatalf("status %q: err = %v, want nil", status, err)
		}
	}
}

func TestArchiveAndTrashBumpLifecycleAndAggregate(t *testing.T) {
	archived, err := domain.Archive(activeDocument(), testNow)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if archived.LifecycleStatus != domain.LifecycleArchived {
		t.Fatalf("lifecycle = %q, want archived", archived.LifecycleStatus)
	}
	if archived.LifecycleRevision != 1 || archived.AggregateRevision != 2 {
		t.Fatalf("revisions = (%d,%d), want (1,2)", archived.LifecycleRevision, archived.AggregateRevision)
	}
	if archived.TrashedAt != nil {
		t.Fatal("archiving must not set trashed_at")
	}

	trashed, err := domain.Trash(archived, testNow)
	if err != nil {
		t.Fatalf("Trash: %v", err)
	}
	if trashed.LifecycleStatus != domain.LifecycleTrashed || trashed.TrashedAt == nil {
		t.Fatalf("trash state = %q, trashed_at = %v", trashed.LifecycleStatus, trashed.TrashedAt)
	}
	if trashed.LifecycleRevision != 2 || trashed.AggregateRevision != 3 {
		t.Fatalf("revisions = (%d,%d), want (2,3)", trashed.LifecycleRevision, trashed.AggregateRevision)
	}
}

func TestArchiveAndTrashRefuseRepeatedTransitions(t *testing.T) {
	archived, err := domain.Archive(activeDocument(), testNow)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if _, err := domain.Archive(archived, testNow); !errors.Is(err, domain.ErrPrecondition) {
		t.Fatalf("second archive err = %v, want a precondition failure", err)
	}
	trashed, err := domain.Trash(archived, testNow)
	if err != nil {
		t.Fatalf("Trash: %v", err)
	}
	if _, err := domain.Trash(trashed, testNow); !errors.Is(err, domain.ErrPrecondition) {
		t.Fatalf("second trash err = %v, want a precondition failure", err)
	}
}

func TestCheckExpectedRevision(t *testing.T) {
	document := activeDocument()
	if err := domain.CheckExpectedRevision(document, 0); err != nil {
		t.Fatalf("expected revision 0 must mean \"do not check\": %v", err)
	}
	if err := domain.CheckExpectedRevision(document, 1); err != nil {
		t.Fatalf("matching revision: %v", err)
	}
	if err := domain.CheckExpectedRevision(document, 2); !errors.Is(err, domain.ErrPrecondition) {
		t.Fatalf("stale revision err = %v, want a precondition failure", err)
	}
}

func TestNextVersionRevision(t *testing.T) {
	revision, err := domain.NextVersionRevision(0)
	if err != nil || revision != 1 {
		t.Fatalf("NextVersionRevision(0) = (%d,%v), want (1,nil)", revision, err)
	}
	revision, err = domain.NextVersionRevision(7)
	if err != nil || revision != 8 {
		t.Fatalf("NextVersionRevision(7) = (%d,%v), want (8,nil)", revision, err)
	}
	if _, err := domain.NextVersionRevision(-1); !errors.Is(err, domain.ErrInvariant) {
		t.Fatalf("negative latest revision err = %v, want an invariant failure", err)
	}
}
