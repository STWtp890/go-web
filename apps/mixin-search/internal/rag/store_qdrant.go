package rag

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"

	"github.com/qdrant/go-client/qdrant"
)

const (
	qdrantDenseVector  = "content_dense"
	qdrantSparseVector = "content_sparse"

	qdrantPayloadStorageID           = "storage_id"
	qdrantPayloadStorageDomain       = "storage_domain"
	qdrantPayloadDocumentID          = "document_id"
	qdrantPayloadVersionID           = "version_id"
	qdrantPayloadOwnerSpaceID        = "owner_space_id"
	qdrantPayloadAuthenticatedPublic = "authenticated_public"
	qdrantPayloadGrantedSpaceIDs     = "granted_space_ids"
	qdrantPayloadActive              = "active"
	qdrantPayloadTombstoned          = "tombstoned"
	qdrantPayloadActivationRevision  = "activation_revision"
	qdrantPayloadAccessRevision      = "access_revision"
	qdrantPayloadLifecycleRevision   = "lifecycle_revision"
	qdrantPayloadContentSHA256       = "content_sha256"
)

type QdrantConfig struct {
	Host   string
	Port   int
	APIKey string
	UseTLS bool
	// Collection is the stable name every read and write addresses. It is an
	// alias over physical collections, not a physical collection itself: a
	// rebuild fills a new <Collection>_<Generation> and points this name at it,
	// so callers never learn which generation answered.
	Collection string
	// Generation labels the physical collection that is created when the alias
	// does not exist yet, as <Collection>_<Generation>. It defaults to g1.
	Generation string
	Dimensions uint64
}

// DefaultQdrantGeneration is the first physical generation of a corpus.
const DefaultQdrantGeneration = "g1"

type QdrantStore struct {
	client *qdrant.Client
	// alias is the stable name every operation uses. Qdrant resolves it, which is
	// what makes a switch atomic for readers and writers alike.
	alias string
	// physical records the collection the alias resolved to at startup. It is for
	// diagnostics only; operations deliberately go through the alias.
	physical string
}

func NewQdrantStore(ctx context.Context, config QdrantConfig) (*QdrantStore, error) {
	if strings.TrimSpace(config.Collection) == "" {
		config.Collection = "rag_chunks"
	}
	if strings.TrimSpace(config.Generation) == "" {
		config.Generation = DefaultQdrantGeneration
	}
	if config.Dimensions == 0 {
		config.Dimensions = localEmbeddingDimensions
	}

	client, err := qdrant.NewClient(&qdrant.Config{
		Host:     config.Host,
		Port:     config.Port,
		APIKey:   config.APIKey,
		UseTLS:   config.UseTLS,
		PoolSize: 1,
	})
	if err != nil {
		return nil, fmt.Errorf("connect qdrant: %w", err)
	}
	store := &QdrantStore{client: client, alias: strings.TrimSpace(config.Collection)}
	if err := store.ensureAlias(ctx, config.Generation, config.Dimensions); err != nil {
		_ = client.Close()
		return nil, err
	}
	return store, nil
}

// ensureAlias makes the configured name a working alias: it resolves the alias
// when it exists, and otherwise creates the configured generation behind it.
//
// A physical collection already sitting on the alias name is refused rather than
// adopted. That layout is what this service used before aliases existed, and
// guessing at it - is this collection the corpus, or a stale generation? - is how
// a stack ends up serving two names for one corpus. The agreed baseline is a
// fresh development volume, so the failure says exactly that.
func (s *QdrantStore) ensureAlias(ctx context.Context, generation string, dimensions uint64) error {
	target, exists, err := s.aliasedCollection(ctx)
	if err != nil {
		return err
	}
	if !exists {
		physical := s.physicalName(generation)
		legacy, err := s.client.CollectionExists(ctx, s.alias)
		if err != nil {
			return fmt.Errorf("check qdrant collection %q: %w", s.alias, err)
		}
		if legacy {
			return fmt.Errorf(
				"qdrant collection %q exists as a physical collection while this corpus requires it to be an alias; "+
					"there is no legacy migration: remove the project volumes and rebuild the stack",
				s.alias,
			)
		}
		physicalExists, err := s.client.CollectionExists(ctx, physical)
		if err != nil {
			return fmt.Errorf("check qdrant collection %q: %w", physical, err)
		}
		if !physicalExists {
			if err := s.createPhysicalCollection(ctx, physical, dimensions); err != nil {
				return err
			}
		}
		if err := s.client.CreateAlias(ctx, s.alias, physical); err != nil {
			return fmt.Errorf("create qdrant alias %q -> %q: %w", s.alias, physical, err)
		}
		target = physical
	}
	if err := s.ensureControlPayloadIndexes(ctx, target); err != nil {
		return err
	}
	s.physical = target
	return nil
}

