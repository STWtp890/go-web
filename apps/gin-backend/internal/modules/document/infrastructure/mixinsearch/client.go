// Package mixinsearch 将文档索引应用端口适配到 mixin-search v1 gRPC 协议。
package mixinsearch

import (
	"context"
	"errors"
	"fmt"
	"strings"

	domain "gin-backend/internal/modules/document/domain"
	mixinsearchv1 "packages/gen/mixin-search/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// authorizationHeader carries the caller capability on every protected RPC.
const authorizationHeader = "authorization"

type Client struct {
	connection *grpc.ClientConn
	client     mixinsearchv1.RAGServiceClient
	issuer     *CapabilityIssuer
}

var _ domain.DocumentIndexClient = (*Client)(nil)
var _ domain.DocumentSearchClient = (*Client)(nil)

// New dials mixin-search. The capability issuer is mandatory: the service
// rejects any caller that cannot prove its identity, so a client without one
// could only produce confusing Unauthenticated failures.
func New(address string, maxSendBytes int, issuer *CapabilityIssuer) (*Client, error) {
	if strings.TrimSpace(address) == "" {
		return nil, errors.New("mixin-search client: address is required")
	}
	if issuer == nil {
		return nil, errors.New("mixin-search client: capability issuer is required")
	}
	if maxSendBytes <= 0 {
		maxSendBytes = 16 << 20
	}
	connection, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(maxSendBytes),
			grpc.MaxCallRecvMsgSize(maxSendBytes),
		),
	)
	if err != nil {
		return nil, err
	}
	return &Client{
		connection: connection,
		client:     mixinsearchv1.NewRAGServiceClient(connection),
		issuer:     issuer,
	}, nil
}

func (client *Client) SearchDocuments(ctx context.Context, input domain.DocumentSearchInput) (domain.DocumentSearchResult, error) {
	// The granted scope is exactly the allow-list go-web already computed from
	// the fact source. mixin-search then verifies requested is contained in
	// granted, so this call cannot ask for more than go-web authorised.
	ctx, err := client.withCapability(ctx, func() (string, error) {
		return client.issuer.SearchToken(FormatUserID(input.CallerUserID), input.AllowedSpaceIDs, input.AllowedDocumentIDs)
	})
	if err != nil {
		return domain.DocumentSearchResult{}, err
	}
	response, err := client.client.SearchDocuments(ctx, &mixinsearchv1.SearchDocumentsRequest{
		Query: input.Query, AllowedSpaceIds: append([]string{}, input.AllowedSpaceIDs...),
		AllowedDocumentIds: append([]string{}, input.AllowedDocumentIDs...), TopK: int32(input.TopK),
	})
	if err != nil {
		return domain.DocumentSearchResult{}, wrapRemoteError(err)
	}
	result := domain.DocumentSearchResult{Query: response.GetQuery(), Truncated: response.GetTruncated()}
	result.Hits = make([]domain.DocumentSearchHit, 0, len(response.GetHits()))
	for _, hit := range response.GetHits() {
		chunk := hit.GetChunk()
		if chunk == nil {
			continue
		}
		result.Hits = append(result.Hits, domain.DocumentSearchHit{
			ChunkID: chunk.GetId(), DocumentID: chunk.GetDocumentId(), VersionID: chunk.GetVersionId(),
			OwnerSpaceID: chunk.GetOwnerSpaceId(), Position: int(chunk.GetPosition()),
			RRFScore: hit.GetRrfScore(), DenseRank: int(hit.GetDenseRank()), SparseRank: int(hit.GetSparseRank()),
			DenseScore: hit.GetDenseScore(), SparseScore: hit.GetSparseScore(),
		})
	}
	return result, nil
}

func (client *Client) Close() error {
	if client == nil || client.connection == nil {
		return nil
	}
	return client.connection.Close()
}

func (client *Client) CheckHealth(ctx context.Context) error {
	response, err := healthpb.NewHealthClient(client.connection).Check(ctx, &healthpb.HealthCheckRequest{
		Service: mixinsearchv1.RAGService_ServiceDesc.ServiceName,
	})
	if err != nil {
		return err
	}
	if response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("mixin-search health is %s", response.GetStatus())
	}
	return nil
}

func (client *Client) IndexDocumentVersion(ctx context.Context, input domain.IndexDocumentVersionInput) error {
	ctx, err := client.withIndexCapability(ctx)
	if err != nil {
		return err
	}
	_, err = client.client.IndexDocumentVersion(ctx, &mixinsearchv1.IndexDocumentVersionRequest{
		OperationId: input.OperationID, DocumentId: input.DocumentID, VersionId: input.VersionID,
		OwnerSpaceId: input.OwnerSpaceID, Filename: input.Filename, Title: input.Title,
		Content: input.Content, ContentSha256: input.ContentSHA256, ChunkSize: input.ChunkSize,
		Overlap: input.Overlap, SourceUri: input.SourceURI, Metadata: input.Metadata,
		LifecycleRevision: input.LifecycleRevision,
	})
	return wrapRemoteError(err)
}

