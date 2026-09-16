package postgresql

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	domain "gin-backend/internal/modules/document/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrIndexDeliveryLeaseLost = errors.New("document index delivery store: lease lost")

func (repository *Repository) ClaimIndexDeliveryEvents(
	ctx context.Context,
	workerID string,
	limit int,
	leaseDuration time.Duration,
) ([]*domain.IndexDeliveryEvent, error) {
	if workerID == "" || limit <= 0 || leaseDuration <= 0 {
		return nil, errors.New("claim document index deliveries: invalid worker id, limit, or lease duration")
	}
	leaseSeconds := int64(math.Ceil(leaseDuration.Seconds()))
	leaseToken := uuid.NewString()
	var records []indexDeliveryEventRecord
	query := `
WITH candidates AS (
    SELECT candidate.event_id
      FROM document_index_delivery_events AS candidate
     WHERE (
              (candidate.state IN ('pending', 'retry') AND candidate.available_at <= clock_timestamp())
           OR (candidate.state = 'processing' AND candidate.lease_expires_at <= clock_timestamp())
           )
       AND NOT EXISTS (
            SELECT 1
              FROM document_index_delivery_events AS earlier
             WHERE earlier.document_id = candidate.document_id
               AND (earlier.aggregate_revision, earlier.created_at, earlier.event_id)
                   < (candidate.aggregate_revision, candidate.created_at, candidate.event_id)
               AND earlier.state <> 'succeeded'
       )
     ORDER BY candidate.aggregate_revision, candidate.created_at, candidate.event_id
     FOR UPDATE SKIP LOCKED
     LIMIT ?
)
UPDATE document_index_delivery_events AS event
   SET state = 'processing',
       attempt_count = event.attempt_count + 1,
       lease_owner = ?,
       lease_token = ?::uuid,
       lease_expires_at = clock_timestamp() + (? * interval '1 second'),
       last_attempt_at = clock_timestamp(),
       updated_at = clock_timestamp()
  FROM candidates
 WHERE event.event_id = candidates.event_id
RETURNING event.*`
	if err := repository.db.WithContext(ctx).Raw(query, limit, workerID, leaseToken, leaseSeconds).Scan(&records).Error; err != nil {
		return nil, fmt.Errorf("claim document index deliveries: %w", err)
	}
	events := make([]*domain.IndexDeliveryEvent, 0, len(records))
	for index := range records {
		event, err := indexDeliveryEventFromRecord(&records[index])
		if err != nil {
			return nil, fmt.Errorf("claim document index deliveries: %w", err)
		}
		events = append(events, event)
	}
	return events, nil
}

func (repository *Repository) RenewIndexDeliveryLease(
	ctx context.Context,
	eventID, workerID, leaseToken string,
	leaseDuration time.Duration,
) error {
	leaseSeconds := int64(math.Ceil(leaseDuration.Seconds()))
	result := repository.db.WithContext(ctx).Exec(`
UPDATE document_index_delivery_events
   SET lease_expires_at = clock_timestamp() + (? * interval '1 second'),
       updated_at = clock_timestamp()
 WHERE event_id = ?::uuid
   AND state = 'processing'
   AND lease_owner = ?
   AND lease_token = ?::uuid`, leaseSeconds, eventID, workerID, leaseToken)
	return requireDeliveryLease(result, "renew document index delivery lease")
}

func (repository *Repository) MarkIndexDeliverySucceeded(ctx context.Context, eventID, workerID, leaseToken string) error {
	result := repository.db.WithContext(ctx).Exec(`
UPDATE document_index_delivery_events
   SET state = 'succeeded',
       delivered_at = clock_timestamp(),
       lease_owner = NULL,
       lease_token = NULL,
       lease_expires_at = NULL,
       last_grpc_code = '',
       last_error = '',
       updated_at = clock_timestamp()
 WHERE event_id = ?::uuid
   AND state = 'processing'
   AND lease_owner = ?
   AND lease_token = ?::uuid`, eventID, workerID, leaseToken)
	return requireDeliveryLease(result, "mark document index delivery succeeded")
}

func (repository *Repository) RescheduleIndexDelivery(
	ctx context.Context,
	eventID, workerID, leaseToken string,
	nextAttempt time.Time,
	grpcCode, message string,
) error {
	result := repository.db.WithContext(ctx).Exec(`
UPDATE document_index_delivery_events
   SET state = 'retry',
       available_at = ?,
       lease_owner = NULL,
       lease_token = NULL,
       lease_expires_at = NULL,
       last_grpc_code = ?,
       last_error = ?,
       updated_at = clock_timestamp()
 WHERE event_id = ?::uuid
   AND state = 'processing'
   AND lease_owner = ?
   AND lease_token = ?::uuid`, nextAttempt.UTC(), grpcCode, message, eventID, workerID, leaseToken)
	return requireDeliveryLease(result, "reschedule document index delivery")
}