// aliasedCollection resolves this corpus's alias to its physical collection.
func (s *QdrantStore) aliasedCollection(ctx context.Context) (string, bool, error) {
	aliases, err := s.client.ListAliases(ctx)
	if err != nil {
		return "", false, fmt.Errorf("list qdrant aliases: %w", err)
	}
	for _, description := range aliases {
		if description.GetAliasName() == s.alias {
			return description.GetCollectionName(), true, nil
		}
	}
	return "", false, nil
}

func (s *QdrantStore) physicalName(generation string) string {
	return s.alias + "_" + strings.TrimSpace(generation)
}

// Alias reports the stable name this store addresses.
func (s *QdrantStore) Alias() string { return s.alias }

// PhysicalCollection resolves the collection the alias currently points at.
func (s *QdrantStore) PhysicalCollection(ctx context.Context) (string, error) {
	target, exists, err := s.aliasedCollection(ctx)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("qdrant alias %q does not exist", s.alias)
	}
	return target, nil
}

// PrepareGeneration creates the physical collection of a new generation without
// pointing the alias at it, so a rebuild can fill and verify it first. An
// existing collection of that name is adopted, which makes a retried rebuild
// idempotent.
func (s *QdrantStore) PrepareGeneration(ctx context.Context, generation string, dimensions uint64) (string, error) {
	generation = strings.TrimSpace(generation)
	if generation == "" {
		return "", errors.New("qdrant generation label is required")
	}
	if dimensions == 0 {
		dimensions = localEmbeddingDimensions
	}
	physical := s.physicalName(generation)
	exists, err := s.client.CollectionExists(ctx, physical)
	if err != nil {
		return "", fmt.Errorf("check qdrant collection %q: %w", physical, err)
	}
	if !exists {
		if err := s.createPhysicalCollection(ctx, physical, dimensions); err != nil {
			return "", err
		}
	}
	if err := s.ensureControlPayloadIndexes(ctx, physical); err != nil {
		return "", err
	}
	return physical, nil
}

