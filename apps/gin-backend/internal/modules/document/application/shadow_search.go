package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gin-backend/internal/modules/document/domain"

	"github.com/google/uuid"
)

type ShadowSearchRequest struct {
	Source          string
	OwnerID         int64
	Query           string
	Page            int
	PageSize        int
	BM25Total       int64
	BM25Latency     time.Duration
	BM25DocumentIDs []string
}

// ShadowSearchScheduler 是正式查询服务使用的非阻塞提交端口。
type ShadowSearchScheduler interface {
	Observe(ShadowSearchRequest) bool
}

type ShadowSearchObserverConfig struct {
	QueueSize     int
	Concurrency   int
	Timeout       time.Duration
	RecordTimeout time.Duration
	TopK          int
}

func DefaultShadowSearchObserverConfig() ShadowSearchObserverConfig {
	return ShadowSearchObserverConfig{
		QueueSize: 256, Concurrency: 4, Timeout: 750 * time.Millisecond,
		RecordTimeout: 2 * time.Second, TopK: 50,
	}
}

// ShadowSearchObserver 在正式 BM25 查询完成后异步执行 mixin-search，并把
// 差异和正确性复核写入 PostgreSQL。队列满时直接丢弃观测，不反压 HTTP。
type ShadowSearchObserver struct {
	store  domain.ShadowSearchStore
	client domain.DocumentSearchClient
	config ShadowSearchObserverConfig
	jobs   chan ShadowSearchRequest
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.RWMutex
	closed bool
	wait   sync.WaitGroup
}

func NewShadowSearchObserver(
	store domain.ShadowSearchStore,
	client domain.DocumentSearchClient,
	config ShadowSearchObserverConfig,
) (*ShadowSearchObserver, error) {
	if store == nil || client == nil {
		return nil, errors.New("document shadow search: store and client are required")
	}
	defaults := DefaultShadowSearchObserverConfig()
	if config.QueueSize <= 0 {
		config.QueueSize = defaults.QueueSize
	}
	if config.Concurrency <= 0 {
		config.Concurrency = defaults.Concurrency
	}
	if config.Timeout <= 0 {
		config.Timeout = defaults.Timeout
	}
	if config.RecordTimeout <= 0 {
		config.RecordTimeout = defaults.RecordTimeout
	}
	if config.TopK <= 0 || config.TopK > 100 {
		config.TopK = defaults.TopK
	}
	ctx, cancel := context.WithCancel(context.Background())
	observer := &ShadowSearchObserver{
		store: store, client: client, config: config, jobs: make(chan ShadowSearchRequest, config.QueueSize),
		ctx: ctx, cancel: cancel,
	}
	for range config.Concurrency {
		observer.wait.Go(observer.run)
	}
	return observer, nil
}

func (observer *ShadowSearchObserver) Observe(request ShadowSearchRequest) bool {
	request.Query = strings.TrimSpace(request.Query)
	request.BM25DocumentIDs = append([]string{}, request.BM25DocumentIDs...)
	observer.mu.RLock()
	defer observer.mu.RUnlock()
	if observer.closed {
		return false
	}
	select {
	case observer.jobs <- request:
		return true
	default:
		slog.Warn("document_shadow_search_dropped",
			slog.String("reason", "queue_full"), slog.Int64("owner_id", request.OwnerID),
			slog.String("query_sha256", querySHA256(request.Query)))
		return false
	}
}

func (observer *ShadowSearchObserver) Close() {
	if observer == nil {
		return
	}
	observer.mu.Lock()
	if observer.closed {
		observer.mu.Unlock()
		return
	}
	observer.closed = true
	close(observer.jobs)
	observer.mu.Unlock()
	observer.wait.Wait()
	observer.cancel()
}

func (observer *ShadowSearchObserver) run() {
	for request := range observer.jobs {
		observer.execute(request)
	}
}

func (observer *ShadowSearchObserver) execute(request ShadowSearchRequest) {
	startedAt := time.Now()
	observation := domain.ShadowSearchObservation{
		ObservationID: uuid.NewString(), Source: normalizedShadowSource(request.Source), OwnerID: request.OwnerID,
		QuerySHA256: querySHA256(request.Query), QueryRuneCount: utf8.RuneCountInString(request.Query),
		Page: request.Page, PageSize: request.PageSize, BM25Total: request.BM25Total,
		BM25LatencyMicros: request.BM25Latency.Microseconds(), BM25DocumentIDs: request.BM25DocumentIDs,
		Status: domain.ShadowSearchSucceeded, CreatedAt: time.Now().UTC(),
	}
	requestedTopK := observer.config.TopK
	if needed := request.Page * request.PageSize; needed > requestedTopK {
		requestedTopK = needed
	}
	if requestedTopK > 100 {
		requestedTopK = 100
	}
	observation.RequestedTopK = requestedTopK

	callCtx, cancel := context.WithTimeout(observer.ctx, observer.config.Timeout)
	spaceID, err := observer.store.GetOwnerPrivateSpaceID(callCtx, request.OwnerID)
	if err == nil {
		var result domain.DocumentSearchResult
		result, err = observer.client.SearchDocuments(callCtx, domain.DocumentSearchInput{
			Query: request.Query, AllowedSpaceIDs: []string{spaceID}, TopK: requestedTopK,
			CallerUserID: request.OwnerID,
		})
		if err == nil {
			observation.Truncated = result.Truncated
			observer.compare(callCtx, request, spaceID, result.Hits, &observation)
		}
	}
	cancel()
	observation.ShadowLatencyMicros = time.Since(startedAt).Microseconds()
	if err != nil {
		observation.Status, observation.GRPCCode = shadowFailure(err)
		observation.ErrorMessage = shadowErrorMessage(err)
	}

	recordCtx, recordCancel := context.WithTimeout(context.Background(), observer.config.RecordTimeout)
	recordErr := observer.store.RecordShadowSearchObservation(recordCtx, observation)
	recordCancel()
	if recordErr != nil {
		slog.Error("document_shadow_search_record_failed", slog.String("observation_id", observation.ObservationID), slog.String("error", recordErr.Error()))
		return
	}
	slog.Info("document_shadow_search_observed",
		slog.String("observation_id", observation.ObservationID), slog.String("status", string(observation.Status)),
		slog.String("query_sha256", observation.QuerySHA256), slog.Int("overlap", observation.OverlapCount),
		slog.Int("permission_violations", observation.PermissionViolationCount),
		slog.Int("lifecycle_violations", observation.LifecycleViolationCount),
		slog.Int("active_version_violations", observation.ActiveVersionViolationCount),
		slog.Int("formal_scope_mismatches", observation.FormalScopeMismatchCount),
		slog.Int64("bm25_latency_us", observation.BM25LatencyMicros),
		slog.Int64("shadow_latency_us", observation.ShadowLatencyMicros), slog.String("grpc_code", observation.GRPCCode))
}

