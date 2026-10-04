// Package qdrant owns the document search service's vector collection.
//
// The collection is several physical collections under one stable alias: reads
// and writes always address the alias, and only the maintenance path
// (EnsureGeneration, PrepareGeneration, SwitchAlias, PruneGenerations,
// DropCollection) touches physical names. The alias, storage domain, embedding
// profile and dimensions are inputs, never defaults invented here: the caller
// reads them from document_search.index_generations, which is the record of what
// this service's index generation is.
package qdrant

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"document-search/internal/vectorindex"

	qdrantclient "github.com/qdrant/go-client/qdrant"
)

// Payload keys. They are part of the collection's on-disk contract: a rebuild
// reads back what an earlier process wrote, so a key is only renamed together
// with a new index generation.
const (
	payloadStorageDomain       = "storage_domain"
	payloadDocumentID          = "document_id"
	payloadVersionID           = "version_id"
	payloadOwnerSpaceID        = "owner_space_id"
	payloadOwnerSubjectKey     = "owner_subject_key"
	payloadAuthenticatedPublic = "authenticated_public"
	payloadAllowedSpaceIDs     = "allowed_space_ids"
	payloadActive              = "active"
	payloadTombstoned          = "tombstoned"
	payloadAggregateRevision   = "aggregate_revision"
	payloadAccessRevision      = "access_revision"
	payloadLifecycleRevision   = "lifecycle_revision"
	payloadVectorProfile       = "vector_profile"
	payloadVectorDimensions    = "vector_dimensions"
	payloadChunkIndex          = "chunk_index"
	payloadTitle               = "title"
)

// Config is everything the store needs to address and describe one collection.
type Config struct {
	Host           string
	Port           int
	APIKey         string
	UseTLS         bool
	Alias          string
	Generation     string
	StorageDomain  string
	VectorProfile  string
	Dimensions     uint64
	Timeout        time.Duration
	RequestTimeout time.Duration
}

// Document, Chunk, Filter and Candidate are the port types of the vector index,
// aliased here so this package's implementation and the application share one
// definition.
type (
	Document  = vectorindex.Document
	Chunk     = vectorindex.Chunk
	Filter    = vectorindex.Filter
	Candidate = vectorindex.Candidate
)

// Store is the Qdrant-backed vector collection.
type Store struct {
	client        *qdrantclient.Client
	alias         string
	generation    string
	storageDomain string
	profile       string
	dimensions    uint64
	timeout       time.Duration
}

