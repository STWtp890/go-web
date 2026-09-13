package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"gin-backend/internal/common/service/sessioncookie"

	"github.com/gin-gonic/gin"
	jwtlib "github.com/golang-jwt/jwt/v5"
)

func TestAuthRequiredAbortsHandlerWhenAccessCookieMissing(t *testing.T) {
	executed, recorder := runProtectedRequest(t, http.MethodGet, AuthRequired(nil))
	assertAbortedFailure(t, recorder, executed, http.StatusUnauthorized, "UNAUTHORIZED")
}

func TestManagerAuthRequiredAbortsHandlerWhenAccessCookieMissing(t *testing.T) {
	executed, recorder := runProtectedRequest(t, http.MethodGet, ManagerAuthRequired(nil))
	assertAbortedFailure(t, recorder, executed, http.StatusUnauthorized, "UNAUTHORIZED")
}

func TestCSRFProtectionAbortsHandlerWhenTokenMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	executed := false
	router.POST("/protected",
		func(c *gin.Context) {
			c.Set("claims", jwtlib.MapClaims{"sid": "session-1"})
			c.Next()
		},
		CSRFProtection(sessioncookie.UserCSRFCookie),
		func(c *gin.Context) {
			executed = true
			c.JSON(http.StatusOK, gin.H{"unexpected": true})
		},
	)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/protected", nil)
	router.ServeHTTP(recorder, req)
	assertAbortedFailure(t, recorder, executed, http.StatusForbidden, "FORBIDDEN")
}

func runProtectedRequest(t *testing.T, method string, middleware gin.HandlerFunc) (bool, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	executed := false
	router.Handle(method, "/protected", middleware, func(c *gin.Context) {
		executed = true
		c.JSON(http.StatusOK, gin.H{"unexpected": true})
	})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/protected", nil)
	router.ServeHTTP(recorder, req)
	return executed, recorder
}

func assertAbortedFailure(t *testing.T, recorder *httptest.ResponseRecorder, executed bool, status int, code string) {
	t.Helper()
	if executed {
		t.Fatal("protected handler executed after middleware failure")
	}
	if recorder.Code != status {
		t.Fatalf("unexpected status: got %d want %d", recorder.Code, status)
	}
	decoder := json.NewDecoder(recorder.Body)
	var body struct {
		Success bool `json:"success"`
		Error   struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := decoder.Decode(&body); err != nil {
		t.Fatalf("decode failure response: %v", err)
	}
	if body.Success || body.Error.Code != code {
		t.Fatalf("unexpected failure response: %#v", body)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("response contains trailing payload: err=%v payload=%#v", err, trailing)
	}
}
