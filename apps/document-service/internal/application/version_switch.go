package application

import (
	"context"
	"time"

	"document-service/internal/domain"

	"github.com/jackc/pgx/v5"
)

// This file holds the one version switching rule of the document service.
//
// Two commands move the active version: PublishDocument activates a version the
// caller names, SaveDocument activates the version it just appended. They differ
// in where the target comes from, not in what "become the active version" means.
// Duplicating the supersede/mark-published/bump-activation sequence in both use
// cases would let them drift, so both call switchActiveVersion.

// switchActiveVersion makes target the active version of document inside the
// caller's transaction.
//
// It reports whether anything moved. A target that is already the published
// active version is a no-op: the revisions and the Outbox must not be inflated by
// re-publishing the state that is already committed, and the caller uses the
// false result to skip the event that would otherwise describe no change.
//
// The caller is responsible for the ownership and expected-revision checks and
// for emitting the Outbox event for the committed state; this function only
// applies the switch and persists the new aggregate head.
func (service *Service) switchActiveVersion(ctx context.Context, tx pgx.Tx, document domain.Document, target domain.Version, now time.Time) (domain.Document, bool, error) {
	if err := domain.ValidatePublishable(target); err != nil {
		return document, false, err
	}
	if target.VersionID == document.ActiveVersionID && target.PublicationStatus == domain.PublicationPublished {
		return document, false, nil
	}

	previousActive := document.ActiveVersionID
	if previousActive != "" && previousActive != target.VersionID {
		if err := service.store.SetVersionPublicationStatus(ctx, tx, previousActive, domain.PublicationSuperseded); err != nil {
			return document, false, err
		}
	}
	if err := service.store.SetVersionPublicationStatus(ctx, tx, target.VersionID, domain.PublicationPublished); err != nil {
		return document, false, err
	}

	activated, err := domain.ActivateVersion(document, target.VersionID, now)
	if err != nil {
		return document, false, err
	}
	if err := service.store.UpdateDocumentState(ctx, tx, activated); err != nil {
		return document, false, err
	}
	return activated, true, nil
}