// New creates the client. It performs no I/O: EnsureGeneration is what opens the
// collection, so a caller that only wants to inspect an alias can do that
// without writing anything.
func New(config Config) (*Store, error) {
	alias := strings.TrimSpace(config.Alias)
	if alias == "" {
		return nil, errors.New("qdrant: collection alias is required")
	}
	generation := strings.TrimSpace(config.Generation)
	if generation == "" {
		return nil, errors.New("qdrant: generation label is required")
	}
	if config.Dimensions == 0 {
		return nil, errors.New("qdrant: vector dimensions are required")
	}
	if strings.TrimSpace(config.StorageDomain) == "" {
		return nil, errors.New("qdrant: storage domain is required")
	}
	if strings.TrimSpace(config.VectorProfile) == "" {
		return nil, errors.New("qdrant: vector profile is required")
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = config.RequestTimeout
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	client, err := qdrantclient.NewClient(&qdrantclient.Config{
		Host:                strings.TrimSpace(config.Host),
		Port:                config.Port,
		APIKey:              strings.TrimSpace(config.APIKey),
		UseTLS:              config.UseTLS,
		PoolSize:            3,
		VersionCheckTimeout: timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant: create client: %w", err)
	}
	return &Store{
		client:        client,
		alias:         alias,
		generation:    generation,
		storageDomain: strings.TrimSpace(config.StorageDomain),
		profile:       strings.TrimSpace(config.VectorProfile),
		dimensions:    config.Dimensions,
		timeout:       timeout,
	}, nil
}

// Alias reports the stable collection name every operation addresses.
func (store *Store) Alias() string { return store.alias }

// Generation reports the generation label this store was opened with.
func (store *Store) Generation() string { return store.generation }

// StorageDomain reports the domain recorded in every payload.
func (store *Store) StorageDomain() string { return store.storageDomain }

// Profile reports the embedding profile this collection was opened with.
func (store *Store) Profile() string { return store.profile }

// Dimensions reports the vector width of the collection.
func (store *Store) Dimensions() int { return int(store.dimensions) }

// Close releases the gRPC connections.
func (store *Store) Close() error {
	if store == nil || store.client == nil {
		return nil
	}
	return store.client.Close()
}

// GenerationCollection is the one place the physical collection name is derived.
// Keeping it a function of (alias, generation) is what lets pruning decide what
// belongs to this corpus without a second naming convention.
func GenerationCollection(alias, generation string) string {
	return strings.TrimSpace(alias) + "_" + strings.TrimSpace(generation)
}

// PhysicalCollection resolves the collection the alias currently points at.
func (store *Store) PhysicalCollection(ctx context.Context) (string, error) {
	callCtx, cancel := store.callContext(ctx)
	defer cancel()
	target, exists, err := store.aliasedCollection(callCtx)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("qdrant: alias %q does not exist", store.alias)
	}
	return target, nil
}

// EnsureGeneration makes the configured generation usable: the physical
// collection exists with the configured dimensions, the payload indexes the
// filters need exist, and the alias points at it.
//
// A physical collection already sitting on the alias name is refused rather than
// adopted. That layout is what a pre-alias deployment looks like, and guessing
// whether such a collection is the corpus or a stale generation is how one stack
// ends up serving two names for one corpus.
func (store *Store) EnsureGeneration(ctx context.Context) error {
	callCtx, cancel := store.callContext(ctx)
	defer cancel()
	target, exists, err := store.aliasedCollection(callCtx)
	if err != nil {
		return err
	}
	physical := GenerationCollection(store.alias, store.generation)
	if !exists {
		legacy, err := store.client.CollectionExists(callCtx, store.alias)
		if err != nil {
			return fmt.Errorf("qdrant: check collection %q: %w", store.alias, err)
		}
		if legacy {
			return fmt.Errorf(
				"qdrant: collection %q exists as a physical collection while this service requires it to be an alias; "+
					"there is no legacy migration: remove the development volume and rebuild the stack",
				store.alias,
			)
		}
		if err := store.prepare(callCtx, physical); err != nil {
			return err
		}
		if err := store.client.CreateAlias(callCtx, store.alias, physical); err != nil {
			// Two processes opening the same generation at once is expected (the
			// service and a test suite, or two replicas); the loser of the race
			// finds the alias already pointing where it wanted it.
			created, exists, checkErr := store.aliasedCollection(callCtx)
			if checkErr != nil || !exists || created != physical {
				return fmt.Errorf("qdrant: create alias %q -> %q: %w", store.alias, physical, err)
			}
		}
		target = physical
	}
	if target != physical {
		return fmt.Errorf(
			"qdrant: alias %q points at %q while the active generation is %q; "+
				"the alias and document_search.index_generations disagree and must be reconciled before serving",
			store.alias, target, physical,
		)
	}
	return store.ensurePayloadIndexes(callCtx, target)
}

// PrepareGeneration creates a generation's physical collection without pointing
// the alias at it, so a future generation can be filled and verified first. An
// existing collection of that name is adopted, which makes a retried preparation
// idempotent.
func (store *Store) PrepareGeneration(ctx context.Context, generation string) (string, error) {
	callCtx, cancel := store.callContext(ctx)
	defer cancel()
	generation = strings.TrimSpace(generation)
	if generation == "" {
		return "", errors.New("qdrant: generation label is required")
	}
	physical := GenerationCollection(store.alias, generation)
	if err := store.prepare(callCtx, physical); err != nil {
		return "", err
	}
	return physical, nil
}

// SwitchAlias points the alias at another generation of this corpus.
//
// The move is one UpdateAliases request carrying a delete action and a create
// action, which Qdrant applies atomically for concurrent observers but not as a
// transaction: a failing create action can leave the alias deleted, so the
// failure path restores the alias to the collection that was serving. The target
// must be one of this corpus's own generations, validated before anything moves.
func (store *Store) SwitchAlias(ctx context.Context, target string) error {
	callCtx, cancel := store.callContext(ctx)
	defer cancel()
	target = strings.TrimSpace(target)
	if target == store.alias || !strings.HasPrefix(target, store.alias+"_") {
		return fmt.Errorf(
			"qdrant: alias %q can only switch to one of its own generations (%s_<label>), not to %q",
			store.alias, store.alias, target,
		)
	}
	exists, err := store.client.CollectionExists(callCtx, target)
	if err != nil {
		return fmt.Errorf("qdrant: check alias target %q: %w", target, err)
	}
	if !exists {
		return fmt.Errorf("qdrant: alias target %q does not exist", target)
	}
	if err := store.verifyDimensions(callCtx, target); err != nil {
		return err
	}
	if err := store.ensurePayloadIndexes(callCtx, target); err != nil {
		return err
	}
	current, aliased, err := store.aliasedCollection(callCtx)
	if err != nil {
		return err
	}
	if !aliased {
		if err := store.client.CreateAlias(callCtx, store.alias, target); err != nil {
			return fmt.Errorf("qdrant: create alias %q -> %q: %w", store.alias, target, err)
		}
	} else if current != target {
		actions := []*qdrantclient.AliasOperations{
			qdrantclient.NewAliasDelete(store.alias),
			qdrantclient.NewAliasCreate(store.alias, target),
		}
		if err := store.client.UpdateAliases(callCtx, actions); err != nil {
			if repairErr := store.client.CreateAlias(callCtx, store.alias, current); repairErr != nil {
				return fmt.Errorf(
					"qdrant: switch alias %q to %q failed (%w) and restoring it to %q also failed: %w",
					store.alias, target, err, current, repairErr,
				)
			}
			return fmt.Errorf("qdrant: switch alias %q to %q: %w (alias restored to %q)", store.alias, target, err, current)
		}
	}
	// A switch is only done when the alias says so: the mapping is read back
	// rather than assumed.
	switched, aliased, err := store.aliasedCollection(callCtx)
	if err != nil {
		return fmt.Errorf("qdrant: verify alias %q after switching to %q: %w", store.alias, target, err)
	}
	if !aliased || switched != target {
		return fmt.Errorf("qdrant: alias %q points at %q after switching, want %q", store.alias, switched, target)
	}
	return nil
}

// DropCollection removes one physical collection of this corpus. The alias
// itself, anything outside the alias prefix, and the collection the alias
// currently serves are all refused: this is the deletion path for superseded
// generations, not a way to take the live corpus offline.
func (store *Store) DropCollection(ctx context.Context, name string) error {
	callCtx, cancel := store.callContext(ctx)
	defer cancel()
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("qdrant: collection name is required")
	}
	if name == store.alias || !strings.HasPrefix(name, store.alias+"_") {
		return fmt.Errorf("qdrant: %q is not one of alias %q's generations", name, store.alias)
	}
	current, aliased, err := store.aliasedCollection(callCtx)
	if err != nil {
		return err
	}
	if aliased && current == name {
		return fmt.Errorf("qdrant: refusing to drop %q while alias %q serves it", name, store.alias)
	}
	if err := store.client.DeleteCollection(callCtx, name); err != nil {
		return fmt.Errorf("qdrant: drop collection %q: %w", name, err)
	}
	return nil
}

