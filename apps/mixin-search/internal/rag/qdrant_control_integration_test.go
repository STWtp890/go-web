package rag

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/qdrant/go-client/qdrant"
)

func TestQdrantDocumentControlIntegration(t *testing.T) {
	if os.Getenv("QDRANT_INTEGRATION") != "1" {
		t.Skip("set QDRANT_INTEGRATION=1 to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	collection := fmt.Sprintf("rag_control_%d", time.Now().UnixNano())
	store, err := NewQdrantStore(ctx, QdrantConfig{
		Host:       qdrantIntegrationHost(),
		Port:       qdrantIntegrationPort(t),
		Collection: collection,
		Dimensions: localEmbeddingDimensions,
	})
	if err != nil {
		t.Fatal(err)
	}
	core, err := NewServiceWithStore(ctx, store)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	defer func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		deleteAliasAndPhysical(t, cleanupContext, store)
		if err := core.Close(); err != nil {
			t.Errorf("close qdrant store: %v", err)
		}
	}()
	service, err := NewDocumentIndexService(core)
	if err != nil {
		t.Fatal(err)
	}

	needle := fmt.Sprintf("controlneedle%d", time.Now().UnixNano())
	mustIndexContractVersion(t, service, "qdrant-public", "v1", "public-owner", 0, needle)
	mustActivateContractVersion(t, service, "qdrant-public", "v1", 1, 0)
	if _, err := service.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID: "qdrant-public-access-1", DocumentID: "qdrant-public", AccessRevision: 1,
		AuthenticatedPublic: true,
	}); err != nil {
		t.Fatal(err)
	}
	mustIndexContractVersion(t, service, "qdrant-private", "v1", "private-owner", 0, needle)
	mustActivateContractVersion(t, service, "qdrant-private", "v1", 1, 0)
	mustIndexContractVersion(t, service, "qdrant-inactive", "v1", "public-owner", 0, needle)
	if _, err := service.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID: "qdrant-inactive-public", DocumentID: "qdrant-inactive", AccessRevision: 1,
		AuthenticatedPublic: true,
	}); err != nil {
		t.Fatal(err)
	}

	publicOnly := mustSearchDocumentVersions(t, service, SearchDocumentsRequest{Query: needle, TopK: 10})
	assertDocumentVersions(t, publicOnly, map[string]string{"qdrant-public": "v1"})
	assertQdrantControlPayload(t, ctx, store, "qdrant-public", "v1", true, false, 1, 1, 0)

	ownerAuthorized := mustSearchDocumentVersions(t, service, SearchDocumentsRequest{
		Query: needle, AllowedSpaceIDs: []string{"private-owner"}, TopK: 10,
	})
	assertDocumentVersions(t, ownerAuthorized, map[string]string{
		"qdrant-public": "v1", "qdrant-private": "v1",
	})

	if _, err := service.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID: "qdrant-private-grant", DocumentID: "qdrant-private", AccessRevision: 1,
		GrantedSpaceIDs: []string{"shared-space"},
	}); err != nil {
		t.Fatal(err)
	}
	spaceAuthorized := mustSearchDocumentVersions(t, service, SearchDocumentsRequest{
		Query: needle, AllowedSpaceIDs: []string{"shared-space"}, TopK: 10,
	})
	assertDocumentVersions(t, spaceAuthorized, map[string]string{
		"qdrant-public": "v1", "qdrant-private": "v1",
	})
	if _, err := service.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID: "qdrant-private-revoke", DocumentID: "qdrant-private", AccessRevision: 2,
	}); err != nil {
		t.Fatal(err)
	}
	afterRevoke := mustSearchDocumentVersions(t, service, SearchDocumentsRequest{
		Query: needle, AllowedSpaceIDs: []string{"shared-space"}, TopK: 10,
	})
	assertDocumentVersions(t, afterRevoke, map[string]string{"qdrant-public": "v1"})
	explicitDocument := mustSearchDocumentVersions(t, service, SearchDocumentsRequest{
		Query: needle, AllowedDocumentIDs: []string{"qdrant-private"}, TopK: 10,
	})
	assertDocumentVersions(t, explicitDocument, map[string]string{
		"qdrant-public": "v1", "qdrant-private": "v1",
	})

	mustIndexContractVersion(t, service, "qdrant-public", "v2", "public-owner", 0, needle)
	mustActivateContractVersion(t, service, "qdrant-public", "v2", 2, 0)
	afterSwitch := mustSearchDocumentVersions(t, service, SearchDocumentsRequest{Query: needle, TopK: 10})
	assertDocumentVersions(t, afterSwitch, map[string]string{"qdrant-public": "v2"})
	assertQdrantControlPayload(t, ctx, store, "qdrant-public", "v1", false, false, 2, 1, 0)
	assertQdrantControlPayload(t, ctx, store, "qdrant-public", "v2", true, false, 2, 1, 0)

	if _, err := service.DeleteDocument(ctx, DeleteDocumentRequest{
		OperationID: "qdrant-public-delete", DocumentID: "qdrant-public", LifecycleRevision: 1,
	}); err != nil {
		t.Fatal(err)
	}
	afterDelete := mustSearchDocumentVersions(t, service, SearchDocumentsRequest{Query: needle, TopK: 10})
	assertDocumentVersions(t, afterDelete, map[string]string{})

	mustIndexContractVersion(t, service, "qdrant-public", "v3", "public-owner", 2, needle)
	mustActivateContractVersion(t, service, "qdrant-public", "v3", 3, 2)
	if _, err := service.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID: "qdrant-public-access-2", DocumentID: "qdrant-public", AccessRevision: 2,
		LifecycleRevision: 2, AuthenticatedPublic: true,
	}); err != nil {
		t.Fatal(err)
	}
	afterRepublish := mustSearchDocumentVersions(t, service, SearchDocumentsRequest{Query: needle, TopK: 10})
	assertDocumentVersions(t, afterRepublish, map[string]string{"qdrant-public": "v3"})
}

