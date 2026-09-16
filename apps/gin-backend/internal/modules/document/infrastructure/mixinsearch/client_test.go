package mixinsearch

import (
	"context"
	"errors"
	"strings"
	"testing"

	domain "gin-backend/internal/modules/document/domain"
	mixinsearchv1 "packages/gen/mixin-search/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestClientMapsStableIndexPayload(t *testing.T) {
	remote := &ragClientStub{}
	client := &Client{client: remote, issuer: newTestIssuer(t, "go-web-index-worker")}
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
	// Every protected RPC must carry a capability: mixin-search rejects callers
	// that cannot prove their identity.
	claims := remote.claims(t)
	if claims.Role != RoleIndexWriter || claims.Subject != "go-web-index-worker" {
		t.Fatalf("index call capability = %+v", claims)
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
	client := &Client{client: remote, issuer: newTestIssuer(t, "go-web-shadow-search")}
	result, err := client.SearchDocuments(context.Background(), domain.DocumentSearchInput{
		Query: "query", AllowedSpaceIDs: []string{"space"}, AllowedDocumentIDs: []string{"doc"}, TopK: 20,
		CallerUserID: 42,
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
	// The granted scope is the allow-list go-web already authorised, and the
	// end user is recorded for audit only.
	claims := remote.claims(t)
	if claims.Role != RoleSearcher || claims.UserID != "42" {
		t.Fatalf("search capability = %+v", claims)
	}
	if len(claims.AllowedSpaceIDs) != 1 || claims.AllowedSpaceIDs[0] != "space" ||
		len(claims.AllowedDocumentIDs) != 1 || claims.AllowedDocumentIDs[0] != "doc" {
		t.Fatalf("search capability scope = %+v", claims)
	}
}

func TestClientRejectsCallsWithoutAnIssuer(t *testing.T) {
	client := &Client{client: &ragClientStub{}}
	for name, call := range map[string]func() error{
		"index": func() error {
			return client.IndexDocumentVersion(context.Background(), domain.IndexDocumentVersionInput{})
		},
		"search": func() error {
			_, err := client.SearchDocuments(context.Background(), domain.DocumentSearchInput{Query: "x"})
			return err
		},
	} {
		if err := call(); err == nil {
			t.Fatalf("%s call without an issuer succeeded, want a failure", name)
		}
	}
}

func TestNewRequiresIssuer(t *testing.T) {
	if _, err := New("127.0.0.1:1", 1<<20, nil); err == nil {
		t.Fatal("New without a capability issuer succeeded, want an error")
	}
}

func TestClientClassifiesRemoteErrors(t *testing.T) {
	tests := []struct {
		code      codes.Code
		retryable bool
	}{
		{codes.Unavailable, true}, {codes.DeadlineExceeded, true}, {codes.Aborted, true},
		{codes.InvalidArgument, false}, {codes.FailedPrecondition, false}, {codes.NotFound, false},
		// A rejected capability or an out-of-scope search must never be retried.
		{codes.Unauthenticated, false}, {codes.PermissionDenied, false},
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
	lastContext    context.Context
}

// claims decodes the capability the client attached to its last call.
func (client *ragClientStub) claims(t *testing.T) capabilityClaims {
	t.Helper()
	if client.lastContext == nil {
		t.Fatal("no call was recorded")
	}
	values := metadata.ValueFromIncomingContext(client.lastContext, authorizationHeader)
	if len(values) != 1 {
		t.Fatalf("outgoing %s = %v, want exactly one value", authorizationHeader, values)
	}
	token, found := strings.CutPrefix(values[0], "Bearer ")
	if !found {
		t.Fatalf("outgoing capability %q does not use the Bearer scheme", values[0])
	}
	return decodeClaims(t, token)
}

func (client *ragClientStub) IndexDocumentVersion(ctx context.Context, request *mixinsearchv1.IndexDocumentVersionRequest, _ ...grpc.CallOption) (*mixinsearchv1.IndexDocumentVersionResponse, error) {
	client.indexRequest = request
	client.lastContext = outgoingAsIncoming(ctx)
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
func (client *ragClientStub) SearchDocuments(ctx context.Context, request *mixinsearchv1.SearchDocumentsRequest, _ ...grpc.CallOption) (*mixinsearchv1.SearchDocumentsResponse, error) {
	client.searchRequest = request
	client.lastContext = outgoingAsIncoming(ctx)
	if client.searchResponse != nil {
		return client.searchResponse, nil
	}
	return &mixinsearchv1.SearchDocumentsResponse{}, nil
}

// outgoingAsIncoming moves the outgoing metadata to the incoming side so the
// stub can read what the client attached without a live server.
func outgoingAsIncoming(ctx context.Context) context.Context {
	outgoing, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return nil
	}
	return metadata.NewIncomingContext(ctx, outgoing)
}
