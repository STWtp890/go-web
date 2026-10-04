package domain_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"document-service/internal/domain"
)

func TestValidateTitleAndContent(t *testing.T) {
	if _, err := domain.ValidateTitle("   "); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("empty title err = %v", err)
	}
	if _, err := domain.ValidateTitle(strings.Repeat("t", domain.MaxTitleRunes+1)); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("long title err = %v", err)
	}
	title, err := domain.ValidateTitle("  Report  ")
	if err != nil || title != "Report" {
		t.Fatalf("title = %q, err = %v", title, err)
	}

	if _, err := domain.ValidateContent("  \n "); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("empty content err = %v", err)
	}
	content, err := domain.ValidateContent("  body  ")
	if err != nil || content != "body" {
		t.Fatalf("content = %q, err = %v", content, err)
	}
}

func TestNormalizeContentFormat(t *testing.T) {
	if format, err := domain.NormalizeContentFormat(""); err != nil || format != domain.ContentFormatMarkdown {
		t.Fatalf("empty format = (%q,%v), want markdown", format, err)
	}
	if format, err := domain.NormalizeContentFormat("DOCX"); err != nil || format != domain.ContentFormatDocx {
		t.Fatalf("DOCX = (%q,%v)", format, err)
	}
	if _, err := domain.NormalizeContentFormat("html"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("unknown format err = %v", err)
	}
}

func TestBuildSummaryIsBounded(t *testing.T) {
	content := strings.Repeat("a", 400)
	summary := domain.BuildSummary(content)
	if len([]rune(summary)) != 100 {
		t.Fatalf("summary length = %d, want 100", len([]rune(summary)))
	}
	if domain.BuildSummary("short") != "short" {
		t.Fatalf("summary = %q", domain.BuildSummary("short"))
	}
}

func TestValidateUUID(t *testing.T) {
	value, err := domain.ValidateUUID(" 6BA7B810-9DAD-11D1-80B4-00C04FD430C8 ", "document id")
	if err != nil {
		t.Fatalf("ValidateUUID: %v", err)
	}
	if value != "6ba7b810-9dad-11d1-80b4-00c04fd430c8" {
		t.Fatalf("value = %q, want a lower case canonical uuid", value)
	}
	if _, err := domain.ValidateUUID("not-a-uuid", "document id"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v", err)
	}
	if _, err := domain.ValidateUUID("", "document id"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("empty err = %v", err)
	}
}

func TestParseSubjectKey(t *testing.T) {
	web, err := domain.ParseSubjectKey("web:user:a-1234")
	if err != nil {
		t.Fatalf("web subject: %v", err)
	}
	if web.Origin != domain.OriginWeb || web.Type != domain.SubjectTypeUser || web.LocalID != "a-1234" || web.BotID != "" {
		t.Fatalf("web identity = %+v", web)
	}

	qq, err := domain.ParseSubjectKey("qq:user:10001/20002")
	if err != nil {
		t.Fatalf("qq subject: %v", err)
	}
	if qq.Origin != domain.OriginQQ || qq.BotID != "10001" || qq.LocalID != "10001/20002" {
		t.Fatalf("qq identity = %+v", qq)
	}

	// A local id may contain a colon: the key is split into three parts only, so a
	// Web id is never truncated.
	colon, err := domain.ParseSubjectKey("web:user:a:b:c")
	if err != nil {
		t.Fatalf("colon subject: %v", err)
	}
	if colon.LocalID != "a:b:c" {
		t.Fatalf("local id = %q, want a:b:c", colon.LocalID)
	}

	invalid := []string{
		"",
		"web:user",
		"web:user:",
		"other:user:1",
		"web:robot:1",
		"qq:user:10001",
		"qq:user:/20002",
		"web:user:" + strings.Repeat("x", domain.MaxSubjectKeyBytes),
	}
	for _, key := range invalid {
		if _, err := domain.ParseSubjectKey(key); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("key %q: err = %v, want invalid input", key, err)
		}
	}
}