// PruneGenerations drops every physical collection of this alias that no
// index_generations row references. The keep-set is passed in rather than read
// here because the generation record lives in PostgreSQL, and this package owns
// only the vector side.
//
// The collection the alias currently serves is never dropped, even when it is
// not in the keep-set: an unrecorded live collection is a configuration problem
// to report, not one to fix by deleting the corpus.
func (store *Store) PruneGenerations(ctx context.Context, referenced []string) ([]string, error) {
	callCtx, cancel := store.callContext(ctx)
	defer cancel()
	keep := make(map[string]struct{}, len(referenced))
	for _, name := range referenced {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			keep[trimmed] = struct{}{}
		}
	}
	names, err := store.client.ListCollections(callCtx)
	if err != nil {
		return nil, fmt.Errorf("qdrant: list collections: %w", err)
	}
	current, aliased, err := store.aliasedCollection(callCtx)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	dropped := make([]string, 0, len(names))
	for _, name := range names {
		if !strings.HasPrefix(name, store.alias+"_") {
			continue
		}
		if _, kept := keep[name]; kept {
			continue
		}
		if aliased && name == current {
			continue
		}
		if err := store.client.DeleteCollection(callCtx, name); err != nil {
			return dropped, fmt.Errorf("qdrant: prune collection %q: %w", name, err)
		}
		dropped = append(dropped, name)
	}
	return dropped, nil
}