func (repository *Repository) MarkIndexDeliveryDeadLetter(
	ctx context.Context,
	eventID, workerID, leaseToken, grpcCode, message string,
) error {
	result := repository.db.WithContext(ctx).Exec(`
UPDATE document_index_delivery_events
   SET state = 'dead_letter',
       lease_owner = NULL,
       lease_token = NULL,
       lease_expires_at = NULL,
       last_grpc_code = ?,
       last_error = ?,
       updated_at = clock_timestamp()
 WHERE event_id = ?::uuid
   AND state = 'processing'
   AND lease_owner = ?
   AND lease_token = ?::uuid`, grpcCode, message, eventID, workerID, leaseToken)
	return requireDeliveryLease(result, "mark document index delivery dead letter")
}

func (repository *Repository) ListIndexDeliveryFailures(ctx context.Context, limit int) ([]*domain.IndexDeliveryEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	var records []indexDeliveryEventRecord
	if err := repository.db.WithContext(ctx).
		Where("state IN ?", []string{string(domain.IndexDeliveryRetry), string(domain.IndexDeliveryDeadLetter)}).
		Order("updated_at DESC").Limit(limit).Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list document index delivery failures: %w", err)
	}
	result := make([]*domain.IndexDeliveryEvent, 0, len(records))
	for index := range records {
		event, err := indexDeliveryEventFromRecord(&records[index])
		if err != nil {
			return nil, fmt.Errorf("list document index delivery failures: %w", err)
		}
		result = append(result, event)
	}
	return result, nil
}

func (repository *Repository) GetIndexDeliveryStats(ctx context.Context) (*domain.IndexDeliveryStats, error) {
	var stats domain.IndexDeliveryStats
	row := repository.db.WithContext(ctx).Raw(`
WITH delivery_stats AS (
    SELECT count(*) FILTER (WHERE state = 'pending') AS pending,
           count(*) FILTER (WHERE state = 'processing') AS processing,
           count(*) FILTER (WHERE state = 'retry') AS retry,
           count(*) FILTER (WHERE state = 'dead_letter') AS dead_letter,
           count(*) FILTER (WHERE state = 'succeeded') AS succeeded,
           count(*) FILTER (WHERE state = 'processing' AND lease_expires_at <= clock_timestamp()) AS expired_leases,
           COALESCE(sum(CASE
               WHEN state IN ('retry', 'dead_letter') THEN attempt_count
               ELSE greatest(attempt_count - 1, 0)
           END), 0) AS failed_attempts,
           min(created_at) FILTER (WHERE state <> 'succeeded') AS oldest_unfinished_at,
           max(delivered_at) AS last_delivered_at
      FROM document_index_delivery_events
), last_delivery AS (
    SELECT extract(epoch FROM (delivered_at - created_at)) * 1000 AS latency_millis
      FROM document_index_delivery_events
     WHERE delivered_at IS NOT NULL
     ORDER BY delivered_at DESC
     LIMIT 1
)
SELECT clock_timestamp(), pending, processing, retry, dead_letter, succeeded,
       expired_leases, failed_attempts, oldest_unfinished_at,
       COALESCE(extract(epoch FROM (clock_timestamp() - oldest_unfinished_at)), 0)::bigint,
       last_delivered_at, COALESCE((SELECT latency_millis FROM last_delivery), 0)::bigint
  FROM delivery_stats`).Row()
	if err := row.Scan(
		&stats.ObservedAt, &stats.Pending, &stats.Processing, &stats.Retry,
		&stats.DeadLetter, &stats.Succeeded, &stats.ExpiredLeases, &stats.FailedAttempts,
		&stats.OldestUnfinishedAt, &stats.OldestUnfinishedAgeSeconds,
		&stats.LastDeliveredAt, &stats.LastDeliveryLatencyMillis,
	); err != nil {
		return nil, fmt.Errorf("get document index delivery stats: %w", err)
	}
	return &stats, nil
}

