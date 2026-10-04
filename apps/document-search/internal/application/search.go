package application

import (
	"context"
	"fmt"
	"strings"

	documentsearchv1 "packages/gen/documentsearch/v1"
	"packages/serviceauth"
)

// The query path is deliberately self-contained: it reads only
// document_search.document_index and never talks to document-service, go-web or
// py-agent. The authorization ceiling comes from the capability the fact source
// already signed; the range actually searched is the request's, after it has
// been checked to be a subset of that ceiling. internal/architecture asserts
// that this file (and everything it calls) imports no source client.

// searchSourceName is the corpus label every hit carries. A future combined
// query service merges labelled results, so the label is part of the answer.
const searchSourceName = "document"

// snippetLength bounds the returned excerpt, in characters.
const snippetLength = 240

// maxSearchTotal bounds the int32 total without importing math: a result set
// larger than int32 can express is reported saturated, never negative.
const maxSearchTotal = int64(1)<<31 - 1

// searchScope is the resolved filter the SQL applies. It is derived from the
// capability (the authorization ceiling) and the request (what the caller
// actually asked for), never from the request body alone.
type searchScope struct {
	// SpaceIDs and DocumentIDs are the identifier families that may match. Both
	// are non-nil: an empty family matches nothing, which is the fail-closed
	// direction.
	SpaceIDs    []string
	DocumentIDs []string
	// IncludePublic turns the authenticated-public floor on. It is part of the
	// granted envelope and applies only when the request named no range at all.
	IncludePublic bool
	// OwnedBySubject restricts the answer to the capability's own subject, and
	// OwnerSubjectKey is that subject. It is empty when the caller did not ask
	// for a personal search.
	OwnedBySubject  bool
	OwnerSubjectKey string
}

// effectiveSearchScope turns "what was granted" and "what was asked for" into
// the exact sets the query filters on.
//
// This is where the previous implementation was wrong: it validated the request
// against the capability and then filtered by the capability anyway, so a
// request that named space A still returned everything granted. The rules are:
//
//   - The capability is the authorization ceiling. Every requested identifier
//     was already checked against it before this function is called.
//   - A named family is the actual filter for that family. Once the caller names
//     a space or a document, the answer is exactly the named set: the unnamed
//     family contributes nothing, because falling back to the granted family
//     would let "only space A" return a document-level grant outside A - the
//     same authorized-but-not-requested leak in another shape.
//   - Nothing named means the whole granted envelope, including the
//     authenticated-public floor.
//   - `owned_by_subject_only` is an ownership filter, not a range: it restricts
//     whatever range applies to documents the capability's subject owns.
func effectiveSearchScope(
	capability *serviceauth.CapabilityClaims,
	grantedSpaces, grantedDocuments, requestedSpaces, requestedDocuments []string,
	ownedBySubjectOnly bool,
) searchScope {
	scope := searchScope{SpaceIDs: []string{}, DocumentIDs: []string{}}
	if len(requestedSpaces) > 0 || len(requestedDocuments) > 0 {
		// The caller named a range: that range is the answer. The
		// authenticated-public floor is part of the granted envelope, so it does
		// not widen a named range.
		scope.SpaceIDs = append(scope.SpaceIDs, uuidIDs(requestedSpaces)...)
		scope.DocumentIDs = append(scope.DocumentIDs, uuidIDs(requestedDocuments)...)
	} else {
		// Only identifiers that can match an indexed row reach SQL. A granted
		// value that is not a UUID cannot match anything, so dropping it
		// narrows the effective range, which is the fail-closed direction.
		scope.SpaceIDs = append(scope.SpaceIDs, uuidIDs(grantedSpaces)...)
		scope.DocumentIDs = append(scope.DocumentIDs, uuidIDs(grantedDocuments)...)
		// The floor is part of every valid capability. The credential contract
		// defines an empty envelope as "authenticated-public only", so the
		// capability's authenticated_public flag records which family the fact
		// source resolved rather than switching the floor off. What the request
		// controls is whether the floor applies at all: naming a range replaces
		// the envelope, so a narrowed request never falls back to it.
		scope.IncludePublic = true
	}
	if ownedBySubjectOnly {
		scope.OwnedBySubject = true
		if capability != nil {
			scope.OwnerSubjectKey = strings.TrimSpace(capability.SubjectKey)
		}
	}
	return scope
}