func (observer *ShadowSearchObserver) compare(
	ctx context.Context,
	request ShadowSearchRequest,
	allowedSpaceID string,
	hits []domain.DocumentSearchHit,
	observation *domain.ShadowSearchObservation,
) {
	uniqueHits := deduplicateShadowHits(hits)
	ids := make([]string, 0, len(uniqueHits))
	for _, hit := range uniqueHits {
		ids = append(ids, hit.DocumentID)
	}
	observation.ShadowDocumentIDs = ids
	facts, err := observer.store.GetShadowSearchFacts(ctx, ids)
	if err != nil {
		observation.Status, observation.GRPCCode = shadowFailure(err)
		observation.ErrorMessage = shadowErrorMessage(err)
		return
	}
	comparable := make([]string, 0, len(uniqueHits))
	for _, hit := range uniqueHits {
		fact, exists := facts[hit.DocumentID]
		if !exists || fact.LifecycleStatus != domain.LifecycleActive {
			observation.LifecycleViolationCount++
			continue
		}
		if fact.ActiveVersionID != hit.VersionID {
			observation.ActiveVersionViolationCount++
			continue
		}
		if !fact.AuthenticatedPublic && fact.OwnerSpaceID != allowedSpaceID {
			observation.PermissionViolationCount++
			continue
		}
		if fact.OwnerID != request.OwnerID {
			observation.FormalScopeMismatchCount++
			continue
		}
		comparable = append(comparable, hit.DocumentID)
	}
	start := (request.Page - 1) * request.PageSize
	if start < 0 {
		start = 0
	}
	if start > len(comparable) {
		start = len(comparable)
	}
	end := start + request.PageSize
	if end > len(comparable) {
		end = len(comparable)
	}
	observation.ComparableShadowDocumentIDs = append([]string{}, comparable[start:end]...)
	observation.OverlapCount, observation.OnlyBM25DocumentIDs, observation.OnlyShadowDocumentIDs = compareDocumentIDs(
		request.BM25DocumentIDs, observation.ComparableShadowDocumentIDs,
	)
}

func deduplicateShadowHits(hits []domain.DocumentSearchHit) []domain.DocumentSearchHit {
	seen := make(map[string]struct{}, len(hits))
	result := make([]domain.DocumentSearchHit, 0, len(hits))
	for _, hit := range hits {
		if hit.DocumentID == "" {
			continue
		}
		if _, exists := seen[hit.DocumentID]; exists {
			continue
		}
		seen[hit.DocumentID] = struct{}{}
		result = append(result, hit)
	}
	return result
}

func compareDocumentIDs(left, right []string) (int, []string, []string) {
	leftSet := make(map[string]struct{}, len(left))
	rightSet := make(map[string]struct{}, len(right))
	for _, value := range left {
		leftSet[value] = struct{}{}
	}
	for _, value := range right {
		rightSet[value] = struct{}{}
	}
	overlap := 0
	onlyLeft := make([]string, 0)
	onlyRight := make([]string, 0)
	for _, value := range left {
		if _, exists := rightSet[value]; exists {
			overlap++
		} else {
			onlyLeft = append(onlyLeft, value)
		}
	}
	for _, value := range right {
		if _, exists := leftSet[value]; !exists {
			onlyRight = append(onlyRight, value)
		}
	}
	return overlap, onlyLeft, onlyRight
}

func shadowFailure(err error) (domain.ShadowSearchObservationStatus, string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.ShadowSearchTimedOut, "DeadlineExceeded"
	}
	var remote *domain.RemoteIndexError
	if errors.As(err, &remote) {
		if remote.Code == "DeadlineExceeded" {
			return domain.ShadowSearchTimedOut, remote.Code
		}
		return domain.ShadowSearchFailed, remote.Code
	}
	return domain.ShadowSearchFailed, "Local"
}

func querySHA256(query string) string {
	sum := sha256.Sum256([]byte(query))
	return hex.EncodeToString(sum[:])
}

func normalizedShadowSource(source string) string {
	if strings.TrimSpace(source) == "evaluation" {
		return "evaluation"
	}
	return "runtime"
}

func shadowErrorMessage(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "shadow query deadline exceeded"
	}
	var remote *domain.RemoteIndexError
	if errors.As(err, &remote) {
		return "mixin-search " + remote.Code
	}
	return "shadow query failed"
}
