package application

import (
	"context"

	documentv1 "packages/gen/document/v1"
	"packages/serviceauth"
)

// The methods below are the business entry points the transport boundary calls.
// Each one delegates to the use case of the same name and maps its error onto the
// documented gRPC status. They hold no rule of their own: a rule that lived here
// would be invisible to every other caller of the use case.

// CreateDocument creates a formal document owned by the authenticated subject.
func (service *Service) CreateDocument(ctx context.Context, principal *serviceauth.Principal, request *documentv1.CreateDocumentRequest) (*documentv1.CreateDocumentResponse, error) {
	response, err := service.createDocument(ctx, principal, request)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}

// UpdateDraft appends a draft version to an existing document.
func (service *Service) UpdateDraft(ctx context.Context, principal *serviceauth.Principal, request *documentv1.UpdateDraftRequest) (*documentv1.UpdateDraftResponse, error) {
	response, err := service.updateDraft(ctx, principal, request)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}

// SaveDocument edits and saves a document in one transaction and returns the
// document as committed, after the active version switch.
func (service *Service) SaveDocument(ctx context.Context, principal *serviceauth.Principal, request *documentv1.SaveDocumentRequest) (*documentv1.SaveDocumentResponse, error) {
	response, err := service.saveDocument(ctx, principal, request)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}

// PublishDocument makes a version visible to the formal document index.
func (service *Service) PublishDocument(ctx context.Context, principal *serviceauth.Principal, request *documentv1.PublishDocumentRequest) (*documentv1.PublishDocumentResponse, error) {
	response, err := service.publishDocument(ctx, principal, request)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}

// WithdrawVersion retires a published version without deleting its content.
func (service *Service) WithdrawVersion(ctx context.Context, principal *serviceauth.Principal, request *documentv1.WithdrawVersionRequest) (*documentv1.WithdrawVersionResponse, error) {
	response, err := service.withdrawVersion(ctx, principal, request)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}

// ArchiveDocument moves a document out of the searchable set while keeping it.
func (service *Service) ArchiveDocument(ctx context.Context, principal *serviceauth.Principal, request *documentv1.ArchiveDocumentRequest) (*documentv1.ArchiveDocumentResponse, error) {
	response, err := service.archiveDocument(ctx, principal, request)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}

// TrashDocument removes a document from the index.
func (service *Service) TrashDocument(ctx context.Context, principal *serviceauth.Principal, request *documentv1.TrashDocumentRequest) (*documentv1.TrashDocumentResponse, error) {
	response, err := service.trashDocument(ctx, principal, request)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}

// GetDocument reads one document's detail, subject to a resource decision.
func (service *Service) GetDocument(ctx context.Context, principal *serviceauth.Principal, request *documentv1.GetDocumentRequest) (*documentv1.GetDocumentResponse, error) {
	response, err := service.getDocument(ctx, principal, request)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}

// ListDocuments lists documents the authenticated subject may see.
func (service *Service) ListDocuments(ctx context.Context, principal *serviceauth.Principal, request *documentv1.ListDocumentsRequest) (*documentv1.ListDocumentsResponse, error) {
	response, err := service.listDocuments(ctx, principal, request)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}

// CreateTeamSpace creates a team space and its owner membership in one
// transaction.
func (service *Service) CreateTeamSpace(ctx context.Context, principal *serviceauth.Principal, request *documentv1.CreateTeamSpaceRequest) (*documentv1.CreateTeamSpaceResponse, error) {
	response, err := service.createTeamSpace(ctx, principal, request)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}

// GetSpace reads a space with its members and group bindings.
func (service *Service) GetSpace(ctx context.Context, principal *serviceauth.Principal, request *documentv1.GetSpaceRequest) (*documentv1.GetSpaceResponse, error) {
	response, err := service.getSpace(ctx, principal, request)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}

// ListSubjects reads the document service's own access subject registry.
func (service *Service) ListSubjects(ctx context.Context, principal *serviceauth.Principal, request *documentv1.ListSubjectsRequest) (*documentv1.ListSubjectsResponse, error) {
	response, err := service.listSubjects(ctx, principal, request)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}

// GrantSpaceMember adds or restores an active space membership. It is the only
// path that creates membership; joining a QQ group never calls it.
func (service *Service) GrantSpaceMember(ctx context.Context, principal *serviceauth.Principal, request *documentv1.GrantSpaceMemberRequest) (*documentv1.GrantSpaceMemberResponse, error) {
	response, err := service.grantSpaceMember(ctx, principal, request)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}

// RevokeSpaceMember closes an active space membership. An owner membership cannot
// be revoked.
func (service *Service) RevokeSpaceMember(ctx context.Context, principal *serviceauth.Principal, request *documentv1.RevokeSpaceMemberRequest) (*documentv1.RevokeSpaceMemberResponse, error) {
	response, err := service.revokeSpaceMember(ctx, principal, request)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}

// BindGroupSpace binds a QQ group to a team space.
func (service *Service) BindGroupSpace(ctx context.Context, principal *serviceauth.Principal, request *documentv1.BindGroupSpaceRequest) (*documentv1.BindGroupSpaceResponse, error) {
	response, err := service.bindGroupSpace(ctx, principal, request)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}

// RevokeGroupSpace closes an active QQ group to space binding.
func (service *Service) RevokeGroupSpace(ctx context.Context, principal *serviceauth.Principal, request *documentv1.RevokeGroupSpaceRequest) (*documentv1.RevokeGroupSpaceResponse, error) {
	response, err := service.revokeGroupSpace(ctx, principal, request)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}

// ResolveAccessScope answers the resource half of the scope question.
func (service *Service) ResolveAccessScope(ctx context.Context, principal *serviceauth.Principal, request *documentv1.ResolveAccessScopeRequest) (*documentv1.ResolveAccessScopeResponse, error) {
	response, err := service.resolveAccessScope(ctx, principal, request)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}

// IssueSearchCapability mints a document-search capability from a granted
// resolution. A denied resolution returns a denial with an empty capability.
func (service *Service) IssueSearchCapability(ctx context.Context, principal *serviceauth.Principal, request *documentv1.IssueSearchCapabilityRequest) (*documentv1.IssueSearchCapabilityResponse, error) {
	response, err := service.issueSearchCapability(ctx, principal, request)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}
