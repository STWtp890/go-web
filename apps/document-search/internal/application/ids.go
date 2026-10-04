package application

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

// The service only depends on the standard library for identifiers. Document
// identifiers are PostgreSQL uuid columns, so every identifier that reaches SQL
// is validated and canonicalized here first: an unvalidated string would turn a
// caller error into a database error, or worse, into a silently different key.

// newIdentifier returns a fresh random version 4 UUID in canonical form.
func newIdentifier() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("document-search: generate identifier: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return formatIdentifier(raw), nil
}

// normalizeIdentifier validates a UUID and returns its canonical lower-case
// form. Both the dashed and the compact spellings are accepted; anything else is
// rejected instead of being passed to PostgreSQL.
func normalizeIdentifier(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	compact := ""
	switch len(trimmed) {
	case 36:
		if trimmed[8] != '-' || trimmed[13] != '-' || trimmed[18] != '-' || trimmed[23] != '-' {
			return "", fmt.Errorf("%w: %q", ErrInvalidIdentifier, value)
		}
		compact = strings.ReplaceAll(trimmed, "-", "")
	case 32:
		compact = trimmed
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidIdentifier, value)
	}
	decoded, err := hex.DecodeString(compact)
	if err != nil || len(decoded) != 16 {
		return "", fmt.Errorf("%w: %q", ErrInvalidIdentifier, value)
	}
	var raw [16]byte
	copy(raw[:], decoded)
	return formatIdentifier(raw), nil
}

// formatIdentifier renders 16 raw bytes as a canonical UUID string.
func formatIdentifier(raw [16]byte) string {
	buffer := make([]byte, 36)
	hex.Encode(buffer[0:8], raw[0:4])
	buffer[8] = '-'
	hex.Encode(buffer[9:13], raw[4:6])
	buffer[13] = '-'
	hex.Encode(buffer[14:18], raw[6:8])
	buffer[18] = '-'
	hex.Encode(buffer[19:23], raw[8:10])
	buffer[23] = '-'
	hex.Encode(buffer[24:36], raw[10:16])
	return string(buffer)
}

// truncateRunes bounds a value to the width of its column. PostgreSQL counts
// characters, not bytes, so the cut is made on rune boundaries.
func truncateRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}
