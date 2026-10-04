package domain_test

import (
	"strings"
	"testing"

	"document-service/internal/domain"
)

// The SaveDocument idempotency ledger treats the request id as a durable key, so
// the fingerprint is what keeps a reused id from silently absorbing a different
// payload. These tests pin the parts of the payload that must change the digest.

func TestSaveRequestFingerprintIsStableAndHex(t *testing.T) {
	const documentID = "11111111-1111-4111-8111-111111111111"
	public := true

	first := domain.SaveRequestFingerprint(documentID, "title", "body", "markdown", &public)
	second := domain.SaveRequestFingerprint(documentID, "title", "body", "markdown", &public)

	if first != second {
		t.Fatalf("the same payload produced %q and %q", first, second)
	}
	if len(first) != 64 || strings.Trim(first, "0123456789abcdef") != "" {
		t.Fatalf("fingerprint %q is not a lowercase sha256 hex digest", first)
	}
}

func TestSaveRequestFingerprintCoversEveryBusinessInput(t *testing.T) {
	const documentID = "11111111-1111-4111-8111-111111111111"
	absent := domain.SaveRequestFingerprint(documentID, "title", "body", "markdown", nil)
	public, private := true, false

	variants := map[string]string{
		"baseline":               absent,
		"other document":         domain.SaveRequestFingerprint("22222222-2222-4222-8222-222222222222", "title", "body", "markdown", nil),
		"other title":            domain.SaveRequestFingerprint(documentID, "other", "body", "markdown", nil),
		"other content":          domain.SaveRequestFingerprint(documentID, "title", "other", "markdown", nil),
		"other content format":   domain.SaveRequestFingerprint(documentID, "title", "body", "docx", nil),
		"explicit public":        domain.SaveRequestFingerprint(documentID, "title", "body", "markdown", &public),
		"explicit private":       domain.SaveRequestFingerprint(documentID, "title", "body", "markdown", &private),
		"field boundary shifted": domain.SaveRequestFingerprint(documentID, "t", "itlebody", "markdown", nil),
	}

	seen := make(map[string]string, len(variants))
	for name, digest := range variants {
		if other, duplicated := seen[digest]; duplicated {
			t.Fatalf("%s and %s produced the same fingerprint %q", name, other, digest)
		}
		seen[digest] = name
	}
}

// TestSaveRequestFingerprintDistinguishesAccessPresence pins the presence rule:
// "keep the policy" is a different business input from an explicit value, even
// when the explicit value does not move the policy.
func TestSaveRequestFingerprintDistinguishesAccessPresence(t *testing.T) {
	const documentID = "11111111-1111-4111-8111-111111111111"
	private := false

	absent := domain.SaveRequestFingerprint(documentID, "title", "body", "markdown", nil)
	explicit := domain.SaveRequestFingerprint(documentID, "title", "body", "markdown", &private)
	if absent == explicit {
		t.Fatal("an absent access field and an explicit false must not share a fingerprint")
	}
}
