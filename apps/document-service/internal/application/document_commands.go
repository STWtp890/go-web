package application

import (
	"context"
	"fmt"

	"document-service/internal/domain"

	documentv1 "packages/gen/document/v1"
	"packages/serviceauth"

	"github.com/jackc/pgx/v5"
)

// createDocument implements the CreateDocument use case.
func (service *Service) createDocument(ctx context.Context, principal *serviceauth.Principal, request *documentv1.CreateDocumentRequest) (*documentv1.CreateDocumentResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: a create request is required", domain.ErrInvalidInput)
	}
	caller, err := service.verifyCaller(principal, request.GetRequestId())
	if err != nil {
		return nil, err
	}
	title, err := domain.ValidateTitle(request.GetTitle())
	if err != nil {
		return nil, err
	}
	content, err := domain.ValidateContent(request.GetContent())
	if err != nil {
		return nil, err
	}
	format, err := domain.NormalizeContentFormat(request.GetContentFormat())
	if err != nil {
		return nil, err
	}
	source, err := sourceFromProto(request.GetSource())
	if err != nil {
		return nil, err
	}

	now := service.now().UTC()
	documentID := service.newID()
	versionID := service.newID()

	var detail *documentv1.DocumentDetail
	err = service.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := service.ensureSubject(ctx, tx, caller.Subject, caller); err != nil {
			return err
		}

		// Idempotent creation: the same subject reusing a non-empty request id
		// gets the document it already created. The unique index on
		// (owner_subject_key, create_request_id) is the last line of defence for
		// two concurrent commands carrying the same id.
		if caller.RequestID != "" {
			existing, found, err := service.store.FindDocumentByCreateRequest(ctx, tx, caller.Subject.Key, caller.RequestID)
			if err != nil {
				return err
			}
			if found {
				detail, err = service.loadDetail(ctx, tx, existing)
				return err
			}
		}

		space, err := service.ensurePrivateSpace(ctx, tx, caller, now)
		if err != nil {
			return err
		}

		document := domain.CreateDocumentState(documentID, caller.Subject.Key, space.SpaceID, now)
		document.CreateRequestID = caller.RequestID
		if err := service.store.InsertDocument(ctx, tx, document); err != nil {
			return err
		}

		version := domain.Version{
			VersionID:           versionID,
			DocumentID:          documentID,
			Revision:            1,
			PublicationStatus:   domain.PublicationPublished,
			Title:               title,
			Summary:             domain.BuildSummary(content),
			Content:             content,
			ContentFormat:       format,
			ContentSHA256:       contentDigest(content),
			CreatedBySubjectKey: caller.Subject.Key,
			CreatedAt:           now,
		}
		if err := service.store.InsertVersion(ctx, tx, version); err != nil {
			return err
		}

		// The first version is published and active from the start. The created
		// state already carries activation_revision = 1, so nothing is bumped here:
		// attaching the version is part of creation, not a second activation.
		document.ActiveVersionID = versionID
		if err := service.store.UpdateDocumentState(ctx, tx, document); err != nil {
			return err
		}

		policy := domain.AccessPolicy{
			DocumentID:          documentID,
			AuthenticatedPublic: request.GetAuthenticatedPublic(),
			AccessRevision:      1,
			UpdatedAt:           now,
		}
		if err := service.store.PutAccessPolicy(ctx, tx, policy); err != nil {
			return err
		}
		if source != nil {
			if err := service.store.PutDocumentSource(ctx, tx, documentID, *source, now); err != nil {
				return err
			}
		}
		if err := service.appendDocumentEvent(ctx, tx, domain.EventKindUpsert, document, &version, policy, now); err != nil {
			return err
		}

		detail, err = service.loadDetail(ctx, tx, document)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &documentv1.CreateDocumentResponse{Document: detail}, nil
}