// SwitchAlias points this corpus's alias at another physical collection, so a
// reader sees either the old collection or the new one and never neither.
//
// The re-point is a *single* create-alias action, deliberately not a
// delete-then-create batch: Qdrant applies the actions of one UpdateAliases call
// in order and without rollback (qdrant v1.19.1, `update_aliases` in
// `collection_meta_ops.rs` deletes first and then fails the create with `?`), so
// a batch would delete the alias and leave it deleted whenever the create fails.
// Creating an alias that already exists replaces the mapping in one persisted
// step, which is what makes a failed switch non-destructive.
//
// The target must be one of this corpus's own generations, and it is validated
// before anything moves: it must exist and carry the payload indexes candidate
// filtering needs. A typo therefore fails the switch instead of taking the corpus
// offline, and it fails closed - the alias keeps pointing at the generation that
// was serving.
func (s *QdrantStore) SwitchAlias(ctx context.Context, target string) error {
	target = strings.TrimSpace(target)
	if err := s.validateAliasTarget(target); err != nil {
		return err
	}
	exists, err := s.client.CollectionExists(ctx, target)
	if err != nil {
		return fmt.Errorf("check qdrant alias target %q: %w", target, err)
	}
	if !exists {
		// CollectionExists resolves aliases, so this also catches a target that is
		// another corpus's alias name rather than a collection.
		return fmt.Errorf("qdrant alias target %q does not exist", target)
	}
	current, aliased, err := s.aliasedCollection(ctx)
	if err != nil {
		return err
	}
	if !aliased {
		return fmt.Errorf("qdrant alias %q does not exist, so it cannot be switched", s.alias)
	}
	if current == target {
		s.physical = current
		return nil
	}
	// Only a switch that is actually going to happen prepares the target: index
	// creation is a write, and a failing call should not have written anything.
	if err := s.ensureControlPayloadIndexes(ctx, target); err != nil {
		return err
	}
	if err := s.client.CreateAlias(ctx, s.alias, target); err != nil {
		return fmt.Errorf("switch qdrant alias %q to %q: %w", s.alias, target, err)
	}
	// Read the mapping back: the switch is only done when the alias says so, and a
	// mapping that cannot be read back is reported rather than assumed.
	switched, exists, err := s.aliasedCollection(ctx)
	if err != nil {
		return fmt.Errorf("verify qdrant alias %q after switching to %q: %w", s.alias, target, err)
	}
	if !exists || switched != target {
		return fmt.Errorf("qdrant alias %q points at %q after switching, want %q", s.alias, switched, target)
	}
	s.physical = switched
	return nil
}

// validateAliasTarget refuses a target that is not one of this corpus's own
// generations.
//
// Generations are named <alias>_<label>, so requiring that prefix is what keeps a
// switch inside one corpus: the other corpus's collections cannot match, and
// neither can an arbitrary collection an operator names by mistake. Without this
// the store would happily point the chat alias at the document corpus's
// collection, which is the one pairing the startup guard exists to prevent - and
// the startup guard cannot see a switch performed while the service is running.
func (s *QdrantStore) validateAliasTarget(target string) error {
	if target == "" {
		return errors.New("qdrant alias target is required")
	}
	if target == s.alias || !strings.HasPrefix(target, s.alias+"_") {
		return fmt.Errorf(
			"qdrant alias %q can only switch to one of its own generations (%s_<label>), not to %q",
			s.alias, s.alias, target,
		)
	}
	return nil
}

func (s *QdrantStore) createPhysicalCollection(ctx context.Context, physical string, dimensions uint64) error {
	modifier := qdrant.Modifier_Idf
	if err := s.client.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: physical,
		VectorsConfig: qdrant.NewVectorsConfigMap(map[string]*qdrant.VectorParams{
			qdrantDenseVector: {
				Size:     dimensions,
				Distance: qdrant.Distance_Cosine,
			},
		}),
		SparseVectorsConfig: qdrant.NewSparseVectorsConfig(map[string]*qdrant.SparseVectorParams{
			qdrantSparseVector: {Modifier: &modifier},
		}),
	}); err != nil {
		return fmt.Errorf("create qdrant collection %q: %w", physical, err)
	}
	return nil
}

func (s *QdrantStore) ensureControlPayloadIndexes(ctx context.Context, physical string) error {
	info, err := s.client.GetCollectionInfo(ctx, physical)
	if err != nil {
		return fmt.Errorf("inspect qdrant payload indexes: %w", err)
	}
	fields := map[string]qdrant.FieldType{
		qdrantPayloadStorageID:           qdrant.FieldType_FieldTypeKeyword,
		qdrantPayloadStorageDomain:       qdrant.FieldType_FieldTypeKeyword,
		qdrantPayloadDocumentID:          qdrant.FieldType_FieldTypeKeyword,
		qdrantPayloadVersionID:           qdrant.FieldType_FieldTypeKeyword,
		qdrantPayloadOwnerSpaceID:        qdrant.FieldType_FieldTypeKeyword,
		qdrantPayloadAuthenticatedPublic: qdrant.FieldType_FieldTypeBool,
		qdrantPayloadGrantedSpaceIDs:     qdrant.FieldType_FieldTypeKeyword,
		qdrantPayloadActive:              qdrant.FieldType_FieldTypeBool,
		qdrantPayloadTombstoned:          qdrant.FieldType_FieldTypeBool,
	}
	wait := true
	for field, fieldType := range fields {
		if _, ok := info.PayloadSchema[field]; ok {
			continue
		}
		fieldType := fieldType
		if _, err := s.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
			CollectionName: physical,
			Wait:           &wait,
			FieldName:      field,
			FieldType:      &fieldType,
		}); err != nil {
			refreshed, inspectErr := s.client.GetCollectionInfo(ctx, physical)
			if inspectErr != nil {
				return fmt.Errorf("create qdrant payload index %q: %w", field, errors.Join(err, inspectErr))
			}
			if _, ok := refreshed.PayloadSchema[field]; !ok {
				return fmt.Errorf("create qdrant payload index %q: %w", field, err)
			}
		}
	}
	return nil
}

