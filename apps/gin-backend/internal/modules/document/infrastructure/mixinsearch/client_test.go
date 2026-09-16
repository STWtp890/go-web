package mixinsearch

import (
	"context"
	"errors"
	"testing"

	domain "gin-backend/internal/modules/document/domain"
	mixinsearchv1 "packages/gen/mixin-search/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestClientMapsStableIndexPayload(t *testing.T) {
	remote := &ragClientStub{}
	client := &Client{client: remote}
	input := domain.IndexDocumentVersionInput{
		OperationID: "event/index", DocumentID: "doc", VersionID: "v1", OwnerSpaceID: "space",
		Filename: "v1.markdown", Title: "P2.3", Content: []byte("content"), ContentSHA256: "hash",
		ChunkSize: 180, Overlap: 20, SourceURI: "go-web://doc/v1",
		Metadata: map[string]string{"index_profile": "markdown-v1"}, LifecycleRevision: 3,
	}
	if err := client.IndexDocumentVersion(context.Background(), input); err != nil {
		t.Fatalf("index: %v", err)
	}
	request := remote.indexRequest
	if request.GetOperationId() != input.OperationID || request.GetDocumentId() != input.DocumentID ||
		request.GetVersionId() != input.VersionID || request.GetContentSha256() != input.ContentSHA256 ||
		request.GetChunkSize() != input.ChunkSize || request.GetLifecycleRevision() != input.LifecycleRevision {
		t.Fatalf("mapped request = %#v", request)
	}
}

func TestClientMapsShadowSearchAndHits(t *testing.T) {
	remote := &ragClientStub{searchResponse: &mixinsearchv1.SearchDocumentsResponse{
		Query: "normalized", Truncated: true,
		Hits: []*mixinsearchv1.SearchHit{{
			Chunk:    &mixinsearchv1.Chunk{Id: "chunk", DocumentId: "doc", VersionId: "v1", OwnerSpaceId: "space", Position: 2},
			RrfScore: 0.5, DenseRank: 1, SparseRank: 3, DenseScore: 0.8, SparseScore: 0.6,
		}},
	}}
	client := &Client{client: remote}
	result, err := client.SearchDocuments(context.Background(), domain.DocumentSearchInput{
		Query: "query", AllowedSpaceIDs: []string{"space"}, AllowedDocumentIDs: []string{"doc"}, TopK: 20,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	request := remote.searchRequest
	if request.GetQuery() != "query" || request.GetTopK() != 20 ||
		len(request.GetAllowedSpaceIds()) != 1 || len(request.GetAllowedDocumentIds()) != 1 {
		t.Fatalf("mapped request = %#v", request)
	}
	if result.Query != "normalized" || !result.Truncated || len(result.Hits) != 1 ||
		result.Hits[0].DocumentID != "doc" || result.Hits[0].DenseRank != 1 {
		t.Fatalf("mapped result = %#v", result)
	}
}

func TestClientClassifiesRemoteErrors(t *testing.T) {
	tests := []struct {
		code      codes.Code
		retryable bool
	}{
		{codes.Unavailable, true}, {codes.DeadlineExceeded, true}, {codes.Aborted, true},
		{codes.InvalidArgument, false}, {codes.FailedPrecondition, false}, {codes.NotFound, false},
	}
	for _, test := range tests {
		t.Run(test.code.String(), func(t *testing.T) {
			err := wrapRemoteError(status.Error(test.code, "failure"))
			var remote *domain.RemoteIndexError
			if !errors.As(err, &remote) {
				t.Fatalf("error = %T, want RemoteIndexError", err)
			}
			if remote.Code != test.code.String() || remote.Retryable != test.retryable {
				t.Fatalf("classification = (%s,%t), want (%s,%t)", remote.Code, remote.Retryable, test.code, test.retryable)
			}
		})
	}
}

type ragClientStub struct {
	indexRequest   *mixinsearchv1.IndexDocumentVersionRequest
	searchRequest  *mixinsearchv1.SearchDocumentsRequest
	searchResponse *mixinsearchv1.SearchDocumentsResponse
}

func (client *ragClientStub) IndexDocumentVersion(_ context.Context, request *mixinsearchv1.IndexDocumentVersionRequest, _ ...grpc.CallOption) (*mixinsearchv1.IndexDocumentVersionResponse, error) {
	client.indexRequest = request
	return &mixinsearchv1.IndexDocumentVersionResponse{}, nil
}
func (*ragClientStub) ActivateDocumentVersion(context.Context, *mixinsearchv1.ActivateDocumentVersionRequest, ...grpc.CallOption) (*mixinsearchv1.ActivateDocumentVersionResponse, error) {
	return &mixinsearchv1.ActivateDocumentVersionResponse{}, nil
}
func (*ragClientStub) UpdateDocumentAccess(context.Context, *mixinsearchv1.UpdateDocumentAccessRequest, ...grpc.CallOption) (*mixinsearchv1.UpdateDocumentAccessResponse, error) {
	return &mixinsearchv1.UpdateDocumentAccessResponse{}, nil
}
func (*ragClientStub) DeleteDocumentVersion(context.Context, *mixinsearchv1.DeleteDocumentVersionRequest, ...grpc.CallOption) (*mixinsearchv1.DeleteDocumentVersionResponse, error) {
	return &mixinsearchv1.DeleteDocumentVersionResponse{}, nil
}
func (*ragClientStub) DeleteDocument(context.Context, *mixinsearchv1.DeleteDocumentRequest, ...grpc.CallOption) (*mixinsearchv1.DeleteDocumentResponse, error) {
	return &mixinsearchv1.DeleteDocumentResponse{}, nil
}
func (*ragClientStub) GetDocumentVersionState(context.Context, *mixinsearchv1.GetDocumentVersionStateRequest, ...grpc.CallOption) (*mixinsearchv1.GetDocumentVersionStateResponse, error) {
	return &mixinsearchv1.GetDocumentVersionStateResponse{}, nil
}
func (client *ragClientStub) SearchDocuments(_ context.Context, request *mixinsearchv1.SearchDocumentsRequest, _ ...grpc.CallOption) (*mixinsearchv1.SearchDocumentsResponse, error) {
	client.searchRequest = request
	if client.searchResponse != nil {
		return client.searchResponse, nil
	}
	return &mixinsearchv1.SearchDocumentsResponse{}, nil
}
