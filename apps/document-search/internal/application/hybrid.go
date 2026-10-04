package application

import (
	"context"
	"fmt"
	"sort"

	documentsearchv1 "packages/gen/documentsearch/v1"
)

// Hybrid retrieval: keyword candidates and vector recall, fused into one ranked
// answer.
//
// The two arms are independent, and the SQL projection is the authority for both.
// The keyword arm ranks full-text and title matches inside the resolved scope.
// The vector arm recalls points from the collection the same scope was translated
// into, and every recalled document id is then read back through the scope
// filter, so a recall can only ever propose a candidate - the committed
// projection decides whether it exists and whether the caller may see it.
//
// The arms are fused with reciprocal rank fusion (RRF): each arm contributes
// 1/(k + rank) for the documents it ranked, the contributions are summed, and
// ties are broken by document id. RRF is used rather than the raw scores because
// a cosine similarity and a ts_rank_cd are not on one scale, and because it makes
// the order depend only on the ranks each arm produced - which is what makes the
// same query against the same index return the same order every time.
//
// The fusion runs over a bounded window: the top keyword matches and the
// documents the vector arm recalled. The window is exactly how deep the vector
// arm can reach into the keyword ranking - it is not what the caller can read.
// The keyword matches outside the window follow the fused window, in keyword
// order, so the complete answer is one deterministic sequence and every page is a
// slice of it. That is what makes `total` honest: a page can always serve the
// results its total promises.

// searchRow is one display row of the index: everything a hit needs, plus the
// signal that says which arm found it.
type searchRow struct {
	DocumentID          string
	VersionID           string
	OwnerSpaceID        string
	Title               string
	Content             string
	Summary             string
	OwnerSubjectKey     string
	AuthenticatedPublic bool
	CreatedAt           string
	UpdatedAt           string
	// KeywordScore is the full-text ranking of the keyword arm.
	KeywordScore float64
	// KeywordMatch reports whether this row also matches the keyword query. It
	// is set by the statement that reads vector-recalled documents back through
	// the scope filter.
	KeywordMatch bool
}

// retrieveBounds are the query bounds the configuration and the request produced.
type retrieveBounds struct {
	// Limit and Offset are the page: the complete answer order is sliced with
	// them.
	Limit  int
	Offset int64
	// KeywordWindow is the width of the RRF fusion window: the top keyword
	// matches that take part in the fusion with the vector recall. It bounds how
	// deep a recall can reorder the answer, not how much of the answer is
	// readable - the keyword matches beyond it follow the fused window in keyword
	// order and are still pageable.
	KeywordWindow int
	// VectorRecall is how many collection points one query recalls.
	VectorRecall int
	// RRFK is the reciprocal-rank-fusion constant.
	RRFK int
	// VectorsEnabled reports whether the vector arm takes part at all.
	VectorsEnabled bool
}

// retrieval is one answered page.
type retrieval struct {
	Hits []*documentsearchv1.SearchHit
	// Total is the size of the answer set the page is drawn from: every keyword
	// match in scope, plus the documents the vector arm recalled that the keyword
	// arm did not match. The answer set is completely ordered - the fused window
	// first, then the keyword matches outside it in keyword order - so every page
	// up to ceil(total/page_size) is non-empty.
	Total int64
	// Truncated reports that more results follow this page
	// (offset+len(hits) < total). It no longer reports that a candidate bound cut
	// the ranking: the window is not a readability bound any more.
	Truncated bool
}

// rankedDocument is one entry of the fused order.
type rankedDocument struct {
	DocumentID string
	Score      float64
}

// fusionWindow is the input of the fusion: the keyword half in keyword order,
// the display row of every document in the window, and the number of documents
// only the vector arm recalled.
type fusionWindow struct {
	KeywordOrder []string
	Rows         map[string]searchRow
	VectorOnly   int64
}

// mergeWindow combines the keyword window and the recalled documents into one
// window.
//
// A document both arms found is one entry of the answer, not two: the keyword
// row is kept, the recall contributes no second row and no second count, and only
// the recalls the keyword arm did not match extend the answer beyond the keyword
// count. The rows cover every recalled document, keyword match or not, because
// the recall order is what the fusion ranks - a recalled keyword match outside
// the keyword window still needs a row to be presented.
func mergeWindow(keywordWindow, vectorRows []searchRow) fusionWindow {
	merged := fusionWindow{
		KeywordOrder: make([]string, 0, len(keywordWindow)),
		Rows:         make(map[string]searchRow, len(keywordWindow)+len(vectorRows)),
	}
	for _, row := range keywordWindow {
		merged.KeywordOrder = append(merged.KeywordOrder, row.DocumentID)
		merged.Rows[row.DocumentID] = row
	}
	for _, row := range vectorRows {
		if _, known := merged.Rows[row.DocumentID]; !known {
			merged.Rows[row.DocumentID] = row
		}
		if !row.KeywordMatch {
			merged.VectorOnly++
		}
	}
	return merged
}

