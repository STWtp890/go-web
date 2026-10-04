package postgres

import (
	"context"
	"errors"

	"document-service/internal/domain"

	"github.com/jackc/pgx/v5"
)

// maxSubjectPage bounds a registry listing that did not name a subject. The
// registry is an operational read, not a bulk export.
const maxSubjectPage = 200

const insertSubjectSQL = `
INSERT INTO document_service.access_subjects (subject_key, subject_type, origin, display_name)
VALUES ($1, $2, $3, $4)
ON CONFLICT (subject_key) DO NOTHING
RETURNING subject_key`

// RegisterSubject inserts a subject the first time a trusted entry point
// declares it and reports whether the row was created. Registration records that
// the subject exists; it grants no space membership and no document permission.
func (store *Store) RegisterSubject(ctx context.Context, tx pgx.Tx, subject domain.Subject) (bool, error) {
	var created string
	err := tx.QueryRow(ctx, insertSubjectSQL,
		subject.SubjectKey, subject.SubjectType, subject.Origin, subject.DisplayName,
	).Scan(&created)
	if errors.Is(err, pgx.ErrNoRows) {
		// ON CONFLICT DO NOTHING returned no row: the subject was already known.
		return false, nil
	}
	if err != nil {
		return false, mapError(err)
	}
	return true, nil
}

const selectSubjectSQL = `
SELECT subject_key, subject_type, origin, display_name, active, created_at, updated_at
FROM document_service.access_subjects
WHERE subject_key = $1`

// GetSubject reads one registered subject. A missing subject is ErrNotFound, not
// an inactive one: the resolver has to tell "never seen" from "deactivated".
func (store *Store) GetSubject(ctx context.Context, tx pgx.Tx, subjectKey string) (domain.Subject, error) {
	return scanSubject(tx.QueryRow(ctx, selectSubjectSQL, subjectKey))
}

// LockSubject reads one registered subject FOR UPDATE so a caller can decide
// based on a value no concurrent transaction can change underneath it.
func (store *Store) LockSubject(ctx context.Context, tx pgx.Tx, subjectKey string) (domain.Subject, error) {
	return scanSubject(tx.QueryRow(ctx, selectSubjectSQL+" FOR UPDATE", subjectKey))
}

type subjectScanner interface {
	Scan(dest ...any) error
}

func scanSubject(row subjectScanner) (domain.Subject, error) {
	var subject domain.Subject
	if err := row.Scan(
		&subject.SubjectKey, &subject.SubjectType, &subject.Origin,
		&subject.DisplayName, &subject.Active, &subject.CreatedAt, &subject.UpdatedAt,
	); err != nil {
		return domain.Subject{}, mapError(err)
	}
	return subject, nil
}

const listSubjectsSQL = `
SELECT subject_key, subject_type, origin, display_name, active, created_at, updated_at
FROM document_service.access_subjects
WHERE ($1::text IS NULL OR subject_key = $1)
ORDER BY subject_key
LIMIT $2`

// ListSubjects reads the registry, either one exact key or a bounded page in key
// order.
func (store *Store) ListSubjects(ctx context.Context, tx pgx.Tx, subjectKey string, limit int) ([]domain.Subject, error) {
	if limit <= 0 || limit > maxSubjectPage {
		limit = maxSubjectPage
	}
	rows, err := tx.Query(ctx, listSubjectsSQL, nullableText(subjectKey), limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	subjects := make([]domain.Subject, 0, 8)
	for rows.Next() {
		subject, err := scanSubject(rows)
		if err != nil {
			return nil, err
		}
		subjects = append(subjects, subject)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return subjects, nil
}