func TestQdrantStorageDomainIsolationIntegration(t *testing.T) {
	if os.Getenv("QDRANT_INTEGRATION") != "1" {
		t.Skip("set QDRANT_INTEGRATION=1 to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	collection := fmt.Sprintf("rag_domain_%d", time.Now().UnixNano())
	store, err := NewQdrantStore(ctx, QdrantConfig{
		Host:       qdrantIntegrationHost(),
		Port:       qdrantIntegrationPort(t),
		Collection: collection,
		Dimensions: localEmbeddingDimensions,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		deleteAliasAndPhysical(t, cleanupContext, store)
		if err := store.Close(); err != nil {
			t.Errorf("close qdrant store: %v", err)
		}
	}()

	newIndexService := func() *DocumentIndexService {
		core, err := NewServiceWithStore(ctx, store)
		if err != nil {
			t.Fatal(err)
		}
		service, err := NewDocumentIndexService(core)
		if err != nil {
			t.Fatal(err)
		}
		return service
	}
	serviceA := newIndexService()
	serviceB := newIndexService()
	mustIndexContractVersion(t, serviceA, "same-document", "v1", "owner", 0, "domainneedle alpha")
	mustActivateContractVersion(t, serviceA, "same-document", "v1", 1, 0)
	if _, err := serviceA.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID: "domain-access", DocumentID: "same-document", AccessRevision: 1, AuthenticatedPublic: true,
	}); err != nil {
		t.Fatal(err)
	}
	mustIndexContractVersion(t, serviceB, "same-document", "v1", "owner", 0, "domainneedle beta")
	mustActivateContractVersion(t, serviceB, "same-document", "v1", 1, 0)
	if _, err := serviceB.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID: "domain-access", DocumentID: "same-document", AccessRevision: 1, AuthenticatedPublic: true,
	}); err != nil {
		t.Fatal(err)
	}

	resultA, err := serviceA.SearchDocuments(ctx, SearchDocumentsRequest{Query: "domainneedle", TopK: 10})
	if err != nil {
		t.Fatal(err)
	}
	resultB, err := serviceB.SearchDocuments(ctx, SearchDocumentsRequest{Query: "domainneedle", TopK: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(resultA.Hits) != 1 || resultA.Hits[0].Chunk.Content != "domainneedle alpha" {
		t.Fatalf("domain A hits = %+v", resultA.Hits)
	}
	if len(resultB.Hits) != 1 || resultB.Hits[0].Chunk.Content != "domainneedle beta" {
		t.Fatalf("domain B hits = %+v", resultB.Hits)
	}
}

func qdrantIntegrationHost() string {
	if host := os.Getenv("QDRANT_HOST"); host != "" {
		return host
	}
	return "localhost"
}

func qdrantIntegrationPort(t *testing.T) int {
	t.Helper()
	value := os.Getenv("QDRANT_PORT")
	if value == "" {
		return 6334
	}
	port, err := strconv.Atoi(value)
	if err != nil || port <= 0 || port > 65535 {
		t.Fatalf("invalid QDRANT_PORT %q", value)
	}
	return port
}

func mustSearchDocumentVersions(
	t *testing.T,
	service *DocumentIndexService,
	request SearchDocumentsRequest,
) map[string]string {
	t.Helper()
	result, err := service.SearchDocuments(context.Background(), request)
	if err != nil {
		t.Fatalf("search %q: %v", request.Query, err)
	}
	documents := make(map[string]string, len(result.Hits))
	for _, hit := range result.Hits {
		if previous, duplicate := documents[hit.Chunk.DocumentID]; duplicate && previous != hit.Chunk.VersionID {
			t.Fatalf("document %q returned multiple versions: %q and %q", hit.Chunk.DocumentID, previous, hit.Chunk.VersionID)
		}
		documents[hit.Chunk.DocumentID] = hit.Chunk.VersionID
	}
	return documents
}

func assertDocumentVersions(t *testing.T, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("document versions = %+v, want %+v", got, want)
	}
	for documentID, versionID := range want {
		if got[documentID] != versionID {
			t.Fatalf("document versions = %+v, want %+v", got, want)
		}
	}
}