// retrieveDocuments answers one query from both arms and fuses the result.
func (service *Service) retrieveDocuments(
	ctx context.Context,
	query string,
	scope searchScope,
	bounds retrieveBounds,
) (retrieval, error) {
	keywordTotal, err := countMatches(ctx, service, query, scope)
	if err != nil {
		return retrieval{}, err
	}

	window := bounds.KeywordWindow
	if window <= 0 {
		window = 1
	}
	keywordWindow, err := service.keywordCandidates(ctx, query, scope, window)
	if err != nil {
		return retrieval{}, err
	}

	vectorOrder, vectorRows, err := service.vectorCandidates(ctx, query, scope, bounds)
	if err != nil {
		return retrieval{}, err
	}

	merged := mergeWindow(keywordWindow, vectorRows)
	total := keywordTotal + merged.VectorOnly

	fused := fuseRanks(merged.KeywordOrder, vectorOrder, bounds.RRFK)
	hits, err := service.pageHits(ctx, query, scope, fused, merged.Rows, bounds)
	if err != nil {
		return retrieval{}, err
	}
	return retrieval{
		Hits:      hits,
		Total:     total,
		Truncated: bounds.Offset+int64(len(hits)) < total,
	}, nil
}

// pageWindow is how one page maps onto the complete answer order: which slice of
// the fused window it shows, and which slice of the keyword tail follows it.
type pageWindow struct {
	// FusedFrom and FusedTo are the half-open range of the fused window.
	FusedFrom int64
	FusedTo   int64
	// TailOffset and TailLimit are the slice of the keyword tail. TailLimit is
	// zero when the page ends inside the window.
	TailOffset int64
	TailLimit  int64
}

// pageWindowFor maps a page onto the complete answer order, which is the fused
// window of fusedLength entries followed by the keyword tail.
//
// It is a pure function so the arithmetic that makes "every page the total
// promises" true is testable without a database: the fused range is clamped to
// the window, and the tail slice is exactly what remains of the page.
func pageWindowFor(offset int64, limit int, fusedLength int) pageWindow {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 1
	}
	length := int64(fusedLength)
	if length < 0 {
		length = 0
	}
	plan := pageWindow{FusedFrom: offset}
	if plan.FusedFrom > length {
		plan.FusedFrom = length
	}
	shown := length - plan.FusedFrom
	if shown > int64(limit) {
		shown = int64(limit)
	}
	plan.FusedTo = plan.FusedFrom + shown
	remaining := int64(limit) - shown
	if remaining <= 0 {
		return plan
	}
	tailOffset := offset - length
	if tailOffset < 0 {
		tailOffset = 0
	}
	plan.TailOffset = tailOffset
	plan.TailLimit = remaining
	return plan
}

// pageHits assembles one page of the complete answer order, which is the fused
// window followed by the keyword matches outside it in keyword order.
//
// The window is small - the configured fusion width plus what the vector arm
// recalled - and is already in memory. The tail is not: it can be as large as the
// match set, so the page's slice of it is read with its own offset and limit.
// Reading the tail this way is what lets a page at any depth be answered from the
// same ordered answer, instead of only from a bounded candidate list.
func (service *Service) pageHits(
	ctx context.Context,
	query string,
	scope searchScope,
	fused []rankedDocument,
	windowRows map[string]searchRow,
	bounds retrieveBounds,
) ([]*documentsearchv1.SearchHit, error) {
	limit := bounds.Limit
	if limit <= 0 {
		limit = 1
	}
	plan := pageWindowFor(bounds.Offset, limit, len(fused))
	hits := make([]*documentsearchv1.SearchHit, 0, limit)
	for index := plan.FusedFrom; index < plan.FusedTo; index++ {
		entry := fused[index]
		row, known := windowRows[entry.DocumentID]
		if !known {
			// Every fused entry is either a keyword window row or a recalled
			// document that passed the scope re-check, so both carry a row.
			return nil, fmt.Errorf("%w: the fusion window has no row for document %s", ErrDatabase, entry.DocumentID)
		}
		hits = append(hits, newSearchHit(row, query, entry.Score))
	}
	if plan.TailLimit <= 0 {
		return hits, nil
	}

	// The page reaches past the window: the rest of it is a slice of the keyword
	// matches that are not in the window, in keyword order. The window's
	// documents are excluded by the statement itself, so a document can never be
	// answered twice.
	windowIDs := make([]string, 0, len(fused))
	for _, entry := range fused {
		windowIDs = append(windowIDs, entry.DocumentID)
	}
	tail, err := service.keywordTail(ctx, query, scope, windowIDs, plan.TailOffset, plan.TailLimit)
	if err != nil {
		return nil, err
	}
	for _, row := range tail {
		// A document outside the fusion window has no RRF evidence. Its score is
		// reported as zero, which is below every window entry, so the returned
		// order stays descending by score while the tail keeps the keyword order
		// the fusion window did not rank.
		hits = append(hits, newSearchHit(row, query, 0))
	}
	return hits, nil
}