func (client *Client) UpdateDocumentAccess(ctx context.Context, input domain.UpdateDocumentAccessInput) error {
	ctx, err := client.withIndexCapability(ctx)
	if err != nil {
		return err
	}
	_, err = client.client.UpdateDocumentAccess(ctx, &mixinsearchv1.UpdateDocumentAccessRequest{
		OperationId: input.OperationID, DocumentId: input.DocumentID, AccessRevision: input.AccessRevision,
		LifecycleRevision: input.LifecycleRevision, AuthenticatedPublic: input.AuthenticatedPublic,
		GrantedSpaceIds: append([]string{}, input.GrantedSpaceIDs...),
	})
	return wrapRemoteError(err)
}

func (client *Client) ActivateDocumentVersion(ctx context.Context, input domain.ActivateDocumentVersionInput) error {
	ctx, err := client.withIndexCapability(ctx)
	if err != nil {
		return err
	}
	_, err = client.client.ActivateDocumentVersion(ctx, &mixinsearchv1.ActivateDocumentVersionRequest{
		OperationId: input.OperationID, DocumentId: input.DocumentID, VersionId: input.VersionID,
		ActivationRevision: input.ActivationRevision, ExpectedPreviousVersionId: input.ExpectedPreviousVersionID,
		LifecycleRevision: input.LifecycleRevision,
	})
	return wrapRemoteError(err)
}

func (client *Client) DeleteDocumentVersion(ctx context.Context, input domain.DeleteDocumentVersionInput) error {
	ctx, err := client.withIndexCapability(ctx)
	if err != nil {
		return err
	}
	_, err = client.client.DeleteDocumentVersion(ctx, &mixinsearchv1.DeleteDocumentVersionRequest{
		OperationId: input.OperationID, DocumentId: input.DocumentID, VersionId: input.VersionID,
		LifecycleRevision: input.LifecycleRevision,
	})
	return wrapRemoteError(err)
}

func (client *Client) DeleteDocument(ctx context.Context, input domain.DeleteDocumentInput) error {
	ctx, err := client.withIndexCapability(ctx)
	if err != nil {
		return err
	}
	_, err = client.client.DeleteDocument(ctx, &mixinsearchv1.DeleteDocumentRequest{
		OperationId: input.OperationID, DocumentId: input.DocumentID, LifecycleRevision: input.LifecycleRevision,
	})
	return wrapRemoteError(err)
}

func (client *Client) GetDocumentVersionState(ctx context.Context, documentID, versionID string) (domain.DocumentVersionIndexState, error) {
	ctx, err := client.withIndexCapability(ctx)
	if err != nil {
		return domain.DocumentVersionIndexState{}, err
	}
	response, err := client.client.GetDocumentVersionState(ctx, &mixinsearchv1.GetDocumentVersionStateRequest{
		DocumentId: documentID, VersionId: versionID,
	})
	if err != nil {
		return domain.DocumentVersionIndexState{}, wrapRemoteError(err)
	}
	result := domain.DocumentVersionIndexState{Exists: response.GetExists()}
	if state := response.GetState(); state != nil {
		result.DocumentID = state.GetRef().GetDocumentId()
		result.VersionID = state.GetRef().GetVersionId()
		result.Status = state.GetStatus().String()
		result.ActivationRevision = state.GetActivationRevision()
		result.AccessRevision = state.GetAccessRevision()
		result.LifecycleRevision = state.GetLifecycleRevision()
		result.ContentSHA256 = state.GetContentSha256()
	}
	return result, nil
}

// withIndexCapability attaches an index-writer capability to the outgoing call.
func (client *Client) withIndexCapability(ctx context.Context) (context.Context, error) {
	return client.withCapability(ctx, client.issuer.IndexToken)
}

// withCapability mints a capability and puts it on the outgoing metadata. Minting
// is per call on purpose: the capability is short-lived, so a leaked request
// cannot be replayed later, and the search scope always matches this request.
func (client *Client) withCapability(ctx context.Context, mint func() (string, error)) (context.Context, error) {
	// New already rejects a missing issuer; this guard keeps a zero-value Client
	// (as constructed in tests or by future call sites) from panicking inside the
	// closure instead of reporting that it cannot prove its identity.
	if client.issuer == nil {
		return nil, errors.New("mixin-search client: capability issuer is required")
	}
	token, err := mint()
	if err != nil {
		return nil, fmt.Errorf("mint mixin-search capability: %w", err)
	}
	return metadata.AppendToOutgoingContext(ctx, authorizationHeader, "Bearer "+token), nil
}

func wrapRemoteError(err error) error {
	if err == nil {
		return nil
	}
	code := status.Code(err)
	retryable := false
	switch code {
	case codes.Canceled, codes.DeadlineExceeded, codes.ResourceExhausted,
		codes.Aborted, codes.Internal, codes.Unavailable:
		retryable = true
	}
	return &domain.RemoteIndexError{Code: code.String(), Retryable: retryable, Err: err}
}
