package serviceauth

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Caller identifies a service that holds the boundary key and may present a
// service assertion. Callers are fixed strings, not free-form: a new caller is a
// contract change, not a configuration change.
type Caller string

const (
	// CallerGoWeb is the Web surface. It may only claim "web:*" subjects.
	CallerGoWeb Caller = "go-web"
	// CallerPyAgent is the QQ bot runtime. It holds QQ identity and session
	// facts and may only claim "qq:*" subjects.
	CallerPyAgent Caller = "py-agent"
	// CallerDocumentService is the document service itself; it mints resource
	// capabilities and may run administrative entry points.
	CallerDocumentService Caller = "document-service"
	// CallerSpacectl is the administrative CLI entry point of go-web. It shares
	// the go-web namespace but is distinguishable in audit records.
	CallerSpacectl Caller = "spacectl"
)

// SubjectNamespace returns the subject-key prefix a caller is allowed to claim.
// An empty prefix means the caller may not claim resource subjects at all.
func (caller Caller) SubjectNamespace() string {
	switch caller {
	case CallerGoWeb, CallerSpacectl:
		return SubjectOriginWeb
	case CallerPyAgent:
		return SubjectOriginQQ
	case CallerDocumentService:
		// The document service acts on behalf of the subjects it resolves, but
		// never asserts a new subject identity on an inbound request.
		return ""
	default:
		return ""
	}
}

// Valid reports whether the caller is a registered identity.
func (caller Caller) Valid() bool {
	switch caller {
	case CallerGoWeb, CallerPyAgent, CallerDocumentService, CallerSpacectl:
		return true
	default:
		return false
	}
}

// Audience identifies the receiving service. One audience per service keeps a
// credential minted for one boundary from being replayed at another.
type Audience string

const (
	// AudienceDocumentService is the document service command boundary.
	AudienceDocumentService Audience = "document-service"
	// AudienceDocumentSearch is the formal document search boundary.
	AudienceDocumentSearch Audience = "document-search"
	// AudienceQQSearch is the raw QQ content search boundary.
	AudienceQQSearch Audience = "qq-search"
)

// Scope names a capability family. Scopes decide which RPCs a caller may invoke;
// they never widen the resource range the caller may request.
type Scope string

const (
	// ScopeDocumentWrite allows formal document create/update/publish/withdraw/
	// archive/trash.
	ScopeDocumentWrite Scope = "document.write"
	// ScopeDocumentRead allows document detail and listing reads.
	ScopeDocumentRead Scope = "document.read"
	// ScopeSpaceAdmin allows space and member administration, including group to
	// space bindings.
	ScopeSpaceAdmin Scope = "space.admin"
	// ScopeAccessResolve allows resource scope resolution. It does not grant any
	// resource by itself; it only allows asking.
	ScopeAccessResolve Scope = "access.resolve"

	// ScopeDocumentIndexWriter indexes formal documents in document-search.
	ScopeDocumentIndexWriter Scope = "document-index-writer"
	// ScopeDocumentSearcher queries the formal document index.
	ScopeDocumentSearcher Scope = "document-searcher"

	// ScopeDocumentEventReader streams the document service change Outbox. Only
	// the formal document index consumer holds it.
	ScopeDocumentEventReader Scope = "document-event-reader"

	// ScopeQQIndexWriter indexes raw QQ records in qq-search.
	ScopeQQIndexWriter Scope = "qq-index-writer"
	// ScopeQQSearcher queries the raw QQ indexes.
	ScopeQQSearcher Scope = "qq-searcher"
)

var allScopes = []Scope{
	ScopeDocumentWrite, ScopeDocumentRead, ScopeSpaceAdmin, ScopeAccessResolve,
	ScopeDocumentIndexWriter, ScopeDocumentSearcher, ScopeDocumentEventReader,
	ScopeQQIndexWriter, ScopeQQSearcher,
}

// KnownScope reports whether the scope is registered.
func KnownScope(scope Scope) bool {
	for _, candidate := range allScopes {
		if candidate == scope {
			return true
		}
	}
	return false
}

// Subject origins. A subject key is "<origin>:<type>:<local id>", for example
// "web:user:42" or "qq:10001:user:20002".
const (
	SubjectOriginWeb = "web"
	SubjectOriginQQ  = "qq"
)

// Subject key separators. The document service stores subject keys verbatim
// because it must be able to answer "who is this principal" without calling back
// into go-web or py-agent.
const subjectSeparator = ":"

// CapabilityIssuer returns the one caller the contract allows to mint a resource
// capability for an audience.
//
// The fact source is the only issuer (ADR-017 decision 4): a formal document
// range is signed by document-service, a QQ channel range by py-agent. Accepting
// "any registered caller" as the issuer would let one fact source mint a range
// over the other one's corpus, which is exactly the trust the split removes: a
// search service validates the credential offline precisely because it cannot
// check the range itself.
//
// An audience with no registered issuer is not "unrestricted"; callers must fail
// closed on it.
func CapabilityIssuer(audience Audience) (Caller, bool) {
	switch audience {
	case AudienceDocumentSearch:
		return CallerDocumentService, true
	case AudienceQQSearch:
		return CallerPyAgent, true
	default:
		return "", false
	}
}

