package domain

import "time"

// IndexDeliveryKind 表示一次已提交文档变更需要在派生索引执行的动作。
// sync_document 会按 index -> access -> activate 的顺序投递。
type IndexDeliveryKind string

const (
	IndexDeliverySyncDocument   IndexDeliveryKind = "sync_document"
	IndexDeliverySyncAccess     IndexDeliveryKind = "sync_access"
	IndexDeliveryDeleteVersion  IndexDeliveryKind = "delete_version"
	IndexDeliveryDeleteDocument IndexDeliveryKind = "delete_document"
)

type IndexDeliverySource string

const (
	IndexDeliverySourceTransaction IndexDeliverySource = "transaction"
	IndexDeliverySourceReconcile   IndexDeliverySource = "reconcile"
	IndexDeliverySourceRebuild     IndexDeliverySource = "rebuild"
)

type IndexDeliveryState string

const (
	IndexDeliveryPending    IndexDeliveryState = "pending"
	IndexDeliveryProcessing IndexDeliveryState = "processing"
	IndexDeliveryRetry      IndexDeliveryState = "retry"
	IndexDeliverySucceeded  IndexDeliveryState = "succeeded"
	IndexDeliveryDeadLetter IndexDeliveryState = "dead_letter"
)

const DefaultIndexProfile = "markdown-v1"

// IndexDeliveryEvent 是事务型 Outbox 中的不可变投递快照。
// 正文不复制到事件中，Worker 通过 VersionID 读取不可变版本内容；访问快照
// 必须保存在事件里，保证超时重试时 operation_id 与 payload 始终一致。
type IndexDeliveryEvent struct {
	EventID             string
	DedupeKey           string
	Source              IndexDeliverySource
	SourceRunID         *string
	DocumentID          string
	AggregateRevision   int64
	Kind                IndexDeliveryKind
	VersionID           *string
	PreviousVersionID   *string
	OwnerSpaceID        string
	ActivationRevision  int64
	AccessRevision      int64
	LifecycleRevision   int64
	AuthenticatedPublic bool
	GrantedSpaceIDs     []string
	ContentSHA256       string
	IndexProfile        string
	State               IndexDeliveryState
	AttemptCount        int
	AvailableAt         time.Time
	LeaseOwner          *string
	LeaseToken          *string
	LeaseExpiresAt      *time.Time
	LastGRPCCode        string
	LastError           string
	LastAttemptAt       *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	DeliveredAt         *time.Time
}

type IndexDocumentSnapshot struct {
	Document        *Document
	Version         *DocumentVersion
	Policy          *AccessPolicy
	GrantedSpaceIDs []string
}

type IndexRebuildState string

const (
	IndexRebuildPreparing IndexRebuildState = "preparing"
	IndexRebuildRunning   IndexRebuildState = "running"
	IndexRebuildSucceeded IndexRebuildState = "succeeded"
	IndexRebuildFailed    IndexRebuildState = "failed"
)

type IndexRebuildRun struct {
	RunID             string
	State             IndexRebuildState
	SnapshotStartedAt time.Time
	CompletedAt       *time.Time
	EventCount        int64
	FailureCount      int64
	LastError         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// IndexDeliveryStats 是运维状态投影，不参与正式查询或投递决策。
type IndexDeliveryStats struct {
	ObservedAt                 time.Time  `json:"observedAt"`
	Pending                    int64      `json:"pending"`
	Processing                 int64      `json:"processing"`
	Retry                      int64      `json:"retry"`
	DeadLetter                 int64      `json:"deadLetter"`
	Succeeded                  int64      `json:"succeeded"`
	ExpiredLeases              int64      `json:"expiredLeases"`
	FailedAttempts             int64      `json:"failedAttempts"`
	OldestUnfinishedAt         *time.Time `json:"oldestUnfinishedAt,omitempty"`
	OldestUnfinishedAgeSeconds int64      `json:"oldestUnfinishedAgeSeconds"`
	LastDeliveredAt            *time.Time `json:"lastDeliveredAt,omitempty"`
	LastDeliveryLatencyMillis  int64      `json:"lastDeliveryLatencyMillis"`
}

func (event *IndexDeliveryEvent) OperationID(step string) string {
	return event.EventID + "/" + step
}
