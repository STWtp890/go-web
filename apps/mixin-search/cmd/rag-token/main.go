package main

import (
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"mixin-search/internal/security"
)

// rag-token mints a caller capability for local RPC checks.
//
// It is a developer tool rather than a product capability: it needs the shared
// boundary key, which is exactly what real consumers such as py-agent must never
// hold. Online callers receive their capability from go-web instead.
//
// Keep this binary out of the product image; see apps/mixin-search/Dockerfile.
func main() {
	capabilityKeyPath := flag.String(
		"capability-key-file",
		os.Getenv("MIXIN_SEARCH_CAPABILITY_KEY_FILE"),
		"file holding the shared boundary key",
	)
	capabilityKey := flag.String("capability-key", "", "boundary key inline; prefer -capability-key-file")
	issuer := flag.String("issuer", "go-web", "issuer identity recorded in the token")
	subject := flag.String("subject", "dev-tool", "caller identity the token is minted for")
	audience := flag.String("audience", "", "audience the token is minted for (default: derived from the role)")
	role := flag.String("role", security.RoleSearcher, "caller role: index-writer, searcher, ops or a chat-* role")
	userID := flag.String("user", "", "end user the caller acts for (optional)")
	spaces := flag.String("space", "", "comma-separated granted space IDs (searcher only)")
	documents := flag.String("document", "", "comma-separated granted document IDs (searcher only)")
	ttl := flag.Duration("ttl", 10*time.Minute, "token lifetime")
	flag.Parse()

	// The role decides the audience, so a hand-minted chat token cannot carry the
	// document audience (or the reverse) and produce a credential that the
	// service rejects only after a confusing permission error.
	resolvedAudience := strings.TrimSpace(*audience)
	if resolvedAudience == "" {
		derived, ok := security.RoleAudience(*role)
		if !ok {
			log.Fatalf("role %q has no audience binding; pass -audience explicitly only for a custom setup", *role)
		}
		resolvedAudience = derived
	}

	key, err := loadBoundaryKey(*capabilityKeyPath, *capabilityKey)
	if err != nil {
		log.Fatalf("load capability boundary key: %v", err)
	}
	mintingIssuer, err := security.NewIssuer(key, *issuer, resolvedAudience, *ttl)
	if err != nil {
		log.Fatalf("build capability issuer: %v", err)
	}
	token, err := mintingIssuer.Issue(*subject, *role, *userID, splitList(*spaces), splitList(*documents))
	if err != nil {
		log.Fatalf("mint capability: %v", err)
	}
	slog.Info("minted caller capability",
		slog.String("subject", *subject),
		slog.String("role", *role),
		slog.String("audience", resolvedAudience),
		slog.Duration("ttl", *ttl),
	)
	fmt.Println(token)
}

func loadBoundaryKey(path, inline string) ([]byte, error) {
	path = strings.TrimSpace(path)
	inline = strings.TrimSpace(inline)
	switch {
	case path != "":
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		return []byte(strings.TrimSpace(string(contents))), nil
	case inline != "":
		return []byte(inline), nil
	default:
		return nil, fmt.Errorf("set -capability-key-file or -capability-key")
	}
}

func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	trimmed := make([]string, 0, len(parts))
	for _, part := range parts {
		if entry := strings.TrimSpace(part); entry != "" {
			trimmed = append(trimmed, entry)
		}
	}
	return trimmed
}
