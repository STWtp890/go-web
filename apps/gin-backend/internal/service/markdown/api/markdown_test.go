package api

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSetRouteGroupRegistersMarkdownRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	SetRouteGroup(router.Group("/api/v1/public"), router.Group("/api/v1/protected"))

	routes := make(map[string]struct{})
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	for _, expected := range []string{
		"GET /api/v1/protected/markdown/search",
		"PUT /api/v1/protected/markdown/:markdownId",
		"DELETE /api/v1/protected/markdown/:markdownId",
	} {
		if _, ok := routes[expected]; !ok {
			t.Fatalf("Markdown route is not registered: %s", expected)
		}
	}
}
