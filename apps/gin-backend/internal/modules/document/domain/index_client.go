package domain

import (
	"context"
	"fmt"
)

type IndexDocumentVersionInput struct {
	OperationID       string
	DocumentID        string
	VersionID         string
	OwnerSpaceID      string
	Filename          string
	Title             string
	Content           []byte
	ContentSHA256     string
	ChunkSize         int32
	Overlap           int32
	SourceURI         string
	Metadata          map[string]string
	LifecycleRevision uint64
}

type UpdateDocumentAccessInput struct {
	OperationID         string
	DocumentID          string
	AccessRevision      uint64
	LifecycleRevision   uint64
	AuthenticatedPublic bool
	GrantedSpaceIDs     []string
}

type ActivateDocumentVersionInput struct {
	OperationID               string
	DocumentID                string
	VersionID                 string
	ActivationRevision        uint64
	ExpectedPreviousVersionID string
	LifecycleRevision         uint64
}

type DeleteDocumentVersionInput struct {
	OperationID       string
	DocumentID        string
	VersionID         string
	LifecycleRevision uint64
}

type DeleteDocumentInput struct {
	OperationID       string
	DocumentID        string
	LifecycleRevision uint64
}

type DocumentVersionIndexState struct {
	Exists             bool
	DocumentID         string
	VersionID          string
	Status             string
	ActivationRevision uint64
	AccessRevision     uint64
	LifecycleRevision  uint64
	ContentSHA256      string
}

// DocumentIndexClient 是 mixin-search v1 的领域端口，具体 Protobuf 类型不会
// 穿透到领域和应用服务。
type DocumentIndexClient interface {
	IndexDocumentVersion(context.Context, IndexDocumentVersionInput) error
	UpdateDocumentAccess(context.Context, UpdateDocumentAccessInput) error
	ActivateDocumentVersion(context.Context, ActivateDocumentVersionInput) error
	DeleteDocumentVersion(context.Context, DeleteDocumentVersionInput) error
	DeleteDocument(context.Context, DeleteDocumentInput) error
	GetDocumentVersionState(context.Context, string, string) (DocumentVersionIndexState, error)
}

// RemoteIndexError 将传输层错误压缩为应用层可判定的重试信息。
type RemoteIndexError struct {
	Code      string
	Retryable bool
	Err       error
}

func (err *RemoteIndexError) Error() string {
	return fmt.Sprintf("mixin-search %s: %v", err.Code, err.Err)
}

func (err *RemoteIndexError) Unwrap() error { return err.Err }