// searchDocuments answers a query inside the range the caller asked for, which
// must be a subset of what the capability granted. Any identifier outside the
// grant rejects the whole request, and nothing is ever trimmed silently.
func (service *Service) searchDocuments(
	ctx context.Context,
	capability *serviceauth.CapabilityClaims,
	request *documentsearchv1.SearchDocumentsRequest,
) (*documentsearchv1.SearchDocumentsResponse, error) {
	if request == nil {
		return nil, grpcError(fmt.Errorf("%w: request is required", ErrInvalidInput))
	}
	query := strings.TrimSpace(request.GetQuery())
	if query == "" {
		return nil, grpcError(ErrMissingQuery)
	}
	if request.GetPageSize() < 0 || request.GetPage() < 0 {
		return nil, grpcError(fmt.Errorf("%w: page_size and page must not be negative", ErrInvalidInput))
	}
	if capability == nil {
		return nil, grpcError(fmt.Errorf("%w: no resource capability", ErrScopeNotGranted))
	}

	// The granted range is the union of the labelled families the document
	// service resolved. An empty grant is the empty set, which means
	// "authenticated public only" - never "everything".
	grantedSpaces := canonicalIDs(grantedSpaceIDs(capability))
	grantedDocuments := canonicalIDs(capability.AllowedDocumentIDs)

	// The request may only narrow. Both families are checked before anything is
	// queried: one identifier outside the grant rejects the whole request, so a
	// caller can never probe an identifier it was not granted.
	requestedSpaces := canonicalIDs(request.GetAllowedSpaceIds())
	requestedDocuments := canonicalIDs(request.GetAllowedDocumentIds())
	if !serviceauth.ContainsAll(grantedSpaces, requestedSpaces) {
		return nil, grpcError(fmt.Errorf("%w: requested spaces are outside the granted range", ErrScopeNotGranted))
	}
	if !serviceauth.ContainsAll(grantedDocuments, requestedDocuments) {
		return nil, grpcError(fmt.Errorf("%w: requested documents are outside the granted range", ErrScopeNotGranted))
	}

	scope := effectiveSearchScope(capability, grantedSpaces, grantedDocuments,
		requestedSpaces, requestedDocuments, request.GetOwnedBySubjectOnly())

	// A personal search without a subject cannot be answered: every document it
	// could return belongs to a subject the capability does not identify. The
	// fail-closed answer is no documents, never an unfiltered one.
	if scope.OwnedBySubject && scope.OwnerSubjectKey == "" {
		return &documentsearchv1.SearchDocumentsResponse{
			Query: query,
			Hits:  []*documentsearchv1.SearchHit{},
			Total: 0,
		}, nil
	}

	pageSize := int(request.GetPageSize())
	truncated := false
	if pageSize == 0 {
		pageSize = service.cfg.Index.DefaultTopK
	}
	if pageSize <= 0 {
		pageSize = 10
	}
	if maxPageSize := service.cfg.Index.MaxTopK; maxPageSize > 0 && pageSize > maxPageSize {
		pageSize = maxPageSize
		truncated = true
	}
	page := int(request.GetPage())
	if page <= 0 {
		page = 1
	}
	offset := int64(page-1) * int64(pageSize)

	if service.pool == nil || service.pool.Pgx() == nil {
		return nil, grpcError(fmt.Errorf("%w: database pool is not initialized", ErrDatabase))
	}

	settings, err := service.cfg.VectorConfig()
	if err != nil {
		return nil, grpcError(fmt.Errorf("%w: %v", ErrInvalidInput, err))
	}
	retrieval, err := service.retrieveDocuments(ctx, query, scope, retrieveBounds{
		Limit:          pageSize,
		Offset:         offset,
		KeywordWindow:  settings.MaxKeywordCandidates,
		VectorRecall:   settings.Recall,
		RRFK:           settings.RRFK,
		VectorsEnabled: settings.Enabled,
	})
	if err != nil {
		return nil, grpcError(err)
	}
	total := retrieval.Total
	if total > maxSearchTotal {
		total = maxSearchTotal
	}
	return &documentsearchv1.SearchDocumentsResponse{
		Query:     query,
		Hits:      retrieval.Hits,
		Truncated: truncated || retrieval.Truncated,
		Total:     int32(total),
	}, nil
}

// countMatches reports how many documents match the keyword half of the query in
// the same scope the page is drawn from.
//
// The total a response reports is not this number alone: the vector arm adds the
// documents it recalls that the keyword arm did not match, which is why
// retrieveDocuments adds them before answering. When the vector arm is disabled
// or recalls nothing new, this is exactly the Stage A count. Either way every
// counted match is pageable: the ones outside the fusion window follow it in
// keyword order.
func countMatches(ctx context.Context, service *Service, query string, scope searchScope) (int64, error) {
	var total int64
	if err := service.pool.Pgx().QueryRow(ctx, searchCountSQL,
		query, scope.SpaceIDs, scope.DocumentIDs, scope.IncludePublic, scope.OwnerSubjectKey,
	).Scan(&total); err != nil {
		return 0, fmt.Errorf("%w: count matching documents: %v", ErrDatabase, err)
	}
	return total, nil
}

