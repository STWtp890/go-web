package serviceauth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadBoundaryKeyFile pins the rule every caller shares: the file is read,
// trimmed, and rejected when it is missing or too short. A key that is accepted
// here is one NewCodec accepts too.
func TestLoadBoundaryKeyFile(t *testing.T) {
	directory := t.TempDir()

	valid := filepath.Join(directory, "valid.key")
	if err := os.WriteFile(valid, []byte(strings.Repeat("k", 32)+"\n"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	key, err := LoadBoundaryKeyFile(valid)
	if err != nil {
		t.Fatalf("LoadBoundaryKeyFile: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("trailing whitespace must be trimmed, got %d bytes", len(key))
	}
	if _, err := NewCodec(key); err != nil {
		t.Fatalf("a key this loader accepts must build a codec: %v", err)
	}

	short := filepath.Join(directory, "short.key")
	if err := os.WriteFile(short, []byte("too-short"), 0o600); err != nil {
		t.Fatalf("write short key: %v", err)
	}
	if _, err := LoadBoundaryKeyFile(short); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("a short key must be a configuration error, got %v", err)
	}

	if _, err := LoadBoundaryKeyFile(""); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("an empty path must be a configuration error, got %v", err)
	}
	if _, err := LoadBoundaryKeyFile(filepath.Join(directory, "missing.key")); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("a missing file must be a configuration error, got %v", err)
	}
}