// newSearchHit renders one ordered row as a hit.
func newSearchHit(row searchRow, query string, score float64) *documentsearchv1.SearchHit {
	return &documentsearchv1.SearchHit{
		DocumentId:          row.DocumentID,
		VersionId:           row.VersionID,
		OwnerSpaceId:        row.OwnerSpaceID,
		Title:               row.Title,
		Snippet:             buildSnippet(row.Summary, row.Content, query),
		Score:               score,
		Source:              searchSourceName,
		OwnerSubjectKey:     row.OwnerSubjectKey,
		AuthenticatedPublic: row.AuthenticatedPublic,
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
	}
}

// keywordCandidates reads the keyword half of the fusion window: the top ranked
// keyword matches in the scope.
func (service *Service) keywordCandidates(
	ctx context.Context,
	query string,
	scope searchScope,
	limit int,
) ([]searchRow, error) {
	rows, err := service.pool.Pgx().Query(ctx, searchKeywordWindowSQL,
		query, scope.SpaceIDs, scope.DocumentIDs, scope.IncludePublic, scope.OwnerSubjectKey, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("%w: query the document index: %v", ErrDatabase, err)
	}
	defer rows.Close()
	window := make([]searchRow, 0, limit)
	for rows.Next() {
		row, err := scanSearchRow(rows, true)
		if err != nil {
			return nil, err
		}
		window = append(window, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: read the keyword window: %v", ErrDatabase, err)
	}
	return window, nil
}

// keywordTail reads the keyword matches outside the fusion window, in keyword
// order, sliced to the page's own offset and limit.
//
// The window's documents are excluded by the statement, so a document that the
// vector arm pulled into the window is not answered a second time from the keyword
// ranking. The count, the window and this tail all filter on the same scope
// predicate, which is why the pages of the answer add up to the reported total.
func (service *Service) keywordTail(
	ctx context.Context,
	query string,
	scope searchScope,
	windowIDs []string,
	offset int64,
	limit int64,
) ([]searchRow, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := service.pool.Pgx().Query(ctx, searchKeywordTailSQL,
		query, scope.SpaceIDs, scope.DocumentIDs, scope.IncludePublic, scope.OwnerSubjectKey, windowIDs, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("%w: query the document index tail: %v", ErrDatabase, err)
	}
	defer rows.Close()
	tail := make([]searchRow, 0, limit)
	for rows.Next() {
		row, err := scanSearchRow(rows, true)
		if err != nil {
			return nil, err
		}
		tail = append(tail, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: read the keyword tail: %v", ErrDatabase, err)
	}
	return tail, nil
}

// vectorCandidates recalls the vector arm and reads the recalled documents back
// through the authorization scope. It returns the recall order of the documents
// that survived the scope re-check and their display rows. The rows cover every
// survivor, whether or not the keyword arm also matched it: a recalled document
// that is also a keyword match with a rank outside the keyword window still needs
// a row to be presented.
func (service *Service) vectorCandidates(
	ctx context.Context,
	query string,
	scope searchScope,
	bounds retrieveBounds,
) ([]string, []searchRow, error) {
	if !bounds.VectorsEnabled || service.vectors == nil {
		return nil, nil, nil
	}
	// The recall families are the ones the caller named or was granted: spaces and
	// documents. The authenticated-public floor is deliberately not a recall
	// family.
	//
	// The floor is an authorization statement, not a corpus subscription: it says
	// a public document may be read, and the keyword arm already reaches public
	// documents through their terms. Using it as a recall family would make every
	// un-narrowed query pull in whatever the deployment's entire public corpus
	// happens to look like under the embedding - a result set that grows with
	// unrelated content. The scope re-check below still runs with the floor
	// enabled, so a recalled document is admitted exactly when the committed
	// projection says the caller may read it; this only keeps the recall inside
	// the families the caller actually asked about.
	//
	// A caller with no family at all (an empty envelope) is therefore served by the
	// keyword arm alone.
	filter := VectorFilter{
		SpaceIDs:        scope.SpaceIDs,
		DocumentIDs:     scope.DocumentIDs,
		OwnerSubjectKey: scope.OwnerSubjectKey,
	}
	if filter.MatchesNothing() {
		// The resolved scope names no family at all, so there is nothing to recall
		// and no reason to ask the collection.
		return nil, nil, nil
	}
	recall := bounds.VectorRecall
	if recall <= 0 {
		recall = 1
	}
	candidates, err := service.vectors.Search(ctx, embedText32(query), recall, filter)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrVectorIndex, err)
	}
	if len(candidates) == 0 {
		return nil, nil, nil
	}
	order := make([]string, 0, len(candidates))
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		order = append(order, candidate.DocumentID)
		ids = append(ids, candidate.DocumentID)
	}
	rows, err := service.scopedDocuments(ctx, query, scope, ids)
	if err != nil {
		return nil, nil, err
	}
	visible := make(map[string]searchRow, len(rows))
	for _, row := range rows {
		visible[row.DocumentID] = row
	}
	// A recalled document that did not come back through the scope filter is not
	// part of the answer, so it must not occupy a rank either. The survivors keep
	// the recall order, and each one brings its display row.
	kept := make([]string, 0, len(order))
	keptRows := make([]searchRow, 0, len(order))
	for _, documentID := range order {
		row, found := visible[documentID]
		if !found {
			continue
		}
		kept = append(kept, documentID)
		keptRows = append(keptRows, row)
	}
	return kept, keptRows, nil
}

