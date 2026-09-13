package postgresql_test

import (
	"context"
	"errors"
	"testing"

	"gin-backend/internal/modules/document/application"
	"gin-backend/internal/modules/document/domain"
)

func TestQueryRepositoryIntegration(t *testing.T) {
	db, repository := openRepository(t)
	ctx := context.Background()
	ownerID := createUser(t, db, "query-owner")
	readerID := createUser(t, db, "query-reader")
	commands, err := application.NewCommandService(repository)
	if err != nil {
		t.Fatal(err)
	}
	queries, err := application.NewQueryService(repository, nil)
	if err != nil {
		t.Fatal(err)
	}

	keyword := "p13orionquery"
	publicDocument, err := commands.Create(ctx, application.CreateCommand{
		OwnerID: ownerID, Title: keyword + " public", Content: "public body", AuthenticatedPublic: true,
	})
	if err != nil {
		t.Fatalf("create public document: %v", err)
	}
	privateDocument, err := commands.Create(ctx, application.CreateCommand{
		OwnerID: ownerID, Title: keyword + " private", Content: "private body", AuthenticatedPublic: false,
	})
	if err != nil {
		t.Fatalf("create private document: %v", err)
	}

	mine, minePage, err := queries.ListMine(ctx, ownerID, 1, 10)
	if err != nil {
		t.Fatalf("list mine: %v", err)
	}
	if minePage.Total != 2 || !containsDocument(mine, publicDocument.Document.DocumentID) || !containsDocument(mine, privateDocument.Document.DocumentID) {
		t.Fatalf("mine = %#v page = %#v", mine, minePage)
	}

	public, publicPage, err := queries.ListPublic(ctx, 1, 10)
	if err != nil {
		t.Fatalf("list public: %v", err)
	}
	if publicPage.Total != 1 || !containsDocument(public, publicDocument.Document.DocumentID) || containsDocument(public, privateDocument.Document.DocumentID) {
		t.Fatalf("public = %#v page = %#v", public, publicPage)
	}

	search, searchPage, normalized, err := queries.SearchMine(ctx, ownerID, "  "+keyword+"  ", 1, 10)
	if err != nil {
		t.Fatalf("search mine: %v", err)
	}
	if normalized != keyword || searchPage.Total != 2 || !containsDocument(search, publicDocument.Document.DocumentID) || !containsDocument(search, privateDocument.Document.DocumentID) {
		t.Fatalf("search = %#v page = %#v keyword = %q", search, searchPage, normalized)
	}

	view, err := queries.Get(ctx, readerID, publicDocument.Document.DocumentID)
	if err != nil || view.Content != "public body" {
		t.Fatalf("reader get public: view=%#v error=%v", view, err)
	}
	if _, err := queries.Get(ctx, readerID, privateDocument.Document.DocumentID); !errors.Is(err, application.ErrQueryForbidden) {
		t.Fatalf("reader get private error = %v, want forbidden", err)
	}

	if err := commands.Trash(ctx, application.TrashCommand{OwnerID: ownerID, DocumentID: publicDocument.Document.DocumentID}); err != nil {
		t.Fatalf("trash public document: %v", err)
	}
	if _, err := queries.Get(ctx, ownerID, publicDocument.Document.DocumentID); !errors.Is(err, application.ErrQueryNotFound) {
		t.Fatalf("get trashed error = %v, want not found", err)
	}
	search, searchPage, _, err = queries.SearchMine(ctx, ownerID, keyword, 1, 10)
	if err != nil {
		t.Fatalf("search after trash: %v", err)
	}
	if searchPage.Total != 1 || containsDocument(search, publicDocument.Document.DocumentID) {
		t.Fatalf("trashed document remained searchable: %#v page=%#v", search, searchPage)
	}
}

func containsDocument(items []domain.DocumentSummary, documentID string) bool {
	for _, item := range items {
		if item.DocumentID == documentID {
			return true
		}
	}
	return false
}
