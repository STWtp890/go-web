package postgresql_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	domain "gin-backend/internal/modules/document/domain"
	repository "gin-backend/internal/modules/document/infrastructure/postgresql"

	"github.com/google/uuid"
)

func TestIndexDeliveryStoreIntegration(t *testing.T) {
	if os.Getenv("INDEX_DELIVERY_STORE_INTEGRATION") != "1" {
		t.Skip("INDEX_DELIVERY_STORE_INTEGRATION is not set")
	}
	db, store := openRepository(t)
	ctx := context.Background()
	ownerID := createUser(t, db, "p2-3-delivery")
	spaceID := uuid.NewString()
	documentID := uuid.NewString()
	versionID := uuid.NewString()
	content := "P2.3 durable index delivery"
	digest := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(digest[:])
	now := time.Now().UTC()

	if err := store.InTransaction(ctx, func(tx domain.Repository) error {
		if err := tx.CreateKnowledgeSpace(ctx, &domain.KnowledgeSpace{SpaceID: spaceID, OwnerID: ownerID, SpaceType: domain.SpaceTypePrivate, Name: "P2.3"}); err != nil {
			return err
		}
		if err := tx.CreateSpaceMember(ctx, &domain.SpaceMember{SpaceID: spaceID, UserID: ownerID, MemberRole: domain.MemberRoleOwner}); err != nil {
			return err
		}
		if err := tx.CreateDocument(ctx, &domain.Document{DocumentID: documentID, OwnerID: ownerID, OwnerSpaceID: spaceID, LifecycleStatus: domain.LifecycleActive, AccessRevision: 1}); err != nil {
			return err
		}
		if err := tx.CreateDocumentVersion(ctx, &domain.DocumentVersion{VersionID: versionID, DocumentID: documentID, Revision: 1, PublicationStatus: domain.PublicationDraft, Title: "P2.3", Content: content, ContentFormat: "markdown", ContentSHA256: hash, CreatedBy: ownerID}); err != nil {
			return err
		}
		if err := tx.UpdateVersionPublicationStatus(ctx, versionID, domain.PublicationPublished); err != nil {
			return err
		}
		if err := tx.SetActiveVersion(ctx, documentID, versionID, 1, 1); err != nil {
			return err
		}
		if err := tx.PutAccessPolicy(ctx, &domain.AccessPolicy{DocumentID: documentID, AccessRevision: 1}); err != nil {
			return err
		}
		for revision := int64(1); revision <= 3; revision++ {
			event := integrationDeliveryEvent(documentID, versionID, spaceID, hash, revision, now.Add(time.Duration(revision)*time.Millisecond))
			if err := tx.AppendIndexDeliveryEvent(ctx, event); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("prepare delivery events: %v", err)
	}

	first := claimOne(t, store, "worker-a")
	if first.AggregateRevision != 1 {
		t.Fatalf("first aggregate revision = %d", first.AggregateRevision)
	}
	if claimed, err := store.ClaimIndexDeliveryEvents(ctx, "worker-b", 10, time.Minute); err != nil || len(claimed) != 0 {
		t.Fatalf("second claim while first leased = %#v, %v", claimed, err)
	}
	if err := store.MarkIndexDeliverySucceeded(ctx, first.EventID, "worker-a", *first.LeaseToken); err != nil {
		t.Fatalf("complete first: %v", err)
	}

	second := claimOne(t, store, "worker-a")
	if second.AggregateRevision != 2 {
		t.Fatalf("second aggregate revision = %d", second.AggregateRevision)
	}
	if err := store.MarkIndexDeliverySucceeded(ctx, second.EventID, "worker-a", uuid.NewString()); !errors.Is(err, repository.ErrIndexDeliveryLeaseLost) {
		t.Fatalf("wrong-token completion error = %v", err)
	}
	if err := store.RescheduleIndexDelivery(ctx, second.EventID, "worker-a", *second.LeaseToken, time.Now().UTC().Add(-time.Second), "Unavailable", "offline"); err != nil {
		t.Fatalf("reschedule second: %v", err)
	}
	second = claimOne(t, store, "worker-b")
	if second.AttemptCount != 2 {
		t.Fatalf("second attempt count = %d, want 2", second.AttemptCount)
	}
	if err := store.MarkIndexDeliveryDeadLetter(ctx, second.EventID, "worker-b", *second.LeaseToken, "FailedPrecondition", "conflict"); err != nil {
		t.Fatalf("dead-letter second: %v", err)
	}
	if claimed, err := store.ClaimIndexDeliveryEvents(ctx, "worker-c", 10, time.Minute); err != nil || len(claimed) != 0 {
		t.Fatalf("dead letter should block later event: %#v, %v", claimed, err)
	}
	if err := store.RequeueIndexDelivery(ctx, second.EventID); err != nil {
		t.Fatalf("requeue second: %v", err)
	}
	second = claimOne(t, store, "worker-c")
	if err := store.MarkIndexDeliverySucceeded(ctx, second.EventID, "worker-c", *second.LeaseToken); err != nil {
		t.Fatalf("complete replayed second: %v", err)
	}

	third := claimOne(t, store, "worker-a")
	if err := db.Exec("UPDATE document_index_delivery_events SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE event_id = ?", third.EventID).Error; err != nil {
		t.Fatalf("expire third lease: %v", err)
	}
	takenOver := claimOne(t, store, "worker-b")
	if takenOver.EventID != third.EventID || takenOver.AttemptCount != 2 {
		t.Fatalf("takeover = %#v", takenOver)
	}
	if err := store.MarkIndexDeliverySucceeded(ctx, third.EventID, "worker-a", *third.LeaseToken); !errors.Is(err, repository.ErrIndexDeliveryLeaseLost) {
		t.Fatalf("stale worker completion error = %v", err)
	}
	if err := store.MarkIndexDeliverySucceeded(ctx, takenOver.EventID, "worker-b", *takenOver.LeaseToken); err != nil {
		t.Fatalf("complete takeover: %v", err)
	}
	stats, err := store.GetIndexDeliveryStats(ctx)
	if err != nil {
		t.Fatalf("get delivery stats: %v", err)
	}
	if stats.Pending != 0 || stats.Processing != 0 || stats.Retry != 0 || stats.DeadLetter != 0 || stats.Succeeded != 3 {
		t.Fatalf("delivery stats = %#v", stats)
	}
	if stats.ExpiredLeases != 0 || stats.FailedAttempts != 3 || stats.OldestUnfinishedAt != nil || stats.LastDeliveredAt == nil {
		t.Fatalf("delivery observability stats = %#v", stats)
	}
}

func integrationDeliveryEvent(documentID, versionID, spaceID, hash string, revision int64, now time.Time) *domain.IndexDeliveryEvent {
	return &domain.IndexDeliveryEvent{
		EventID: uuid.NewString(), DedupeKey: "integration:" + documentID + ":" + string(rune('0'+revision)),
		Source: domain.IndexDeliverySourceTransaction, DocumentID: documentID, AggregateRevision: revision,
		Kind: domain.IndexDeliverySyncDocument, VersionID: &versionID, OwnerSpaceID: spaceID,
		ActivationRevision: revision, AccessRevision: 1, ContentSHA256: hash,
		GrantedSpaceIDs: []string{}, IndexProfile: domain.DefaultIndexProfile,
		State: domain.IndexDeliveryPending, AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}
}

func claimOne(t *testing.T, store *repository.Repository, workerID string) *domain.IndexDeliveryEvent {
	t.Helper()
	events, err := store.ClaimIndexDeliveryEvents(context.Background(), workerID, 10, time.Minute)
	if err != nil {
		t.Fatalf("claim for %s: %v", workerID, err)
	}
	if len(events) != 1 {
		t.Fatalf("claim for %s returned %d events, want 1", workerID, len(events))
	}
	return events[0]
}
