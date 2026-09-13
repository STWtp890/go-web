package application_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	command "gin-backend/internal/modules/document/application"
	domain "gin-backend/internal/modules/document/domain"
	repository "gin-backend/internal/modules/document/infrastructure/postgresql"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCommandServiceIntegration(t *testing.T) {
	db, store := openCommandRepository(t)
	ctx := context.Background()

	t.Run("create update and trash preserve aggregate invariants", func(t *testing.T) {
		ownerID := createCommandUser(t, db, "lifecycle")
		service := newCommandService(t, store)

		created, err := service.Create(ctx, command.CreateCommand{
			OwnerID: ownerID, Title: " First title ", Content: "first content",
		})
		if err != nil {
			t.Fatalf("create document: %v", err)
		}
		if created.Version.Revision != 1 || created.Version.PublicationStatus != domain.PublicationPublished {
			t.Fatalf("created version = %#v, want published revision 1", created.Version)
		}
		assertDocumentRevisions(t, created.Document, 1, 1, 0, 1)
		assertScopedCount(t, db, "knowledge_spaces", "space_id = ?", created.Document.OwnerSpaceID, 1)
		assertScopedCount(t, db, "space_members", "space_id = ?", created.Document.OwnerSpaceID, 1)
		assertDocumentCounts(t, db, created.Document.DocumentID, 1, 1)

		firstVersionID := created.Version.VersionID
		updated, err := service.Update(ctx, command.UpdateCommand{
			OwnerID: ownerID, DocumentID: created.Document.DocumentID,
			Title: "Second title", Content: "second content", AuthenticatedPublic: true,
		})
		if err != nil {
			t.Fatalf("update document and access: %v", err)
		}
		if updated.Version.Revision != 2 || !updated.Policy.AuthenticatedPublic {
			t.Fatalf("updated result = %#v, want public revision 2", updated)
		}
		assertDocumentRevisions(t, updated.Document, 2, 2, 0, 3)
		assertVersionStatus(t, db, firstVersionID, domain.PublicationSuperseded)
		assertVersionStatus(t, db, updated.Version.VersionID, domain.PublicationPublished)

		third, err := service.Update(ctx, command.UpdateCommand{
			OwnerID: ownerID, DocumentID: created.Document.DocumentID,
			Title: "Third title", Content: "third content", AuthenticatedPublic: true,
		})
		if err != nil {
			t.Fatalf("update document without access change: %v", err)
		}
		assertDocumentRevisions(t, third.Document, 3, 2, 0, 4)
		assertDocumentCounts(t, db, created.Document.DocumentID, 3, 1)

		if err := service.Trash(ctx, command.TrashCommand{
			OwnerID: ownerID, DocumentID: created.Document.DocumentID,
		}); err != nil {
			t.Fatalf("trash document: %v", err)
		}
		stored, err := store.GetDocument(ctx, created.Document.DocumentID)
		if err != nil {
			t.Fatalf("get trashed document: %v", err)
		}
		if stored.LifecycleStatus != domain.LifecycleTrashed || stored.TrashedAt == nil {
			t.Fatalf("trashed document = %#v", stored)
		}
		assertDocumentRevisions(t, stored, 3, 2, 1, 5)
		assertDocumentCounts(t, db, stored.DocumentID, 3, 0)
		if err := service.Trash(ctx, command.TrashCommand{OwnerID: ownerID, DocumentID: stored.DocumentID}); !errors.Is(err, command.ErrDocumentNotActive) {
			t.Fatalf("second trash error = %v, want ErrDocumentNotActive", err)
		}
	})

	t.Run("concurrent updates allocate distinct revisions", func(t *testing.T) {
		ownerID := createCommandUser(t, db, "concurrent")
		service := newCommandService(t, store)
		created, err := service.Create(ctx, command.CreateCommand{
			OwnerID: ownerID, Title: "Concurrent", Content: "revision one",
		})
		if err != nil {
			t.Fatalf("create concurrent document: %v", err)
		}

		start := make(chan struct{})
		errorsCh := make(chan error, 2)
		var wait sync.WaitGroup
		for index := 0; index < 2; index++ {
			index := index
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				_, updateErr := service.Update(ctx, command.UpdateCommand{
					OwnerID: ownerID, DocumentID: created.Document.DocumentID,
					Title:   fmt.Sprintf("Concurrent %d", index+2),
					Content: fmt.Sprintf("revision %d", index+2),
				})
				errorsCh <- updateErr
			}()
		}
		close(start)
		wait.Wait()
		close(errorsCh)
		for updateErr := range errorsCh {
			if updateErr != nil {
				t.Fatalf("concurrent update: %v", updateErr)
			}
		}

		stored, err := store.GetDocument(ctx, created.Document.DocumentID)
		if err != nil {
			t.Fatalf("get concurrent document: %v", err)
		}
		assertDocumentRevisions(t, stored, 3, 1, 0, 3)
		versions, err := store.ListDocumentVersions(ctx, stored.DocumentID)
		if err != nil {
			t.Fatalf("list concurrent versions: %v", err)
		}
		if len(versions) != 3 || versions[0].Revision != 1 || versions[1].Revision != 2 || versions[2].Revision != 3 {
			t.Fatalf("concurrent revisions = %#v, want 1,2,3", versions)
		}
	})

	t.Run("multiple documents reuse one private space", func(t *testing.T) {
		ownerID := createCommandUser(t, db, "private-space")
		service := newCommandService(t, store)
		first, err := service.Create(ctx, command.CreateCommand{OwnerID: ownerID, Title: "First", Content: "first"})
		if err != nil {
			t.Fatalf("create first private document: %v", err)
		}
		second, err := service.Create(ctx, command.CreateCommand{OwnerID: ownerID, Title: "Second", Content: "second"})
		if err != nil {
			t.Fatalf("create second private document: %v", err)
		}
		if first.Document.OwnerSpaceID != second.Document.OwnerSpaceID {
			t.Fatalf("private spaces differ: %s and %s", first.Document.OwnerSpaceID, second.Document.OwnerSpaceID)
		}
		assertScopedCount(t, db, "knowledge_spaces", "owner_id = ?", ownerID, 1)
		assertScopedCount(t, db, "space_members", "user_id = ?", ownerID, 1)
	})
}

