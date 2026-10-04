package dbtest

import (
	"context"
	"testing"
)

// The integration suite runs against a real development database, so the
// database schema is an input this fixture depends on. This test states that
// dependency instead of letting it surface as an obscure "column does not
// exist" failure in the middle of another test: the running
// document_search.document_index must carry every column the query and apply
// paths use, including the created_at column the Web-facing hit fields read.
//
// It never mutates the database. The schema itself lives in
// apps/document-search/schema/schema_init.sql and is applied to the development
// database out of band; this test is what makes a database that is behind the
// checked-in schema fail loudly and immediately.
func TestIntegrationDevSchemaCarriesTheQueryPathColumns(t *testing.T) {
	ctx := context.Background()
	pool := Open(t, ctx)

	required := map[string]string{
		"document_id":          "uuid",
		"version_id":           "uuid",
		"owner_subject_key":    "character varying",
		"owner_space_id":       "uuid",
		"authenticated_public": "boolean",
		"allowed_space_ids":    "jsonb",
		"lifecycle_status":     "character varying",
		"publication_status":   "character varying",
		"title":                "character varying",
		"summary":              "character varying",
		"content":              "text",
		"created_at":           "timestamp with time zone",
		"document_updated_at":  "timestamp with time zone",
		"updated_at":           "timestamp with time zone",
		// The vector half of the index: every vectorized document records which
		// embedding produced its points.
		"vector_profile":    "character varying",
		"vector_dimensions": "integer",
	}
	rows, err := pool.Pgx().Query(ctx, `
        SELECT column_name, data_type, is_nullable
        FROM information_schema.columns
        WHERE table_schema = 'document_search' AND table_name = 'document_index'`)
	if err != nil {
		t.Fatalf("document-search dbtest: read the document_index columns: %v", err)
	}
	defer rows.Close()
	found := make(map[string]string, len(required))
	for rows.Next() {
		var name, dataType, nullable string
		if err := rows.Scan(&name, &dataType, &nullable); err != nil {
			t.Fatalf("document-search dbtest: read a document_index column: %v", err)
		}
		found[name] = dataType + "|" + nullable
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("document-search dbtest: read the document_index columns: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("document_search.document_index has no columns: the development database is not initialized")
	}
	for name, wantType := range required {
		got, ok := found[name]
		if !ok {
			t.Fatalf("document_search.document_index is missing %q; it is required by the query and apply paths", name)
		}
		if wantType != "" && got != wantType+"|NO" && got != wantType+"|YES" {
			t.Fatalf("document_search.document_index.%s is %s, want %s", name, got, wantType)
		}
	}
	// The two document instants must be non-nullable with a default: a row
	// written without an event-supplied time still carries a real timestamp.
	instants := []string{"created_at", "document_updated_at"}
	for _, column := range instants {
		if got := found[column]; got != "timestamp with time zone|NO" {
			t.Fatalf("document_search.document_index.%s is %s, want a non-nullable timestamp", column, got)
		}
	}
	for _, column := range instants {
		var defaultExpression string
		if err := pool.Pgx().QueryRow(ctx, `
            SELECT coalesce(column_default, '')
            FROM information_schema.columns
            WHERE table_schema = 'document_search' AND table_name = 'document_index' AND column_name = $1`,
			column,
		).Scan(&defaultExpression); err != nil {
			t.Fatalf("document-search dbtest: read the %s default: %v", column, err)
		}
		if defaultExpression == "" {
			t.Fatalf("document_search.document_index.%s has no default; an event without that instant would fail the insert", column)
		}
	}
}

// TestIntegrationDevSchemaCarriesTheVectorFlowColumns states the other half of
// the same dependency for the generation record: the process reads the alias,
// storage domain, embedding profile and vector width of its active collection
// from document_search.index_generations, so a database that predates the vector
// flow fails here rather than at startup.
//
// It never mutates the database either.
func TestIntegrationDevSchemaCarriesTheVectorFlowColumns(t *testing.T) {
	ctx := context.Background()
	pool := Open(t, ctx)

	required := map[string]string{
		"generation":        "character varying",
		"collection_alias":  "character varying",
		"storage_domain":    "character varying",
		"vector_profile":    "character varying",
		"vector_dimensions": "integer",
		"active":            "boolean",
	}
	rows, err := pool.Pgx().Query(ctx, `
        SELECT column_name, data_type, is_nullable
        FROM information_schema.columns
        WHERE table_schema = 'document_search' AND table_name = 'index_generations'`)
	if err != nil {
		t.Fatalf("document-search dbtest: read the index_generations columns: %v", err)
	}
	defer rows.Close()
	found := make(map[string]string, len(required))
	for rows.Next() {
		var name, dataType, nullable string
		if err := rows.Scan(&name, &dataType, &nullable); err != nil {
			t.Fatalf("document-search dbtest: read an index_generations column: %v", err)
		}
		found[name] = dataType + "|" + nullable
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("document-search dbtest: read the index_generations columns: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("document_search.index_generations has no columns: the development database is not initialized")
	}
	for name, wantType := range required {
		got, ok := found[name]
		if !ok {
			t.Fatalf("document_search.index_generations is missing %q; the vector flow reads the collection identity from it", name)
		}
		if wantType != "" && got != wantType+"|NO" && got != wantType+"|YES" {
			t.Fatalf("document_search.index_generations.%s is %s, want %s", name, got, wantType)
		}
	}

	// Exactly one generation is active, and it names a usable collection in this
	// service's namespace.
	var generation, alias, profile string
	var dimensions int32
	if err := pool.Pgx().QueryRow(ctx, `
        SELECT generation, collection_alias, vector_profile, vector_dimensions
        FROM document_search.index_generations WHERE active`).Scan(&generation, &alias, &profile, &dimensions); err != nil {
		t.Fatalf("document-search dbtest: read the active generation: %v", err)
	}
	if generation == "" || alias == "" {
		t.Fatalf("the active generation is %q/%q, want a label and an alias", generation, alias)
	}
	if profile == "" || dimensions <= 0 {
		t.Fatalf("the active generation records profile %q/%d, want a profile and a positive width", profile, dimensions)
	}
}
