package application

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"document-service/internal/domain"
	"document-service/internal/infrastructure/postgres"

	documentv1 "packages/gen/document/v1"
	"packages/serviceauth"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
)

// Page size bounds of ListDocuments. The contract caps a page at 100; the default
// keeps an unfiltered call small.
const (
	defaultDocumentPageSize = 20
	maxDocumentPageSize     = 100
)

// privateSpaceName is the name of a lazily created private space. A subject has at
// most one, enforced by a partial unique index.
const privateSpaceName = "Private space"

// privateSpaceAuditReason is recorded when the lazy private space is created.
const privateSpaceAuditReason = "private space created with the subject's first document"

// getDocument implements the GetDocument use case: a detail read gated by the
// resource decision.
func (service *Service) getDocument(ctx context.Context, principal *serviceauth.Principal, request *documentv1.GetDocumentRequest) (*documentv1.GetDocumentResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: a get request is required", domain.ErrInvalidInput)
	}
	caller, err := service.verifyCaller(principal, "")
	if err != nil {
		return nil, err
	}
	documentID, err := domain.ValidateUUID(request.GetDocumentId(), "document id")
	if err != nil {
		return nil, err
	}

	var detail *documentv1.DocumentDetail
	err = service.store.InReadOnly(ctx, func(ctx context.Context, tx pgx.Tx) error {
		facts, err := service.store.LoadAccessFacts(ctx, tx, documentID, caller.Subject.Key)
		if err != nil {
			return err
		}
		if err := facts.Validate(); err != nil {
			return err
		}
		if !domain.MayReadDocument(facts) {
			// The document exists, so the caller is told it may not read it. It is
			// never disguised as NOT_FOUND: the contract requires the difference to
			// stay visible, and a fake NOT_FOUND would hide an authorization bug.
			return fmt.Errorf("%w: subject %s may not read document %s",
				domain.ErrForbidden, caller.Subject.Key, documentID)
		}
		detail, err = service.loadDetail(ctx, tx, domain.DocumentFromFacts(facts))
		return err
	})
	if err != nil {
		return nil, err
	}
	return &documentv1.GetDocumentResponse{Document: detail}, nil
}

// listDocuments implements the ListDocuments use case.
func (service *Service) listDocuments(ctx context.Context, principal *serviceauth.Principal, request *documentv1.ListDocumentsRequest) (*documentv1.ListDocumentsResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: a list request is required", domain.ErrInvalidInput)
	}
	caller, err := service.verifyCaller(principal, "")
	if err != nil {
		return nil, err
	}
	ownerFilter := strings.TrimSpace(request.GetOwnerSubjectKey())
	if ownerFilter != "" {
		owner, err := domain.ParseSubjectKey(ownerFilter)
		if err != nil {
			return nil, err
		}
		ownerFilter = owner.Key
	}
	spaceFilter := strings.TrimSpace(request.GetSpaceId())
	if spaceFilter != "" {
		if spaceFilter, err = domain.ValidateUUID(spaceFilter, "space id"); err != nil {
			return nil, err
		}
	}
	lifecycleFilter, err := lifecycleFromProto(request.GetLifecycleStatus())
	if err != nil {
		return nil, err
	}
	pageSize := int(request.GetPageSize())
	if pageSize <= 0 {
		pageSize = defaultDocumentPageSize
	}
	if pageSize > maxDocumentPageSize {
		pageSize = maxDocumentPageSize
	}
	cursorTime, cursorDocumentID, err := decodePageToken(request.GetPageToken())
	if err != nil {
		return nil, err
	}

	var summaries []domain.Summary
	var totalCount int64
	err = service.store.InReadOnly(ctx, func(ctx context.Context, tx pgx.Tx) error {
		filter := postgres.DocumentFilter{
			SubjectKey:              caller.Subject.Key,
			OwnerSubjectKey:         ownerFilter,
			SpaceID:                 spaceFilter,
			LifecycleStatus:         lifecycleFilter,
			AuthenticatedPublicOnly: request.GetAuthenticatedPublicOnly(),
			CursorTime:              cursorTime,
			CursorDocumentID:        cursorDocumentID,
			Limit:                   pageSize,
		}
		var err error
		summaries, err = service.store.ListReadableDocuments(ctx, tx, filter)
		if err != nil {
			return err
		}
		// The total runs the same predicate as the page, without the cursor and
		// the limit, so a caller can render an honest page count instead of
		// inferring one from the page size.
		totalCount, err = service.store.CountReadableDocuments(ctx, tx, filter)
		return err
	})
	if err != nil {
		return nil, err
	}

	documents := make([]*documentv1.DocumentSummary, 0, len(summaries))
	for _, summary := range summaries {
		documents = append(documents, summaryProto(summary))
	}
	response := &documentv1.ListDocumentsResponse{Documents: documents, TotalCount: totalCount}
	if len(summaries) == pageSize {
		last := summaries[len(summaries)-1]
		response.NextPageToken = encodePageToken(last.CreatedAt, last.DocumentID)
	}
	return response, nil
}