// updateDraft implements the UpdateDraft use case: append a draft version without
// touching the active one.
func (service *Service) updateDraft(ctx context.Context, principal *serviceauth.Principal, request *documentv1.UpdateDraftRequest) (*documentv1.UpdateDraftResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: an update request is required", domain.ErrInvalidInput)
	}
	caller, err := service.verifyCaller(principal, request.GetRequestId())
	if err != nil {
		return nil, err
	}
	// A draft edit never changes the access policy, and the field is an optional
	// bool precisely so the service can tell "omitted" from "explicitly false".
	// A caller that asks this command for an access change is therefore refused
	// instead of silently ignored: access changes belong to SaveDocument, which
	// states explicitly whether a policy change is being requested.
	if request.AuthenticatedPublic != nil {
		return nil, fmt.Errorf("%w: UpdateDraft never changes the access policy; request an authenticated_public change with SaveDocument",
			domain.ErrInvalidInput)
	}
	documentID, err := domain.ValidateUUID(request.GetDocumentId(), "document id")
	if err != nil {
		return nil, err
	}
	title, err := domain.ValidateTitle(request.GetTitle())
	if err != nil {
		return nil, err
	}
	content, err := domain.ValidateContent(request.GetContent())
	if err != nil {
		return nil, err
	}
	format, err := domain.NormalizeContentFormat(request.GetContentFormat())
	if err != nil {
		return nil, err
	}

	now := service.now().UTC()
	versionID := service.newID()

	var detail *documentv1.DocumentDetail
	err = service.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		document, err := service.store.LockDocument(ctx, tx, documentID)
		if err != nil {
			return err
		}
		if err := requireOwner(document, caller.Subject.Key); err != nil {
			return err
		}
		if err := domain.CheckExpectedRevision(document, request.GetExpectedAggregateRevision()); err != nil {
			return err
		}
		if err := service.ensureSubject(ctx, tx, caller.Subject, caller); err != nil {
			return err
		}

		latest, found, err := service.store.GetLatestVersion(ctx, tx, documentID)
		if err != nil {
			return err
		}
		latestRevision := int64(0)
		if found {
			latestRevision = latest.Revision
		}
		revision, err := domain.NextVersionRevision(latestRevision)
		if err != nil {
			return err
		}

		document, err = domain.AppendDraftVersion(document, now)
		if err != nil {
			return err
		}
		version := domain.Version{
			VersionID:           versionID,
			DocumentID:          documentID,
			Revision:            revision,
			PublicationStatus:   domain.PublicationDraft,
			Title:               title,
			Summary:             domain.BuildSummary(content),
			Content:             content,
			ContentFormat:       format,
			ContentSHA256:       contentDigest(content),
			CreatedBySubjectKey: caller.Subject.Key,
			CreatedAt:           now,
		}
		if err := service.store.InsertVersion(ctx, tx, version); err != nil {
			return err
		}

		// Appending a draft never changes the access policy, and it is not an index
		// event either: a draft is not indexable state, and the eventual activation
		// emits the upsert that carries the final snapshot.
		if err := service.store.UpdateDocumentState(ctx, tx, document); err != nil {
			return err
		}

		detail, err = service.loadDetail(ctx, tx, document)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &documentv1.UpdateDraftResponse{Document: detail}, nil
}