func (s *QdrantStore) ReplaceDocument(ctx context.Context, documentID string, chunks []IndexedChunk) error {
	wait := true
	_, err := s.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: s.alias,
		Wait:           &wait,
		Points: qdrant.NewPointsSelectorFilter(&qdrant.Filter{
			Must: []*qdrant.Condition{qdrant.NewMatchKeyword(qdrantPayloadStorageID, documentID)},
		}),
	})
	if err != nil {
		return fmt.Errorf("delete old qdrant chunks: %w", err)
	}
	if len(chunks) == 0 {
		return nil
	}

	points := make([]*qdrant.PointStruct, 0, len(chunks))
	for _, chunk := range chunks {
		indices, values := termsToSparseVector(chunk.Terms)
		payload, err := qdrant.TryValueMap(map[string]any{
			"chunk_id":                       chunk.Chunk.ID,
			qdrantPayloadStorageID:           chunk.Chunk.DocumentID,
			qdrantPayloadStorageDomain:       "",
			qdrantPayloadDocumentID:          "",
			qdrantPayloadVersionID:           "",
			qdrantPayloadOwnerSpaceID:        "",
			qdrantPayloadAuthenticatedPublic: false,
			qdrantPayloadGrantedSpaceIDs:     []any{},
			qdrantPayloadActive:              false,
			qdrantPayloadTombstoned:          false,
			qdrantPayloadActivationRevision:  0,
			qdrantPayloadAccessRevision:      0,
			qdrantPayloadLifecycleRevision:   0,
			qdrantPayloadContentSHA256:       "",
			"title":                          chunk.Chunk.Title,
			"content":                        chunk.Chunk.Content,
			"position":                       chunk.Chunk.Position,
			"format":                         chunk.Chunk.Format,
			"source":                         chunk.Chunk.Source,
			"section":                        chunk.Chunk.Section,
		})
		if err != nil {
			return fmt.Errorf("build qdrant payload for %q: %w", chunk.Chunk.ID, err)
		}
		points = append(points, &qdrant.PointStruct{
			Id: qdrant.NewID(deterministicUUID(chunk.Chunk.ID)),
			Vectors: qdrant.NewVectorsMap(map[string]*qdrant.Vector{
				qdrantDenseVector:  qdrant.NewVectorDense(toFloat32(chunk.Dense)),
				qdrantSparseVector: qdrant.NewVectorSparse(indices, values),
			}),
			Payload: payload,
		})
	}
	if _, err := s.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: s.alias,
		Wait:           &wait,
		Points:         points,
	}); err != nil {
		return fmt.Errorf("upsert qdrant chunks: %w", err)
	}
	return nil
}