func assertQdrantControlPayload(
	t *testing.T,
	ctx context.Context,
	store *QdrantStore,
	documentID string,
	versionID string,
	active bool,
	tombstoned bool,
	activationRevision uint64,
	accessRevision uint64,
	lifecycleRevision uint64,
) {
	t.Helper()
	limit := uint32(10)
	points, err := store.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: store.alias,
		Filter: &qdrant.Filter{Must: []*qdrant.Condition{
			qdrant.NewMatchKeyword(qdrantPayloadDocumentID, documentID),
			qdrant.NewMatchKeyword(qdrantPayloadVersionID, versionID),
		}},
		Limit:       &limit,
		WithPayload: qdrant.NewWithPayload(true),
	})
	if err != nil {
		t.Fatalf("scroll qdrant payload: %v", err)
	}
	if len(points) == 0 {
		t.Fatalf("missing qdrant payload for %s/%s", documentID, versionID)
	}
	for _, point := range points {
		payload := point.Payload
		if payload[qdrantPayloadStorageID].GetStringValue() == "" ||
			payload[qdrantPayloadStorageDomain].GetStringValue() == "" ||
			payload[qdrantPayloadOwnerSpaceID].GetStringValue() == "" ||
			payload[qdrantPayloadActive].GetBoolValue() != active ||
			payload[qdrantPayloadTombstoned].GetBoolValue() != tombstoned ||
			uint64(payload[qdrantPayloadActivationRevision].GetIntegerValue()) != activationRevision ||
			uint64(payload[qdrantPayloadAccessRevision].GetIntegerValue()) != accessRevision ||
			uint64(payload[qdrantPayloadLifecycleRevision].GetIntegerValue()) != lifecycleRevision ||
			payload[qdrantPayloadContentSHA256].GetStringValue() == "" {
			t.Fatalf("unexpected qdrant control payload: %+v", payload)
		}
	}
}
