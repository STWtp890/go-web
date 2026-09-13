package domain

import (
	"context"
	"time"
)

// DocumentHead 是构造版本化缓存键和执行访问判断所需的最小当前状态。
type DocumentHead struct {
	DocumentID          string
	OwnerID             int64
	ActiveVersionID     string
	AuthenticatedPublic bool
	ActivationRevision  int64
	AccessRevision      int64
	LifecycleRevision   int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// DocumentView 是当前活动版本的详情读模型。
type DocumentView struct {
	DocumentHead
	Title   string
	Summary string
	Content string
}

// DocumentSummary 是列表和搜索使用的当前版本投影。
type DocumentSummary struct {
	DocumentID          string
	OwnerID             int64
	AuthenticatedPublic bool
	Title               string
	Summary             string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// QueryRepository 提供 document 当前状态的只读查询端口。
type QueryRepository interface {
	GetActiveDocumentHead(context.Context, string) (*DocumentHead, error)
	GetActiveDocumentView(context.Context, DocumentHead) (*DocumentView, error)
	ListOwnedDocuments(context.Context, int64, int, int) ([]DocumentSummary, int64, error)
	ListPublicDocuments(context.Context, int, int) ([]DocumentSummary, int64, error)
	SearchOwnedDocuments(context.Context, int64, string, int, int) ([]DocumentSummary, int64, error)
}

// DocumentViewLoader 在缓存未命中时加载当前文档详情。
type DocumentViewLoader func(context.Context) (*DocumentView, error)

// QueryCache 使用包含活动版本和三类修订号的键缓存不可变详情快照。
type QueryCache interface {
	GetDocumentView(context.Context, DocumentHead, DocumentViewLoader) (*DocumentView, error)
}
