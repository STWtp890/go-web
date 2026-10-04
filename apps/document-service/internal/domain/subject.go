package domain

import (
	"fmt"
	"strings"
)

// SubjectIdentity is a parsed resource access subject key. The document service
// owns the registry these keys live in; the two identity families are separate
// and are never required to be bound to each other (ADR-017 v2 section 3).
type SubjectIdentity struct {
	// Key is the canonical subject key, for example "web:user:42" or
	// "qq:user:10001/20002".
	Key string
	// Origin is the namespace segment: web or qq.
	Origin string
	// Type is the subject type segment: user or group.
	Type string
	// LocalID is everything after the type segment.
	LocalID string
	// BotID is the Bot namespace of a QQ identity and is empty for a Web
	// identity. The same numeric QQ id under two Bots is two subjects.
	BotID string
}

// ParseSubjectKey parses and validates a canonical subject key. A malformed key
// must never quietly become a new principal, so it is rejected instead of being
// normalized into something plausible.
func ParseSubjectKey(raw string) (SubjectIdentity, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return SubjectIdentity{}, fmt.Errorf("%w: subject key is required", ErrInvalidInput)
	}
	if len(key) > MaxSubjectKeyBytes {
		return SubjectIdentity{}, fmt.Errorf("%w: subject key exceeds %d bytes", ErrInvalidInput, MaxSubjectKeyBytes)
	}
	// SplitN with three parts keeps any ':' inside the local id, so a Web user id
	// that happens to contain a colon is not silently truncated.
	parts := strings.SplitN(key, ":", 3)
	if len(parts) != 3 {
		return SubjectIdentity{}, fmt.Errorf("%w: subject key %q must be <origin>:<type>:<local id>", ErrInvalidInput, raw)
	}
	origin, subjectType, localID := parts[0], parts[1], strings.TrimSpace(parts[2])

	switch origin {
	case OriginWeb, OriginQQ:
	default:
		return SubjectIdentity{}, fmt.Errorf("%w: subject key %q has unknown origin %q", ErrInvalidInput, raw, origin)
	}
	switch subjectType {
	case SubjectTypeUser, SubjectTypeGroup:
	default:
		return SubjectIdentity{}, fmt.Errorf("%w: subject key %q has unknown type %q", ErrInvalidInput, raw, subjectType)
	}
	if localID == "" {
		return SubjectIdentity{}, fmt.Errorf("%w: subject key %q has an empty local id", ErrInvalidInput, raw)
	}

	identity := SubjectIdentity{Key: key, Origin: origin, Type: subjectType, LocalID: localID}
	if origin == OriginQQ && subjectType == SubjectTypeUser {
		botID, externalID, found := strings.Cut(localID, "/")
		if !found || strings.TrimSpace(botID) == "" || strings.TrimSpace(externalID) == "" {
			return SubjectIdentity{}, fmt.Errorf(
				"%w: QQ subject key %q must be qq:user:<bot id>/<external user id>", ErrInvalidInput, raw)
		}
		identity.BotID = strings.TrimSpace(botID)
	}
	return identity, nil
}

// SubjectKeyOrigin returns the namespace of a subject key, or an empty string
// when the key is malformed.
func SubjectKeyOrigin(raw string) string {
	identity, err := ParseSubjectKey(raw)
	if err != nil {
		return ""
	}
	return identity.Origin
}

// SubjectTypeOf maps the parsed subject type onto the registry column.
func SubjectTypeOf(identity SubjectIdentity) string {
	switch identity.Type {
	case SubjectTypeGroup:
		return SubjectTypeGroup
	default:
		return SubjectTypeUser
	}
}

// IsQQSubjectKey reports whether the key belongs to the QQ namespace.
func IsQQSubjectKey(raw string) bool {
	return SubjectKeyOrigin(raw) == OriginQQ
}