// saveDocument implements the SaveDocument use case: edit and save in one
// transaction.
//
// The whole sequence commits or rolls back together - ownership, the expected
// revision, the new version, the active version switch, the optional access
// policy change, the idempotency ledger entry and the Outbox event - so a caller
// never observes a saved body whose index event is missing, and a stale revision
// leaves nothing behind.
//
// Repeating a request id returns the state the first attempt committed instead of
// appending a second version. The durable ledger holds every committed request id
// of a document, so a retry is recognized whenever it arrives, not only while it
// is the most recent save. The lookup runs after the row lock and before the
// expected-revision check: the lock serializes concurrent attempts, and the first
// attempt has already moved the revision the caller would otherwise be comparing
// against.
func (service *Service) saveDocument(ctx context.Context, principal *serviceauth.Principal, request *documentv1.SaveDocumentRequest) (*documentv1.SaveDocumentResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: a save request is required", domain.ErrInvalidInput)
	}
	caller, err := service.verifyCaller(principal, request.GetRequestId())
	if err != nil {
		return nil, err
	}
	documentID, err := domain.ValidateUUID(request.GetDocumentId(), "document id")
	if err != nil {
		return nil, err
	}
	title, err := domain.ValidateTitle(request.GetTitle())
	if err != nil {
		return nil, err
	}
	content, err := domain.ValidateContent(request.GetContent())
	if err != nil {
		return nil, err
	}
	format, err := domain.NormalizeContentFormat(request.GetContentFormat())
	if err != nil {
		return nil, err
	}
	// The optional access policy change. Presence, not the boolean value, is what
	// distinguishes "keep the policy" from "set it": an ordinary body edit sends
	// no field, and the policy it does not mention is left exactly as it was.
	accessChange := request.AuthenticatedPublic

	// The fingerprint of every business input of this command except the expected
	// revision. A durable request id must not silently absorb a different payload:
	// the same id carrying a different body is a caller defect and is refused
	// instead of being answered with the first attempt's result.
	fingerprint := domain.SaveRequestFingerprint(documentID, title, content, format, accessChange)

	now := service.now().UTC()
	versionID := service.newID()

	var detail *documentv1.DocumentDetail
	appliedVersionID := ""
	replayed := false
	err = service.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		document, err := service.store.LockDocument(ctx, tx, documentID)
		if err != nil {
			return err
		}
		if err := requireOwner(document, caller.Subject.Key); err != nil {
			return err
		}

		// Idempotent save: the committed request id is read from the durable
		// ledger, so a retry that arrives after later saves still replays its own
		// first attempt. A replay changes nothing at all - no version, no policy,
		// no event, no revision - and returns the CURRENT committed state together
		// with the version the first attempt created.
		if caller.RequestID != "" {
			committed, found, err := service.store.FindSaveRequest(ctx, tx, documentID, caller.RequestID)
			if err != nil {
				return err
			}
			if found {
				if committed.PayloadFingerprint != fingerprint {
					return fmt.Errorf("%w: request id %s was already committed for document %s with a different payload",
						domain.ErrAlreadyExists, caller.RequestID, documentID)
				}
				replayed = true
				appliedVersionID = committed.VersionID
				detail, err = service.loadDetail(ctx, tx, document)
				return err
			}
		}
		if err := domain.CheckExpectedRevision(document, request.GetExpectedAggregateRevision()); err != nil {
			return err
		}
		if err := service.ensureSubject(ctx, tx, caller.Subject, caller); err != nil {
			return err
		}

		latest, found, err := service.store.GetLatestVersion(ctx, tx, documentID)
		if err != nil {
			return err
		}
		latestRevision := int64(0)
		if found {
			latestRevision = latest.Revision
		}
		revision, err := domain.NextVersionRevision(latestRevision)
		if err != nil {
			return err
		}

		document, err = domain.AppendDraftVersion(document, now)
		if err != nil {
			return err
		}
		version := domain.Version{
			VersionID:           versionID,
			DocumentID:          documentID,
			Revision:            revision,
			PublicationStatus:   domain.PublicationDraft,
			Title:               title,
			Summary:             domain.BuildSummary(content),
			Content:             content,
			ContentFormat:       format,
			ContentSHA256:       contentDigest(content),
			CreatedBySubjectKey: caller.Subject.Key,
			CreatedAt:           now,
		}
		if err := service.store.InsertVersion(ctx, tx, version); err != nil {
			return err
		}

		// The ledger entry names the version just written, so it can only be
		// committed once that version exists. It travels in the same transaction as
		// everything else: a failure below leaves no "already handled" record, and
		// the caller can retry the very same request id.
		if caller.RequestID != "" {
			if err := service.store.InsertSaveRequest(ctx, tx, domain.SaveRequest{
				DocumentID:         documentID,
				RequestID:          caller.RequestID,
				VersionID:          versionID,
				PayloadFingerprint: fingerprint,
				CreatedAt:          now,
			}); err != nil {
				return err
			}
		}

		// The appended version becomes active through the same rule PublishDocument
		// uses. SaveDocument and PublishDocument therefore cannot disagree about
		// what activation means: the previous active version is superseded, this
		// one is marked published and activation/aggregate advance once.
		document, switched, err := service.switchActiveVersion(ctx, tx, document, version, now)
		if err != nil {
			return err
		}
		if !switched {
			return fmt.Errorf("%w: saving document %s did not activate the appended version", domain.ErrInvariant, documentID)
		}
		version.PublicationStatus = domain.PublicationPublished

		policy, hasPolicy, err := service.store.GetAccessPolicy(ctx, tx, documentID)
		if err != nil {
			return err
		}
		if !hasPolicy {
			policy = domain.AccessPolicy{DocumentID: documentID, AccessRevision: document.AccessRevision, UpdatedAt: now}
		}
		if accessChange != nil {
			current := domain.AuthenticatedPublicState{Value: policy.AuthenticatedPublic, Set: hasPolicy}
			var changed bool
			document, changed, err = domain.ChangeAccess(document, *accessChange, current, now)
			if err != nil {
				return err
			}
			if changed {
				policy.AuthenticatedPublic = *accessChange
				policy.AccessRevision = document.AccessRevision
				policy.UpdatedAt = now
				if err := service.store.PutAccessPolicy(ctx, tx, policy); err != nil {
					return err
				}
				if err := service.store.UpdateDocumentState(ctx, tx, document); err != nil {
					return err
				}
			}
		}

		if err := service.appendDocumentEvent(ctx, tx, domain.EventKindUpsert, document, &version, policy, now); err != nil {
			return err
		}

		detail, err = service.loadDetail(ctx, tx, document)
		if err != nil {
			return err
		}
		// The freshly saved version is what this request applied, and the returned
		// document must agree: a caller that reads applied_version_id off the
		// response and looks it up in the detail it received must find it active.
		appliedVersionID = versionID
		if activeVersionID := detail.GetSummary().GetActiveVersionId(); activeVersionID != versionID {
			return fmt.Errorf("%w: saved version %s is not the active version %s of document %s",
				domain.ErrInvariant, versionID, activeVersionID, documentID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &documentv1.SaveDocumentResponse{
		Document:         detail,
		Replayed:         replayed,
		AppliedVersionId: appliedVersionID,
	}, nil
}

// publishDocument implements the PublishDocument use case.
func (service *Service) publishDocument(ctx context.Context, principal *serviceauth.Principal, request *documentv1.PublishDocumentRequest) (*documentv1.PublishDocumentResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: a publish request is required", domain.ErrInvalidInput)
	}
	caller, err := service.verifyCaller(principal, request.GetRequestId())
	if err != nil {
		return nil, err
	}
	documentID, err := domain.ValidateUUID(request.GetDocumentId(), "document id")
	if err != nil {
		return nil, err
	}
	versionID, err := domain.ValidateUUID(request.GetVersionId(), "version id")
	if err != nil {
		return nil, err
	}

	now := service.now().UTC()
	var detail *documentv1.DocumentDetail
	err = service.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		document, err := service.store.LockDocument(ctx, tx, documentID)
		if err != nil {
			return err
		}
		if err := requireOwner(document, caller.Subject.Key); err != nil {
			return err
		}
		if err := domain.CheckExpectedRevision(document, request.GetExpectedAggregateRevision()); err != nil {
			return err
		}
		if err := service.ensureSubject(ctx, tx, caller.Subject, caller); err != nil {
			return err
		}

		target, err := service.store.GetVersion(ctx, tx, documentID, versionID)
		if err != nil {
			return err
		}

		// Activation goes through the single version switching rule shared with
		// SaveDocument. Republishing the version that is already active changes
		// nothing, so it must not inflate the revisions or emit a second event.
		document, switched, err := service.switchActiveVersion(ctx, tx, document, target, now)
		if err != nil {
			return err
		}
		if !switched {
			detail, err = service.loadDetail(ctx, tx, document)
			return err
		}
		target.PublicationStatus = domain.PublicationPublished

		policy, hasPolicy, err := service.store.GetAccessPolicy(ctx, tx, documentID)
		if err != nil {
			return err
		}
		if !hasPolicy {
			policy = domain.AccessPolicy{DocumentID: documentID, AccessRevision: document.AccessRevision, UpdatedAt: now}
		}
		if err := service.appendDocumentEvent(ctx, tx, domain.EventKindUpsert, document, &target, policy, now); err != nil {
			return err
		}

		detail, err = service.loadDetail(ctx, tx, document)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &documentv1.PublishDocumentResponse{Document: detail}, nil
}

