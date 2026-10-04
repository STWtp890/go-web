package domain

import "errors"

// Sentinel errors returned by the use cases. They are the only contract between
// the business layer and the transport layer: the transport maps them to gRPC
// status codes (see application/errors.go) and never inspects message text.
var (
	// ErrNotFound means the addressed document, space, subject, version or
	// binding does not exist.
	ErrNotFound = errors.New("document-service: not found")
	// ErrForbidden means the resource exists but the calling subject may not
	// touch it. It is never used to hide a resource the caller may not read: the
	// contract requires PERMISSION_DENIED rather than a fake NOT_FOUND.
	ErrForbidden = errors.New("document-service: forbidden")
	// ErrPrecondition means the command conflicts with the current state:
	// a stale optimistic revision, a lifecycle that does not allow the
	// transition, or a version that cannot take the requested publication
	// status.
	ErrPrecondition = errors.New("document-service: failed precondition")
	// ErrInvalidInput means the request is malformed: empty title, malformed
	// identifier, unknown content format or an unusable conversation context.
	ErrInvalidInput = errors.New("document-service: invalid argument")
	// ErrAlreadyExists means the mutation would create a second active row where
	// the model allows one (for example two active bindings for one group).
	ErrAlreadyExists = errors.New("document-service: already exists")
	// ErrUnavailable means the datastore could not answer. It is retryable and
	// never means "no".
	ErrUnavailable = errors.New("document-service: unavailable")
	// ErrInvariant means a server side invariant was violated while assembling a
	// result (for example overlapping resource scope labels). It is a defect,
	// not a caller error, and must never be silently repaired.
	ErrInvariant = errors.New("document-service: invariant violated")
)
