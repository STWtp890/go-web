package serviceauth

import (
	"fmt"
	"os"
	"strings"
)

// LoadBoundaryKeyFile reads the shared boundary key from disk.
//
// The key lives in a file rather than in configuration so it never appears in
// committed YAML, in a process command line or in a configuration dump. The
// loader is part of the shared boundary package because every service and every
// caller needs the same rule - trimmed, at least minBoundaryKeyBytes long, and a
// missing or short key is a configuration error rather than a degraded mode.
func LoadBoundaryKeyFile(path string) ([]byte, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: boundary key path is empty", ErrConfiguration)
	}
	contents, err := os.ReadFile(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%w: read boundary key %s: %v", ErrConfiguration, trimmed, err)
	}
	key := []byte(strings.TrimSpace(string(contents)))
	if len(key) < minBoundaryKeyBytes {
		return nil, fmt.Errorf(
			"%w: boundary key %s holds %d bytes, need at least %d",
			ErrConfiguration, trimmed, len(key), minBoundaryKeyBytes,
		)
	}
	return key, nil
}