// scopedDocuments reads the display rows of the named documents, keeping only the
// ones the resolved scope allows.
func (service *Service) scopedDocuments(
	ctx context.Context,
	query string,
	scope searchScope,
	documentIDs []string,
) ([]searchRow, error) {
	rows, err := service.pool.Pgx().Query(ctx, selectScopedDocumentsSQL,
		query, scope.SpaceIDs, scope.DocumentIDs, scope.IncludePublic, scope.OwnerSubjectKey, documentIDs)
	if err != nil {
		return nil, fmt.Errorf("%w: read the recalled documents: %v", ErrDatabase, err)
	}
	defer rows.Close()
	scoped := make([]searchRow, 0, len(documentIDs))
	for rows.Next() {
		row, err := scanSearchRow(rows, false)
		if err != nil {
			return nil, err
		}
		scoped = append(scoped, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: read the recalled documents: %v", ErrDatabase, err)
	}
	return scoped, nil
}

// scanSearchRow reads the shared display projection. The ranking column is either
// the keyword score or the keyword-match flag, which is why it is selected by the
// statement rather than inferred here.
func scanSearchRow(rows interface {
	Scan(dest ...any) error
}, keywordScore bool) (searchRow, error) {
	var (
		row     searchRow
		ranking float64
		matched bool
	)
	if keywordScore {
		if err := rows.Scan(&row.DocumentID, &row.VersionID, &row.OwnerSpaceID, &row.Title, &row.Content,
			&row.Summary, &row.OwnerSubjectKey, &row.AuthenticatedPublic, &row.CreatedAt, &row.UpdatedAt,
			&ranking); err != nil {
			return searchRow{}, fmt.Errorf("%w: read a keyword candidate: %v", ErrDatabase, err)
		}
		row.KeywordScore = ranking
		row.KeywordMatch = true
		return row, nil
	}
	if err := rows.Scan(&row.DocumentID, &row.VersionID, &row.OwnerSpaceID, &row.Title, &row.Content,
		&row.Summary, &row.OwnerSubjectKey, &row.AuthenticatedPublic, &row.CreatedAt, &row.UpdatedAt,
		&matched); err != nil {
		return searchRow{}, fmt.Errorf("%w: read a recalled document: %v", ErrDatabase, err)
	}
	row.KeywordMatch = matched
	return row, nil
}

// fuseRanks applies reciprocal rank fusion to the two arms.
//
// Each ranked list contributes 1/(k + rank) with rank starting at 1, so a
// document both arms found outranks a document only one of them found, and a
// document the second arm ranked highly can overtake a document the first arm
// ranked lower. The final order is deterministic: ties break on the document id.
//
// The result is the fused window. It contains exactly the documents the two lists
// named, once each, and the caller appends the keyword matches the window did not
// name after it.
func fuseRanks(keywordOrder, vectorOrder []string, rrfK int) []rankedDocument {
	if rrfK <= 0 {
		rrfK = 60
	}
	scores := make(map[string]float64, len(keywordOrder)+len(vectorOrder))
	for rank, documentID := range keywordOrder {
		scores[documentID] += 1 / float64(rrfK+rank+1)
	}
	for rank, documentID := range vectorOrder {
		scores[documentID] += 1 / float64(rrfK+rank+1)
	}
	fused := make([]rankedDocument, 0, len(scores))
	for documentID, score := range scores {
		fused = append(fused, rankedDocument{DocumentID: documentID, Score: score})
	}
	sort.Slice(fused, func(left, right int) bool {
		if fused[left].Score != fused[right].Score {
			return fused[left].Score > fused[right].Score
		}
		return fused[left].DocumentID < fused[right].DocumentID
	})
	return fused
}
