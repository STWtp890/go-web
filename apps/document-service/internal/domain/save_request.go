package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
)

// This file owns the payload fingerprint of the SaveDocument idempotency ledger.
//
// The ledger recognizes a repeated request id whenever it arrives. That makes the
// request id a durable key, and a durable key must not silently absorb a
// different payload: "same id, different body" is a caller defect (a reused id,
// two tenants sharing a generator, a buggy retry that rebuilt the request), and
// answering it with the first attempt's result would hide a real bug and lose
// the second payload. The fingerprint is what turns that into a refusal.
//
// The digest deliberately covers every business input of the command except
// expected_aggregate_revision: the expectation is a concurrency guard for one
// attempt, not part of what the caller asked to store, so a retry is allowed to
// carry the (now stale) expectation the first attempt moved away from.

// SaveRequestFingerprint digests the business inputs of one SaveDocument command.
//
// The title, content and content format are the normalized values the service
// would store (the use case validates them before calling this function), so two
// requests that differ only in leading or trailing whitespace, or in the case of
// the content format, are the same payload. authenticated_public is captured by
// presence as well as by value: "absent" (keep the policy) is a different
// business input from an explicit false (set the policy to private).
func SaveRequestFingerprint(documentID, title, content, contentFormat string, authenticatedPublic *bool) string {
	hasher := sha256.New()
	writeFingerprintField(hasher, documentID)
	writeFingerprintField(hasher, title)
	writeFingerprintField(hasher, content)
	writeFingerprintField(hasher, contentFormat)
	switch {
	case authenticatedPublic == nil:
		writeFingerprintField(hasher, "authenticated_public:absent")
	case *authenticatedPublic:
		writeFingerprintField(hasher, "authenticated_public:true")
	default:
		writeFingerprintField(hasher, "authenticated_public:false")
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// writeFingerprintField writes one field with a length prefix. Without the prefix
// two different splits of the same bytes would hash identically ("ab"+"c" and
// "a"+"bc"), which is exactly the collision a fingerprint must not have.
func writeFingerprintField(hasher hash.Hash, value string) {
	_, _ = fmt.Fprintf(hasher, "%d:", len(value))
	_, _ = hasher.Write([]byte(value))
}