// DropAlias removes this corpus's alias without touching its collections. It is
// the first half of removing a corpus: the alias is what keeps a collection live,
// so pruning can only take the collections away after the alias is gone.
func (store *Store) DropAlias(ctx context.Context) error {
	callCtx, cancel := store.callContext(ctx)
	defer cancel()
	if err := store.client.DeleteAlias(callCtx, store.alias); err != nil {
		return fmt.Errorf("qdrant: drop alias %q: %w", store.alias, err)
	}
	return nil
}

// ReplaceDocuments removes every point of each document and writes the chunks
// given for it, in that order. Deleting first is what keeps a document that
// shrank from leaving an old chunk behind.
func (store *Store) ReplaceDocuments(ctx context.Context, documents []Document) error {
	if len(documents) == 0 {
		return nil
	}
	callCtx, cancel := store.callContext(ctx)
	defer cancel()
	wait := true
	if err := store.deleteByDocumentIDs(callCtx, documentIDs(documents), wait); err != nil {
		return err
	}
	points := make([]*qdrantclient.PointStruct, 0, len(documents))
	for _, document := range documents {
		for _, chunk := range document.Chunks {
			payload, err := store.chunkPayload(document, chunk.Index)
			if err != nil {
				return err
			}
			points = append(points, &qdrantclient.PointStruct{
				Id:      qdrantclient.NewID(ChunkPointID(document.DocumentID, chunk.Index)),
				Vectors: qdrantclient.NewVectorsDense(chunk.Vector),
				Payload: payload,
			})
		}
	}
	if len(points) == 0 {
		return nil
	}
	if _, err := store.client.Upsert(callCtx, &qdrantclient.UpsertPoints{
		CollectionName: store.alias,
		Wait:           &wait,
		Points:         points,
	}); err != nil {
		return fmt.Errorf("qdrant: upsert %d points: %w", len(points), err)
	}
	return nil
}

// DeleteDocuments removes every point of the named documents.
func (store *Store) DeleteDocuments(ctx context.Context, documentIDs []string) error {
	ids := normalizeIDs(documentIDs)
	if len(ids) == 0 {
		return nil
	}
	callCtx, cancel := store.callContext(ctx)
	defer cancel()
	return store.deleteByDocumentIDs(callCtx, ids, true)
}

