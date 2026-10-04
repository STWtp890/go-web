package domain

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits that mirror the database column definitions. Validation happens in the
// business layer first so a caller gets INVALID_ARGUMENT instead of a raw
// constraint violation, and the database stays the last line of defence.
const (
	MaxTitleRunes       = 255
	MaxSummaryRunes     = 512
	MaxSpaceNameRunes   = 128
	MaxSubjectKeyBytes  = 192
	MaxExternalIDLength = 64
	MaxRequestIDLength  = 128
	MaxReasonLength     = 512
	MaxActorLength      = 128
	MaxDisplayNameRunes = 128
	MaxDocumentIDList   = 100
	summaryRunes        = 100
)

// ValidateTitle normalizes and validates a document title.
func ValidateTitle(raw string) (string, error) {
	title := strings.TrimSpace(raw)
	if title == "" {
		return "", fmt.Errorf("%w: title is required", ErrInvalidInput)
	}
	if utf8.RuneCountInString(title) > MaxTitleRunes {
		return "", fmt.Errorf("%w: title exceeds %d characters", ErrInvalidInput, MaxTitleRunes)
	}
	return title, nil
}

// ValidateContent normalizes and validates document content. Content is the
// indexable payload, so an empty body is rejected rather than stored.
func ValidateContent(raw string) (string, error) {
	content := strings.TrimSpace(raw)
	if content == "" {
		return "", fmt.Errorf("%w: content is required", ErrInvalidInput)
	}
	return content, nil
}

// NormalizeContentFormat maps an empty format to markdown and rejects formats the
// version table does not accept.
func NormalizeContentFormat(raw string) (string, error) {
	format := strings.ToLower(strings.TrimSpace(raw))
	if format == "" {
		return ContentFormatMarkdown, nil
	}
	switch format {
	case ContentFormatMarkdown, ContentFormatDoc, ContentFormatDocx:
		return format, nil
	default:
		return "", fmt.Errorf("%w: unknown content format %q", ErrInvalidInput, raw)
	}
}

// BuildSummary derives the stored summary from the content.
func BuildSummary(content string) string {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) > summaryRunes {
		runes = runes[:summaryRunes]
	}
	return string(runes)
}

// ValidateSpaceName normalizes and validates a knowledge space name.
func ValidateSpaceName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("%w: space name is required", ErrInvalidInput)
	}
	if utf8.RuneCountInString(name) > MaxSpaceNameRunes {
		return "", fmt.Errorf("%w: space name exceeds %d characters", ErrInvalidInput, MaxSpaceNameRunes)
	}
	return name, nil
}

// ValidateUUID normalizes an identifier that the database stores as uuid.
func ValidateUUID(raw, field string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("%w: %s is required", ErrInvalidInput, field)
	}
	if !IsUUID(value) {
		return "", fmt.Errorf("%w: %s must be a UUID", ErrInvalidInput, field)
	}
	return strings.ToLower(value), nil
}

// IsUUID reports whether a string is a canonical UUID. It is a local check so the
// domain package stays free of third party dependencies.
func IsUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, char := range value {
		switch index {
		case 8, 13, 18, 23:
			if char != '-' {
				return false
			}
		default:
			if !isHexDigit(char) {
				return false
			}
		}
	}
	return true
}

func isHexDigit(char rune) bool {
	switch {
	case char >= '0' && char <= '9':
		return true
	case char >= 'a' && char <= 'f':
		return true
	case char >= 'A' && char <= 'F':
		return true
	default:
		return false
	}
}

// ValidateRequestID normalizes an optional caller supplied request id. It is a
// deduplication hint, never an authorization input.
func ValidateRequestID(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if len(value) > MaxRequestIDLength {
		return "", fmt.Errorf("%w: request id exceeds %d characters", ErrInvalidInput, MaxRequestIDLength)
	}
	return value, nil
}

// ValidateReason requires an audit reason: every space, membership and binding
// mutation must be explainable afterwards.
func ValidateReason(raw string) (string, error) {
	reason := strings.TrimSpace(raw)
	if reason == "" {
		return "", fmt.Errorf("%w: reason is required", ErrInvalidInput)
	}
	if len(reason) > MaxReasonLength {
		return "", fmt.Errorf("%w: reason exceeds %d characters", ErrInvalidInput, MaxReasonLength)
	}
	return reason, nil
}

// ValidateActor requires the actor recorded in an audit row.
func ValidateActor(raw string) (string, error) {
	actor := strings.TrimSpace(raw)
	if actor == "" {
		return "", fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}
	if len(actor) > MaxActorLength {
		return "", fmt.Errorf("%w: actor exceeds %d characters", ErrInvalidInput, MaxActorLength)
	}
	return actor, nil
}

// ValidateDisplayName normalizes the optional display name stored with a
// registered subject.
func ValidateDisplayName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if utf8.RuneCountInString(name) > MaxDisplayNameRunes {
		return "", fmt.Errorf("%w: display name exceeds %d characters", ErrInvalidInput, MaxDisplayNameRunes)
	}
	return name, nil
}

// ValidExternalID reports whether an identifier fits a QQ bot, user or group
// column. The check is deliberately about shape, not about digits: the database
// defines the column as a bounded non-empty string and the service must not
// invent a stricter contract than the schema it owns.
func ValidExternalID(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || len(trimmed) > MaxExternalIDLength {
		return false
	}
	for _, char := range trimmed {
		if unicode.IsSpace(char) || unicode.IsControl(char) {
			return false
		}
	}
	return true
}

// NormalizeRole maps an empty role to "member" and rejects unknown roles.
func NormalizeRole(raw string) (string, error) {
	role := strings.ToLower(strings.TrimSpace(raw))
	switch role {
	case "":
		return RoleMember, nil
	case RoleOwner, RoleAdmin, RoleMember:
		return role, nil
	default:
		return "", fmt.Errorf("%w: unknown member role %q", ErrInvalidInput, raw)
	}
}

// NormalizeSource is the audit "source" column: spacectl writes are attributed to
// the operations CLI, every other trusted caller is recorded as go-web-api.
//
// ADR-016 v3 also lists document-service-api as a value; the column accepts it,
// but no inbound assertion can claim to be an internal write, so mapping every
// non-spacectl caller to go-web-api keeps the value derivable from the verified
// caller alone.
func NormalizeSource(caller string) string {
	if strings.TrimSpace(caller) == "spacectl" {
		return "spacectl"
	}
	return "go-web-api"
}