// withdrawVersion implements the WithdrawVersion use case.
func (service *Service) withdrawVersion(ctx context.Context, principal *serviceauth.Principal, request *documentv1.WithdrawVersionRequest) (*documentv1.WithdrawVersionResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: a withdraw request is required", domain.ErrInvalidInput)
	}
	caller, err := service.verifyCaller(principal, request.GetRequestId())
	if err != nil {
		return nil, err
	}
	documentID, err := domain.ValidateUUID(request.GetDocumentId(), "document id")
	if err != nil {
		return nil, err
	}
	versionID, err := domain.ValidateUUID(request.GetVersionId(), "version id")
	if err != nil {
		return nil, err
	}

	now := service.now().UTC()
	var detail *documentv1.DocumentDetail
	err = service.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		document, err := service.store.LockDocument(ctx, tx, documentID)
		if err != nil {
			return err
		}
		if err := requireOwner(document, caller.Subject.Key); err != nil {
			return err
		}
		if err := service.ensureSubject(ctx, tx, caller.Subject, caller); err != nil {
			return err
		}
		target, err := service.store.GetVersion(ctx, tx, documentID, versionID)
		if err != nil {
			return err
		}

		document, wasActive, err := domain.WithdrawVersion(document, target, now)
		if err != nil {
			return err
		}
		if err := service.store.SetVersionPublicationStatus(ctx, tx, target.VersionID, domain.PublicationWithdrawn); err != nil {
			return err
		}
		target.PublicationStatus = domain.PublicationWithdrawn
		if err := service.store.UpdateDocumentState(ctx, tx, document); err != nil {
			return err
		}

		policy, hasPolicy, err := service.store.GetAccessPolicy(ctx, tx, documentID)
		if err != nil {
			return err
		}
		if !hasPolicy {
			policy = domain.AccessPolicy{DocumentID: documentID, AccessRevision: document.AccessRevision, UpdatedAt: now}
		}

		// Retiring the active version removes the document from the index; retiring
		// a superseded draft does not, so the index is told to re-read the current
		// state instead.
		if wasActive {
			if err := service.appendDocumentEvent(ctx, tx, domain.EventKindDelete, document, &target, policy, now); err != nil {
				return err
			}
		} else {
			current, err := service.currentVersion(ctx, tx, document)
			if err != nil {
				return err
			}
			if err := service.appendDocumentEvent(ctx, tx, domain.EventKindUpsert, document, current, policy, now); err != nil {
				return err
			}
		}

		detail, err = service.loadDetail(ctx, tx, document)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &documentv1.WithdrawVersionResponse{Document: detail}, nil
}

