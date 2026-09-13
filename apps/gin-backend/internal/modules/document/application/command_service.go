// Package application 编排文档聚合的写事务，不依赖 HTTP、GORM 或检索传输层。
package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	domain "gin-backend/internal/modules/document/domain"

	"github.com/google/uuid"
)

const summaryMaxRunes = 100

var (
	ErrInvalidInput      = errors.New("document command: invalid input")
	ErrDocumentNotFound  = errors.New("document command: document not found")
	ErrDocumentForbidden = errors.New("document command: forbidden")
	ErrDocumentNotActive = errors.New("document command: document is not active")
)

type CreateCommand struct {
	OwnerID             int64
	Title               string
	Content             string
	AuthenticatedPublic bool
}

type UpdateCommand struct {
	OwnerID             int64
	DocumentID          string
	Title               string
	Content             string
	AuthenticatedPublic bool
}

type TrashCommand struct {
	OwnerID    int64
	DocumentID string
}

type MutationResult struct {
	Document *domain.Document
	Version  *domain.DocumentVersion
	Policy   *domain.AccessPolicy
}

type CommandService struct {
	repository domain.Repository
	newID      func() string
	now        func() time.Time
}

type Option func(*CommandService)

func WithIDGenerator(generator func() string) Option {
	return func(service *CommandService) {
		if generator != nil {
			service.newID = generator
		}
	}
}

func WithClock(clock func() time.Time) Option {
	return func(service *CommandService) {
		if clock != nil {
			service.now = clock
		}
	}
}

