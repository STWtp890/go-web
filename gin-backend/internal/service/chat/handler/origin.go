package handler

import (
	"net/http"
	"slices"

	"gin-backend/internal/config"
)

// browserOriginAllowed permits non-browser clients with no Origin header and
// requires browser connection handshakes to come from an explicit CORS origin.
func browserOriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || slices.Contains(config.CustomConfig().CORS.AllowOrigins, origin)
}
