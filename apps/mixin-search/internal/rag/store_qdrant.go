package rag

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"

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
	Host       string
	Port       int
	APIKey     string
	UseTLS     bool
	Collection string
	Dimensions uint64
}

type QdrantStore struct {
	client     *qdrant.Client
	collection string
}

func NewQdrantStore(ctx context.Context, config QdrantConfig) (*QdrantStore, error) {
	if config.Collection == "" {
		config.Collection = "rag_chunks"
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
	store := &QdrantStore{client: client, collection: config.Collection}
	if err := store.ensureCollection(ctx, config.Dimensions); err != nil {
		_ = client.Close()
		return nil, err
	}
	return store, nil
}

func (s *QdrantStore) ensureCollection(ctx context.Context, dimensions uint64) error {
	exists, err := s.client.CollectionExists(ctx, s.collection)
	if err != nil {
		return fmt.Errorf("check qdrant collection: %w", err)
	}
	if !exists {
		modifier := qdrant.Modifier_Idf
		if err := s.client.CreateCollection(ctx, &qdrant.CreateCollection{
			CollectionName: s.collection,
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
			return fmt.Errorf("create qdrant collection: %w", err)
		}
	}
	return s.ensureControlPayloadIndexes(ctx)
}

func (s *QdrantStore) ensureControlPayloadIndexes(ctx context.Context) error {
	info, err := s.client.GetCollectionInfo(ctx, s.collection)
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
			CollectionName: s.collection,
			Wait:           &wait,
			FieldName:      field,
			FieldType:      &fieldType,
		}); err != nil {
			refreshed, inspectErr := s.client.GetCollectionInfo(ctx, s.collection)
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
		CollectionName: s.collection,
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
		CollectionName: s.collection,
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
			CollectionName: s.collection,
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
		CollectionName: s.collection,
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
