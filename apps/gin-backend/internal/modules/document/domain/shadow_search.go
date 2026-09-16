package domain

import (
	"context"
	"time"
)

// DocumentSearchInput 是 gin-backend 调用 mixin-search 时使用的稳定领域输入。
// HTTP 查询语义仍由 gin-backend 负责，Protobuf 类型不会穿透此边界。
type DocumentSearchInput struct {
	Query              string
	AllowedSpaceIDs    []string
	AllowedDocumentIDs []string
	TopK               int
	// CallerUserID 是发起检索的最终用户，只用于 mixin-search 的调用审计，
	// 不参与授权判定：授权范围由 AllowedSpaceIDs/AllowedDocumentIDs 表达。
	// 0 表示未知。
	CallerUserID int64
}

type DocumentSearchHit struct {
	ChunkID      string
	DocumentID   string
	VersionID    string
	OwnerSpaceID string
	Position     int
	RRFScore     float64
	DenseRank    int
	SparseRank   int
	DenseScore   float64
	SparseScore  float64
}

type DocumentSearchResult struct {
	Query     string
	Hits      []DocumentSearchHit
	Truncated bool
}

type DocumentSearchClient interface {
	SearchDocuments(context.Context, DocumentSearchInput) (DocumentSearchResult, error)
}

// ShadowSearchFact 是复核候选授权、生命周期和活动版本所需的事实快照。
type ShadowSearchFact struct {
	DocumentID          string
	OwnerID             int64
	OwnerSpaceID        string
	ActiveVersionID     string
	LifecycleStatus     LifecycleStatus
	AuthenticatedPublic bool
}

type ShadowSearchObservationStatus string

const (
	ShadowSearchSucceeded ShadowSearchObservationStatus = "succeeded"
	ShadowSearchTimedOut  ShadowSearchObservationStatus = "timed_out"
	ShadowSearchFailed    ShadowSearchObservationStatus = "failed"
)

// ShadowSearchObservation 保存影子查询的可审计差异；查询正文只记录 SHA-256，
// 避免把用户搜索内容复制到观测表。
type ShadowSearchObservation struct {
	ObservationID               string
	Source                      string
	OwnerID                     int64
	QuerySHA256                 string
	QueryRuneCount              int
	Page                        int
	PageSize                    int
	RequestedTopK               int
	BM25Total                   int64
	BM25LatencyMicros           int64
	ShadowLatencyMicros         int64
	Status                      ShadowSearchObservationStatus
	GRPCCode                    string
	ErrorMessage                string
	Truncated                   bool
	BM25DocumentIDs             []string
	ShadowDocumentIDs           []string
	ComparableShadowDocumentIDs []string
	OnlyBM25DocumentIDs         []string
	OnlyShadowDocumentIDs       []string
	OverlapCount                int
	PermissionViolationCount    int
	LifecycleViolationCount     int
	ActiveVersionViolationCount int
	FormalScopeMismatchCount    int
	CreatedAt                   time.Time
}

type ShadowSearchStats struct {
	Total                      int64      `json:"total"`
	Succeeded                  int64      `json:"succeeded"`
	TimedOut                   int64      `json:"timedOut"`
	Failed                     int64      `json:"failed"`
	PermissionViolations       int64      `json:"permissionViolations"`
	LifecycleViolations        int64      `json:"lifecycleViolations"`
	ActiveVersionViolations    int64      `json:"activeVersionViolations"`
	FormalScopeMismatches      int64      `json:"formalScopeMismatches"`
	AverageBM25LatencyMicros   float64    `json:"averageBm25LatencyMicros"`
	AverageShadowLatencyMicros float64    `json:"averageShadowLatencyMicros"`
	LastObservedAt             *time.Time `json:"lastObservedAt,omitempty"`
}

type ShadowSearchStore interface {
	GetOwnerPrivateSpaceID(context.Context, int64) (string, error)
	GetShadowSearchFacts(context.Context, []string) (map[string]ShadowSearchFact, error)
	RecordShadowSearchObservation(context.Context, ShadowSearchObservation) error
	GetShadowSearchStats(context.Context, string) (ShadowSearchStats, error)
}