// grantedSpaceIDs is the complete space family of a capability: the explicit
// allowed spaces plus the three labelled families the document service resolved.
func grantedSpaceIDs(capability *serviceauth.CapabilityClaims) []string {
	if capability == nil {
		return nil
	}
	spaces := make([]string, 0, len(capability.AllowedSpaceIDs)+len(capability.PrivateSpaceIDs)+len(capability.OtherTeamSpaceIDs)+1)
	spaces = append(spaces, capability.AllowedSpaceIDs...)
	spaces = append(spaces, capability.PrivateSpaceIDs...)
	spaces = append(spaces, capability.OtherTeamSpaceIDs...)
	if strings.TrimSpace(capability.CurrentTeamSpaceID) != "" {
		spaces = append(spaces, capability.CurrentTeamSpaceID)
	}
	return spaces
}

// buildSnippet returns a deterministic excerpt around the first match, or the
// leading text when the match is only in the ranking signals.
func buildSnippet(summary, content, query string) string {
	body := strings.TrimSpace(content)
	if body == "" {
		body = strings.TrimSpace(summary)
	}
	if body == "" {
		return ""
	}
	runes := []rune(body)
	lowerRunes := []rune(strings.ToLower(body))
	needle := []rune(strings.ToLower(strings.TrimSpace(query)))
	index := -1
	// Lower-casing can change the rune count for a few scripts. The window is
	// only centred when the offsets are comparable, so a rare script can never
	// shift the excerpt onto the wrong text.
	if len(lowerRunes) == len(runes) {
		index = indexOfRunes(lowerRunes, needle)
		if index < 0 {
			for _, token := range strings.Fields(string(needle)) {
				if len([]rune(token)) < 2 {
					continue
				}
				if found := indexOfRunes(lowerRunes, []rune(token)); found >= 0 {
					index = found
					break
				}
			}
		}
	}
	if index < 0 {
		if len(runes) <= snippetLength {
			return collapseWhitespace(body)
		}
		return collapseWhitespace(string(runes[:snippetLength])) + "…"
	}
	start := index - snippetLength/3
	if start < 0 {
		start = 0
	}
	end := start + snippetLength
	if end > len(runes) {
		end = len(runes)
	}
	snippet := collapseWhitespace(string(runes[start:end]))
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(runes) {
		snippet += "…"
	}
	return snippet
}

// indexOfRunes reports the first rune offset of needle in haystack, or -1.
func indexOfRunes(haystack, needle []rune) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	for start := 0; start+len(needle) <= len(haystack); start++ {
		matched := true
		for offset := range needle {
			if haystack[start+offset] != needle[offset] {
				matched = false
				break
			}
		}
		if matched {
			return start
		}
	}
	return -1
}

// collapseWhitespace folds the excerpt into one line.
func collapseWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// searchScopeFilter is the authorization, ownership and lifecycle filter. The
// parameters are the resolved scope produced by effectiveSearchScope, never the
// raw request:
//
//	$1 = query text
//	$2 = effective space ids       text[]
//	$3 = effective document ids    text[]
//	$4 = include the authenticated-public floor (boolean)
//	$5 = owner subject key, '' for no owner filter
//	$6 = statement specific: the fusion window width, or the window's document
//	     ids on the tail statement
//	$7 = statement specific: the tail's limit
//	$8 = statement specific: the tail's offset
//
// The parameter order is part of this constant's contract, because every
// statement below appends to it and every caller binds the arguments
// positionally.
//
// The public floor is a parameter rather than a constant because it is part of
// the granted envelope: an empty capability still means "authenticated-public
// documents only", while a request that names spaces or documents is answered
// from the named set alone. A capability is never minted from a denied
// resolution, so there is no "denied" shape to represent here.
//
// The owner conjunct is the personal-search rule: it is a filter over whatever
// range applies, so an owned-by-subject search never returns another subject's
// public or private document.
//
// The keyword conjunct is deliberately not part of this constant. The vector arm
// recalls documents that need not match the keyword query, and those are still
// subject to the same scope; the two halves therefore share this filter and
// differ only in the conjunct appended to it.
const searchScopeFilter = `
        d.lifecycle_status = 'active'
    AND d.publication_status = 'published'
    AND ($5::text = '' OR d.owner_subject_key = $5::text)
    AND (
        ($4::boolean AND d.authenticated_public)
        OR d.document_id = ANY($3::text[]::uuid[])
        OR d.owner_space_id = ANY($2::text[]::uuid[])
        OR EXISTS (
            SELECT 1
            FROM jsonb_array_elements_text(d.allowed_space_ids) AS allowed(space_id)
            WHERE allowed.space_id = ANY($2::text[])
        )
    )`

