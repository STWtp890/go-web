package httpadapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gin-backend/internal/modules/document/application"
	"gin-backend/internal/modules/document/domain"
	"gin-backend/internal/platform/httpserver/identity"

	"github.com/gin-gonic/gin"
)

type commandStub struct {
	create application.CreateCommand
}

func (stub *commandStub) Create(_ context.Context, command application.CreateCommand) (*application.MutationResult, error) {
	stub.create = command
	now := time.Unix(1_700_000_000, 0)
	return &application.MutationResult{
		Document: &domain.Document{DocumentID: "document-id", OwnerID: command.OwnerID, CreatedAt: now, UpdatedAt: now},
		Version:  &domain.DocumentVersion{Title: command.Title, Summary: "summary", Content: command.Content},
		Policy:   &domain.AccessPolicy{AuthenticatedPublic: command.AuthenticatedPublic},
	}, nil
}

func (*commandStub) Update(context.Context, application.UpdateCommand) (*application.MutationResult, error) {
	return nil, nil
}

func (*commandStub) Trash(context.Context, application.TrashCommand) error { return nil }

type queryStub struct{}

func (*queryStub) Get(context.Context, int64, string) (*domain.DocumentView, error) {
	return nil, nil
}

func (*queryStub) ListMine(context.Context, int64, int, int) ([]domain.DocumentSummary, application.Page, error) {
	return nil, application.Page{}, nil
}

func (*queryStub) ListPublic(context.Context, int, int) ([]domain.DocumentSummary, application.Page, error) {
	return nil, application.Page{}, nil
}

func (*queryStub) SearchMine(context.Context, int64, string, int, int) ([]domain.DocumentSummary, application.Page, string, error) {
	return nil, application.Page{}, "", nil
}

func TestRegisterRoutesRegistersDocumentSurface(t *testing.T) {
	handler, err := New(&commandStub{}, &queryStub{})
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v1/public"), router.Group("/api/v1/protected"))

	routes := make(map[string]struct{})
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	for _, expected := range []string{
		"POST /api/v1/protected/documents",
		"GET /api/v1/protected/documents/mine",
		"GET /api/v1/protected/documents/public",
		"GET /api/v1/protected/documents/search",
		"GET /api/v1/protected/documents/:documentId",
		"PUT /api/v1/protected/documents/:documentId",
		"DELETE /api/v1/protected/documents/:documentId",
	} {
		if _, ok := routes[expected]; !ok {
			t.Fatalf("route is not registered: %s", expected)
		}
	}
}

func TestCreateParsesJWTSubjectAsUsersID(t *testing.T) {
	commands := &commandStub{}
	handler, _ := New(commands, &queryStub{})
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/upload", func(c *gin.Context) {
		identity.Set(c, identity.Principal{Kind: identity.KindUser, Subject: "42", UserID: 42, SessionID: "session-1"})
		handler.create(c)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(`{"title":"标题","content":"正文","visibility":"public"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated || commands.create.OwnerID != 42 || !commands.create.AuthenticatedPublic {
		t.Fatalf("unexpected create result: status=%d command=%#v body=%s", recorder.Code, commands.create, recorder.Body.String())
	}
	var body struct {
		Data struct {
			DocumentID string `json:"documentId"`
		} `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil || body.Data.DocumentID != "document-id" {
		t.Fatalf("unexpected response: body=%#v error=%v", body, err)
	}
}

func TestCreateRejectsNonNumericJWTSubject(t *testing.T) {
	handler, _ := New(&commandStub{}, &queryStub{})
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/upload", func(c *gin.Context) {
		// 主体无法解析为数值时, 中间件注入的 Principal.UserID 为 0
		identity.Set(c, identity.Principal{Kind: identity.KindUser, Subject: "not-a-user-id", SessionID: "session-1"})
		handler.create(c)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(`{"title":"标题","content":"正文"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusUnauthorized, recorder.Body.String())
	}
}
