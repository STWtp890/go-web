package postgresql

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gin-backend/internal/modules/document/domain"
)

var _ domain.ShadowSearchStore = (*Repository)(nil)

type shadowSearchFactRow struct {
	DocumentID          string  `gorm:"column:document_id"`
	OwnerID             int64   `gorm:"column:owner_id"`
	OwnerSpaceID        string  `gorm:"column:owner_space_id"`
	ActiveVersionID     *string `gorm:"column:active_version_id"`
	LifecycleStatus     string  `gorm:"column:lifecycle_status"`
	AuthenticatedPublic bool    `gorm:"column:authenticated_public"`
}

func (repository *Repository) GetOwnerPrivateSpaceID(ctx context.Context, ownerID int64) (string, error) {
	var spaceID string
	err := repository.db.WithContext(ctx).
		Table("knowledge_spaces").
		Select("space_id").
		Where("owner_id = ? AND space_type = ?", ownerID, string(domain.SpaceTypePrivate)).
		Take(&spaceID).Error
	if err != nil {
		return "", wrapRepositoryError("get owner private space", err)
	}
	return spaceID, nil
}

func (repository *Repository) GetShadowSearchFacts(ctx context.Context, documentIDs []string) (map[string]domain.ShadowSearchFact, error) {
	result := make(map[string]domain.ShadowSearchFact, len(documentIDs))
	if len(documentIDs) == 0 {
		return result, nil
	}
	var rows []shadowSearchFactRow
	err := repository.db.WithContext(ctx).
		Table("documents AS document").
		Select(`document.document_id, document.owner_id, document.owner_space_id,
document.active_version_id, document.lifecycle_status, policy.authenticated_public`).
		Joins("JOIN document_access_policies AS policy ON policy.document_id = document.document_id").
		Where("document.document_id IN ?", documentIDs).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("get shadow search facts: %w", err)
	}
	for _, row := range rows {
		activeVersionID := ""
		if row.ActiveVersionID != nil {
			activeVersionID = *row.ActiveVersionID
		}
		result[row.DocumentID] = domain.ShadowSearchFact{
			DocumentID: row.DocumentID, OwnerID: row.OwnerID, OwnerSpaceID: row.OwnerSpaceID,
			ActiveVersionID: activeVersionID, LifecycleStatus: domain.LifecycleStatus(row.LifecycleStatus),
			AuthenticatedPublic: row.AuthenticatedPublic,
		}
	}
	return result, nil
}

func (repository *Repository) RecordShadowSearchObservation(ctx context.Context, observation domain.ShadowSearchObservation) error {
	encoded := make([]string, 5)
	values := [][]string{
		observation.BM25DocumentIDs,
		observation.ShadowDocumentIDs,
		observation.ComparableShadowDocumentIDs,
		observation.OnlyBM25DocumentIDs,
		observation.OnlyShadowDocumentIDs,
	}
	for index, value := range values {
		if value == nil {
			value = []string{}
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode shadow search observation document ids: %w", err)
		}
		encoded[index] = string(raw)
	}
	createdAt := observation.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	err := repository.db.WithContext(ctx).Exec(`
INSERT INTO document_search_shadow_observations (
    observation_id, source, owner_id, query_sha256, query_rune_count,
    page, page_size, requested_top_k, bm25_total, bm25_latency_micros,
    shadow_latency_micros, status, grpc_code, error_message, truncated,
    bm25_document_ids, shadow_document_ids, comparable_shadow_document_ids,
    only_bm25_document_ids, only_shadow_document_ids, overlap_count,
    permission_violation_count, lifecycle_violation_count,
    active_version_violation_count, formal_scope_mismatch_count, created_at
) VALUES (
    ?::uuid, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
    ?::jsonb, ?::jsonb, ?::jsonb, ?::jsonb, ?::jsonb, ?, ?, ?, ?, ?, ?
)`,
		observation.ObservationID, observation.Source, observation.OwnerID,
		observation.QuerySHA256, observation.QueryRuneCount, observation.Page,
		observation.PageSize, observation.RequestedTopK, observation.BM25Total,
		observation.BM25LatencyMicros, observation.ShadowLatencyMicros,
		string(observation.Status), observation.GRPCCode, observation.ErrorMessage,
		observation.Truncated, encoded[0], encoded[1], encoded[2], encoded[3], encoded[4],
		observation.OverlapCount, observation.PermissionViolationCount,
		observation.LifecycleViolationCount, observation.ActiveVersionViolationCount,
		observation.FormalScopeMismatchCount, createdAt,
	).Error
	if err != nil {
		return fmt.Errorf("record shadow search observation: %w", err)
	}
	return nil
}

func (repository *Repository) GetShadowSearchStats(ctx context.Context, source string) (domain.ShadowSearchStats, error) {
	var stats domain.ShadowSearchStats
	row := repository.db.WithContext(ctx).Raw(`
SELECT count(*) AS total,
       count(*) FILTER (WHERE status = 'succeeded') AS succeeded,
       count(*) FILTER (WHERE status = 'timed_out') AS timed_out,
       count(*) FILTER (WHERE status = 'failed') AS failed,
       coalesce(sum(permission_violation_count), 0) AS permission_violations,
       coalesce(sum(lifecycle_violation_count), 0) AS lifecycle_violations,
       coalesce(sum(active_version_violation_count), 0) AS active_version_violations,
       coalesce(sum(formal_scope_mismatch_count), 0) AS formal_scope_mismatches,
       coalesce(avg(bm25_latency_micros), 0) AS average_bm25_latency_micros,
       coalesce(avg(shadow_latency_micros), 0) AS average_shadow_latency_micros,
       max(created_at) AS last_observed_at
  FROM document_search_shadow_observations
 WHERE (? = '' OR source = ?)`, source, source).Row()
	if err := row.Scan(
		&stats.Total, &stats.Succeeded, &stats.TimedOut, &stats.Failed,
		&stats.PermissionViolations, &stats.LifecycleViolations,
		&stats.ActiveVersionViolations, &stats.FormalScopeMismatches,
		&stats.AverageBM25LatencyMicros, &stats.AverageShadowLatencyMicros,
		&stats.LastObservedAt,
	); err != nil {
		return domain.ShadowSearchStats{}, fmt.Errorf("get shadow search stats: %w", err)
	}
	return stats, nil
}