func (repository *Repository) RequeueIndexDelivery(ctx context.Context, eventID string) error {
	result := repository.db.WithContext(ctx).Exec(`
UPDATE document_index_delivery_events
   SET state = 'retry',
       available_at = clock_timestamp(),
       lease_owner = NULL,
       lease_token = NULL,
       lease_expires_at = NULL,
       updated_at = clock_timestamp()
 WHERE event_id = ?::uuid
   AND state = 'dead_letter'`, eventID)
	if result.Error != nil {
		return fmt.Errorf("requeue document index delivery: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("requeue document index delivery: %w", domain.ErrNotFound)
	}
	return nil
}

func (repository *Repository) HasUnfinishedIndexDelivery(ctx context.Context, documentID string) (bool, error) {
	var count int64
	if err := repository.db.WithContext(ctx).Model(&indexDeliveryEventRecord{}).
		Where("document_id = ? AND state <> ?", documentID, string(domain.IndexDeliverySucceeded)).
		Count(&count).Error; err != nil {
		return false, fmt.Errorf("check unfinished document index delivery: %w", err)
	}
	return count > 0, nil
}

func (repository *Repository) CreateIndexRebuildRun(ctx context.Context, runID string) error {
	now := time.Now().UTC()
	record := &indexRebuildRunRecord{
		RunID: runID, State: string(domain.IndexRebuildPreparing), SnapshotStartedAt: now,
		LastError: "", CreatedAt: now, UpdatedAt: now,
	}
	if err := repository.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(record).Error; err != nil {
		return fmt.Errorf("create document index rebuild run: %w", err)
	}
	return nil
}

func (repository *Repository) FailIndexRebuildRun(ctx context.Context, runID, message string) error {
	result := repository.db.WithContext(ctx).Model(&indexRebuildRunRecord{}).
		Where("run_id = ?", runID).
		Updates(map[string]any{
			"state": string(domain.IndexRebuildFailed), "completed_at": nil,
			"last_error": message, "updated_at": time.Now().UTC(),
		})
	if result.Error != nil {
		return fmt.Errorf("fail document index rebuild run: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("fail document index rebuild run: %w", domain.ErrNotFound)
	}
	return nil
}

func (repository *Repository) RefreshIndexRebuildRun(ctx context.Context, runID string) (*domain.IndexRebuildRun, error) {
	var total, succeeded, failed int64
	row := repository.db.WithContext(ctx).Raw(`
SELECT count(*) AS total,
       count(*) FILTER (WHERE state = 'succeeded') AS succeeded,
       count(*) FILTER (WHERE state = 'dead_letter') AS failed
  FROM document_index_delivery_events
 WHERE source = 'rebuild' AND source_run_id = ?::uuid`, runID).Row()
	if err := row.Scan(&total, &succeeded, &failed); err != nil {
		return nil, fmt.Errorf("count document index rebuild deliveries: %w", err)
	}
	state := domain.IndexRebuildRunning
	var completedAt any
	if failed > 0 {
		state = domain.IndexRebuildFailed
	} else if succeeded == total {
		state = domain.IndexRebuildSucceeded
		completedAt = time.Now().UTC()
	}
	result := repository.db.WithContext(ctx).Model(&indexRebuildRunRecord{}).
		Where("run_id = ?", runID).
		Updates(map[string]any{
			"state": string(state), "event_count": total, "failure_count": failed,
			"completed_at": completedAt, "updated_at": time.Now().UTC(),
		})
	if result.Error != nil {
		return nil, fmt.Errorf("refresh document index rebuild run: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return nil, fmt.Errorf("refresh document index rebuild run: %w", domain.ErrNotFound)
	}
	return repository.GetIndexRebuildRun(ctx, runID)
}

func (repository *Repository) GetIndexRebuildRun(ctx context.Context, runID string) (*domain.IndexRebuildRun, error) {
	var record indexRebuildRunRecord
	if err := repository.db.WithContext(ctx).Where("run_id = ?", runID).First(&record).Error; err != nil {
		return nil, wrapRepositoryError("get document index rebuild run", err)
	}
	return &domain.IndexRebuildRun{
		RunID: record.RunID, State: domain.IndexRebuildState(record.State), SnapshotStartedAt: record.SnapshotStartedAt,
		CompletedAt: record.CompletedAt, EventCount: record.EventCount, FailureCount: record.FailureCount,
		LastError: record.LastError, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}, nil
}

func requireDeliveryLease(result *gorm.DB, operation string) error {
	if result.Error != nil {
		return fmt.Errorf("%s: %w", operation, result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("%s: %w", operation, ErrIndexDeliveryLeaseLost)
	}
	return nil
}
