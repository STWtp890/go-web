package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"gin-backend/internal/service/markdown/logic"

	"github.com/gin-gonic/gin"
	jwtlib "github.com/golang-jwt/jwt/v5"
)

func TestSearchHandlerRejectsInvalidKeyword(t *testing.T) {
	tests := []struct {
		name    string
		keyword string
	}{
		{name: "blank", keyword: "   "},
		{name: "too long", keyword: strings.Repeat("文", logic.MaxSearchKeywordLength+1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := performSearchRequest(t, tt.keyword, true)
			assertSearchFailure(t, recorder, http.StatusBadRequest, "VALIDATION_FAILED")
		})
	}
}

func TestSearchHandlerRequiresUserIdentity(t *testing.T) {
	recorder := performSearchRequest(t, "Go", false)
	assertSearchFailure(t, recorder, http.StatusUnauthorized, "UNAUTHORIZED")
}

func performSearchRequest(t *testing.T, keyword string, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/search", func(c *gin.Context) {
		if authenticated {
			c.Set("claims", jwtlib.MapClaims{"sub": "1"})
		}
		SearchHandler(c)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/search?keyword="+url.QueryEscape(keyword), nil)
	router.ServeHTTP(recorder, request)
	return recorder
}

func assertSearchFailure(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("unexpected status: got %d want %d", recorder.Code, wantStatus)
	}
	var body struct {
		Success bool `json:"success"`
		Error   struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Success || body.Error.Code != wantCode {
		t.Fatalf("unexpected response: %#v", body)
	}
}