// requireOwner is the ownership gate of every document mutation.
func requireOwner(document domain.Document, subjectKey string) error {
	if document.OwnerSubjectKey != subjectKey {
		return fmt.Errorf("%w: document %s is owned by another subject", domain.ErrForbidden, document.DocumentID)
	}
	return nil
}

// ensurePrivateSpace lazily creates the subject's private space together with its
// owner membership. Both rows must exist before the transaction commits: a
// deferred constraint trigger refuses a space whose owner is not an active owner
// member.
func (service *Service) ensurePrivateSpace(ctx context.Context, tx pgx.Tx, caller callerIdentity, now time.Time) (domain.Space, error) {
	if err := service.store.LockPrivateSpaceOwner(ctx, tx, caller.Subject.Key); err != nil {
		return domain.Space{}, err
	}
	space, found, err := service.store.FindPrivateSpace(ctx, tx, caller.Subject.Key)
	if err != nil {
		return domain.Space{}, err
	}
	if found {
		return space, nil
	}

	created := domain.Space{
		SpaceID:         service.newID(),
		OwnerSubjectKey: caller.Subject.Key,
		SpaceType:       domain.SpaceTypePrivate,
		Name:            privateSpaceName,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := service.store.InsertSpace(ctx, tx, created); err != nil {
		return domain.Space{}, err
	}
	if err := service.store.InsertMember(ctx, tx, domain.Member{
		SpaceID:    created.SpaceID,
		SubjectKey: caller.Subject.Key,
		MemberRole: domain.RoleOwner,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		return domain.Space{}, err
	}
	if err := service.appendAudit(ctx, tx, domain.AuditEvent{
		SubjectType: domain.AuditSubjectTypeSpace,
		Action:      domain.AuditActionCreate,
		SubjectKey:  caller.Subject.Key,
		SpaceID:     created.SpaceID,
		NewTarget:   "space:" + created.SpaceID,
		Actor:       caller.Actor,
		Source:      caller.Source,
		Reason:      privateSpaceAuditReason,
		RequestID:   caller.RequestID,
	}); err != nil {
		return domain.Space{}, err
	}
	return created, nil
}

// appendLifecycleEvent writes the delete event an archive or trash transition
// produces.
func (service *Service) appendLifecycleEvent(ctx context.Context, tx pgx.Tx, document domain.Document, now time.Time) error {
	policy, hasPolicy, err := service.store.GetAccessPolicy(ctx, tx, document.DocumentID)
	if err != nil {
		return err
	}
	if !hasPolicy {
		policy = domain.AccessPolicy{DocumentID: document.DocumentID, AccessRevision: document.AccessRevision, UpdatedAt: now}
	}
	var version *domain.Version
	if document.ActiveVersionID != "" {
		if version, err = service.currentVersion(ctx, tx, document); err != nil {
			return err
		}
	}
	return service.appendDocumentEvent(ctx, tx, domain.EventKindDelete, document, version, policy, now)
}

// appendDocumentEvent renders and appends one Outbox row in the caller's
// transaction. Business change and event commit together, so a document write is
// never conditional on the search service being reachable.
func (service *Service) appendDocumentEvent(ctx context.Context, tx pgx.Tx, kind string, document domain.Document, version *domain.Version, policy domain.AccessPolicy, now time.Time) error {
	sequence, err := service.store.NextEventSequence(ctx, tx)
	if err != nil {
		return err
	}
	grantedSpaceIDs, err := service.store.ListGrantedSpaceIDs(ctx, tx, document.DocumentID)
	if err != nil {
		return err
	}
	source, hasSource, err := service.store.GetDocumentSource(ctx, tx, document.DocumentID)
	if err != nil {
		return err
	}

	eventID := service.newID()
	input := envelopeInput{
		Sequence:        sequence,
		EventID:         eventID,
		Kind:            kind,
		Document:        document,
		Version:         version,
		Policy:          policy,
		GrantedSpaceIDs: grantedSpaceIDs,
		OccurredAt:      now,
	}
	if hasSource {
		input.Source = &source
	}
	payload, err := proto.Marshal(buildEnvelope(input))
	if err != nil {
		return fmt.Errorf("document-service: encode document event: %w", err)
	}
	return service.store.AppendEvent(ctx, tx, domain.Event{
		Sequence:          sequence,
		EventID:           eventID,
		DocumentID:        document.DocumentID,
		EventKind:         kind,
		AggregateRevision: document.AggregateRevision,
		Payload:           payload,
		OccurredAt:        now,
	}, dedupeKey(document.DocumentID, kind, document.AggregateRevision))
}

// currentVersion returns the active version, or the newest version when nothing is
// active. It returns nil for a document with no version at all.
func (service *Service) currentVersion(ctx context.Context, tx pgx.Tx, document domain.Document) (*domain.Version, error) {
	if document.ActiveVersionID != "" {
		version, err := service.store.GetVersion(ctx, tx, document.DocumentID, document.ActiveVersionID)
		if err != nil {
			return nil, err
		}
		return &version, nil
	}
	version, found, err := service.store.GetLatestVersion(ctx, tx, document.DocumentID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return &version, nil
}

// loadDetail assembles the DocumentDetail response from the committed rows.
func (service *Service) loadDetail(ctx context.Context, tx pgx.Tx, document domain.Document) (*documentv1.DocumentDetail, error) {
	version, err := service.currentVersion(ctx, tx, document)
	if err != nil {
		return nil, err
	}
	policy, hasPolicy, err := service.store.GetAccessPolicy(ctx, tx, document.DocumentID)
	if err != nil {
		return nil, err
	}
	if !hasPolicy {
		policy = domain.AccessPolicy{
			DocumentID:     document.DocumentID,
			AccessRevision: document.AccessRevision,
			UpdatedAt:      document.UpdatedAt,
		}
	}
	grantedSpaceIDs, err := service.store.ListGrantedSpaceIDs(ctx, tx, document.DocumentID)
	if err != nil {
		return nil, err
	}
	source, hasSource, err := service.store.GetDocumentSource(ctx, tx, document.DocumentID)
	if err != nil {
		return nil, err
	}

	detail := &documentv1.DocumentDetail{
		Summary:             documentSummary(document, version, policy),
		AuthenticatedPublic: policy.AuthenticatedPublic,
		GrantedSpaceIds:     grantedSpaceIDs,
	}
	if version != nil {
		detail.Version = versionProto(*version)
	}
	if hasSource {
		detail.Source = sourceProto(source)
	}
	return detail, nil
}

// pageToken encodes the listing cursor. Callers treat it as opaque.
func encodePageToken(createdAt time.Time, documentID string) string {
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "|" + documentID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodePageToken parses an opaque cursor. A malformed token is a caller error
// rather than "start from the beginning": silently replaying from the first page
// would be indistinguishable from a lost page.
func decodePageToken(token string) (time.Time, string, error) {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return time.Time{}, "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(trimmed)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: page token is not valid base64url", domain.ErrInvalidInput)
	}
	parts := strings.SplitN(string(decoded), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("%w: page token is malformed", domain.ErrInvalidInput)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: page token carries an invalid timestamp", domain.ErrInvalidInput)
	}
	documentID, err := domain.ValidateUUID(parts[1], "page token document id")
	if err != nil {
		return time.Time{}, "", err
	}
	return createdAt, documentID, nil
}