// Errors returned by this package. They are stable identifiers: callers map them
// to gRPC status codes and must not match on message text.
var (
	// ErrMalformedCredential means the credential is absent, not a Bearer token,
	// not two dot-separated parts, or not valid base64url.
	ErrMalformedCredential = errors.New("serviceauth: malformed credential")
	// ErrSignatureMismatch means the HMAC did not match the boundary key.
	ErrSignatureMismatch = errors.New("serviceauth: signature mismatch")
	// ErrUnknownClaim means the payload carried an unsupported version or an
	// unregistered caller/audience/scope.
	ErrUnknownClaim = errors.New("serviceauth: unknown claim")
	// ErrExpired means the credential is outside its validity window.
	ErrExpired = errors.New("serviceauth: credential expired")
	// ErrAudienceMismatch means the credential was minted for another service.
	ErrAudienceMismatch = errors.New("serviceauth: audience mismatch")
	// ErrScopeNotGranted means the caller does not hold a required scope.
	ErrScopeNotGranted = errors.New("serviceauth: scope not granted")
	// ErrNamespaceViolation means a caller tried to claim a subject outside its
	// own namespace.
	ErrNamespaceViolation = errors.New("serviceauth: subject namespace violation")
	// ErrSubjectNotAllowed means the caller may not claim resource subjects.
	ErrSubjectNotAllowed = errors.New("serviceauth: caller may not claim a subject")
	// ErrConfiguration means the component was constructed without a usable key
	// or identity. It is a startup failure, never an authentication result.
	ErrConfiguration = errors.New("serviceauth: invalid configuration")
)

// defaultLeeway is the clock skew tolerance applied to both ends of the validity
// window. It matches the existing mixin-search caller boundary.
const defaultLeeway = 30

// CanonicalScope normalizes a scope set: trimmed, de-duplicated, sorted. Both
// the minting and the validating side canonicalize, so ordering or duplicates
// never change an authorization decision.
func CanonicalScope(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, duplicate := seen[trimmed]; duplicate {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	if len(result) == 0 {
		return nil
	}
	sort.Strings(result)
	return result
}

// CanonicalIDs normalizes an identifier set the same way CanonicalScope does.
// Search request ranges are compared as sets, never as ordered slices.
func CanonicalIDs(values []string) []string {
	return CanonicalScope(values)
}

// ContainsAll reports whether granted is a superset of requested. This is the
// inclusion rule: a caller may only narrow, never widen. An empty requested set
// is the empty set, which is a subset of everything.
func ContainsAll(granted, requested []string) bool {
	if len(requested) == 0 {
		return true
	}
	allowed := make(map[string]struct{}, len(granted))
	for _, value := range granted {
		allowed[value] = struct{}{}
	}
	for _, value := range requested {
		if _, ok := allowed[value]; !ok {
			return false
		}
	}
	return true
}

// SubjectKey builds a canonical subject key. It returns an error rather than
// producing a partially formed key, because a malformed subject key silently
// becoming a new principal is exactly the failure this contract prevents.
func SubjectKey(origin, subjectType, localID string) (string, error) {
	origin = strings.TrimSpace(origin)
	subjectType = strings.TrimSpace(subjectType)
	localID = strings.TrimSpace(localID)
	if origin == "" || subjectType == "" || localID == "" {
		return "", fmt.Errorf("%w: subject key parts must be non-empty", ErrUnknownClaim)
	}
	for _, part := range []string{origin, subjectType, localID} {
		if strings.Contains(part, subjectSeparator) {
			return "", fmt.Errorf("%w: subject key part %q contains %q", ErrUnknownClaim, part, subjectSeparator)
		}
	}
	return origin + subjectSeparator + subjectType + subjectSeparator + localID, nil
}

// QQSubjectKey builds the subject key for a QQ identity that is scoped to one
// Bot. The same numeric QQ id under two Bots is two different subjects.
func QQSubjectKey(botID, externalUserID string) (string, error) {
	botID = strings.TrimSpace(botID)
	externalUserID = strings.TrimSpace(externalUserID)
	if botID == "" || externalUserID == "" {
		return "", fmt.Errorf("%w: QQ subject requires bot id and external user id", ErrUnknownClaim)
	}
	// A Bot id is part of the identity, so it is folded into the local id rather
	// than dropped: "qq:10001:user:20002".
	return SubjectKey(SubjectOriginQQ, "user", botID+"/"+externalUserID)
}

// WebSubjectKey builds the subject key for a Web account. The document service
// never invents this value: go-web asserts it and document-service records it.
func WebSubjectKey(localUserID string) (string, error) {
	return SubjectKey(SubjectOriginWeb, "user", localUserID)
}

// SubjectOriginOf returns the origin segment of a canonical subject key, or an
// empty string when the key is not well formed.
func SubjectOriginOf(subjectKey string) string {
	parts := strings.Split(strings.TrimSpace(subjectKey), subjectSeparator)
	if len(parts) < 3 {
		return ""
	}
	return parts[0]
}

// inNamespace reports whether subjectKey belongs to the caller's namespace.
func (caller Caller) inNamespace(subjectKey string) bool {
	namespace := caller.SubjectNamespace()
	if namespace == "" {
		return false
	}
	return SubjectOriginOf(subjectKey) == namespace
}
