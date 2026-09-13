package postgresql_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	domain "gin-backend/internal/modules/document/domain"
	repository "gin-backend/internal/modules/document/infrastructure/postgresql"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	spaceID       = "018f3f0e-7b20-7000-8000-000000000101"
	documentID    = "018f3f0e-7b20-7000-8000-000000000102"
	versionID     = "018f3f0e-7b20-7000-8000-000000000103"
	grantID       = "018f3f0e-7b20-7000-8000-000000000104"
	secondSpaceID = "018f3f0e-7b20-7000-8000-000000000106"
	secondDocID   = "018f3f0e-7b20-7000-8000-000000000107"
)

func TestRepositoryIntegration(t *testing.T) {
	db, store := openRepository(t)
	ctx := context.Background()
	ownerID := createUser(t, db, "owner")
	readerID := createUser(t, db, "reader")

	t.Run("persists complete aggregate in one transaction", func(t *testing.T) {
		content := "P1.1 repository integration content"
		digest := sha256.Sum256([]byte(content))
		err := store.InTransaction(ctx, func(tx domain.Repository) error {
			if err := tx.CreateKnowledgeSpace(ctx, &domain.KnowledgeSpace{
				SpaceID: spaceID, OwnerID: ownerID, SpaceType: domain.SpaceTypePrivate, Name: "Owner private space",
			}); err != nil {
				return err
			}
			if err := tx.CreateSpaceMember(ctx, &domain.SpaceMember{
				SpaceID: spaceID, UserID: ownerID, MemberRole: domain.MemberRoleOwner,
			}); err != nil {
				return err
			}
			if err := tx.CreateDocument(ctx, &domain.Document{
				DocumentID: documentID, OwnerID: ownerID, OwnerSpaceID: spaceID,
				LifecycleStatus: domain.LifecycleActive, AccessRevision: 1,
			}); err != nil {
				return err
			}
			if err := tx.CreateDocumentVersion(ctx, &domain.DocumentVersion{
				VersionID: versionID, DocumentID: documentID, Revision: 1,
				PublicationStatus: domain.PublicationDraft, Title: "P1.1", Summary: "integration",
				Content: content, ContentFormat: "markdown", ContentSHA256: hex.EncodeToString(digest[:]), CreatedBy: ownerID,
			}); err != nil {
				return err
			}
			if err := tx.UpdateVersionPublicationStatus(ctx, versionID, domain.PublicationPublished); err != nil {
				return err
			}
			if err := tx.SetActiveVersion(ctx, documentID, versionID, 1, 1); err != nil {
				return err
			}
			if err := tx.PutAccessPolicy(ctx, &domain.AccessPolicy{
				DocumentID: documentID, AuthenticatedPublic: false, AccessRevision: 1,
			}); err != nil {
				return err
			}
			if err := tx.CreateGrant(ctx, &domain.DocumentGrant{
				GrantID: grantID, DocumentID: documentID, SubjectType: domain.GrantSubjectUser,
				GranteeUserID: &readerID, AccessRevision: 1,
			}); err != nil {
				return err
			}
			if err := tx.PutSearchProjection(ctx, &domain.SearchProjection{
				DocumentID: documentID, VersionID: versionID, OwnerID: ownerID, OwnerSpaceID: spaceID,
				Title: "P1.1", Summary: "integration", SearchText: "P1.1 integration content",
				ActivationRevision: 1, AccessRevision: 1,
			}); err != nil {
				return err
			}
			locked, err := tx.LockDocument(ctx, documentID)
			if err != nil {
				return err
			}
			if locked.AggregateRevision != 1 {
				return fmt.Errorf("aggregate revision = %d, want 1", locked.AggregateRevision)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("persist aggregate: %v", err)
		}

		stored, err := store.GetDocument(ctx, documentID)
		if err != nil {
			t.Fatalf("get document: %v", err)
		}
		if stored.ActiveVersionID == nil || *stored.ActiveVersionID != versionID {
			t.Fatalf("active version = %v, want %s", stored.ActiveVersionID, versionID)
		}
		versions, err := store.ListDocumentVersions(ctx, documentID)
		if err != nil {
			t.Fatalf("list versions: %v", err)
		}
		if len(versions) != 1 || versions[0].PublicationStatus != domain.PublicationPublished {
			t.Fatalf("versions = %#v, want one published version", versions)
		}
		assertRepositoryCount(t, db, "document_access_policies", "document_id = ?", documentID, 1)
		assertRepositoryCount(t, db, "document_grants", "document_id = ?", documentID, 1)
		assertRepositoryCount(t, db, "document_search_projection", "document_id = ?", documentID, 1)
	})

	t.Run("rejects duplicate document revision", func(t *testing.T) {
		digest := sha256.Sum256([]byte("duplicate"))
		err := store.CreateDocumentVersion(ctx, &domain.DocumentVersion{
			VersionID: "018f3f0e-7b20-7000-8000-000000000108", DocumentID: documentID, Revision: 1,
			PublicationStatus: domain.PublicationDraft, Title: "duplicate", Content: "duplicate",
			ContentFormat: "markdown", ContentSHA256: hex.EncodeToString(digest[:]), CreatedBy: ownerID,
		})
		if err == nil {
			t.Fatal("duplicate revision was accepted")
		}
	})

	t.Run("rejects active version from another document", func(t *testing.T) {
		if err := store.CreateDocument(ctx, &domain.Document{
			DocumentID: secondDocID, OwnerID: ownerID, OwnerSpaceID: spaceID, LifecycleStatus: domain.LifecycleActive,
		}); err != nil {
			t.Fatalf("create second document: %v", err)
		}
		err := store.InTransaction(ctx, func(tx domain.Repository) error {
			return tx.SetActiveVersion(ctx, secondDocID, versionID, 1, 1)
		})
		if err == nil {
			t.Fatal("cross-document active version was accepted")
		}
	})

	t.Run("rejects non-owner member in private space and rolls back", func(t *testing.T) {
		err := store.InTransaction(ctx, func(tx domain.Repository) error {
			if err := tx.CreateKnowledgeSpace(ctx, &domain.KnowledgeSpace{
				SpaceID: secondSpaceID, OwnerID: ownerID, SpaceType: domain.SpaceTypePrivate, Name: "Invalid private space",
			}); err != nil {
				return err
			}
			if err := tx.CreateSpaceMember(ctx, &domain.SpaceMember{
				SpaceID: secondSpaceID, UserID: ownerID, MemberRole: domain.MemberRoleOwner,
			}); err != nil {
				return err
			}
			if err := tx.CreateSpaceMember(ctx, &domain.SpaceMember{
				SpaceID: secondSpaceID, UserID: readerID, MemberRole: domain.MemberRoleMember,
			}); err != nil {
				return err
			}
			return nil
		})
		if err == nil {
			t.Fatal("invalid private-space member was accepted")
		}
		var count int64
		if err := db.Table("knowledge_spaces").Where("space_id = ?", secondSpaceID).Count(&count).Error; err != nil {
			t.Fatalf("count rolled-back space: %v", err)
		}
		if count != 0 {
			t.Fatalf("rolled-back space count = %d, want 0", count)
		}
	})

	t.Run("rejects mutation of immutable version content", func(t *testing.T) {
		err := db.Exec("UPDATE document_versions SET content = ? WHERE version_id = ?", "mutated", versionID).Error
		if err == nil {
			t.Fatal("immutable version content update was accepted")
		}
	})
}

func openRepository(t *testing.T) (*gorm.DB, *repository.Repository) {
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

func createUser(t *testing.T, db *gorm.DB, role string) int64 {
	t.Helper()
	email := fmt.Sprintf("p1.1-%s-%d@example.test", role, time.Now().UnixNano())
	var id int64
	err := db.Raw(`INSERT INTO users (email, password, nickname, created_at, updated_at)
		VALUES (?, 'test-only-hash', ?, 1700000000, 1700000000) RETURNING id`, email, role).Scan(&id).Error
	if err != nil {
		t.Fatalf("create %s user: %v", role, err)
	}
	return id
}

func assertRepositoryCount(t *testing.T, db *gorm.DB, table, condition string, value any, want int64) {
	t.Helper()
	var count int64
	if err := db.Table(table).Where(condition, value).Count(&count).Error; err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != want {
		t.Fatalf("%s count = %d, want %d", table, count, want)
	}
}
