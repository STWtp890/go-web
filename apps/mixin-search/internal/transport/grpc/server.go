// Package grpcadapter maps the public Protobuf contract to the shared RAG
// business service.
package grpcadapter

import (
	"context"
	"errors"

	"mixin-search/internal/rag"
	mixinsearchv1 "packages/gen/mixin-search/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// KnowledgeService is transport-neutral so a future MCP adapter can reuse the
// same document-index semantics without depending on Protobuf types.
type KnowledgeService interface {
	IndexDocumentVersion(context.Context, rag.IndexDocumentVersionRequest) (rag.DocumentVersionState, error)
	ActivateDocumentVersion(context.Context, rag.ActivateDocumentVersionRequest) (rag.DocumentVersionState, error)
	UpdateDocumentAccess(context.Context, rag.UpdateDocumentAccessRequest) (rag.DocumentAccessState, error)
	DeleteDocumentVersion(context.Context, rag.DeleteDocumentVersionRequest) (rag.DeleteDocumentVersionResult, error)
	DeleteDocument(context.Context, rag.DeleteDocumentRequest) (rag.DeleteDocumentResult, error)
	GetDocumentVersionState(context.Context, rag.GetDocumentVersionStateRequest) (rag.GetDocumentVersionStateResult, error)
	SearchDocuments(context.Context, rag.SearchDocumentsRequest) (rag.SearchDocumentsResult, error)
}

type Server struct {
	mixinsearchv1.UnimplementedRAGServiceServer
	service KnowledgeService
}

func NewServer(service KnowledgeService) (*Server, error) {
	if service == nil {
		return nil, errors.New("knowledge service is required")
	}
	return &Server{service: service}, nil
}

func (s *Server) IndexDocumentVersion(
	ctx context.Context,
	request *mixinsearchv1.IndexDocumentVersionRequest,
) (*mixinsearchv1.IndexDocumentVersionResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	result, err := s.service.IndexDocumentVersion(ctx, rag.IndexDocumentVersionRequest{
		OperationID:       request.GetOperationId(),
		DocumentID:        request.GetDocumentId(),
		VersionID:         request.GetVersionId(),
		OwnerSpaceID:      request.GetOwnerSpaceId(),
		Filename:          request.GetFilename(),
		Title:             request.GetTitle(),
		Content:           request.GetContent(),
		ContentSHA256:     request.GetContentSha256(),
		ChunkSize:         int(request.GetChunkSize()),
		Overlap:           int(request.GetOverlap()),
		SourceURI:         request.GetSourceUri(),
		Metadata:          request.GetMetadata(),
		LifecycleRevision: request.GetLifecycleRevision(),
	})
	if err != nil {
		return nil, mapServiceError(err)
	}
	return &mixinsearchv1.IndexDocumentVersionResponse{State: versionState(result)}, nil
}

func (s *Server) ActivateDocumentVersion(
	ctx context.Context,
	request *mixinsearchv1.ActivateDocumentVersionRequest,
) (*mixinsearchv1.ActivateDocumentVersionResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	result, err := s.service.ActivateDocumentVersion(ctx, rag.ActivateDocumentVersionRequest{
		OperationID:               request.GetOperationId(),
		DocumentID:                request.GetDocumentId(),
		VersionID:                 request.GetVersionId(),
		ActivationRevision:        request.GetActivationRevision(),
		ExpectedPreviousVersionID: request.GetExpectedPreviousVersionId(),
		LifecycleRevision:         request.GetLifecycleRevision(),
	})
	if err != nil {
		return nil, mapServiceError(err)
	}
	return &mixinsearchv1.ActivateDocumentVersionResponse{State: versionState(result)}, nil
}

func (s *Server) UpdateDocumentAccess(
	ctx context.Context,
	request *mixinsearchv1.UpdateDocumentAccessRequest,
) (*mixinsearchv1.UpdateDocumentAccessResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	result, err := s.service.UpdateDocumentAccess(ctx, rag.UpdateDocumentAccessRequest{
		OperationID:         request.GetOperationId(),
		DocumentID:          request.GetDocumentId(),
		AccessRevision:      request.GetAccessRevision(),
		LifecycleRevision:   request.GetLifecycleRevision(),
		AuthenticatedPublic: request.GetAuthenticatedPublic(),
		GrantedSpaceIDs:     request.GetGrantedSpaceIds(),
	})
	if err != nil {
		return nil, mapServiceError(err)
	}
	return &mixinsearchv1.UpdateDocumentAccessResponse{State: accessState(result)}, nil
}

func (s *Server) DeleteDocumentVersion(
	ctx context.Context,
	request *mixinsearchv1.DeleteDocumentVersionRequest,
) (*mixinsearchv1.DeleteDocumentVersionResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	result, err := s.service.DeleteDocumentVersion(ctx, rag.DeleteDocumentVersionRequest{
		OperationID:       request.GetOperationId(),
		DocumentID:        request.GetDocumentId(),
		VersionID:         request.GetVersionId(),
		LifecycleRevision: request.GetLifecycleRevision(),
	})
	if err != nil {
		return nil, mapServiceError(err)
	}
	return &mixinsearchv1.DeleteDocumentVersionResponse{
		Deleted:              result.Deleted,
		ActiveVersionRemoved: result.ActiveVersionRemoved,
	}, nil
}

func (s *Server) DeleteDocument(
	ctx context.Context,
	request *mixinsearchv1.DeleteDocumentRequest,
) (*mixinsearchv1.DeleteDocumentResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	result, err := s.service.DeleteDocument(ctx, rag.DeleteDocumentRequest{
		OperationID:       request.GetOperationId(),
		DocumentID:        request.GetDocumentId(),
		LifecycleRevision: request.GetLifecycleRevision(),
	})
	if err != nil {
		return nil, mapServiceError(err)
	}
	return &mixinsearchv1.DeleteDocumentResponse{
		Tombstoned:        result.Tombstoned,
		LifecycleRevision: result.LifecycleRevision,
	}, nil
}

func (s *Server) GetDocumentVersionState(
	ctx context.Context,
	request *mixinsearchv1.GetDocumentVersionStateRequest,
) (*mixinsearchv1.GetDocumentVersionStateResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	result, err := s.service.GetDocumentVersionState(ctx, rag.GetDocumentVersionStateRequest{
		DocumentID: request.GetDocumentId(),
		VersionID:  request.GetVersionId(),
	})
	if err != nil {
		return nil, mapServiceError(err)
	}
	response := &mixinsearchv1.GetDocumentVersionStateResponse{Exists: result.Exists}
	if result.Exists {
		response.State = versionState(result.State)
	}
	return response, nil
}

func (s *Server) SearchDocuments(
	ctx context.Context,
	request *mixinsearchv1.SearchDocumentsRequest,
) (*mixinsearchv1.SearchDocumentsResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	result, err := s.service.SearchDocuments(ctx, rag.SearchDocumentsRequest{
		Query:              request.GetQuery(),
		AllowedSpaceIDs:    request.GetAllowedSpaceIds(),
		AllowedDocumentIDs: request.GetAllowedDocumentIds(),
		TopK:               int(request.GetTopK()),
	})
	if err != nil {
		return nil, mapServiceError(err)
	}
	return searchResponse(result), nil
}

func versionState(state rag.DocumentVersionState) *mixinsearchv1.DocumentVersionState {
	statusValue := mixinsearchv1.DocumentIndexStatus_DOCUMENT_INDEX_STATUS_INDEXED
	if state.Status == rag.DocumentVersionActive {
		statusValue = mixinsearchv1.DocumentIndexStatus_DOCUMENT_INDEX_STATUS_ACTIVE
	}
	return &mixinsearchv1.DocumentVersionState{
		Ref: &mixinsearchv1.DocumentVersionRef{
			DocumentId: state.DocumentID,
			VersionId:  state.VersionID,
		},
		OwnerSpaceId:       state.OwnerSpaceID,
		Status:             statusValue,
		ActivationRevision: state.ActivationRevision,
		AccessRevision:     state.AccessRevision,
		LifecycleRevision:  state.LifecycleRevision,
		ChunkCount:         int32(state.ChunkCount),
		ContentSha256:      state.ContentSHA256,
	}
}

func accessState(state rag.DocumentAccessState) *mixinsearchv1.DocumentAccessState {
	return &mixinsearchv1.DocumentAccessState{
		DocumentId:          state.DocumentID,
		AccessRevision:      state.AccessRevision,
		LifecycleRevision:   state.LifecycleRevision,
		AuthenticatedPublic: state.AuthenticatedPublic,
		GrantedSpaceIds:     state.GrantedSpaceIDs,
	}
}

func searchResponse(result rag.SearchDocumentsResult) *mixinsearchv1.SearchDocumentsResponse {
	hits := make([]*mixinsearchv1.SearchHit, 0, len(result.Hits))
	for _, hit := range result.Hits {
		hits = append(hits, &mixinsearchv1.SearchHit{
			Chunk: &mixinsearchv1.Chunk{
				Id:            hit.Chunk.ID,
				DocumentId:    hit.Chunk.DocumentID,
				VersionId:     hit.Chunk.VersionID,
				OwnerSpaceId:  hit.Chunk.OwnerSpaceID,
				Title:         hit.Chunk.Title,
				Content:       hit.Chunk.Content,
				Position:      int32(hit.Chunk.Position),
				Format:        hit.Chunk.Format,
				SourceUri:     hit.Chunk.Source,
				Section:       hit.Chunk.Section,
				ContentSha256: hit.Chunk.ContentSHA256,
				Metadata:      hit.Chunk.Metadata,
			},
			RrfScore:    hit.RRFScore,
			DenseRank:   int32(hit.DenseRank),
			SparseRank:  int32(hit.SparseRank),
			DenseScore:  hit.DenseScore,
			SparseScore: hit.SparseScore,
		})
	}
	return &mixinsearchv1.SearchDocumentsResponse{
		Query:     result.Query,
		Hits:      hits,
		Truncated: result.Truncated,
	}
}

func mapServiceError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	case errors.Is(err, rag.ErrInvalidInput):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, rag.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, rag.ErrControlStoreConflict):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, rag.ErrControlStoreUnavailable):
		return status.Error(codes.Unavailable, err.Error())
	case errors.Is(err, rag.ErrConflict),
		errors.Is(err, rag.ErrStaleActivation),
		errors.Is(err, rag.ErrStaleAccess),
		errors.Is(err, rag.ErrStaleLifecycle):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