func newCommandService(t *testing.T, store domain.Repository) *command.CommandService {
	t.Helper()
	service, err := command.NewCommandService(store)
	if err != nil {
		t.Fatalf("new command service: %v", err)
	}
	return service
}

func openCommandRepository(t *testing.T) (*gorm.DB, *repository.Repository) {
	t.Helper()
	dsn := os.Getenv("DOCUMENT_REPOSITORY_TEST_DSN")
	if dsn == "" {
		t.Skip("DOCUMENT_REPOSITORY_TEST_DSN is not set")
	}
	db, err := gorm.Open(pgdriver.Open(dsn), &gorm.Config{
		SkipDefaultTransaction: true,
		Logger:                 logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	store, err := repository.New(db)
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	return db, store
}

func createCommandUser(t *testing.T, db *gorm.DB, label string) int64 {
	t.Helper()
	var id int64
	email := fmt.Sprintf("p1.2-%s-%d@example.test", label, time.Now().UnixNano())
	if err := db.Raw(`INSERT INTO users (email, password, nickname, created_at, updated_at)
		VALUES (?, 'test-only-hash', ?, 1700000000, 1700000000) RETURNING id`, email, label).Scan(&id).Error; err != nil {
		t.Fatalf("create command user %s: %v", label, err)
	}
	return id
}

func assertDocumentRevisions(t *testing.T, document *domain.Document, activation, access, lifecycle, aggregate int64) {
	t.Helper()
	if document.ActivationRevision != activation || document.AccessRevision != access ||
		document.LifecycleRevision != lifecycle || document.AggregateRevision != aggregate {
		t.Fatalf("document revisions = (%d,%d,%d,%d), want (%d,%d,%d,%d)",
			document.ActivationRevision, document.AccessRevision, document.LifecycleRevision, document.AggregateRevision,
			activation, access, lifecycle, aggregate)
	}
}

func assertDocumentCounts(t *testing.T, db *gorm.DB, documentID string, versions, projections int64) {
	t.Helper()
	assertScopedCount(t, db, "document_versions", "document_id = ?", documentID, versions)
	assertScopedCount(t, db, "document_search_projection", "document_id = ?", documentID, projections)
}

func assertScopedCount(t *testing.T, db *gorm.DB, table, condition string, value any, want int64) {
	t.Helper()
	var count int64
	if err := db.Table(table).Where(condition, value).Count(&count).Error; err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != want {
		t.Fatalf("%s count = %d, want %d", table, count, want)
	}
}

func assertVersionStatus(t *testing.T, db *gorm.DB, versionID string, want domain.PublicationStatus) {
	t.Helper()
	var status string
	if err := db.Table("document_versions").Select("publication_status").Where("version_id = ?", versionID).Scan(&status).Error; err != nil {
		t.Fatalf("read version status: %v", err)
	}
	if status != string(want) {
		t.Fatalf("version %s status = %s, want %s", versionID, status, want)
	}
}