// Search recalls the best points inside the filter and collapses them to one
// candidate per document.
func (store *Store) Search(ctx context.Context, vector []float32, limit int, filter Filter) ([]Candidate, error) {
	if limit <= 0 || len(vector) == 0 {
		return nil, nil
	}
	callCtx, cancel := store.callContext(ctx)
	defer cancel()
	queryLimit := uint64(limit)
	points, err := store.client.Query(callCtx, &qdrantclient.QueryPoints{
		CollectionName: store.alias,
		Query:          qdrantclient.NewQueryDense(vector),
		Limit:          &queryLimit,
		WithPayload:    qdrantclient.NewWithPayload(true),
		Filter:         store.searchFilter(filter),
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant: query collection %q: %w", store.alias, err)
	}
	byDocument := make(map[string]Candidate, len(points))
	order := make([]string, 0, len(points))
	for _, point := range points {
		documentID := payloadString(point.GetPayload(), payloadDocumentID)
		if documentID == "" {
			continue
		}
		candidate, seen := byDocument[documentID]
		if !seen {
			order = append(order, documentID)
			candidate = Candidate{DocumentID: documentID, VersionID: payloadString(point.GetPayload(), payloadVersionID)}
		}
		if score := float64(point.GetScore()); !seen || score > candidate.Score {
			candidate.Score = score
			byDocument[documentID] = candidate
		}
	}
	candidates := make([]Candidate, 0, len(order))
	for _, documentID := range order {
		candidates = append(candidates, byDocument[documentID])
	}
	// Qdrant already orders by score, but the collapsed list is re-sorted so the
	// same query always returns the same order even when two chunks score
	// identically.
	sort.SliceStable(candidates, func(left, right int) bool {
		if candidates[left].Score != candidates[right].Score {
			return candidates[left].Score > candidates[right].Score
		}
		return candidates[left].DocumentID < candidates[right].DocumentID
	})
	return candidates, nil
}

// PointCount reports how many points one document currently has. It is the
// direct check that a replaced version left no old chunk behind.
func (store *Store) PointCount(ctx context.Context, documentID string) (uint64, error) {
	callCtx, cancel := store.callContext(ctx)
	defer cancel()
	exact := true
	count, err := store.client.Count(callCtx, &qdrantclient.CountPoints{
		CollectionName: store.alias,
		Exact:          &exact,
		Filter: &qdrantclient.Filter{
			Must: []*qdrantclient.Condition{
				qdrantclient.NewMatchKeyword(payloadDocumentID, strings.TrimSpace(documentID)),
			},
		},
	})
	if err != nil {
		return 0, fmt.Errorf("qdrant: count points of document %q: %w", documentID, err)
	}
	return count, nil
}

// Health reports whether the vector backend answers.
func (store *Store) Health(ctx context.Context) error {
	callCtx, cancel := store.callContext(ctx)
	defer cancel()
	if _, err := store.client.ListCollections(callCtx); err != nil {
		return fmt.Errorf("qdrant: health check: %w", err)
	}
	return nil
}

// ChunkPointID is the deterministic point id of one chunk. A rebuild therefore
// overwrites the same points instead of adding a second copy of the document.
func ChunkPointID(documentID string, chunkIndex int) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(documentID) + "#" + strconv.Itoa(chunkIndex)))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// callContext bounds one Qdrant call so a stalled server cannot hold an index
// transaction open.
func (store *Store) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, store.timeout)
}

// aliasedCollection resolves this corpus's alias to the physical collection it
// currently points at.
func (store *Store) aliasedCollection(ctx context.Context) (string, bool, error) {
	aliases, err := store.client.ListAliases(ctx)
	if err != nil {
		return "", false, fmt.Errorf("qdrant: list aliases: %w", err)
	}
	for _, description := range aliases {
		if description.GetAliasName() == store.alias {
			return description.GetCollectionName(), true, nil
		}
	}
	return "", false, nil
}

// prepare creates a physical collection and its payload indexes when needed, and
// validates the dimensions of one that already exists.
//
// Creating is idempotent under concurrency: two processes opening the same
// generation at once both check, both may try to create, and the loser adopts
// the collection the winner created rather than failing startup.
func (store *Store) prepare(ctx context.Context, physical string) error {
	exists, err := store.client.CollectionExists(ctx, physical)
	if err != nil {
		return fmt.Errorf("qdrant: check collection %q: %w", physical, err)
	}
	if !exists {
		if err := store.client.CreateCollection(ctx, &qdrantclient.CreateCollection{
			CollectionName: physical,
			VectorsConfig: qdrantclient.NewVectorsConfig(&qdrantclient.VectorParams{
				Size:     store.dimensions,
				Distance: qdrantclient.Distance_Cosine,
			}),
		}); err != nil {
			created, checkErr := store.client.CollectionExists(ctx, physical)
			if checkErr != nil || !created {
				return fmt.Errorf("qdrant: create collection %q: %w", physical, err)
			}
		}
	}
	if err := store.verifyDimensions(ctx, physical); err != nil {
		return err
	}
	return store.ensurePayloadIndexes(ctx, physical)
}