// searchKeywordConjunct is the keyword half of the query: a full-text match or a
// title substring match.
const searchKeywordConjunct = `
    AND (d.search_vector @@ q.tsq OR d.title ILIKE q.pattern)`

// searchScopePredicate is the scope filter plus the keyword conjunct, which is
// what a keyword-only statement filters on.
const searchScopePredicate = searchScopeFilter + searchKeywordConjunct

// searchQueryJoin builds the query signals every search statement joins on.
const searchQueryJoin = `
FROM document_search.document_index d
CROSS JOIN (SELECT websearch_to_tsquery('simple', $1) AS tsq, '%' || $1 || '%' AS pattern) q`

// searchRowColumns is the display projection every candidate statement returns,
// so the keyword and vector rows are scanned by the same code and can never
// drift into two different field sets.
//
// The two timestamps are the document's own instants, taken from the event when
// it was applied (created_at, and the fact source's transaction time stored in
// document_updated_at). They are rendered in SQL as UTC RFC3339 with
// microseconds, so the query path needs no clock or time package of its own.
const searchRowColumns = `
       d.document_id::text, d.version_id::text, d.owner_space_id::text,
       d.title, d.content, d.summary,
       d.owner_subject_key, d.authenticated_public,
       to_char(d.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
       to_char(d.document_updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')`

// searchCountSQL reports the honest keyword match count for the scope. It is the
// full count, not a bounded one: the response's total is this number plus the
// documents only the vector arm recalled.
const searchCountSQL = `
SELECT count(*)` + searchQueryJoin + `
WHERE ` + searchScopePredicate

// searchKeywordWindowSQL returns the top keyword matches, which are the keyword
// half of the RRF fusion window. The full-text rank dominates; a title match adds
// a fixed bonus, and the document id breaks ties, so the same query always returns
// the same order.
//
// The bound ($6) is the fusion window: it decides how deep the vector arm can
// reach into the keyword ranking, not how much of the answer is readable. The
// keyword matches below the window are read by searchKeywordTailSQL, in this same
// order, so the whole answer stays pageable.
const searchKeywordWindowSQL = `
SELECT` + searchRowColumns + `,
       (COALESCE(ts_rank_cd(d.search_vector, q.tsq), 0)::float8 * 2.0
        + CASE WHEN d.title ILIKE q.pattern THEN 1.0 ELSE 0.0 END) AS keyword_score` + searchQueryJoin + `
WHERE ` + searchScopePredicate + `
ORDER BY keyword_score DESC, d.document_id
LIMIT $6`

// searchKeywordTailSQL reads a slice of the keyword matches outside the fusion
// window, in the same keyword order: $7 is the number of rows the page needs and
// $8 is the position in the tail the page starts at.
//
// The window's document ids ($6) are excluded here, so a document the vector arm
// pulled into the window is never answered a second time from the keyword
// ranking. An empty window excludes nothing (the coalesce keeps a null binding
// from filtering every row out). The filter and the ordering are exactly the ones
// the count and the window use, which is what makes the pages of the answer add
// up to the reported total.
const searchKeywordTailSQL = `
SELECT` + searchRowColumns + `,
       (COALESCE(ts_rank_cd(d.search_vector, q.tsq), 0)::float8 * 2.0
        + CASE WHEN d.title ILIKE q.pattern THEN 1.0 ELSE 0.0 END) AS keyword_score` + searchQueryJoin + `
WHERE ` + searchScopePredicate + `
    AND NOT (d.document_id = ANY(COALESCE($6::text[], '{}'::text[])::uuid[]))
ORDER BY keyword_score DESC, d.document_id
LIMIT $7 OFFSET $8`

// selectScopedDocumentsSQL reads the display rows of the documents the vector arm
// recalled, keeping only those that pass the same scope filter, and reports
// whether each one is also a keyword match.
//
// This statement is the authorization decision of the hybrid query. The vector
// collection's payload filter is only a recall optimisation: a point whose
// payload is stale - an old access snapshot, a revision the fence later refused -
// can never widen the answer, because a document only becomes a hit by passing
// this filter on the committed projection.
const selectScopedDocumentsSQL = `
SELECT` + searchRowColumns + `,
       (d.search_vector @@ q.tsq OR d.title ILIKE q.pattern) AS keyword_match` + searchQueryJoin + `
WHERE ` + searchScopeFilter + `
    AND d.document_id = ANY($6::text[]::uuid[])`
