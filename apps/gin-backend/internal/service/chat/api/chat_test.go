package api

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSetRouteGroupRegistersWebSocketWithoutChatSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	SetRouteGroup(router.Group("/api/v1/protected"))

	routes := make(map[string]struct{})
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	if _, ok := routes["GET /api/v1/protected/chat/ws"]; !ok {
		t.Fatal("WebSocket route is not registered")
	}
	if _, ok := routes["POST /api/v1/protected/chat/deliveries/ack"]; !ok {
		t.Fatal("batch delivery ACK route is not registered")
	}
	for _, removed := range []string{
		"GET /api/v1/protected/chat/sse",
		"POST /api/v1/protected/chat/messages",
		"POST /api/v1/protected/chat/sse/messages",
		"POST /api/v1/protected/chat/deliveries/:deliveryId/ack",
	} {
		if _, ok := routes[removed]; ok {
			t.Fatalf("removed chat route is still registered: %s", removed)
		}
	}
}
