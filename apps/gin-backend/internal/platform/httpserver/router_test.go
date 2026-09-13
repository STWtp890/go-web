package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gin-backend/internal/config"
	"gin-backend/internal/config/custom"
	corsconfig "gin-backend/internal/config/custom/cors"
	"gin-backend/internal/config/must"

	"github.com/gin-gonic/gin"
)

func TestChatRoutesAreNotRegistered(t *testing.T) {
	engine := New(testConfig(), Dependencies{})

	for _, route := range engine.Routes() {
		if strings.HasPrefix(route.Path, "/api/v1/protected/chat") {
			t.Fatalf("chat route must remain disconnected, found %s %s", route.Method, route.Path)
		}
	}
}

func TestHealthCheckDoesNotRequireDependencies(t *testing.T) {
	engine := New(testConfig(), Dependencies{})
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

func TestReadinessReflectsInjectedCheck(t *testing.T) {
	tests := []struct {
		name  string
		ready func(context.Context) error
		want  int
	}{
		{name: "missing check", want: http.StatusServiceUnavailable},
		{name: "ready", ready: func(context.Context) error { return nil }, want: http.StatusOK},
		{name: "dependency failure", ready: func(context.Context) error { return errors.New("dependency unavailable") }, want: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := New(testConfig(), Dependencies{Ready: test.ready})
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if recorder.Code != test.want {
				t.Fatalf("GET /readyz status = %d, want %d", recorder.Code, test.want)
			}
		})
	}
}

func testConfig() *config.Config {
	return &config.Config{
		ServerConfig: must.ServerConfig{Mode: gin.TestMode},
		CustomConfig: custom.CustomConfig{
			CORS: corsconfig.CORSConfig{
				AllowOrigins:  []string{"http://localhost"},
				AllowMethods:  []string{"GET"},
				AllowHeaders:  []string{"Content-Type"},
				ExposeHeaders: []string{"X-Request-ID"},
				MaxAge:        time.Minute,
			},
		},
	}
}