func (s *QdrantStore) SyncDocumentControls(ctx context.Context, controls []VectorDocumentControl) error {
	wait := true
	for _, control := range controls {
		if control.StorageID == "" {
			return errors.New("qdrant control storage id is required")
		}
		payload, err := qdrant.TryValueMap(map[string]any{
			qdrantPayloadStorageDomain:       control.StorageDomain,
			qdrantPayloadDocumentID:          control.DocumentID,
			qdrantPayloadVersionID:           control.VersionID,
			qdrantPayloadOwnerSpaceID:        control.OwnerSpaceID,
			qdrantPayloadAuthenticatedPublic: control.AuthenticatedPublic,
			qdrantPayloadGrantedSpaceIDs:     qdrantStringList(control.GrantedSpaceIDs),
			qdrantPayloadActive:              control.Active,
			qdrantPayloadTombstoned:          control.Tombstoned,
			qdrantPayloadActivationRevision:  control.ActivationRevision,
			qdrantPayloadAccessRevision:      control.AccessRevision,
			qdrantPayloadLifecycleRevision:   control.LifecycleRevision,
			qdrantPayloadContentSHA256:       control.ContentSHA256,
		})
		if err != nil {
			return fmt.Errorf("build qdrant control payload for %q: %w", control.StorageID, err)
		}
		_, err = s.client.SetPayload(ctx, &qdrant.SetPayloadPoints{
			CollectionName: s.alias,
			Wait:           &wait,
			Payload:        payload,
			PointsSelector: qdrant.NewPointsSelectorFilter(&qdrant.Filter{
				Must: []*qdrant.Condition{qdrant.NewMatchKeyword(qdrantPayloadStorageID, control.StorageID)},
			}),
		})
		if err != nil {
			return fmt.Errorf("sync qdrant control for %q: %w", control.StorageID, err)
		}
	}
	return nil
}

func (s *QdrantStore) DenseSearch(ctx context.Context, query []float64, limit int) ([]ScoredChunk, error) {
	return s.query(ctx, qdrant.NewQueryDense(toFloat32(query)), qdrantDenseVector, limit, nil)
}

func (s *QdrantStore) DenseSearchFiltered(
	ctx context.Context,
	query []float64,
	limit int,
	filter VectorSearchFilter,
) ([]ScoredChunk, error) {
	return s.query(ctx, qdrant.NewQueryDense(toFloat32(query)), qdrantDenseVector, limit, qdrantControlFilter(filter))
}

func (s *QdrantStore) SparseSearch(ctx context.Context, queryTokens []string, limit int) ([]ScoredChunk, error) {
	terms := make(map[string]int, len(queryTokens))
	for _, token := range queryTokens {
		terms[token]++
	}
	indices, values := termsToSparseVector(terms)
	if len(indices) == 0 {
		return nil, nil
	}
	return s.query(ctx, qdrant.NewQuerySparse(indices, values), qdrantSparseVector, limit, nil)
}

func (s *QdrantStore) SparseSearchFiltered(
	ctx context.Context,
	queryTokens []string,
	limit int,
	filter VectorSearchFilter,
) ([]ScoredChunk, error) {
	terms := make(map[string]int, len(queryTokens))
	for _, token := range queryTokens {
		terms[token]++
	}
	indices, values := termsToSparseVector(terms)
	if len(indices) == 0 {
		return nil, nil
	}
	return s.query(ctx, qdrant.NewQuerySparse(indices, values), qdrantSparseVector, limit, qdrantControlFilter(filter))
}

func (s *QdrantStore) query(
	ctx context.Context,
	query *qdrant.Query,
	vectorName string,
	limit int,
	filter *qdrant.Filter,
) ([]ScoredChunk, error) {
	if limit <= 0 {
		return nil, nil
	}
	queryLimit := uint64(limit)
	points, err := s.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: s.alias,
		Query:          query,
		Using:          qdrant.PtrOf(vectorName),
		Limit:          &queryLimit,
		WithPayload:    qdrant.NewWithPayload(true),
		Filter:         filter,
	})
	if err != nil {
		return nil, fmt.Errorf("query qdrant %s: %w", vectorName, err)
	}

	hits := make([]ScoredChunk, 0, len(points))
	for _, point := range points {
		chunk, err := chunkFromQdrantPayload(point.Payload)
		if err != nil {
			return nil, err
		}
		hits = append(hits, ScoredChunk{Chunk: chunk, Score: float64(point.Score)})
	}
	return hits, nil
}