// verifyDimensions refuses a collection whose vector width differs from the
// configured profile. Writing one width into a collection of another fails deep
// inside Qdrant, so it is checked while the collection is still being opened.
func (store *Store) verifyDimensions(ctx context.Context, physical string) error {
	info, err := store.client.GetCollectionInfo(ctx, physical)
	if err != nil {
		return fmt.Errorf("qdrant: inspect collection %q: %w", physical, err)
	}
	size := info.GetConfig().GetParams().GetVectorsConfig().GetParams().GetSize()
	if size == 0 {
		return fmt.Errorf("qdrant: collection %q has no single dense vector configuration", physical)
	}
	if size != store.dimensions {
		return fmt.Errorf(
			"qdrant: collection %q stores %d-dimensional vectors while the configured profile needs %d",
			physical, size, store.dimensions,
		)
	}
	return nil
}

// ensurePayloadIndexes creates the payload indexes the filters need. A
// concurrent creator makes the create call fail without anything being wrong, so
// the schema is re-read before the failure is reported.
func (store *Store) ensurePayloadIndexes(ctx context.Context, physical string) error {
	info, err := store.client.GetCollectionInfo(ctx, physical)
	if err != nil {
		return fmt.Errorf("qdrant: inspect payload indexes of %q: %w", physical, err)
	}
	fields := map[string]qdrantclient.FieldType{
		payloadStorageDomain:       qdrantclient.FieldType_FieldTypeKeyword,
		payloadDocumentID:          qdrantclient.FieldType_FieldTypeKeyword,
		payloadVersionID:           qdrantclient.FieldType_FieldTypeKeyword,
		payloadOwnerSpaceID:        qdrantclient.FieldType_FieldTypeKeyword,
		payloadOwnerSubjectKey:     qdrantclient.FieldType_FieldTypeKeyword,
		payloadAllowedSpaceIDs:     qdrantclient.FieldType_FieldTypeKeyword,
		payloadVectorProfile:       qdrantclient.FieldType_FieldTypeKeyword,
		payloadAuthenticatedPublic: qdrantclient.FieldType_FieldTypeBool,
		payloadActive:              qdrantclient.FieldType_FieldTypeBool,
		payloadTombstoned:          qdrantclient.FieldType_FieldTypeBool,
	}
	wait := true
	for field, fieldType := range fields {
		if _, exists := info.PayloadSchema[field]; exists {
			continue
		}
		fieldType := fieldType
		if _, err := store.client.CreateFieldIndex(ctx, &qdrantclient.CreateFieldIndexCollection{
			CollectionName: physical,
			Wait:           &wait,
			FieldName:      field,
			FieldType:      &fieldType,
		}); err != nil {
			refreshed, inspectErr := store.client.GetCollectionInfo(ctx, physical)
			if inspectErr != nil {
				return fmt.Errorf("qdrant: create payload index %q: %w", field, errors.Join(err, inspectErr))
			}
			if _, exists := refreshed.PayloadSchema[field]; !exists {
				return fmt.Errorf("qdrant: create payload index %q: %w", field, err)
			}
		}
	}
	return nil
}