// archiveDocument implements the ArchiveDocument use case.
func (service *Service) archiveDocument(ctx context.Context, principal *serviceauth.Principal, request *documentv1.ArchiveDocumentRequest) (*documentv1.ArchiveDocumentResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: an archive request is required", domain.ErrInvalidInput)
	}
	caller, err := service.verifyCaller(principal, request.GetRequestId())
	if err != nil {
		return nil, err
	}
	documentID, err := domain.ValidateUUID(request.GetDocumentId(), "document id")
	if err != nil {
		return nil, err
	}

	now := service.now().UTC()
	var detail *documentv1.DocumentDetail
	err = service.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		document, err := service.store.LockDocument(ctx, tx, documentID)
		if err != nil {
			return err
		}
		if err := requireOwner(document, caller.Subject.Key); err != nil {
			return err
		}
		if err := service.ensureSubject(ctx, tx, caller.Subject, caller); err != nil {
			return err
		}
		document, err = domain.Archive(document, now)
		if err != nil {
			return err
		}
		if err := service.store.UpdateDocumentState(ctx, tx, document); err != nil {
			return err
		}
		if err := service.appendLifecycleEvent(ctx, tx, document, now); err != nil {
			return err
		}
		detail, err = service.loadDetail(ctx, tx, document)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &documentv1.ArchiveDocumentResponse{Document: detail}, nil
}

// trashDocument implements the TrashDocument use case.
func (service *Service) trashDocument(ctx context.Context, principal *serviceauth.Principal, request *documentv1.TrashDocumentRequest) (*documentv1.TrashDocumentResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: a trash request is required", domain.ErrInvalidInput)
	}
	caller, err := service.verifyCaller(principal, request.GetRequestId())
	if err != nil {
		return nil, err
	}
	documentID, err := domain.ValidateUUID(request.GetDocumentId(), "document id")
	if err != nil {
		return nil, err
	}

	now := service.now().UTC()
	var detail *documentv1.DocumentDetail
	err = service.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		document, err := service.store.LockDocument(ctx, tx, documentID)
		if err != nil {
			return err
		}
		if err := requireOwner(document, caller.Subject.Key); err != nil {
			return err
		}
		if err := service.ensureSubject(ctx, tx, caller.Subject, caller); err != nil {
			return err
		}
		document, err = domain.Trash(document, now)
		if err != nil {
			return err
		}
		if err := service.store.UpdateDocumentState(ctx, tx, document); err != nil {
			return err
		}
		if err := service.appendLifecycleEvent(ctx, tx, document, now); err != nil {
			return err
		}
		detail, err = service.loadDetail(ctx, tx, document)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &documentv1.TrashDocumentResponse{Document: detail}, nil
}