func qdrantControlFilter(filter VectorSearchFilter) *qdrant.Filter {
	must := []*qdrant.Condition{
		qdrant.NewMatchBool(qdrantPayloadActive, true),
		qdrant.NewMatchBool(qdrantPayloadTombstoned, false),
	}
	if filter.StorageDomain != "" {
		must = append(must, qdrant.NewMatchKeyword(qdrantPayloadStorageDomain, filter.StorageDomain))
	}
	authorization := []*qdrant.Condition{
		qdrant.NewMatchBool(qdrantPayloadAuthenticatedPublic, true),
	}
	if documents := normalizeStrings(filter.AllowedDocumentIDs); len(documents) > 0 {
		authorization = append(authorization, qdrant.NewMatchKeywords(qdrantPayloadDocumentID, documents...))
	}
	if spaces := normalizeStrings(filter.AllowedSpaceIDs); len(spaces) > 0 {
		authorization = append(
			authorization,
			qdrant.NewMatchKeywords(qdrantPayloadOwnerSpaceID, spaces...),
			qdrant.NewMatchKeywords(qdrantPayloadGrantedSpaceIDs, spaces...),
		)
	}
	return &qdrant.Filter{
		Must:      must,
		MinShould: &qdrant.MinShould{Conditions: authorization, MinCount: 1},
	}
}

func (s *QdrantStore) Close() error {
	return s.client.Close()
}

func chunkFromQdrantPayload(payload map[string]*qdrant.Value) (Chunk, error) {
	required := func(key string) (string, error) {
		value, ok := payload[key]
		if !ok {
			return "", fmt.Errorf("qdrant payload is missing %q", key)
		}
		text := value.GetStringValue()
		if text == "" && key != "title" {
			return "", fmt.Errorf("qdrant payload %q is empty", key)
		}
		return text, nil
	}
	chunkID, err := required("chunk_id")
	if err != nil {
		return Chunk{}, err
	}
	documentID, err := required(qdrantPayloadStorageID)
	if err != nil {
		return Chunk{}, err
	}
	title, err := required("title")
	if err != nil {
		return Chunk{}, err
	}
	content, err := required("content")
	if err != nil {
		return Chunk{}, err
	}
	position, ok := payload["position"]
	if !ok {
		return Chunk{}, errors.New("qdrant payload is missing \"position\"")
	}
	return Chunk{
		ID:         chunkID,
		DocumentID: documentID,
		Title:      title,
		Content:    content,
		Position:   int(position.GetIntegerValue()),
		Format:     optionalQdrantString(payload, "format"),
		Source:     optionalQdrantString(payload, "source"),
		Section:    optionalQdrantString(payload, "section"),
	}, nil
}

func optionalQdrantString(payload map[string]*qdrant.Value, key string) string {
	if value := payload[key]; value != nil {
		return value.GetStringValue()
	}
	return ""
}

func qdrantStringList(values []string) []any {
	normalized := normalizeStrings(values)
	result := make([]any, len(normalized))
	for index, value := range normalized {
		result[index] = value
	}
	return result
}

func termsToSparseVector(terms map[string]int) ([]uint32, []float32) {
	valuesByIndex := make(map[uint32]float32, len(terms))
	for term, frequency := range terms {
		h := fnv.New32a()
		_, _ = h.Write([]byte(term))
		valuesByIndex[h.Sum32()] += float32(frequency)
	}
	indices := make([]uint32, 0, len(valuesByIndex))
	for index := range valuesByIndex {
		indices = append(indices, index)
	}
	sort.Slice(indices, func(i, j int) bool { return indices[i] < indices[j] })
	values := make([]float32, len(indices))
	for i, index := range indices {
		values[i] = valuesByIndex[index]
	}
	return indices, values
}

func deterministicUUID(value string) string {
	sum := sha256.Sum256([]byte(value))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func toFloat32(values []float64) []float32 {
	result := make([]float32, len(values))
	for i, value := range values {
		result[i] = float32(value)
	}
	return result
}