// searchFilter translates the caller's scope into a Qdrant filter. It mirrors
// the SQL scope predicate so the recall is not diluted by documents the caller
// cannot see; the SQL filter remains the authorization decision.
func (store *Store) searchFilter(filter Filter) *qdrantclient.Filter {
	must := []*qdrantclient.Condition{
		qdrantclient.NewMatchKeyword(payloadStorageDomain, store.storageDomain),
		qdrantclient.NewMatchBool(payloadActive, true),
		qdrantclient.NewMatchBool(payloadTombstoned, false),
	}
	if subject := strings.TrimSpace(filter.OwnerSubjectKey); subject != "" {
		must = append(must, qdrantclient.NewMatchKeyword(payloadOwnerSubjectKey, subject))
	}
	authorization := make([]*qdrantclient.Condition, 0, 4)
	if filter.IncludePublic {
		authorization = append(authorization, qdrantclient.NewMatchBool(payloadAuthenticatedPublic, true))
	}
	if documents := normalizeIDs(filter.DocumentIDs); len(documents) > 0 {
		authorization = append(authorization, qdrantclient.NewMatchKeywords(payloadDocumentID, documents...))
	}
	if spaces := normalizeIDs(filter.SpaceIDs); len(spaces) > 0 {
		authorization = append(authorization,
			qdrantclient.NewMatchKeywords(payloadOwnerSpaceID, spaces...),
			qdrantclient.NewMatchKeywords(payloadAllowedSpaceIDs, spaces...),
		)
	}
	result := &qdrantclient.Filter{Must: must}
	if len(authorization) > 0 {
		result.MinShould = &qdrantclient.MinShould{Conditions: authorization, MinCount: 1}
	} else {
		// No authorized family at all: make the filter unsatisfiable instead of
		// returning points, which is the fail-closed direction.
		result.Must = append(result.Must, qdrantclient.NewMatchKeyword(payloadDocumentID, ""))
	}
	return result
}

// deleteByDocumentIDs removes the points of the given documents.
func (store *Store) deleteByDocumentIDs(ctx context.Context, documentIDs []string, wait bool) error {
	if _, err := store.client.Delete(ctx, &qdrantclient.DeletePoints{
		CollectionName: store.alias,
		Wait:           &wait,
		Points: qdrantclient.NewPointsSelectorFilter(&qdrantclient.Filter{
			Must: []*qdrantclient.Condition{
				qdrantclient.NewMatchKeywords(payloadDocumentID, documentIDs...),
			},
		}),
	}); err != nil {
		return fmt.Errorf("qdrant: delete points of %d document(s): %w", len(documentIDs), err)
	}
	return nil
}

// chunkPayload builds one point's payload.
func (store *Store) chunkPayload(document Document, chunkIndex int) (map[string]*qdrantclient.Value, error) {
	allowedSpaces := make([]any, 0, len(document.AllowedSpaceIDs))
	for _, space := range normalizeIDs(document.AllowedSpaceIDs) {
		allowedSpaces = append(allowedSpaces, space)
	}
	profile := strings.TrimSpace(document.VectorProfile)
	if profile == "" {
		profile = store.profile
	}
	payload, err := qdrantclient.TryValueMap(map[string]any{
		payloadStorageDomain:       store.storageDomain,
		payloadDocumentID:          document.DocumentID,
		payloadVersionID:           document.VersionID,
		payloadOwnerSpaceID:        document.OwnerSpaceID,
		payloadOwnerSubjectKey:     document.OwnerSubjectKey,
		payloadAuthenticatedPublic: document.AuthenticatedPublic,
		payloadAllowedSpaceIDs:     allowedSpaces,
		payloadActive:              true,
		payloadTombstoned:          false,
		payloadAggregateRevision:   int64(document.AggregateRevision),
		payloadAccessRevision:      int64(document.AccessRevision),
		payloadLifecycleRevision:   int64(document.LifecycleRevision),
		payloadVectorProfile:       profile,
		payloadVectorDimensions:    int64(document.VectorDimensions),
		payloadChunkIndex:          int64(chunkIndex),
		payloadTitle:               document.Title,
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant: build payload for document %q: %w", document.DocumentID, err)
	}
	return payload, nil
}

// payloadString reads a string payload value, tolerating a missing key.
func payloadString(payload map[string]*qdrantclient.Value, key string) string {
	value, ok := payload[key]
	if !ok {
		return ""
	}
	return value.GetStringValue()
}

// documentIDs returns the document ids of a batch.
func documentIDs(documents []Document) []string {
	ids := make([]string, 0, len(documents))
	for _, document := range documents {
		ids = append(ids, document.DocumentID)
	}
	return normalizeIDs(ids)
}

// normalizeIDs trims, lower-cases, de-duplicates and sorts an identifier set, so
// a filter built from it is stable and comparable.
func normalizeIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed == "" {
			continue
		}
		if _, duplicate := seen[trimmed]; duplicate {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	sort.Strings(result)
	return result
}