func NewCommandService(repository domain.Repository, options ...Option) (*CommandService, error) {
	if repository == nil {
		return nil, errors.New("document command: repository is nil")
	}
	service := &CommandService{
		repository: repository,
		newID:      uuid.NewString,
		now:        time.Now,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

func (service *CommandService) Create(ctx context.Context, command CreateCommand) (*MutationResult, error) {
	title, content, err := validateContentCommand(command.OwnerID, command.Title, command.Content)
	if err != nil {
		return nil, err
	}

	now := service.now().UTC()
	spaceID := service.newID()
	documentID := service.newID()
	versionID := service.newID()
	var result *MutationResult

	err = service.repository.InTransaction(ctx, func(repository domain.Repository) error {
		space := &domain.KnowledgeSpace{
			SpaceID: spaceID, OwnerID: command.OwnerID, SpaceType: domain.SpaceTypePrivate,
			Name: "Private space", CreatedAt: now, UpdatedAt: now,
		}
		created, err := repository.EnsurePrivateSpace(ctx, space)
		if err != nil {
			return err
		}
		if created {
			if err := repository.CreateSpaceMember(ctx, &domain.SpaceMember{
				SpaceID: space.SpaceID, UserID: command.OwnerID, MemberRole: domain.MemberRoleOwner,
				CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				return err
			}
		}

		document := &domain.Document{
			DocumentID: documentID, OwnerID: command.OwnerID, OwnerSpaceID: space.SpaceID,
			LifecycleStatus: domain.LifecycleActive, AccessRevision: 1,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := repository.CreateDocument(ctx, document); err != nil {
			return err
		}

		version := newVersion(versionID, documentID, 1, command.OwnerID, title, content, now)
		if err := repository.CreateDocumentVersion(ctx, version); err != nil {
			return err
		}
		if err := repository.UpdateVersionPublicationStatus(ctx, versionID, domain.PublicationPublished); err != nil {
			return err
		}
		version.PublicationStatus = domain.PublicationPublished
		if err := repository.SetActiveVersion(ctx, documentID, versionID, 1, 1); err != nil {
			return err
		}
		document.ActiveVersionID = stringPointer(versionID)
		document.ActivationRevision = 1
		document.AggregateRevision = 1

		policy := &domain.AccessPolicy{
			DocumentID: documentID, AuthenticatedPublic: command.AuthenticatedPublic,
			AccessRevision: 1, UpdatedAt: now,
		}
		if err := repository.PutAccessPolicy(ctx, policy); err != nil {
			return err
		}
		if err := repository.PutSearchProjection(ctx, newProjection(document, version, policy.AuthenticatedPublic, now)); err != nil {
			return err
		}

		result = &MutationResult{Document: document, Version: version, Policy: policy}
		return nil
	})
	if err != nil {
		return nil, translateRepositoryError("create document", err)
	}
	return result, nil
}

func (service *CommandService) Update(ctx context.Context, command UpdateCommand) (*MutationResult, error) {
	title, content, err := validateContentCommand(command.OwnerID, command.Title, command.Content)
	if err != nil {
		return nil, err
	}
	documentID, err := validateDocumentID(command.DocumentID)
	if err != nil {
		return nil, err
	}

	now := service.now().UTC()
	var result *MutationResult
	err = service.repository.InTransaction(ctx, func(repository domain.Repository) error {
		document, err := repository.LockDocument(ctx, documentID)
		if err != nil {
			return err
		}
		if document.OwnerID != command.OwnerID {
			return ErrDocumentForbidden
		}
		if document.LifecycleStatus != domain.LifecycleActive || document.ActiveVersionID == nil {
			return ErrDocumentNotActive
		}

		latest, err := repository.GetLatestDocumentVersion(ctx, documentID)
		if err != nil {
			return err
		}
		policy, err := repository.GetAccessPolicy(ctx, documentID)
		if err != nil {
			return err
		}

		versionID := service.newID()
		version := newVersion(versionID, documentID, latest.Revision+1, command.OwnerID, title, content, now)
		if err := repository.CreateDocumentVersion(ctx, version); err != nil {
			return err
		}
		if err := repository.UpdateVersionPublicationStatus(ctx, *document.ActiveVersionID, domain.PublicationSuperseded); err != nil {
			return err
		}
		if err := repository.UpdateVersionPublicationStatus(ctx, versionID, domain.PublicationPublished); err != nil {
			return err
		}
		version.PublicationStatus = domain.PublicationPublished

		document.AggregateRevision++
		document.ActivationRevision++
		if err := repository.SetActiveVersion(
			ctx, documentID, versionID, document.ActivationRevision, document.AggregateRevision,
		); err != nil {
			return err
		}
		document.ActiveVersionID = stringPointer(versionID)
		document.UpdatedAt = now

		if policy.AuthenticatedPublic != command.AuthenticatedPublic {
			policy.AuthenticatedPublic = command.AuthenticatedPublic
			policy.AccessRevision++
			policy.UpdatedAt = now
			document.AccessRevision = policy.AccessRevision
			document.AggregateRevision++
			if err := repository.SetAccessRevision(
				ctx, documentID, document.AccessRevision, document.AggregateRevision,
			); err != nil {
				return err
			}
			if err := repository.PutAccessPolicy(ctx, policy); err != nil {
				return err
			}
		}

		if err := repository.PutSearchProjection(ctx, newProjection(document, version, policy.AuthenticatedPublic, now)); err != nil {
			return err
		}
		result = &MutationResult{Document: document, Version: version, Policy: policy}
		return nil
	})
	if err != nil {
		return nil, translateRepositoryError("update document", err)
	}
	return result, nil
}

func (service *CommandService) Trash(ctx context.Context, command TrashCommand) error {
	if command.OwnerID <= 0 {
		return fmt.Errorf("%w: owner id must be positive", ErrInvalidInput)
	}
	documentID, err := validateDocumentID(command.DocumentID)
	if err != nil {
		return err
	}
	now := service.now().UTC()

	err = service.repository.InTransaction(ctx, func(repository domain.Repository) error {
		document, err := repository.LockDocument(ctx, documentID)
		if err != nil {
			return err
		}
		if document.OwnerID != command.OwnerID {
			return ErrDocumentForbidden
		}
		if document.LifecycleStatus != domain.LifecycleActive || document.ActiveVersionID == nil {
			return ErrDocumentNotActive
		}

		document.LifecycleStatus = domain.LifecycleTrashed
		document.LifecycleRevision++
		document.AggregateRevision++
		document.TrashedAt = &now
		document.UpdatedAt = now
		if err := repository.SetLifecycleState(
			ctx, documentID, document.LifecycleStatus, document.LifecycleRevision,
			document.AggregateRevision, document.TrashedAt,
		); err != nil {
			return err
		}
		if err := repository.DeleteSearchProjection(ctx, documentID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return translateRepositoryError("trash document", err)
	}
	return nil
}

func validateContentCommand(ownerID int64, title, content string) (string, string, error) {
	if ownerID <= 0 {
		return "", "", fmt.Errorf("%w: owner id must be positive", ErrInvalidInput)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return "", "", fmt.Errorf("%w: title is required", ErrInvalidInput)
	}
	if utf8.RuneCountInString(title) > 255 {
		return "", "", fmt.Errorf("%w: title exceeds 255 characters", ErrInvalidInput)
	}
	if strings.TrimSpace(content) == "" {
		return "", "", fmt.Errorf("%w: content is required", ErrInvalidInput)
	}
	return title, content, nil
}

func validateDocumentID(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	parsed, err := uuid.Parse(value)
	if err != nil {
		return "", fmt.Errorf("%w: document id must be a UUID", ErrInvalidInput)
	}
	return parsed.String(), nil
}

func newVersion(versionID, documentID string, revision, ownerID int64, title, content string, now time.Time) *domain.DocumentVersion {
	digest := sha256.Sum256([]byte(content))
	return &domain.DocumentVersion{
		VersionID: versionID, DocumentID: documentID, Revision: revision,
		PublicationStatus: domain.PublicationDraft,
		Title:             title, Summary: buildSummary(content), Content: content, ContentFormat: "markdown",
		ContentSHA256: hex.EncodeToString(digest[:]), CreatedBy: ownerID, CreatedAt: now,
	}
}

func newProjection(document *domain.Document, version *domain.DocumentVersion, public bool, now time.Time) *domain.SearchProjection {
	return &domain.SearchProjection{
		DocumentID: document.DocumentID, VersionID: version.VersionID,
		OwnerID: document.OwnerID, OwnerSpaceID: document.OwnerSpaceID,
		AuthenticatedPublic: public,
		Title:               version.Title, Summary: version.Summary,
		SearchText:         strings.Join([]string{version.Title, version.Summary, version.Content}, " "),
		ActivationRevision: document.ActivationRevision,
		AccessRevision:     document.AccessRevision, LifecycleRevision: document.LifecycleRevision,
		UpdatedAt: now,
	}
}

func buildSummary(content string) string {
	trimmed := strings.TrimSpace(content)
	runes := []rune(trimmed)
	if len(runes) > summaryMaxRunes {
		runes = runes[:summaryMaxRunes]
	}
	return string(runes)
}

func translateRepositoryError(operation string, err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("%s: %w", operation, ErrDocumentNotFound)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func stringPointer(value string) *string {
	return &value
}