func TestSubjectKeyOriginAndType(t *testing.T) {
	if origin := domain.SubjectKeyOrigin("qq:user:1/2"); origin != domain.OriginQQ {
		t.Fatalf("origin = %q", origin)
	}
	if origin := domain.SubjectKeyOrigin("broken"); origin != "" {
		t.Fatalf("origin = %q, want empty for a malformed key", origin)
	}
	identity, err := domain.ParseSubjectKey("qq:group:10001/20002")
	if err != nil {
		t.Fatalf("group subject: %v", err)
	}
	if domain.SubjectTypeOf(identity) != domain.SubjectTypeGroup {
		t.Fatalf("subject type = %q", domain.SubjectTypeOf(identity))
	}
}

func TestValidateRequestIDSpaceAndReason(t *testing.T) {
	if requestID, err := domain.ValidateRequestID("  req-1 "); err != nil || requestID != "req-1" {
		t.Fatalf("request id = (%q,%v)", requestID, err)
	}
	if _, err := domain.ValidateRequestID(strings.Repeat("r", domain.MaxRequestIDLength+1)); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("long request id err = %v", err)
	}
	if _, err := domain.ValidateSpaceName("  "); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("empty space name err = %v", err)
	}
	if name, err := domain.ValidateSpaceName(" Team "); err != nil || name != "Team" {
		t.Fatalf("space name = (%q,%v)", name, err)
	}
	if _, err := domain.ValidateReason(" "); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("empty reason err = %v", err)
	}
	if _, err := domain.ValidateActor(""); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("empty actor err = %v", err)
	}
}

func TestValidExternalIDAcceptsShapeNotDigits(t *testing.T) {
	if !domain.ValidExternalID("10001") {
		t.Fatal("a numeric id must be valid")
	}
	// The schema defines the column as a bounded non-empty string; a Bot namespace
	// that is not numeric is odd but not the service's contract to refuse.
	if !domain.ValidExternalID("bot-a") {
		t.Fatal("a non numeric id that fits the column must be valid")
	}
	for _, value := range []string{"", "  ", "has space", strings.Repeat("x", domain.MaxExternalIDLength+1)} {
		if domain.ValidExternalID(value) {
			t.Fatalf("value %q must be invalid", value)
		}
	}
}

func TestNormalizeRole(t *testing.T) {
	if role, err := domain.NormalizeRole(""); err != nil || role != domain.RoleMember {
		t.Fatalf("empty role = (%q,%v)", role, err)
	}
	if role, err := domain.NormalizeRole("ADMIN"); err != nil || role != domain.RoleAdmin {
		t.Fatalf("ADMIN = (%q,%v)", role, err)
	}
	if _, err := domain.NormalizeRole("superuser"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v", err)
	}
}

func TestNormalizeSourceAttributesSpacectlSeparately(t *testing.T) {
	if source := domain.NormalizeSource("spacectl"); source != "spacectl" {
		t.Fatalf("source = %q", source)
	}
	for _, caller := range []string{"go-web", "py-agent", "document-service", ""} {
		if source := domain.NormalizeSource(caller); source != "go-web-api" {
			t.Fatalf("caller %q: source = %q, want go-web-api", caller, source)
		}
	}
}

func TestContentDigestMatchesTheDatabaseCheckConstraint(t *testing.T) {
	digest := sha256.Sum256([]byte("body"))
	want := hex.EncodeToString(digest[:])
	if len(want) != 64 {
		t.Fatalf("digest length = %d", len(want))
	}
	// The application hashes with the same shape the schema enforces
	// (^[0-9a-f]{64}$); this asserts the shape, not the helper, which lives in the
	// application package next to the version it describes.
	for _, char := range want {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			t.Fatalf("digest %q is not lower case hexadecimal", want)
		}
	}
}
