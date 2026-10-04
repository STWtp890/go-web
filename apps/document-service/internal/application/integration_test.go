package application_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"testing"

	"document-service/internal/application"
	"document-service/internal/domain"
	"document-service/internal/infrastructure/postgres"
	"document-service/internal/testsupport"

	documentv1 "packages/gen/document/v1"
	"packages/serviceauth"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// These tests run against the real development PostgreSQL. They never skip: if the
// database cannot be reached the harness fails the test, because a skipped
// integration test cannot tell "the transaction is correct" from "nothing ran".

func newEnvironment(t *testing.T, options ...application.Option) (*testsupport.IDs, *postgres.Pool, *application.Service) {
	t.Helper()
	ids := testsupport.NewIDs()
	cfg := testsupport.TestConfig(t, testsupport.BoundaryKey())
	pool := testsupport.OpenPool(t, cfg)
	service := testsupport.NewService(t, cfg, pool, options...)
	t.Cleanup(func() { ids.Cleanup(t, pool) })
	return ids, pool, service
}

func privateConversation() *documentv1.ConversationContext {
	return &documentv1.ConversationContext{Kind: documentv1.ConversationKind_CONVERSATION_KIND_PRIVATE}
}

func groupConversation(groupID string) *documentv1.ConversationContext {
	return &documentv1.ConversationContext{
		Kind:            documentv1.ConversationKind_CONVERSATION_KIND_GROUP,
		ExternalGroupId: groupID,
	}
}

func expectCode(t *testing.T, err error, want codes.Code, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected %s, got no error", what, want)
	}
	if got := status.Code(err); got != want {
		t.Fatalf("%s: expected %s, got %s (%v)", what, want, got, err)
	}
}

func mustCreateDocument(t *testing.T, session testsupport.Session, title, content string, public bool, requestID string) *documentv1.DocumentDetail {
	t.Helper()
	response, err := session.Service.CreateDocument(session.Ctx, session.Principal, &documentv1.CreateDocumentRequest{
		Title:               title,
		Content:             content,
		ContentFormat:       "markdown",
		AuthenticatedPublic: public,
		RequestId:           requestID,
	})
	if err != nil {
		t.Fatalf("CreateDocument(%q): %v", title, err)
	}
	detail := response.GetDocument()
	if detail == nil || detail.GetSummary() == nil || detail.GetVersion() == nil {
		t.Fatalf("CreateDocument(%q) returned an incomplete detail: %+v", title, detail)
	}
	return detail
}

// latestVersionID reads the newest version id of a document, so a test can
// address the draft it just appended.
func latestVersionID(t *testing.T, ctx context.Context, pool *postgres.Pool, documentID string) string {
	t.Helper()
	var versionID string
	if err := pool.Pgx().QueryRow(ctx,
		`SELECT version_id::text FROM document_service.document_versions
		  WHERE document_id = $1::text::uuid ORDER BY revision DESC LIMIT 1`,
		documentID,
	).Scan(&versionID); err != nil {
		t.Fatalf("read latest version of %s: %v", documentID, err)
	}
	return versionID
}

func mustCreateTeamSpace(t *testing.T, session testsupport.Session, name string) string {
	t.Helper()
	response, err := session.Service.CreateTeamSpace(session.Ctx, session.Principal, &documentv1.CreateTeamSpaceRequest{
		Name: name,
	})
	if err != nil {
		t.Fatalf("CreateTeamSpace(%q): %v", name, err)
	}
	spaceID := response.GetSpace().GetSpaceId()
	if spaceID == "" {
		t.Fatal("CreateTeamSpace returned no space id")
	}
	return spaceID
}

func mustGrantMember(t *testing.T, session testsupport.Session, spaceID, subjectKey string, role documentv1.MemberRole) {
	t.Helper()
	_, err := session.Service.GrantSpaceMember(session.Ctx, session.Principal, &documentv1.GrantSpaceMemberRequest{
		SpaceId:    spaceID,
		SubjectKey: subjectKey,
		MemberRole: role,
	})
	if err != nil {
		t.Fatalf("GrantSpaceMember(%s,%s): %v", spaceID, subjectKey, err)
	}
}

func mustBindGroup(t *testing.T, session testsupport.Session, botID, groupID, spaceID string) *documentv1.GroupSpaceBinding {
	t.Helper()
	response, err := session.Service.BindGroupSpace(session.Ctx, session.Principal, &documentv1.BindGroupSpaceRequest{
		BotId:           botID,
		ExternalGroupId: groupID,
		SpaceId:         spaceID,
		Reason:          "integration test binding",
	})
	if err != nil {
		t.Fatalf("BindGroupSpace(%s,%s,%s): %v", botID, groupID, spaceID, err)
	}
	return response.GetBinding()
}

func mustResolve(t *testing.T, session testsupport.Session, conversation *documentv1.ConversationContext) *documentv1.ResolveAccessScopeResponse {
	t.Helper()
	response, err := session.Service.ResolveAccessScope(session.Ctx, session.Principal, &documentv1.ResolveAccessScopeRequest{
		Conversation: conversation,
	})
	if err != nil {
		t.Fatalf("ResolveAccessScope: %v", err)
	}
	return response
}

func assertDenied(t *testing.T, response *documentv1.ResolveAccessScopeResponse, reason documentv1.DeniedReason) {
	t.Helper()
	if response.GetDecision() != documentv1.Decision_DECISION_DENIED {
		t.Fatalf("decision = %s, want DENIED", response.GetDecision())
	}
	if response.GetDeniedReason() != reason {
		t.Fatalf("denied reason = %s, want %s", response.GetDeniedReason(), reason)
	}
	if len(response.GetPrivateSpaceIds()) != 0 || response.GetCurrentTeamSpaceId() != "" ||
		len(response.GetOtherTeamSpaceIds()) != 0 || len(response.GetMemberSpaceIds()) != 0 {
		t.Fatalf("a denial must not carry a range: %+v", response)
	}
}

// TestIntegrationCreateDocumentCommitsEveryRow asserts that one command commits the
// document, its first version, the access policy, the lazily created private space,
// the owner membership, the audit rows and the Outbox event - and that the event
// payload really decodes into the documented envelope.
func TestIntegrationCreateDocumentCommitsEveryRow(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	subject := ids.Subject("owner")
	session := testsupport.As(service, ids.WebWriter(subject))
	content := "the body of the quarterly report"

	detail := mustCreateDocument(t, session, "Quarterly report", content, false, ids.RequestID("create"))
	documentID := detail.GetSummary().GetDocumentId()
	versionID := detail.GetVersion().GetVersionId()

	if detail.GetVersion().GetRevision() != 1 {
		t.Fatalf("first version revision = %d, want 1", detail.GetVersion().GetRevision())
	}
	if detail.GetVersion().GetPublicationStatus() != documentv1.PublicationStatus_PUBLICATION_STATUS_PUBLISHED {
		t.Fatalf("first version status = %s, want PUBLISHED", detail.GetVersion().GetPublicationStatus())
	}
	digest := sha256.Sum256([]byte(content))
	if detail.GetVersion().GetContentSha256() != hex.EncodeToString(digest[:]) {
		t.Fatalf("content hash = %q", detail.GetVersion().GetContentSha256())
	}
	if detail.GetSummary().GetActiveVersionId() != versionID {
		t.Fatalf("active version = %q, want %q", detail.GetSummary().GetActiveVersionId(), versionID)
	}

	// The document head.
	var (
		lifecycle                     string
		activation, access, aggregate int64
		createRequest                 string
		activeVersion                 string
		ownerSpace                    string
	)
	err := pool.Pgx().QueryRow(context.Background(), `
		SELECT lifecycle_status, activation_revision, access_revision, aggregate_revision,
		       COALESCE(create_request_id, ''), COALESCE(active_version_id::text, ''), owner_space_id::text
		FROM document_service.documents WHERE document_id = $1::text::uuid`, documentID).
		Scan(&lifecycle, &activation, &access, &aggregate, &createRequest, &activeVersion, &ownerSpace)
	if err != nil {
		t.Fatalf("read the created document: %v", err)
	}
	if lifecycle != domain.LifecycleActive {
		t.Fatalf("lifecycle = %q, want active", lifecycle)
	}
	if activation != 1 || access != 1 || aggregate != 1 {
		t.Fatalf("revisions = (%d,%d,%d), want (1,1,1)", activation, access, aggregate)
	}
	if createRequest != ids.RequestID("create") {
		t.Fatalf("create_request_id = %q", createRequest)
	}
	if activeVersion != versionID {
		t.Fatalf("stored active version = %q, want %q", activeVersion, versionID)
	}

	// The private space and its owner membership are created in the same
	// transaction; the deferred trigger refuses a space without an active owner.
	if testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.knowledge_spaces
		 WHERE space_id = $1::text::uuid AND space_type = 'private' AND owner_subject_key = $2`,
		ownerSpace, subject) != 1 {
		t.Fatalf("private space %s for %s was not created", ownerSpace, subject)
	}
	if testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.space_members
		 WHERE space_id = $1::text::uuid AND subject_key = $2 AND member_role = 'owner' AND revoked_at IS NULL`,
		ownerSpace, subject) != 1 {
		t.Fatal("the owner membership of the lazily created private space is missing")
	}

	// The access policy.
	if testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_access_policies
		 WHERE document_id = $1::text::uuid AND authenticated_public = false AND access_revision = 1`,
		documentID) != 1 {
		t.Fatal("the access policy row is missing or wrong")
	}

	// Audit: the subject registration and the private space creation.
	if testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.space_audit_events
		 WHERE subject_type = 'access_subject' AND action = 'register' AND subject_key = $1`, subject) != 1 {
		t.Fatal("the subject registration audit row is missing")
	}
	if testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.space_audit_events
		 WHERE subject_type = 'space' AND action = 'create' AND space_id = $1::text::uuid`, ownerSpace) != 1 {
		t.Fatal("the private space creation audit row is missing")
	}

	// The Outbox row and its payload.
	var sequence int64
	var eventID, eventKind string
	var payload []byte
	err = pool.Pgx().QueryRow(context.Background(), `
		SELECT sequence, event_id::text, event_kind, payload
		FROM document_service.document_events WHERE document_id = $1::text::uuid ORDER BY sequence`, documentID).
		Scan(&sequence, &eventID, &eventKind, &payload)
	if err != nil {
		t.Fatalf("read the Outbox event: %v", err)
	}
	if eventKind != domain.EventKindUpsert {
		t.Fatalf("event kind = %q, want upsert", eventKind)
	}
	envelope := &documentv1.DocumentEventEnvelope{}
	if err := proto.Unmarshal(payload, envelope); err != nil {
		t.Fatalf("decode the Outbox payload: %v", err)
	}
	if envelope.GetSequence() != sequence {
		t.Fatalf("payload sequence = %d, row sequence = %d", envelope.GetSequence(), sequence)
	}
	if envelope.GetEventId() != eventID {
		t.Fatalf("payload event id = %q, row event id = %q", envelope.GetEventId(), eventID)
	}
	if envelope.GetKind() != documentv1.DocumentEventKind_DOCUMENT_EVENT_KIND_UPSERT {
		t.Fatalf("envelope kind = %s", envelope.GetKind())
	}
	if envelope.GetDocumentId() != documentID || envelope.GetVersionId() != versionID {
		t.Fatalf("envelope ids = (%q,%q)", envelope.GetDocumentId(), envelope.GetVersionId())
	}
	if envelope.GetAggregateRevision() != 1 || envelope.GetActivationRevision() != 1 || envelope.GetAccessRevision() != 1 {
		t.Fatalf("envelope revisions = (%d,%d,%d)",
			envelope.GetAggregateRevision(), envelope.GetActivationRevision(), envelope.GetAccessRevision())
	}
	if envelope.GetLifecycleStatus() != documentv1.LifecycleStatus_LIFECYCLE_STATUS_ACTIVE {
		t.Fatalf("envelope lifecycle = %s", envelope.GetLifecycleStatus())
	}
	if envelope.GetPublicationStatus() != documentv1.PublicationStatus_PUBLICATION_STATUS_PUBLISHED {
		t.Fatalf("envelope publication = %s", envelope.GetPublicationStatus())
	}
	if envelope.GetTitle() != "Quarterly report" || envelope.GetContent() != content {
		t.Fatalf("envelope content = (%q,%q)", envelope.GetTitle(), envelope.GetContent())
	}
	if envelope.GetOwnerSubjectKey() != subject || envelope.GetOwnerSpaceId() != ownerSpace {
		t.Fatalf("envelope ownership = (%q,%q)", envelope.GetOwnerSubjectKey(), envelope.GetOwnerSpaceId())
	}
	if envelope.GetIndexProfile() != domain.DefaultIndexProfile {
		t.Fatalf("envelope index profile = %q", envelope.GetIndexProfile())
	}
	if envelope.GetOccurredAt() == "" {
		t.Fatal("envelope occurred_at is empty")
	}
	if envelope.GetCreatedAt() == "" {
		t.Fatal("envelope created_at is empty: a consumer must receive the document's creation instant")
	}
	if envelope.GetCreatedAt() != detail.GetSummary().GetCreatedAt() {
		t.Fatalf("envelope created_at = %q, document created_at = %q",
			envelope.GetCreatedAt(), detail.GetSummary().GetCreatedAt())
	}
	if envelope.GetAuthenticatedPublic() {
		t.Fatal("envelope authenticated_public = true, want false")
	}
}

// TestIntegrationCreateDocumentRollsBackCompletely forces a failure in the middle
// of the create transaction through an audit sink that always fails and asserts
// that nothing at all was committed.
func TestIntegrationCreateDocumentRollsBackCompletely(t *testing.T) {
	ids, pool, service := newEnvironment(t, application.WithAuditSink(failingAuditSink{}))
	subject := ids.Subject("rollback")
	session := testsupport.As(service, ids.WebWriter(subject))

	_, err := session.Service.CreateDocument(session.Ctx, session.Principal, &documentv1.CreateDocumentRequest{
		Title: "never committed", Content: "body",
	})
	if err == nil {
		t.Fatal("CreateDocument must fail when the audit insert fails")
	}

	if testsupport.Count(t, pool, `SELECT count(*) FROM document_service.documents WHERE owner_subject_key = $1`, subject) != 0 {
		t.Fatal("a rolled back create left a document behind")
	}
	if testsupport.Count(t, pool, `SELECT count(*) FROM document_service.knowledge_spaces WHERE owner_subject_key = $1`, subject) != 0 {
		t.Fatal("a rolled back create left a knowledge space behind")
	}
	if testsupport.Count(t, pool, `SELECT count(*) FROM document_service.space_members WHERE subject_key = $1`, subject) != 0 {
		t.Fatal("a rolled back create left a membership behind")
	}
	if testsupport.Count(t, pool, `SELECT count(*) FROM document_service.space_audit_events WHERE subject_key = $1`, subject) != 0 {
		t.Fatal("a rolled back create left an audit row behind")
	}
	if testsupport.Count(t, pool, `SELECT count(*) FROM document_service.access_subjects WHERE subject_key = $1`, subject) != 0 {
		t.Fatal("a rolled back create left a registered subject behind")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_events e
		 JOIN document_service.documents d ON d.document_id = e.document_id
		 WHERE d.owner_subject_key = $1`, subject); got != 0 {
		t.Fatalf("a rolled back create left %d Outbox rows behind", got)
	}
}

type failingAuditSink struct{}

func (failingAuditSink) Append(context.Context, pgx.Tx, domain.AuditEvent) error {
	return errors.New("audit sink is unavailable")
}

// selectiveAuditSink fails exactly the audit calls it is told to fail, counting
// calls in the order the service makes them. It lets a test fail the audit insert
// that happens after the business row was written, which is the case that has to
// roll back.
type selectiveAuditSink struct {
	mu        sync.Mutex
	calls     int
	failCalls map[int]struct{}
}

func (sink *selectiveAuditSink) Append(ctx context.Context, tx pgx.Tx, event domain.AuditEvent) error {
	sink.mu.Lock()
	sink.calls++
	call := sink.calls
	sink.mu.Unlock()
	if _, fail := sink.failCalls[call]; fail {
		return fmt.Errorf("audit sink refused call %d", call)
	}
	return postgres.DefaultAuditSink{}.Append(ctx, tx, event)
}

// TestIntegrationOwnershipIsEnforcedOnEveryMutation asserts the teardown rule: a
// second subject can neither mutate nor read another subject's private document,
// and can read it once it is marked authenticated public.
func TestIntegrationOwnershipIsEnforcedOnEveryMutation(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	owner := ids.Subject("owner")
	stranger := ids.Subject("stranger")
	ownerSession := testsupport.As(service, ids.WebWriter(owner))
	strangerSession := testsupport.As(service, ids.WebWriter(stranger))

	detail := mustCreateDocument(t, ownerSession, "private note", "secret body", false, ids.RequestID("private"))
	documentID := detail.GetSummary().GetDocumentId()
	versionID := detail.GetVersion().GetVersionId()

	// The stranger registers itself first, so a denial below can only be the
	// resource decision and not "unknown subject".
	mustCreateDocument(t, strangerSession, "stranger note", "their own body", false, ids.RequestID("stranger"))

	_, err := strangerSession.Service.UpdateDraft(strangerSession.Ctx, strangerSession.Principal, &documentv1.UpdateDraftRequest{
		DocumentId: documentID, Title: "hijacked", Content: "body",
	})
	expectCode(t, err, codes.PermissionDenied, "UpdateDraft on a foreign document")

	_, err = strangerSession.Service.PublishDocument(strangerSession.Ctx, strangerSession.Principal, &documentv1.PublishDocumentRequest{
		DocumentId: documentID, VersionId: versionID,
	})
	expectCode(t, err, codes.PermissionDenied, "PublishDocument on a foreign document")

	_, err = strangerSession.Service.TrashDocument(strangerSession.Ctx, strangerSession.Principal, &documentv1.TrashDocumentRequest{
		DocumentId: documentID,
	})
	expectCode(t, err, codes.PermissionDenied, "TrashDocument on a foreign document")

	_, err = strangerSession.Service.ArchiveDocument(strangerSession.Ctx, strangerSession.Principal, &documentv1.ArchiveDocumentRequest{
		DocumentId: documentID,
	})
	expectCode(t, err, codes.PermissionDenied, "ArchiveDocument on a foreign document")

	_, err = strangerSession.Service.GetDocument(strangerSession.Ctx, strangerSession.Principal, &documentv1.GetDocumentRequest{
		DocumentId: documentID,
	})
	expectCode(t, err, codes.PermissionDenied, "GetDocument on a foreign private document")

	// Nothing leaked and nothing changed.
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.documents
		 WHERE document_id = $1::text::uuid AND lifecycle_status = 'active' AND aggregate_revision = 1`, documentID); got != 1 {
		t.Fatal("a refused mutation changed the document")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_versions WHERE document_id = $1::text::uuid`, documentID); got != 1 {
		t.Fatal("a refused mutation appended a version")
	}

	// A content edit never changes the access policy. The flag is an optional bool
	// now, so an omitted field means "keep the policy as it is", and a caller that
	// explicitly asks this command for an access change is refused instead of
	// being silently ignored: access changes belong to SaveDocument. The policy
	// stays private after both calls, which is what the stranger's continued
	// denial proves.
	if _, err := ownerSession.Service.UpdateDraft(ownerSession.Ctx, ownerSession.Principal, &documentv1.UpdateDraftRequest{
		DocumentId: documentID, Title: "private note", Content: "secret body",
	}); err != nil {
		t.Fatalf("owner UpdateDraft: %v", err)
	}
	explicitPublic := true
	_, err = ownerSession.Service.UpdateDraft(ownerSession.Ctx, ownerSession.Principal, &documentv1.UpdateDraftRequest{
		DocumentId: documentID, Title: "private note", Content: "secret body", AuthenticatedPublic: &explicitPublic,
	})
	expectCode(t, err, codes.InvalidArgument, "UpdateDraft with an explicit authenticated_public")
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_access_policies
		  WHERE document_id = $1::text::uuid AND authenticated_public = false`, documentID); got != 1 {
		t.Fatal("a content edit changed the access policy; it must never do that")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_versions WHERE document_id = $1::text::uuid`, documentID); got != 2 {
		t.Fatalf("versions = %d, want 2: the refused access change must not append a version", got)
	}
	if _, err := strangerSession.Service.GetDocument(strangerSession.Ctx, strangerSession.Principal, &documentv1.GetDocumentRequest{
		DocumentId: documentID,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a private document must stay private after a content edit, got %v", err)
	}

	// The owner publishes the same content as an authenticated public document.
	// Creation, publication and SaveDocument are the operations that carry an
	// access decision, so this is the path that opens it to a registered subject.
	publicDetail := mustCreateDocument(t, ownerSession, "public note", "public body", true, ids.RequestID("public"))
	publicDocumentID := publicDetail.GetSummary().GetDocumentId()
	read, err := strangerSession.Service.GetDocument(strangerSession.Ctx, strangerSession.Principal, &documentv1.GetDocumentRequest{
		DocumentId: publicDocumentID,
	})
	if err != nil {
		t.Fatalf("a registered subject must be able to read an authenticated public document: %v", err)
	}
	if !read.GetDocument().GetAuthenticatedPublic() {
		t.Fatal("the detail must report the public flag")
	}
}

// TestIntegrationStaleExpectedRevisionChangesNothing pins the optimistic
// concurrency rule.
func TestIntegrationStaleExpectedRevisionChangesNothing(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	subject := ids.Subject("concurrency")
	session := testsupport.As(service, ids.WebWriter(subject))
	detail := mustCreateDocument(t, session, "versioned", "body", false, ids.RequestID("concurrency"))
	documentID := detail.GetSummary().GetDocumentId()

	_, err := session.Service.UpdateDraft(session.Ctx, session.Principal, &documentv1.UpdateDraftRequest{
		DocumentId:                documentID,
		Title:                     "stale write",
		Content:                   "body",
		ExpectedAggregateRevision: 99,
	})
	expectCode(t, err, codes.FailedPrecondition, "UpdateDraft with a stale expected revision")

	if testsupport.Count(t, pool, `SELECT count(*) FROM document_service.document_versions WHERE document_id = $1::text::uuid`, documentID) != 1 {
		t.Fatal("a stale write appended a version")
	}
	if testsupport.Count(t, pool, `SELECT count(*) FROM document_service.documents WHERE document_id = $1::text::uuid AND aggregate_revision = 1`, documentID) != 1 {
		t.Fatal("a stale write moved the aggregate revision")
	}
	if testsupport.Count(t, pool, `SELECT count(*) FROM document_service.document_events WHERE document_id = $1::text::uuid`, documentID) != 1 {
		t.Fatal("a stale write appended an Outbox event")
	}

	// The same write with the correct revision succeeds and moves exactly one step.
	if _, err := session.Service.UpdateDraft(session.Ctx, session.Principal, &documentv1.UpdateDraftRequest{
		DocumentId:                documentID,
		Title:                     "fresh write",
		Content:                   "body",
		ExpectedAggregateRevision: 1,
	}); err != nil {
		t.Fatalf("UpdateDraft with the current revision: %v", err)
	}
	if testsupport.Count(t, pool, `SELECT count(*) FROM document_service.documents WHERE document_id = $1::text::uuid AND aggregate_revision = 2`, documentID) != 1 {
		t.Fatal("a successful draft must move the aggregate revision to 2")
	}
	if testsupport.Count(t, pool, `SELECT count(*) FROM document_service.document_versions WHERE document_id = $1::text::uuid AND revision = 2 AND publication_status = 'draft'`, documentID) != 1 {
		t.Fatal("the appended version must be a draft at revision 2")
	}
}

// TestIntegrationGroupScopeResolvesCurrentTeam covers the group envelope: the
// current group's space is the only current team, other bindings never become the
// current team, and every denial reason is reachable.
func TestIntegrationGroupScopeResolvesCurrentTeam(t *testing.T) {
	ids, _, service := newEnvironment(t)
	admin := ids.SpaceAdmin(ids.Subject("admin"))
	adminSession := testsupport.As(service, admin)
	botID := ids.NumericID()

	spaceOne := mustCreateTeamSpace(t, adminSession, "team one")
	spaceTwo := mustCreateTeamSpace(t, adminSession, "team two")
	groupOne := ids.NumericID()
	groupTwo := ids.NumericID()
	groupThree := ids.NumericID()
	mustBindGroup(t, adminSession, botID, groupOne, spaceOne)
	mustBindGroup(t, adminSession, botID, groupTwo, spaceTwo)

	member := ids.QQSubject(botID, ids.NumericID())
	outside := ids.QQSubject(botID, ids.NumericID())
	mustGrantMember(t, adminSession, spaceOne, member, documentv1.MemberRole_MEMBER_ROLE_MEMBER)
	mustGrantMember(t, adminSession, spaceTwo, member, documentv1.MemberRole_MEMBER_ROLE_MEMBER)
	mustGrantMember(t, adminSession, spaceOne, outside, documentv1.MemberRole_MEMBER_ROLE_MEMBER)

	memberSession := testsupport.As(service, ids.QQAgent(member))
	response := mustResolve(t, memberSession, groupConversation(groupOne))
	if response.GetDecision() != documentv1.Decision_DECISION_GRANTED {
		t.Fatalf("current group resolution = %+v", response)
	}
	if response.GetCurrentTeamSpaceId() != spaceOne {
		t.Fatalf("current team = %q, want %q", response.GetCurrentTeamSpaceId(), spaceOne)
	}
	for _, id := range response.GetOtherTeamSpaceIds() {
		if id == spaceOne {
			t.Fatal("the current team must not also be an other team")
		}
	}
	if len(response.GetOtherTeamSpaceIds()) != 1 || response.GetOtherTeamSpaceIds()[0] != spaceTwo {
		t.Fatalf("other teams = %v, want [%s]", response.GetOtherTeamSpaceIds(), spaceTwo)
	}

	// The other group's binding is the current team only for that group.
	otherResponse := mustResolve(t, memberSession, groupConversation(groupTwo))
	if otherResponse.GetCurrentTeamSpaceId() != spaceTwo {
		t.Fatalf("current team for group two = %q, want %q", otherResponse.GetCurrentTeamSpaceId(), spaceTwo)
	}

	// An unbound group is denied.
	assertDenied(t, mustResolve(t, memberSession, groupConversation(groupThree)), documentv1.DeniedReason_DENIED_REASON_GROUP_UNBOUND)

	// A registered subject that is not a member of the bound space is denied.
	outsideSession := testsupport.As(service, ids.QQAgent(outside))
	assertDenied(t, mustResolve(t, outsideSession, groupConversation(groupTwo)), documentv1.DeniedReason_DENIED_REASON_NOT_SPACE_MEMBER)

	// Revoking the binding is visible on the next resolution.
	if _, err := adminSession.Service.RevokeGroupSpace(adminSession.Ctx, adminSession.Principal, &documentv1.RevokeGroupSpaceRequest{
		BotId: botID, ExternalGroupId: groupOne, Reason: "integration test revocation",
	}); err != nil {
		t.Fatalf("RevokeGroupSpace: %v", err)
	}
	assertDenied(t, mustResolve(t, memberSession, groupConversation(groupOne)), documentv1.DeniedReason_DENIED_REASON_GROUP_BINDING_REVOKED)
}

// TestIntegrationPrivateScopeLabels pins the private conversation envelope.
func TestIntegrationPrivateScopeLabels(t *testing.T) {
	ids, _, service := newEnvironment(t)
	subject := ids.Subject("private")
	session := testsupport.As(service, ids.WebWriter(subject))
	detail := mustCreateDocument(t, session, "own document", "body", false, ids.RequestID("private"))
	privateSpace := detail.GetSummary().GetOwnerSpaceId()
	teamSpace := mustCreateTeamSpace(t, session, "owned team")

	response := mustResolve(t, session, privateConversation())
	if response.GetDecision() != documentv1.Decision_DECISION_GRANTED {
		t.Fatalf("private resolution = %+v", response)
	}
	if len(response.GetPrivateSpaceIds()) != 1 || response.GetPrivateSpaceIds()[0] != privateSpace {
		t.Fatalf("private = %v, want [%s]", response.GetPrivateSpaceIds(), privateSpace)
	}
	if response.GetCurrentTeamSpaceId() != "" {
		t.Fatalf("a private conversation must not carry a current team, got %q", response.GetCurrentTeamSpaceId())
	}
	if len(response.GetOtherTeamSpaceIds()) != 1 || response.GetOtherTeamSpaceIds()[0] != teamSpace {
		t.Fatalf("other teams = %v, want [%s]", response.GetOtherTeamSpaceIds(), teamSpace)
	}
	for _, id := range response.GetMemberSpaceIds() {
		if id == response.GetCurrentTeamSpaceId() {
			t.Fatal("member_space_ids must be a union of disjoint labels")
		}
	}

	// A subject with no space at all is still granted, with an empty envelope: that
	// is "nothing to search", not a denial. Registering it without giving it a space
	// goes through the administrative path, where the caller is registered but the
	// grant is for somebody else.
	empty := ids.Subject("empty")
	emptySession := testsupport.As(service, ids.WebWriter(empty))
	mustGrantMember(t, emptySession, teamSpace, ids.Subject("empty-grantee"), documentv1.MemberRole_MEMBER_ROLE_MEMBER)
	emptyResponse := mustResolve(t, emptySession, privateConversation())
	if emptyResponse.GetDecision() != documentv1.Decision_DECISION_GRANTED {
		t.Fatalf("a known subject with no space must be granted: %+v", emptyResponse)
	}
	if len(emptyResponse.GetMemberSpaceIds()) != 0 {
		t.Fatalf("envelope = %v, want empty", emptyResponse.GetMemberSpaceIds())
	}
}

// TestIntegrationResourceSideInvariant is the ADR-016 invariant: registering a
// subject, binding a group and receiving a group fact never create space
// membership.
func TestIntegrationResourceSideInvariant(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	adminSession := testsupport.As(service, ids.SpaceAdmin(ids.Subject("admin")))
	botID := ids.NumericID()
	groupID := ids.NumericID()
	spaceID := mustCreateTeamSpace(t, adminSession, "invariant team")
	qqSubject := ids.QQSubject(botID, ids.NumericID())

	// The count is scoped to the subject under test: another agent's rows share this
	// database, so a table wide count would be neither meaningful nor stable.
	membershipsOf := func() int64 {
		return testsupport.Count(t, pool,
			`SELECT count(*) FROM document_service.space_members WHERE subject_key = $1`, qqSubject)
	}
	if membershipsOf() != 0 {
		t.Fatal("the QQ subject must not have any membership before it is granted one")
	}

	mustBindGroup(t, adminSession, botID, groupID, spaceID)
	if membershipsOf() != 0 {
		t.Fatal("binding a group to a space created a membership")
	}

	// The QQ event path: the subject appears in a group conversation. Resolution
	// reads facts and registers nothing.
	qqSession := testsupport.As(service, ids.QQAgent(qqSubject))
	assertDenied(t, mustResolve(t, qqSession, groupConversation(groupID)), documentv1.DeniedReason_DENIED_REASON_SUBJECT_UNKNOWN)
	if membershipsOf() != 0 {
		t.Fatal("resolution created a membership")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.access_subjects WHERE subject_key = $1`, qqSubject); got != 0 {
		t.Fatal("a scope read must not register a subject")
	}

	// The explicit administrative path is the only one that grants membership, and it
	// is exercised here for a different space: being in the group is still not a
	// membership of the bound space.
	otherSpace := mustCreateTeamSpace(t, adminSession, "other team")
	mustGrantMember(t, adminSession, otherSpace, qqSubject, documentv1.MemberRole_MEMBER_ROLE_MEMBER)
	if membershipsOf() != 1 {
		t.Fatalf("memberships = %d, want exactly the one explicit grant", membershipsOf())
	}
	assertDenied(t, mustResolve(t, qqSession, groupConversation(groupID)), documentv1.DeniedReason_DENIED_REASON_NOT_SPACE_MEMBER)
	if membershipsOf() != 1 {
		t.Fatal("resolution changed the membership table")
	}
}

// TestIntegrationDenialIsNotAnEmptyGrant asserts that a denial has no range and
// cannot be signed into a capability.
func TestIntegrationDenialIsNotAnEmptyGrant(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	// The caller is py-agent, so the subject has to live in the QQ namespace: an
	// application-level test must still present an identity a trusted entry point
	// could actually have proven.
	unknown := ids.QQSubject(ids.NumericID(), ids.NumericID())
	session := testsupport.As(service, ids.QQAgent(unknown))

	response := mustResolve(t, session, privateConversation())
	assertDenied(t, response, documentv1.DeniedReason_DENIED_REASON_SUBJECT_UNKNOWN)

	capability, err := session.Service.IssueSearchCapability(session.Ctx, session.Principal, &documentv1.IssueSearchCapabilityRequest{
		Conversation: privateConversation(),
	})
	if err != nil {
		t.Fatalf("IssueSearchCapability on a denial must answer, not fail: %v", err)
	}
	if capability.GetDecision() != documentv1.Decision_DECISION_DENIED {
		t.Fatalf("decision = %s, want DENIED", capability.GetDecision())
	}
	if capability.GetDeniedReason() != documentv1.DeniedReason_DENIED_REASON_SUBJECT_UNKNOWN {
		t.Fatalf("denied reason = %s", capability.GetDeniedReason())
	}
	if capability.GetCapability() != "" {
		t.Fatal("a denial must never be signed into a capability")
	}
	if len(capability.GetMemberSpaceIds()) != 0 || capability.GetCurrentTeamSpaceId() != "" {
		t.Fatalf("a denial must not carry a range: %+v", capability)
	}

	// A deactivated subject is denied as inactive, and the denial still carries no
	// range. Deactivation is a registry state the service owns; the test sets it
	// directly because no RPC exposes it yet.
	registered := ids.Subject("deactivated")
	registeredSession := testsupport.As(service, ids.WebWriter(registered))
	mustCreateDocument(t, registeredSession, "own document", "body", true, ids.RequestID("deactivated"))
	testsupport.Exec(t, pool, `UPDATE document_service.access_subjects SET active = false WHERE subject_key = $1`, registered)
	assertDenied(t, mustResolve(t, registeredSession, privateConversation()),
		documentv1.DeniedReason_DENIED_REASON_SUBJECT_INACTIVE)
}

// TestIntegrationRevocationTakesEffectOnTheNextResolution asserts the documented
// effect point of a revocation: the next resolution, with no index work.
func TestIntegrationRevocationTakesEffectOnTheNextResolution(t *testing.T) {
	ids, _, service := newEnvironment(t)
	adminSession := testsupport.As(service, ids.SpaceAdmin(ids.Subject("admin")))
	botID := ids.NumericID()
	groupID := ids.NumericID()
	spaceID := mustCreateTeamSpace(t, adminSession, "revocation team")
	mustBindGroup(t, adminSession, botID, groupID, spaceID)

	member := ids.QQSubject(botID, ids.NumericID())
	mustGrantMember(t, adminSession, spaceID, member, documentv1.MemberRole_MEMBER_ROLE_MEMBER)
	memberSession := testsupport.As(service, ids.QQAgent(member))

	before := mustResolve(t, memberSession, groupConversation(groupID))
	if before.GetCurrentTeamSpaceId() != spaceID {
		t.Fatalf("current team = %q, want %q", before.GetCurrentTeamSpaceId(), spaceID)
	}

	if _, err := adminSession.Service.RevokeSpaceMember(adminSession.Ctx, adminSession.Principal, &documentv1.RevokeSpaceMemberRequest{
		SpaceId: spaceID, SubjectKey: member, RequestId: ids.RequestID("revoke-member"),
	}); err != nil {
		t.Fatalf("RevokeSpaceMember: %v", err)
	}

	after := mustResolve(t, memberSession, groupConversation(groupID))
	assertDenied(t, after, documentv1.DeniedReason_DENIED_REASON_NOT_SPACE_MEMBER)
	for _, id := range after.GetMemberSpaceIds() {
		if id == spaceID {
			t.Fatal("the revoked space is still in the envelope")
		}
	}
}

// TestIntegrationInclusionRuleRejectsTheWholeRequest asserts that a request outside
// the resolved envelope is refused as a whole instead of being trimmed.
func TestIntegrationInclusionRuleRejectsTheWholeRequest(t *testing.T) {
	ids, _, service := newEnvironment(t)
	subject := ids.Subject("inclusion")
	session := testsupport.As(service, ids.WebWriter(subject))
	detail := mustCreateDocument(t, session, "own document", "body", false, ids.RequestID("inclusion"))
	ownedSpace := detail.GetSummary().GetOwnerSpaceId()

	inside := mustResolve(t, session, privateConversation())
	if len(inside.GetMemberSpaceIds()) == 0 {
		t.Fatal("the envelope must contain the private space")
	}

	granted, err := session.Service.IssueSearchCapability(session.Ctx, session.Principal, &documentv1.IssueSearchCapabilityRequest{
		Conversation:      privateConversation(),
		RequestedSpaceIds: []string{ownedSpace},
	})
	if err != nil {
		t.Fatalf("IssueSearchCapability inside the envelope: %v", err)
	}
	if granted.GetDecision() != documentv1.Decision_DECISION_GRANTED {
		t.Fatalf("decision = %s", granted.GetDecision())
	}
	if granted.GetCapability() == "" {
		t.Fatal("a granted capability must carry a signed token")
	}
	if len(granted.GetMemberSpaceIds()) != 1 || granted.GetMemberSpaceIds()[0] != ownedSpace {
		t.Fatalf("narrowed range = %v, want [%s]", granted.GetMemberSpaceIds(), ownedSpace)
	}

	// A token minted for another audience must not open here, and vice versa.
	codec := session.Service.CapabilityCodec()
	seal, err := codec.OpenCapability(granted.GetCapability(), serviceauth.AudienceDocumentSearch)
	if err != nil {
		t.Fatalf("re-open the minted capability for document-search: %v", err)
	}
	if seal.Claims.SubjectKey != subject {
		t.Fatalf("capability subject = %q, want %q", seal.Claims.SubjectKey, subject)
	}
	if _, err := codec.OpenCapability(granted.GetCapability(), serviceauth.AudienceQQSearch); !errors.Is(err, serviceauth.ErrAudienceMismatch) {
		t.Fatalf("cross audience open err = %v, want an audience mismatch", err)
	}

	outsideID := "99999999-9999-4999-8999-999999999999"
	if _, err := session.Service.IssueSearchCapability(session.Ctx, session.Principal, &documentv1.IssueSearchCapabilityRequest{
		Conversation:      privateConversation(),
		RequestedSpaceIds: []string{outsideID},
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("requesting a space outside the envelope: code = %s, want PermissionDenied", status.Code(err))
	}
	if _, err := session.Service.IssueSearchCapability(session.Ctx, session.Principal, &documentv1.IssueSearchCapabilityRequest{
		Conversation:      privateConversation(),
		RequestedSpaceIds: []string{ownedSpace, outsideID},
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("partially outside request: code = %s, want the whole request refused", status.Code(err))
	}
}

// TestIntegrationConcurrentBindProducesOneActiveBinding asserts the activity
// uniqueness of a group binding under concurrency.
func TestIntegrationConcurrentBindProducesOneActiveBinding(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	adminSession := testsupport.As(service, ids.SpaceAdmin(ids.Subject("admin")))
	botID := ids.NumericID()
	groupID := ids.NumericID()
	spaceID := mustCreateTeamSpace(t, adminSession, "concurrent team")

	const attempts = 4
	results := make([]error, attempts)
	var waitGroup sync.WaitGroup
	start := make(chan struct{})
	for index := 0; index < attempts; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			<-start
			_, err := service.BindGroupSpace(adminSession.Ctx, adminSession.Principal, &documentv1.BindGroupSpaceRequest{
				BotId:           botID,
				ExternalGroupId: groupID,
				SpaceId:         spaceID,
				Reason:          "concurrent binding",
			})
			results[index] = err
		}(index)
	}
	close(start)
	waitGroup.Wait()

	succeeded := 0
	for _, err := range results {
		switch {
		case err == nil:
			succeeded++
		case status.Code(err) == codes.AlreadyExists:
		default:
			t.Fatalf("unexpected concurrent bind error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful binds = %d, want exactly 1", succeeded)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.group_space_bindings
		 WHERE bot_id = $1 AND external_group_id = $2 AND revoked_at IS NULL`, botID, groupID); got != 1 {
		t.Fatalf("active bindings = %d, want 1", got)
	}
}

// TestIntegrationReadConsistencyDuringRebind asserts that a reader never observes a
// mixed snapshot while a binding moves from one space to another.
func TestIntegrationReadConsistencyDuringRebind(t *testing.T) {
	ids, _, service := newEnvironment(t)
	adminSession := testsupport.As(service, ids.SpaceAdmin(ids.Subject("admin")))
	botID := ids.NumericID()
	groupID := ids.NumericID()
	spaceOne := mustCreateTeamSpace(t, adminSession, "rebind one")
	spaceTwo := mustCreateTeamSpace(t, adminSession, "rebind two")
	mustBindGroup(t, adminSession, botID, groupID, spaceOne)

	member := ids.QQSubject(botID, ids.NumericID())
	mustGrantMember(t, adminSession, spaceOne, member, documentv1.MemberRole_MEMBER_ROLE_MEMBER)
	mustGrantMember(t, adminSession, spaceTwo, member, documentv1.MemberRole_MEMBER_ROLE_MEMBER)
	memberSession := testsupport.As(service, ids.QQAgent(member))

	const reads = 24
	problems := make(chan string, reads)
	var waitGroup sync.WaitGroup

	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		if _, err := adminSession.Service.BindGroupSpace(adminSession.Ctx, adminSession.Principal, &documentv1.BindGroupSpaceRequest{
			BotId: botID, ExternalGroupId: groupID, SpaceId: spaceTwo, Reason: "rebind during reads",
		}); err != nil {
			problems <- fmt.Sprintf("rebind failed: %v", err)
		}
	}()

	for index := 0; index < reads; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			response, err := service.ResolveAccessScope(memberSession.Ctx, memberSession.Principal, &documentv1.ResolveAccessScopeRequest{
				Conversation: groupConversation(groupID),
			})
			if err != nil {
				problems <- fmt.Sprintf("resolve failed: %v", err)
				return
			}
			if response.GetDecision() != documentv1.Decision_DECISION_GRANTED {
				problems <- fmt.Sprintf("resolve denied during rebind: %+v", response)
				return
			}
			current := response.GetCurrentTeamSpaceId()
			if current != spaceOne && current != spaceTwo {
				problems <- fmt.Sprintf("current team %q is neither the old nor the new binding", current)
				return
			}
			for _, id := range response.GetOtherTeamSpaceIds() {
				if id == current {
					problems <- "the current team also appears as an other team"
					return
				}
			}
			// Both spaces are active memberships, so the union must carry both no
			// matter which binding the read saw.
			if len(response.GetMemberSpaceIds()) != 2 {
				problems <- fmt.Sprintf("member_space_ids = %v, want both spaces", response.GetMemberSpaceIds())
			}
		}()
	}
	waitGroup.Wait()
	close(problems)
	for problem := range problems {
		t.Error(problem)
	}

	final := mustResolve(t, memberSession, groupConversation(groupID))
	if final.GetCurrentTeamSpaceId() != spaceTwo {
		t.Fatalf("after the rebind the current team = %q, want %q", final.GetCurrentTeamSpaceId(), spaceTwo)
	}
	if len(final.GetOtherTeamSpaceIds()) != 1 || final.GetOtherTeamSpaceIds()[0] != spaceOne {
		t.Fatalf("other teams = %v, want [%s]", final.GetOtherTeamSpaceIds(), spaceOne)
	}
}

// TestIntegrationAuditFailureRollsBackTheMutation asserts that a failed audit
// insert leaves neither the binding nor the membership nor an audit row.
func TestIntegrationAuditFailureRollsBackTheMutation(t *testing.T) {
	ids := testsupport.NewIDs()
	cfg := testsupport.TestConfig(t, testsupport.BoundaryKey())
	pool := testsupport.OpenPool(t, cfg)
	t.Cleanup(func() { ids.Cleanup(t, pool) })

	// Audit calls in order: 1 register(admin), 2 create(space one), 3 create(space
	// two), 4 bind, 5 register(member), 6 grant(member). Failing calls 4 and 6 makes
	// the failure happen after the business row was written, which is the case that
	// has to roll back.
	sink := &selectiveAuditSink{failCalls: map[int]struct{}{4: {}, 6: {}}}
	service := testsupport.NewService(t, cfg, pool, application.WithAuditSink(sink))
	adminSession := testsupport.As(service, ids.SpaceAdmin(ids.Subject("admin")))

	spaceOne := mustCreateTeamSpace(t, adminSession, "audit rollback team one")
	spaceTwo := mustCreateTeamSpace(t, adminSession, "audit rollback team two")
	botID := ids.NumericID()
	groupID := ids.NumericID()

	if _, err := service.BindGroupSpace(adminSession.Ctx, adminSession.Principal, &documentv1.BindGroupSpaceRequest{
		BotId: botID, ExternalGroupId: groupID, SpaceId: spaceOne, Reason: "must roll back",
	}); err == nil {
		t.Fatal("BindGroupSpace must fail when the audit insert fails")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.group_space_bindings WHERE bot_id = $1 AND external_group_id = $2`, botID, groupID); got != 0 {
		t.Fatal("a failed audit left a binding row behind")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.space_audit_events
		 WHERE subject_type = 'group_space_binding' AND bot_id = $1 AND external_id = $2`, botID, groupID); got != 0 {
		t.Fatal("a failed audit left an audit row behind")
	}

	// The same rule for a membership mutation. The member is registered just before
	// the failing grant audit, in the same transaction, so both rows must roll back.
	member := ids.QQSubject(botID, ids.NumericID())
	if _, err := service.GrantSpaceMember(adminSession.Ctx, adminSession.Principal, &documentv1.GrantSpaceMemberRequest{
		SpaceId: spaceTwo, SubjectKey: member, MemberRole: documentv1.MemberRole_MEMBER_ROLE_MEMBER,
	}); err == nil {
		t.Fatal("GrantSpaceMember must fail when the audit insert fails")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.space_members WHERE space_id = $1::text::uuid AND subject_key = $2`, spaceTwo, member); got != 0 {
		t.Fatal("a failed audit left a membership row behind")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.space_audit_events
		 WHERE subject_type = 'space_member' AND subject_key = $1`, member); got != 0 {
		t.Fatal("a failed audit left a membership audit row behind")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.access_subjects WHERE subject_key = $1`, member); got != 0 {
		t.Fatal("a failed audit left a registered subject behind")
	}
}

// TestIntegrationOutboxStreamCursor asserts the consumer contract of the Outbox:
// sequence order, at-least-once re-reads from the same cursor, and an exhausted
// cursor past the end.
func TestIntegrationOutboxStreamCursor(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	subject := ids.Subject("outbox")
	session := testsupport.As(service, ids.WebWriter(subject))

	before := maxSequence(t, pool)
	detail := mustCreateDocument(t, session, "streamed document", "body for the stream", false, ids.RequestID("outbox"))
	documentID := detail.GetSummary().GetDocumentId()

	// The stream reads in sequence order from the very beginning. It is a
	// service-to-service read, so the consumer identity carries the event-reader
	// scope instead of a resource subject.
	readerIdentity := ids.Principal(serviceauth.CallerDocumentService, "", serviceauth.ScopeDocumentEventReader)
	readerCtx := serviceauth.WithPrincipal(context.Background(), readerIdentity)
	reader, err := service.OpenEventReader(readerCtx, readerIdentity, 0, 50, true)
	if err != nil {
		t.Fatalf("OpenEventReader: %v", err)
	}
	defer reader.Close()
	seenSequence := int64(-1)
	var sawDocument bool
	for {
		batch, err := reader.Next(readerCtx)
		if errors.Is(err, serviceauth.ErrStreamExhausted) {
			break
		}
		if err != nil {
			t.Fatalf("read the Outbox: %v", err)
		}
		for _, envelope := range batch {
			if envelope.GetSequence() <= seenSequence {
				t.Fatalf("sequence %d arrived after %d: the stream is not ordered", envelope.GetSequence(), seenSequence)
			}
			seenSequence = envelope.GetSequence()
			if envelope.GetDocumentId() == documentID {
				sawDocument = true
				if envelope.GetContent() != "body for the stream" {
					t.Fatalf("streamed content = %q", envelope.GetContent())
				}
				if envelope.GetEventId() == "" {
					t.Fatal("a streamed event must carry its event id for consumer side deduplication")
				}
			}
		}
	}
	if !sawDocument {
		t.Fatal("the created document never appeared in the Outbox stream")
	}

	// Re-reading from the same cursor returns the same events: delivery is
	// at-least-once and the consumer owns the cursor.
	first, err := service.OpenEventReader(readerCtx, readerIdentity, before, 50, false)
	if err != nil {
		t.Fatalf("OpenEventReader: %v", err)
	}
	defer first.Close()
	firstBatch, err := first.Next(readerCtx)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if len(firstBatch) == 0 {
		t.Fatal("the first read after a document was created returned nothing")
	}

	second, err := service.OpenEventReader(readerCtx, readerIdentity, before, 50, false)
	if err != nil {
		t.Fatalf("OpenEventReader: %v", err)
	}
	defer second.Close()
	secondBatch, err := second.Next(readerCtx)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if len(secondBatch) < len(firstBatch) {
		t.Fatalf("re-read returned %d events, first read returned %d", len(secondBatch), len(firstBatch))
	}
	for index, envelope := range firstBatch {
		if secondBatch[index].GetSequence() != envelope.GetSequence() ||
			secondBatch[index].GetEventId() != envelope.GetEventId() {
			t.Fatalf("re-read event %d differs: (%d,%s) vs (%d,%s)", index,
				secondBatch[index].GetSequence(), secondBatch[index].GetEventId(),
				envelope.GetSequence(), envelope.GetEventId())
		}
	}

	// A cursor past the end of the stream is exhausted, for a bounded and for a
	// live reader alike.
	past := maxSequence(t, pool) + 10000
	bounded, err := service.OpenEventReader(readerCtx, readerIdentity, past, 10, true)
	if err != nil {
		t.Fatalf("OpenEventReader: %v", err)
	}
	defer bounded.Close()
	if _, err := bounded.Next(readerCtx); !errors.Is(err, serviceauth.ErrStreamExhausted) {
		t.Fatalf("bounded read past the end: err = %v, want ErrStreamExhausted", err)
	}
	live, err := service.OpenEventReader(readerCtx, readerIdentity, past, 10, false)
	if err != nil {
		t.Fatalf("OpenEventReader: %v", err)
	}
	defer live.Close()
	if _, err := live.Next(readerCtx); !errors.Is(err, serviceauth.ErrStreamExhausted) {
		t.Fatalf("live read past the end: err = %v, want ErrStreamExhausted", err)
	}
}

func maxSequence(t *testing.T, pool *postgres.Pool) int64 {
	t.Helper()
	var sequence int64
	if err := pool.Pgx().QueryRow(context.Background(),
		`SELECT COALESCE(MAX(sequence), 0) FROM document_service.document_events`).Scan(&sequence); err != nil {
		t.Fatalf("read the Outbox high water mark: %v", err)
	}
	return sequence
}

// TestIntegrationCreateDocumentIsIdempotentPerRequestID asserts that the same
// subject reusing a request id gets the document it already created.
func TestIntegrationCreateDocumentIsIdempotentPerRequestID(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	subject := ids.Subject("idempotent")
	session := testsupport.As(service, ids.WebWriter(subject))
	requestID := ids.RequestID("same-request")

	first := mustCreateDocument(t, session, "idempotent document", "body", false, requestID)
	second := mustCreateDocument(t, session, "idempotent document", "body", false, requestID)

	if first.GetSummary().GetDocumentId() != second.GetSummary().GetDocumentId() {
		t.Fatalf("a repeated request id created a second document: %q vs %q",
			first.GetSummary().GetDocumentId(), second.GetSummary().GetDocumentId())
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.documents WHERE owner_subject_key = $1 AND create_request_id = $2`,
		subject, requestID); got != 1 {
		t.Fatalf("documents with the request id = %d, want 1", got)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_versions WHERE document_id = $1::text::uuid`,
		first.GetSummary().GetDocumentId()); got != 1 {
		t.Fatalf("versions = %d, want 1", got)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_events WHERE document_id = $1::text::uuid`,
		first.GetSummary().GetDocumentId()); got != 1 {
		t.Fatalf("Outbox events = %d, want 1: an idempotent repeat must not emit a second event", got)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.knowledge_spaces WHERE owner_subject_key = $1 AND space_type = 'private'`,
		subject); got != 1 {
		t.Fatalf("private spaces = %d, want 1", got)
	}
}

// failingEventSink refuses every Outbox append, which is how the rollback test
// forces the last step of a save transaction to fail after the version and the
// policy have already been written.
type failingEventSink struct{}

func (failingEventSink) Append(context.Context, pgx.Tx, domain.Event, string) error {
	return errors.New("event sink is unavailable")
}

func mustSaveDocument(t *testing.T, session testsupport.Session, request *documentv1.SaveDocumentRequest) *documentv1.SaveDocumentResponse {
	t.Helper()
	response, err := session.Service.SaveDocument(session.Ctx, session.Principal, request)
	if err != nil {
		t.Fatalf("SaveDocument(%s): %v", request.GetDocumentId(), err)
	}
	return response
}

// summaryOf finds one document in a listing response.
func summaryOf(t *testing.T, list *documentv1.ListDocumentsResponse, documentID string) *documentv1.DocumentSummary {
	t.Helper()
	for _, summary := range list.GetDocuments() {
		if summary.GetDocumentId() == documentID {
			return summary
		}
	}
	t.Fatalf("document %s is missing from the listing", documentID)
	return nil
}

// latestEventEnvelope decodes the newest Outbox row of a document.
func latestEventEnvelope(t *testing.T, ctx context.Context, pool *postgres.Pool, documentID string) *documentv1.DocumentEventEnvelope {
	t.Helper()
	var payload []byte
	if err := pool.Pgx().QueryRow(ctx,
		`SELECT payload FROM document_service.document_events
		  WHERE document_id = $1::text::uuid ORDER BY sequence DESC LIMIT 1`,
		documentID,
	).Scan(&payload); err != nil {
		t.Fatalf("read the latest Outbox event of %s: %v", documentID, err)
	}
	envelope := &documentv1.DocumentEventEnvelope{}
	if err := proto.Unmarshal(payload, envelope); err != nil {
		t.Fatalf("decode the latest Outbox event of %s: %v", documentID, err)
	}
	return envelope
}

// TestIntegrationSaveDocumentCommitsNewActiveVersion pins the happy path: one
// transaction writes the version, switches the active version, emits the Outbox
// event and returns the state after the switch, so a detail read immediately
// returns the saved body.
func TestIntegrationSaveDocumentCommitsNewActiveVersion(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	subject := ids.Subject("saver")
	session := testsupport.As(service, ids.WebWriter(subject))
	created := mustCreateDocument(t, session, "first version", "body one", false, ids.RequestID("save-create"))
	documentID := created.GetSummary().GetDocumentId()
	firstVersion := created.GetVersion().GetVersionId()
	saveRequestID := ids.RequestID("save-one")

	saved := mustSaveDocument(t, session, &documentv1.SaveDocumentRequest{
		DocumentId:                documentID,
		Title:                     "saved title",
		Content:                   "saved body",
		ContentFormat:             "markdown",
		ExpectedAggregateRevision: 1,
		RequestId:                 saveRequestID,
	})
	if saved.GetReplayed() {
		t.Fatal("the first attempt with a request id is not a replay")
	}
	detail := saved.GetDocument()
	secondVersion := detail.GetVersion().GetVersionId()
	if secondVersion == "" || secondVersion == firstVersion {
		t.Fatalf("saved version = %q, first version = %q", secondVersion, firstVersion)
	}
	// applied_version_id is the version this request applied, and on a fresh save
	// it is exactly the version the returned detail reports as active.
	if saved.GetAppliedVersionId() != secondVersion {
		t.Fatalf("applied version = %q, want the newly activated %q", saved.GetAppliedVersionId(), secondVersion)
	}
	if detail.GetSummary().GetActiveVersionId() != secondVersion {
		t.Fatalf("active version = %q, want the saved version %q", detail.GetSummary().GetActiveVersionId(), secondVersion)
	}
	if detail.GetVersion().GetRevision() != 2 || detail.GetVersion().GetContent() != "saved body" ||
		detail.GetVersion().GetPublicationStatus() != documentv1.PublicationStatus_PUBLICATION_STATUS_PUBLISHED {
		t.Fatalf("the saved version must be revision 2, published, with the new body: %+v", detail.GetVersion())
	}
	if detail.GetSummary().GetTitle() != "saved title" {
		t.Fatalf("summary title = %q", detail.GetSummary().GetTitle())
	}

	// The detail read returns the body that was just saved.
	read, err := session.Service.GetDocument(session.Ctx, session.Principal, &documentv1.GetDocumentRequest{DocumentId: documentID})
	if err != nil {
		t.Fatalf("GetDocument after save: %v", err)
	}
	if read.GetDocument().GetVersion().GetContent() != "saved body" {
		t.Fatalf("GetDocument content = %q, want the saved body", read.GetDocument().GetVersion().GetContent())
	}
	if read.GetDocument().GetSummary().GetActiveVersionId() != secondVersion {
		t.Fatalf("GetDocument active version = %q, want %q", read.GetDocument().GetSummary().GetActiveVersionId(), secondVersion)
	}

	// The stored head and the version statuses reflect the switch.
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_versions
		 WHERE version_id = $1::text::uuid AND publication_status = 'superseded'`, firstVersion); got != 1 {
		t.Fatal("saving must supersede the previous active version")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.documents
		 WHERE document_id = $1::text::uuid AND active_version_id = $2::text::uuid
		   AND activation_revision = 2 AND aggregate_revision = 3`,
		documentID, secondVersion); got != 1 {
		t.Fatal("the stored document must point at the saved version")
	}
	// The request id and the version it produced live in the durable idempotency
	// ledger, not on the document head: the ledger is what recognizes a retry that
	// arrives after later saves.
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_save_requests
		 WHERE document_id = $1::text::uuid AND request_id = $2 AND version_id = $3::text::uuid`,
		documentID, saveRequestID, secondVersion); got != 1 {
		t.Fatal("the committed request id must be recorded in the idempotency ledger with the version it produced")
	}
	// An ordinary body save leaves the access policy exactly where it was.
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_access_policies
		 WHERE document_id = $1::text::uuid AND authenticated_public = false AND access_revision = 1`,
		documentID); got != 1 {
		t.Fatal("a body-only save changed the access policy")
	}

	// Exactly one new Outbox event, describing the committed state.
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_events WHERE document_id = $1::text::uuid`, documentID); got != 2 {
		t.Fatalf("Outbox events = %d, want 2: creation plus saving", got)
	}
	envelope := latestEventEnvelope(t, context.Background(), pool, documentID)
	if envelope.GetKind() != documentv1.DocumentEventKind_DOCUMENT_EVENT_KIND_UPSERT {
		t.Fatalf("event kind = %s", envelope.GetKind())
	}
	if envelope.GetVersionId() != secondVersion || envelope.GetTitle() != "saved title" || envelope.GetContent() != "saved body" {
		t.Fatalf("event payload = (%q,%q,%q)", envelope.GetVersionId(), envelope.GetTitle(), envelope.GetContent())
	}
	if envelope.GetPublicationStatus() != documentv1.PublicationStatus_PUBLICATION_STATUS_PUBLISHED {
		t.Fatalf("event publication status = %s", envelope.GetPublicationStatus())
	}
	if envelope.GetAggregateRevision() != 3 || envelope.GetActivationRevision() != 2 {
		t.Fatalf("event revisions = (%d,%d), want (3,2)", envelope.GetAggregateRevision(), envelope.GetActivationRevision())
	}
	if envelope.GetAuthenticatedPublic() {
		t.Fatal("a body-only save must emit the unchanged private policy")
	}
	if envelope.GetCreatedAt() != detail.GetSummary().GetCreatedAt() {
		t.Fatalf("event created_at = %q, document created_at = %q", envelope.GetCreatedAt(), detail.GetSummary().GetCreatedAt())
	}
}

// TestIntegrationSaveDocumentStaleRevisionChangesNothing pins the optimistic
// concurrency rule of the save command: a stale expectation fails the whole
// transaction, so no version, no policy change and no event is left behind.
func TestIntegrationSaveDocumentStaleRevisionChangesNothing(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	subject := ids.Subject("save-concurrency")
	session := testsupport.As(service, ids.WebWriter(subject))
	created := mustCreateDocument(t, session, "stable", "body", false, ids.RequestID("save-stale-create"))
	documentID := created.GetSummary().GetDocumentId()
	staleRequestID := ids.RequestID("save-stale")

	_, err := session.Service.SaveDocument(session.Ctx, session.Principal, &documentv1.SaveDocumentRequest{
		DocumentId:                documentID,
		Title:                     "stale save",
		Content:                   "stale body",
		ExpectedAggregateRevision: 99,
		RequestId:                 staleRequestID,
	})
	expectCode(t, err, codes.FailedPrecondition, "SaveDocument with a stale expected revision")

	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_versions WHERE document_id = $1::text::uuid`, documentID); got != 1 {
		t.Fatal("a stale save appended a version")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.documents
		 WHERE document_id = $1::text::uuid AND aggregate_revision = 1`, documentID); got != 1 {
		t.Fatal("a stale save moved the aggregate revision")
	}
	// A refused attempt must not leave a ledger entry either: the request id was
	// never committed, so a later retry with it is a fresh save, not a replay.
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_save_requests
		 WHERE document_id = $1::text::uuid AND request_id = $2`, documentID, staleRequestID); got != 0 {
		t.Fatal("a stale save recorded its request id in the idempotency ledger")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_events WHERE document_id = $1::text::uuid`, documentID); got != 1 {
		t.Fatal("a stale save appended an Outbox event")
	}
}

// TestIntegrationSaveDocumentIsIdempotentPerRequestID asserts that repeating a
// request id returns the committed state instead of appending a second version
// and a second event, while a different request id really does save again.
func TestIntegrationSaveDocumentIsIdempotentPerRequestID(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	subject := ids.Subject("save-idempotent")
	session := testsupport.As(service, ids.WebWriter(subject))
	created := mustCreateDocument(t, session, "idempotent save", "body one", false, ids.RequestID("save-idem-create"))
	documentID := created.GetSummary().GetDocumentId()
	requestID := ids.RequestID("save-idem")

	request := func() *documentv1.SaveDocumentRequest {
		return &documentv1.SaveDocumentRequest{
			DocumentId:                documentID,
			Title:                     "idempotent save",
			Content:                   "body two",
			ContentFormat:             "markdown",
			ExpectedAggregateRevision: 1,
			RequestId:                 requestID,
		}
	}
	first := mustSaveDocument(t, session, request())
	if first.GetReplayed() {
		t.Fatal("the first attempt must not report a replay")
	}
	if first.GetAppliedVersionId() != first.GetDocument().GetSummary().GetActiveVersionId() {
		t.Fatalf("applied version = %q, active version = %q: a fresh save must report the version it activated",
			first.GetAppliedVersionId(), first.GetDocument().GetSummary().GetActiveVersionId())
	}
	// The repeat carries the same request id and the same (now stale) expectation
	// the first attempt moved away from; it must still replay rather than fail.
	second := mustSaveDocument(t, session, request())
	if !second.GetReplayed() {
		t.Fatal("repeating a request id must report replayed = true")
	}
	if second.GetDocument().GetSummary().GetActiveVersionId() != first.GetDocument().GetSummary().GetActiveVersionId() ||
		second.GetDocument().GetVersion().GetContent() != first.GetDocument().GetVersion().GetContent() ||
		second.GetDocument().GetSummary().GetAggregateRevision() != first.GetDocument().GetSummary().GetAggregateRevision() {
		t.Fatalf("a replay must return the first result: %+v vs %+v", second.GetDocument(), first.GetDocument())
	}
	// applied_version_id is the correlation handle of the original attempt, so an
	// immediate replay of the newest save reports the same version.
	if second.GetAppliedVersionId() != first.GetAppliedVersionId() {
		t.Fatalf("replay applied version = %q, first attempt = %q", second.GetAppliedVersionId(), first.GetAppliedVersionId())
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_versions WHERE document_id = $1::text::uuid`, documentID); got != 2 {
		t.Fatalf("versions = %d, want 2: a replay must not append a version", got)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_save_requests WHERE document_id = $1::text::uuid`, documentID); got != 1 {
		t.Fatalf("idempotency ledger rows = %d, want 1: a replay must not add a second entry", got)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_events WHERE document_id = $1::text::uuid`, documentID); got != 2 {
		t.Fatalf("Outbox events = %d, want 2: a replay must not append an event", got)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.documents WHERE document_id = $1::text::uuid AND aggregate_revision = 3`,
		documentID); got != 1 {
		t.Fatal("a replay must not move the aggregate revision")
	}

	// A different request id is a new save, not a replay.
	third := mustSaveDocument(t, session, &documentv1.SaveDocumentRequest{
		DocumentId:                documentID,
		Title:                     "idempotent save",
		Content:                   "body three",
		ContentFormat:             "markdown",
		ExpectedAggregateRevision: 3,
		RequestId:                 ids.RequestID("save-idem-two"),
	})
	if third.GetReplayed() {
		t.Fatal("a new request id must save, not replay")
	}
	if third.GetDocument().GetVersion().GetContent() != "body three" {
		t.Fatalf("second save content = %q", third.GetDocument().GetVersion().GetContent())
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_versions WHERE document_id = $1::text::uuid`, documentID); got != 3 {
		t.Fatalf("versions = %d, want 3 after a second distinct save", got)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_save_requests WHERE document_id = $1::text::uuid`, documentID); got != 2 {
		t.Fatalf("idempotency ledger rows = %d, want 2 after a second distinct request id", got)
	}
}

// TestIntegrationSaveDocumentReplaysADelayedRequestID is the F01 regression: the
// acceptance reproduction that a save request redelivered AFTER a later save must
// still be recognized as a replay.
//
// Create -> save A ("alpha") -> save B ("beta") -> redeliver A. With only a
// last_save_request_id column the third call was executed as a fresh save: it
// appended a fourth version and overwrote B's body with alpha. The durable ledger
// must instead report replayed = true, add nothing, keep three versions, and
// return the CURRENT committed state (beta) together with the version A's first
// attempt produced.
func TestIntegrationSaveDocumentReplaysADelayedRequestID(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	subject := ids.Subject("save-delayed")
	session := testsupport.As(service, ids.WebWriter(subject))
	created := mustCreateDocument(t, session, "delayed replay", "initial", false, ids.RequestID("save-delayed-create"))
	documentID := created.GetSummary().GetDocumentId()

	requestIDOf := func(name, content string) *documentv1.SaveDocumentRequest {
		return &documentv1.SaveDocumentRequest{
			DocumentId:                documentID,
			Title:                     "delayed replay",
			Content:                   content,
			ContentFormat:             "markdown",
			ExpectedAggregateRevision: 0,
			RequestId:                 ids.RequestID(name),
		}
	}
	requestA := requestIDOf("save-delayed-a", "alpha")
	requestB := requestIDOf("save-delayed-b", "beta")

	firstA := mustSaveDocument(t, session, requestA)
	if firstA.GetReplayed() {
		t.Fatal("the first delivery of A must not report a replay")
	}
	savedB := mustSaveDocument(t, session, requestB)
	if savedB.GetReplayed() {
		t.Fatal("B is a distinct request id and must not report a replay")
	}
	versionA := firstA.GetAppliedVersionId()
	versionB := savedB.GetAppliedVersionId()
	if versionA == "" || versionB == "" || versionA == versionB {
		t.Fatalf("applied versions = (%q,%q), want two distinct non-empty ids", versionA, versionB)
	}
	if got := savedB.GetDocument().GetVersion().GetContent(); got != "beta" {
		t.Fatalf("B content = %q, want beta", got)
	}

	// The redelivery of A arrives after B committed. Its expectation (0 = "do not
	// check") is irrelevant: a replay must be recognized before any check that the
	// first attempt already invalidated.
	replay := mustSaveDocument(t, session, requestA)
	if !replay.GetReplayed() {
		t.Fatalf("a delayed redelivery of a committed request id must replay, got replayed = false (versions = %d)",
			testsupport.Count(t, pool,
				`SELECT count(*) FROM document_service.document_versions WHERE document_id = $1::text::uuid`, documentID))
	}
	// applied_version_id is the version the FIRST attempt created, which B has
	// since superseded: it is a correlation handle, not the current head.
	if got := replay.GetAppliedVersionId(); got != versionA {
		t.Fatalf("replay applied version = %q, want the version A's first attempt created %q", got, versionA)
	}
	// The returned document is the current committed state, so the caller sees
	// beta, not the alpha its own request would have written.
	if got := replay.GetDocument().GetSummary().GetActiveVersionId(); got != versionB {
		t.Fatalf("replay active version = %q, want the current head %q", got, versionB)
	}
	if got := replay.GetDocument().GetVersion().GetVersionId(); got != versionB {
		t.Fatalf("replay detail version = %q, want %q", got, versionB)
	}
	if got := replay.GetDocument().GetVersion().GetContent(); got != "beta" {
		t.Fatalf("replay content = %q, want the current committed body beta", got)
	}

	// Nothing moved: three versions (initial, A, B), three events, aggregate 5,
	// activation 3, and exactly two ledger entries still pointing at A's and B's
	// own versions.
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_versions WHERE document_id = $1::text::uuid`, documentID); got != 3 {
		t.Fatalf("versions = %d, want 3: a delayed replay must not append a version", got)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_events WHERE document_id = $1::text::uuid`, documentID); got != 3 {
		t.Fatalf("Outbox events = %d, want 3: a delayed replay must not append an event", got)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.documents
		 WHERE document_id = $1::text::uuid AND active_version_id = $2::text::uuid
		   AND activation_revision = 3 AND aggregate_revision = 5`,
		documentID, versionB); got != 1 {
		t.Fatalf("the document head must still be B's commit (active %s, activation 3, aggregate 5)", versionB)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_versions
		 WHERE version_id = $1::text::uuid AND publication_status = 'superseded'`, versionA); got != 1 {
		t.Fatal("A's version must stay superseded: the replay must not resurrect it")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_save_requests WHERE document_id = $1::text::uuid`, documentID); got != 2 {
		t.Fatalf("idempotency ledger rows = %d, want 2: a delayed replay must not add an entry", got)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_save_requests
		 WHERE document_id = $1::text::uuid AND request_id = $2 AND version_id = $3::text::uuid`,
		documentID, requestA.GetRequestId(), versionA); got != 1 {
		t.Fatal("the ledger must still map A's request id to A's own version")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_save_requests
		 WHERE document_id = $1::text::uuid AND request_id = $2 AND version_id = $3::text::uuid`,
		documentID, requestB.GetRequestId(), versionB); got != 1 {
		t.Fatal("the ledger must still map B's request id to B's own version")
	}
}

// TestIntegrationSaveDocumentRejectsAKnownRequestIDWithADifferentPayload pins the
// other half of the durable ledger: a request id that was already committed must
// not silently absorb a different body. Reusing it with any other business input
// is ALREADY_EXISTS and changes nothing at all.
func TestIntegrationSaveDocumentRejectsAKnownRequestIDWithADifferentPayload(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	subject := ids.Subject("save-payload")
	session := testsupport.As(service, ids.WebWriter(subject))
	created := mustCreateDocument(t, session, "payload", "body one", false, ids.RequestID("save-payload-create"))
	documentID := created.GetSummary().GetDocumentId()
	requestID := ids.RequestID("save-payload")

	base := func() *documentv1.SaveDocumentRequest {
		return &documentv1.SaveDocumentRequest{
			DocumentId:                documentID,
			Title:                     "payload",
			Content:                   "body two",
			ContentFormat:             "markdown",
			ExpectedAggregateRevision: 1,
			RequestId:                 requestID,
		}
	}
	first := mustSaveDocument(t, session, base())
	if first.GetReplayed() {
		t.Fatal("the first attempt must not report a replay")
	}
	committedVersion := first.GetAppliedVersionId()

	countVersions := func() int64 {
		return testsupport.Count(t, pool,
			`SELECT count(*) FROM document_service.document_versions WHERE document_id = $1::text::uuid`, documentID)
	}
	countEvents := func() int64 {
		return testsupport.Count(t, pool,
			`SELECT count(*) FROM document_service.document_events WHERE document_id = $1::text::uuid`, documentID)
	}
	countLedger := func() int64 {
		return testsupport.Count(t, pool,
			`SELECT count(*) FROM document_service.document_save_requests WHERE document_id = $1::text::uuid`, documentID)
	}
	versionsBefore, eventsBefore, ledgerBefore := countVersions(), countEvents(), countLedger()
	if versionsBefore != 2 || eventsBefore != 2 || ledgerBefore != 1 {
		t.Fatalf("after the first save: versions = %d, events = %d, ledger = %d; want 2, 2, 1",
			versionsBefore, eventsBefore, ledgerBefore)
	}

	explicitPublic := true
	explicitPrivate := false
	cases := []struct {
		name   string
		mutate func(*documentv1.SaveDocumentRequest)
	}{
		{"different content", func(request *documentv1.SaveDocumentRequest) { request.Content = "body three" }},
		{"different title", func(request *documentv1.SaveDocumentRequest) { request.Title = "payload changed" }},
		{"different content format", func(request *documentv1.SaveDocumentRequest) { request.ContentFormat = "doc" }},
		{"access change added", func(request *documentv1.SaveDocumentRequest) { request.AuthenticatedPublic = &explicitPublic }},
		{"access change explicit false", func(request *documentv1.SaveDocumentRequest) { request.AuthenticatedPublic = &explicitPrivate }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// The expectation is stale (the first attempt moved the aggregate to 3),
			// so the payload check must be decided before the concurrency check:
			// the caller gets the actionable ALREADY_EXISTS, not a revision race.
			candidate := base()
			testCase.mutate(candidate)
			_, err := session.Service.SaveDocument(session.Ctx, session.Principal, candidate)
			expectCode(t, err, codes.AlreadyExists, testCase.name)
		})
	}

	// Presence is part of the payload even when the value does not move the
	// policy: a request id first used with an explicit false cannot be retried
	// with the field omitted, and the other way round.
	presenceRequestID := ids.RequestID("save-payload-presence")
	withExplicitFalse := func() *documentv1.SaveDocumentRequest {
		return &documentv1.SaveDocumentRequest{
			DocumentId:                documentID,
			Title:                     "payload",
			Content:                   "body three",
			ContentFormat:             "markdown",
			AuthenticatedPublic:       &explicitPrivate,
			ExpectedAggregateRevision: 3,
			RequestId:                 presenceRequestID,
		}
	}
	if _, err := session.Service.SaveDocument(session.Ctx, session.Principal, withExplicitFalse()); err != nil {
		t.Fatalf("a save with an explicit private policy: %v", err)
	}
	omitted := withExplicitFalse()
	omitted.AuthenticatedPublic = nil
	omitted.ExpectedAggregateRevision = 5
	_, err := session.Service.SaveDocument(session.Ctx, session.Principal, omitted)
	expectCode(t, err, codes.AlreadyExists, "the same request id with the access field omitted")

	// Every refusal left the committed state exactly as the first save produced
	// it, plus the one explicit-private save above.
	if got := countVersions(); got != versionsBefore+1 {
		t.Fatalf("versions = %d, want %d: a refused payload must not append a version", got, versionsBefore+1)
	}
	if got := countEvents(); got != eventsBefore+1 {
		t.Fatalf("Outbox events = %d, want %d: a refused payload must not append an event", got, eventsBefore+1)
	}
	if got := countLedger(); got != ledgerBefore+1 {
		t.Fatalf("idempotency ledger rows = %d, want %d", got, ledgerBefore+1)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_save_requests
		 WHERE document_id = $1::text::uuid AND request_id = $2 AND version_id = $3::text::uuid`,
		documentID, requestID, committedVersion); got != 1 {
		t.Fatal("a refused payload must leave the original ledger entry untouched")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.documents
		 WHERE document_id = $1::text::uuid AND aggregate_revision = 5`, documentID); got != 1 {
		t.Fatal("a refused payload must not move the aggregate revision")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_access_policies
		 WHERE document_id = $1::text::uuid AND authenticated_public = false AND access_revision = 1`, documentID); got != 1 {
		t.Fatal("a refused payload must not change the access policy")
	}
}

// TestIntegrationConcurrentSaveWithTheSameRequestIDWritesOneVersion proves the
// replay path is serialized by the document row lock: the replay key is read from
// the locked row inside the transaction, so two identical requests racing against
// each other leave one version and one event, and the loser reports a replay
// instead of appending its own version.
func TestIntegrationConcurrentSaveWithTheSameRequestIDWritesOneVersion(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	subject := ids.Subject("save-race")
	session := testsupport.As(service, ids.WebWriter(subject))
	created := mustCreateDocument(t, session, "race", "body one", false, ids.RequestID("save-race-create"))
	documentID := created.GetSummary().GetDocumentId()
	requestID := ids.RequestID("save-race")

	type outcome struct {
		replayed bool
		err      error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	for attempt := 0; attempt < 2; attempt++ {
		go func() {
			racer := testsupport.As(service, ids.WebWriter(subject))
			<-start
			response, err := racer.Service.SaveDocument(racer.Ctx, racer.Principal, &documentv1.SaveDocumentRequest{
				DocumentId:                documentID,
				Title:                     "race",
				Content:                   "body two",
				ExpectedAggregateRevision: 1,
				RequestId:                 requestID,
			})
			if err != nil {
				results <- outcome{err: err}
				return
			}
			results <- outcome{replayed: response.GetReplayed()}
		}()
	}
	close(start)

	writes, replays := 0, 0
	for attempt := 0; attempt < 2; attempt++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent SaveDocument: %v", result.err)
		}
		if result.replayed {
			replays++
		} else {
			writes++
		}
	}
	if writes != 1 || replays != 1 {
		t.Fatalf("writes = %d, replays = %d; want exactly one of each", writes, replays)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_versions WHERE document_id = $1::text::uuid`, documentID); got != 2 {
		t.Fatalf("versions = %d, want 2: a concurrent duplicate must not append a second version", got)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_events WHERE document_id = $1::text::uuid`, documentID); got != 2 {
		t.Fatalf("Outbox events = %d, want 2: a concurrent duplicate must not append a second event", got)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.documents
		 WHERE document_id = $1::text::uuid AND aggregate_revision = 3`,
		documentID); got != 1 {
		t.Fatal("a concurrent duplicate must move the aggregate revision exactly once")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_save_requests
		 WHERE document_id = $1::text::uuid AND request_id = $2 AND version_id = (
		     SELECT active_version_id FROM document_service.documents WHERE document_id = $1::text::uuid)`,
		documentID, requestID); got != 1 {
		t.Fatal("a concurrent duplicate must leave exactly one ledger entry, pointing at the committed version")
	}
}

// TestIntegrationSaveDocumentRejectsNonOwner asserts that the ownership gate runs
// before anything is written: a stranger can neither save nor record a request id
// on another subject's document.
func TestIntegrationSaveDocumentRejectsNonOwner(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	owner := ids.Subject("save-owner")
	stranger := ids.Subject("save-stranger")
	ownerSession := testsupport.As(service, ids.WebWriter(owner))
	strangerSession := testsupport.As(service, ids.WebWriter(stranger))

	created := mustCreateDocument(t, ownerSession, "owned", "body one", false, ids.RequestID("save-owner-create"))
	documentID := created.GetSummary().GetDocumentId()
	// The stranger registers itself first, so the refusal can only be the
	// ownership decision and not "unknown subject".
	mustCreateDocument(t, strangerSession, "stranger", "their body", false, ids.RequestID("save-stranger-create"))

	_, err := strangerSession.Service.SaveDocument(strangerSession.Ctx, strangerSession.Principal, &documentv1.SaveDocumentRequest{
		DocumentId: documentID, Title: "hijacked", Content: "hijacked body", RequestId: ids.RequestID("save-hijack"),
	})
	expectCode(t, err, codes.PermissionDenied, "SaveDocument on a foreign document")

	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.documents
		 WHERE document_id = $1::text::uuid AND aggregate_revision = 1`, documentID); got != 1 {
		t.Fatal("a refused save changed the document head")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_versions WHERE document_id = $1::text::uuid`, documentID); got != 1 {
		t.Fatal("a refused save appended a version")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_save_requests WHERE document_id = $1::text::uuid`, documentID); got != 0 {
		t.Fatal("a refused save recorded an idempotency ledger entry")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_events WHERE document_id = $1::text::uuid`, documentID); got != 1 {
		t.Fatal("a refused save appended an Outbox event")
	}
}

// TestIntegrationSaveDocumentRollsBackWhenTheEventCannotBeWritten forces the last
// step of the save transaction to fail and asserts that the version, the access
// policy and the idempotency ledger entry written before it are rolled back with
// it - and that the very same request id can then be retried successfully.
func TestIntegrationSaveDocumentRollsBackWhenTheEventCannotBeWritten(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	subject := ids.Subject("save-rollback")
	session := testsupport.As(service, ids.WebWriter(subject))
	created := mustCreateDocument(t, session, "rollback", "body one", false, ids.RequestID("save-rollback-create"))
	documentID := created.GetSummary().GetDocumentId()
	firstVersion := created.GetVersion().GetVersionId()

	// A second service over the same database whose Outbox writer always fails.
	cfg := testsupport.TestConfig(t, testsupport.BoundaryKey())
	broken := testsupport.NewService(t, cfg, pool, application.WithEventSink(failingEventSink{}))
	brokenSession := testsupport.As(broken, ids.WebWriter(subject))

	requestID := ids.RequestID("save-rollback")
	explicitPublic := true
	request := func() *documentv1.SaveDocumentRequest {
		return &documentv1.SaveDocumentRequest{
			DocumentId:                documentID,
			Title:                     "rolled back then retried",
			Content:                   "body two",
			AuthenticatedPublic:       &explicitPublic,
			ExpectedAggregateRevision: 1,
			RequestId:                 requestID,
		}
	}

	_, err := brokenSession.Service.SaveDocument(brokenSession.Ctx, brokenSession.Principal, request())
	if err == nil {
		t.Fatal("SaveDocument must fail when the Outbox write fails")
	}

	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_versions WHERE document_id = $1::text::uuid`, documentID); got != 1 {
		t.Fatal("a rolled back save left a version behind")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.documents
		 WHERE document_id = $1::text::uuid AND aggregate_revision = 1 AND active_version_id = $2::text::uuid`,
		documentID, firstVersion); got != 1 {
		t.Fatal("a rolled back save changed the document head")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_access_policies
		 WHERE document_id = $1::text::uuid AND authenticated_public = false AND access_revision = 1`, documentID); got != 1 {
		t.Fatal("a rolled back save left the access policy changed")
	}
	// The ledger entry is written in the same transaction, so a rolled back save
	// leaves no "already handled" record: the retry below must be a real save.
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_save_requests
		 WHERE document_id = $1::text::uuid AND request_id = $2`, documentID, requestID); got != 0 {
		t.Fatal("a rolled back save left an idempotency ledger entry behind")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_events WHERE document_id = $1::text::uuid`, documentID); got != 1 {
		t.Fatal("a rolled back save left an Outbox event behind")
	}

	// The healthy service still sees the original body: nothing leaked.
	read, err := session.Service.GetDocument(session.Ctx, session.Principal, &documentv1.GetDocumentRequest{DocumentId: documentID})
	if err != nil {
		t.Fatalf("GetDocument after the rolled back save: %v", err)
	}
	if read.GetDocument().GetVersion().GetContent() != "body one" {
		t.Fatalf("content = %q, want the body that was committed", read.GetDocument().GetVersion().GetContent())
	}

	// Retrying the same request id after the rollback is a fresh save, not a
	// replay: nothing was ever committed under that id.
	retried := mustSaveDocument(t, session, request())
	if retried.GetReplayed() {
		t.Fatal("retrying a request id whose transaction rolled back must save, not replay")
	}
	retriedVersion := retried.GetAppliedVersionId()
	if retriedVersion == "" || retriedVersion == firstVersion {
		t.Fatalf("retried applied version = %q, first version = %q", retriedVersion, firstVersion)
	}
	if retried.GetDocument().GetSummary().GetActiveVersionId() != retriedVersion {
		t.Fatalf("active version = %q, want the retried version %q",
			retried.GetDocument().GetSummary().GetActiveVersionId(), retriedVersion)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_versions WHERE document_id = $1::text::uuid`, documentID); got != 2 {
		t.Fatalf("versions = %d, want 2 after the retry committed", got)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_save_requests
		 WHERE document_id = $1::text::uuid AND request_id = $2 AND version_id = $3::text::uuid`,
		documentID, requestID, retriedVersion); got != 1 {
		t.Fatal("the successful retry must record its request id with the version it produced")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_access_policies
		 WHERE document_id = $1::text::uuid AND authenticated_public = true AND access_revision = 2`, documentID); got != 1 {
		t.Fatal("the retry must apply the access change it asked for")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_events WHERE document_id = $1::text::uuid`, documentID); got != 2 {
		t.Fatalf("Outbox events = %d, want 2: creation plus the retried save", got)
	}
}

// TestIntegrationSaveDocumentAccessPolicyIsConsistentEverywhere walks the three
// access states of a save: an ordinary body edit keeps the policy, an explicit
// change applies it, and the detail, the list summary and the Outbox event always
// agree with each other and with the resource decision.
func TestIntegrationSaveDocumentAccessPolicyIsConsistentEverywhere(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	owner := ids.Subject("save-policy-owner")
	stranger := ids.Subject("save-policy-stranger")
	ownerSession := testsupport.As(service, ids.WebWriter(owner))
	strangerSession := testsupport.As(service, ids.WebWriter(stranger))

	created := mustCreateDocument(t, ownerSession, "policy", "body one", false, ids.RequestID("save-policy-create"))
	documentID := created.GetSummary().GetDocumentId()
	// Register the stranger so a later grant is decided on the resource facts.
	mustCreateDocument(t, strangerSession, "stranger", "their body", false, ids.RequestID("save-policy-stranger"))

	assertAccess := func(what string, wantPublic bool, wantAccessRevision int64, wantLifecycleEvents int64) {
		t.Helper()
		read, err := ownerSession.Service.GetDocument(ownerSession.Ctx, ownerSession.Principal, &documentv1.GetDocumentRequest{DocumentId: documentID})
		if err != nil {
			t.Fatalf("%s: GetDocument: %v", what, err)
		}
		if read.GetDocument().GetAuthenticatedPublic() != wantPublic {
			t.Fatalf("%s: detail authenticated_public = %v, want %v", what, read.GetDocument().GetAuthenticatedPublic(), wantPublic)
		}
		if read.GetDocument().GetSummary().GetAuthenticatedPublic() != wantPublic {
			t.Fatalf("%s: detail summary authenticated_public = %v, want %v", what, read.GetDocument().GetSummary().GetAuthenticatedPublic(), wantPublic)
		}
		list, err := ownerSession.Service.ListDocuments(ownerSession.Ctx, ownerSession.Principal, &documentv1.ListDocumentsRequest{
			OwnerSubjectKey: owner, PageSize: 10,
		})
		if err != nil {
			t.Fatalf("%s: ListDocuments: %v", what, err)
		}
		if got := summaryOf(t, list, documentID).GetAuthenticatedPublic(); got != wantPublic {
			t.Fatalf("%s: list summary authenticated_public = %v, want %v", what, got, wantPublic)
		}
		envelope := latestEventEnvelope(t, context.Background(), pool, documentID)
		if envelope.GetAuthenticatedPublic() != wantPublic {
			t.Fatalf("%s: event authenticated_public = %v, want %v", what, envelope.GetAuthenticatedPublic(), wantPublic)
		}
		if envelope.GetAccessRevision() != uint64(wantAccessRevision) {
			t.Fatalf("%s: event access revision = %d, want %d", what, envelope.GetAccessRevision(), wantAccessRevision)
		}
		if got := testsupport.Count(t, pool,
			`SELECT count(*) FROM document_service.document_events WHERE document_id = $1::text::uuid`, documentID); got != wantLifecycleEvents {
			t.Fatalf("%s: Outbox events = %d, want %d", what, got, wantLifecycleEvents)
		}
		// The resource decision follows the policy: a registered stranger may read
		// exactly when the document is authenticated public.
		_, err = strangerSession.Service.GetDocument(strangerSession.Ctx, strangerSession.Principal, &documentv1.GetDocumentRequest{DocumentId: documentID})
		if wantPublic && err != nil {
			t.Fatalf("%s: a public document must be readable by a registered subject: %v", what, err)
		}
		if !wantPublic && status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s: a private document must stay denied, got %v", what, err)
		}
	}

	// An ordinary body edit: the policy is not mentioned, so it is not touched.
	if _, err := ownerSession.Service.SaveDocument(ownerSession.Ctx, ownerSession.Principal, &documentv1.SaveDocumentRequest{
		DocumentId: documentID, Title: "policy", Content: "body two", RequestId: ids.RequestID("save-policy-body"),
	}); err != nil {
		t.Fatalf("ordinary SaveDocument: %v", err)
	}
	assertAccess("after a body-only save", false, 1, 2)

	// An explicit true opens the document in the same transaction as the version.
	explicitPublic := true
	if _, err := ownerSession.Service.SaveDocument(ownerSession.Ctx, ownerSession.Principal, &documentv1.SaveDocumentRequest{
		DocumentId: documentID, Title: "policy", Content: "body three",
		AuthenticatedPublic: &explicitPublic, RequestId: ids.RequestID("save-policy-public"),
	}); err != nil {
		t.Fatalf("SaveDocument with an explicit access change: %v", err)
	}
	assertAccess("after an explicit public save", true, 2, 3)

	// An explicit false closes it again, and an identical repeat does not inflate
	// the access revision: the flag did not move.
	explicitPrivate := false
	if _, err := ownerSession.Service.SaveDocument(ownerSession.Ctx, ownerSession.Principal, &documentv1.SaveDocumentRequest{
		DocumentId: documentID, Title: "policy", Content: "body four",
		AuthenticatedPublic: &explicitPrivate, RequestId: ids.RequestID("save-policy-private"),
	}); err != nil {
		t.Fatalf("SaveDocument with an explicit private change: %v", err)
	}
	if _, err := ownerSession.Service.SaveDocument(ownerSession.Ctx, ownerSession.Principal, &documentv1.SaveDocumentRequest{
		DocumentId: documentID, Title: "policy", Content: "body five",
		AuthenticatedPublic: &explicitPrivate, RequestId: ids.RequestID("save-policy-private-again"),
	}); err != nil {
		t.Fatalf("SaveDocument repeating the explicit private value: %v", err)
	}
	assertAccess("after explicit private saves", false, 3, 5)
}

// TestIntegrationDocumentLifecycleTransitions walks publish, withdraw, archive and
// trash and asserts the revisions, the version statuses and the event kinds.
func TestIntegrationDocumentLifecycleTransitions(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	subject := ids.Subject("lifecycle")
	session := testsupport.As(service, ids.WebWriter(subject))
	detail := mustCreateDocument(t, session, "lifecycle", "body one", false, ids.RequestID("lifecycle"))
	documentID := detail.GetSummary().GetDocumentId()
	firstVersion := detail.GetVersion().GetVersionId()

	// A draft is appended, then published: the first version becomes superseded.
	//
	// DocumentDetail always means "the head plus its active version" (the same shape
	// GetDocument returns), so the response to UpdateDraft still shows the published
	// version 1; the appended draft is asserted where it lives.
	draft, err := session.Service.UpdateDraft(session.Ctx, session.Principal, &documentv1.UpdateDraftRequest{
		DocumentId: documentID, Title: "lifecycle two", Content: "body two",
	})
	if err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
	if got := draft.GetDocument().GetSummary().GetActiveVersionId(); got != firstVersion {
		t.Fatalf("appending a draft must not change the active version: %q, want %q", got, firstVersion)
	}
	if draft.GetDocument().GetSummary().GetAggregateRevision() != 2 {
		t.Fatalf("aggregate revision = %d, want 2", draft.GetDocument().GetSummary().GetAggregateRevision())
	}
	var secondVersion string
	if err := pool.Pgx().QueryRow(context.Background(), `
		SELECT version_id::text FROM document_service.document_versions
		WHERE document_id = $1::text::uuid AND revision = 2`, documentID).Scan(&secondVersion); err != nil {
		t.Fatalf("read the appended version: %v", err)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_versions
		 WHERE document_id = $1::text::uuid AND revision = 2 AND publication_status = 'draft'
		   AND title = 'lifecycle two' AND content = 'body two'`, documentID); got != 1 {
		t.Fatal("the appended version must be a draft at revision 2 with the new content")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_versions WHERE document_id = $1::text::uuid`, documentID); got != 2 {
		t.Fatalf("versions = %d, want 2", got)
	}
	// Appending a draft is not an index event: nothing about the indexable state
	// changed, and the access flag did not move either.
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_events WHERE document_id = $1::text::uuid`, documentID); got != 1 {
		t.Fatalf("Outbox events = %d, want 1: a content-only draft append is not indexable", got)
	}

	published, err := session.Service.PublishDocument(session.Ctx, session.Principal, &documentv1.PublishDocumentRequest{
		DocumentId: documentID, VersionId: secondVersion,
	})
	if err != nil {
		t.Fatalf("PublishDocument: %v", err)
	}
	if published.GetDocument().GetSummary().GetActiveVersionId() != secondVersion {
		t.Fatalf("active version = %q, want %q", published.GetDocument().GetSummary().GetActiveVersionId(), secondVersion)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_versions WHERE version_id = $1::text::uuid AND publication_status = 'superseded'`,
		firstVersion); got != 1 {
		t.Fatal("publishing a new version must supersede the previous active one")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.documents WHERE document_id = $1::text::uuid AND activation_revision = 2`,
		documentID); got != 1 {
		t.Fatal("publishing must bump the activation revision")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_events WHERE document_id = $1::text::uuid AND event_kind = 'upsert'`,
		documentID); got != 2 {
		t.Fatalf("upsert events = %d, want 2: creation plus publication (the draft append is not indexable)", got)
	}

	// Withdrawing the active version removes it and emits a delete event.
	withdrawn, err := session.Service.WithdrawVersion(session.Ctx, session.Principal, &documentv1.WithdrawVersionRequest{
		DocumentId: documentID, VersionId: secondVersion,
	})
	if err != nil {
		t.Fatalf("WithdrawVersion: %v", err)
	}
	if withdrawn.GetDocument().GetSummary().GetActiveVersionId() != "" {
		t.Fatal("withdrawing the active version must clear the active version")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_events WHERE document_id = $1::text::uuid AND event_kind = 'delete'`,
		documentID); got != 1 {
		t.Fatal("withdrawing the active version must emit one delete event")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.documents WHERE document_id = $1::text::uuid AND active_version_id IS NULL`,
		documentID); got != 1 {
		t.Fatal("the stored document must no longer have an active version")
	}

	// Archive and trash are lifecycle transitions with their own revisions.
	if _, err := session.Service.ArchiveDocument(session.Ctx, session.Principal, &documentv1.ArchiveDocumentRequest{
		DocumentId: documentID,
	}); err != nil {
		t.Fatalf("ArchiveDocument: %v", err)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.documents
		 WHERE document_id = $1::text::uuid AND lifecycle_status = 'archived' AND lifecycle_revision = 1`,
		documentID); got != 1 {
		t.Fatal("archiving must set the archived lifecycle at revision 1")
	}
	if _, err := session.Service.TrashDocument(session.Ctx, session.Principal, &documentv1.TrashDocumentRequest{
		DocumentId: documentID,
	}); err != nil {
		t.Fatalf("TrashDocument: %v", err)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.documents
		 WHERE document_id = $1::text::uuid AND lifecycle_status = 'trashed' AND lifecycle_revision = 2 AND trashed_at IS NOT NULL`,
		documentID); got != 1 {
		t.Fatal("trashing must set the trashed lifecycle at revision 2 with a timestamp")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_events WHERE document_id = $1::text::uuid AND event_kind = 'delete'`,
		documentID); got != 3 {
		t.Fatal("withdraw, archive and trash must each emit a delete event")
	}
}

// TestIntegrationGetDocumentAndListDocuments covers the detail read and the
// permission filtered listing, including the authenticated public filter.
func TestIntegrationGetDocumentAndListDocuments(t *testing.T) {
	ids, _, service := newEnvironment(t)
	owner := ids.Subject("lister")
	session := testsupport.As(service, ids.WebWriter(owner))
	publicDetail := mustCreateDocument(t, session, "public doc", "public body", true, ids.RequestID("public"))
	privateDetail := mustCreateDocument(t, session, "private doc", "private body", false, ids.RequestID("private-list"))

	read, err := session.Service.GetDocument(session.Ctx, session.Principal, &documentv1.GetDocumentRequest{
		DocumentId: privateDetail.GetSummary().GetDocumentId(),
	})
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	if read.GetDocument().GetVersion().GetContent() != "private body" {
		t.Fatal("the detail read must return the active version content")
	}
	if len(read.GetDocument().GetGrantedSpaceIds()) != 0 {
		t.Fatal("a document without space grants must report none")
	}

	list, err := session.Service.ListDocuments(session.Ctx, session.Principal, &documentv1.ListDocumentsRequest{
		OwnerSubjectKey: owner,
		PageSize:        10,
	})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(list.GetDocuments()) != 2 {
		t.Fatalf("documents = %d, want 2", len(list.GetDocuments()))
	}
	if list.GetTotalCount() != 2 {
		t.Fatalf("total_count = %d, want 2: the total counts the filter, not the page", list.GetTotalCount())
	}
	publicSeen := false
	for _, summary := range list.GetDocuments() {
		if summary.GetOwnerSubjectKey() != owner {
			t.Fatalf("listing leaked a document of %q", summary.GetOwnerSubjectKey())
		}
		// The summary carries the access policy's flag, never an inference from
		// the publication status: both documents here are published.
		wantPublic := summary.GetDocumentId() == publicDetail.GetSummary().GetDocumentId()
		if summary.GetAuthenticatedPublic() != wantPublic {
			t.Fatalf("summary of %s reports authenticated_public = %v, want %v",
				summary.GetDocumentId(), summary.GetAuthenticatedPublic(), wantPublic)
		}
		publicSeen = publicSeen || summary.GetAuthenticatedPublic()
	}
	if !publicSeen {
		t.Fatal("the public document's summary must report authenticated_public = true")
	}

	publicOnly, err := session.Service.ListDocuments(session.Ctx, session.Principal, &documentv1.ListDocumentsRequest{
		OwnerSubjectKey:         owner,
		AuthenticatedPublicOnly: true,
		PageSize:                10,
	})
	if err != nil {
		t.Fatalf("ListDocuments with authenticated_public_only: %v", err)
	}
	if len(publicOnly.GetDocuments()) != 1 ||
		publicOnly.GetDocuments()[0].GetDocumentId() != publicDetail.GetSummary().GetDocumentId() {
		t.Fatalf("authenticated_public_only returned %d documents", len(publicOnly.GetDocuments()))
	}
	if publicOnly.GetTotalCount() != 1 {
		t.Fatalf("total_count with authenticated_public_only = %d, want 1", publicOnly.GetTotalCount())
	}
	if !publicOnly.GetDocuments()[0].GetAuthenticatedPublic() {
		t.Fatal("a document matched by the public filter must report authenticated_public = true")
	}

	// Pagination: one document per page, with an opaque cursor.
	firstPage, err := session.Service.ListDocuments(session.Ctx, session.Principal, &documentv1.ListDocumentsRequest{
		OwnerSubjectKey: owner, PageSize: 1,
	})
	if err != nil {
		t.Fatalf("ListDocuments page one: %v", err)
	}
	if len(firstPage.GetDocuments()) != 1 || firstPage.GetNextPageToken() == "" {
		t.Fatalf("page one = %d documents, token %q", len(firstPage.GetDocuments()), firstPage.GetNextPageToken())
	}
	if firstPage.GetTotalCount() != 2 {
		t.Fatalf("total_count = %d on a one-document page, want 2: the total is independent of the page", firstPage.GetTotalCount())
	}
	secondPage, err := session.Service.ListDocuments(session.Ctx, session.Principal, &documentv1.ListDocumentsRequest{
		OwnerSubjectKey: owner, PageSize: 1, PageToken: firstPage.GetNextPageToken(),
	})
	if err != nil {
		t.Fatalf("ListDocuments page two: %v", err)
	}
	if len(secondPage.GetDocuments()) != 1 {
		t.Fatalf("page two = %d documents, want 1", len(secondPage.GetDocuments()))
	}
	if secondPage.GetDocuments()[0].GetDocumentId() == firstPage.GetDocuments()[0].GetDocumentId() {
		t.Fatal("the second page repeated the first document")
	}

	// A malformed cursor is a caller error, never a silent restart.
	if _, err := session.Service.ListDocuments(session.Ctx, session.Principal, &documentv1.ListDocumentsRequest{
		PageToken: "not a cursor",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("malformed page token: code = %s, want InvalidArgument", status.Code(err))
	}
}

// TestIntegrationValidationRejections pins the INVALID_ARGUMENT surface of the
// commands that are easy to get wrong.
func TestIntegrationValidationRejections(t *testing.T) {
	ids, _, service := newEnvironment(t)
	subject := ids.Subject("validation")
	session := testsupport.As(service, ids.WebWriter(subject))

	cases := []struct {
		name string
		call func() error
	}{
		{"empty title", func() error {
			_, err := session.Service.CreateDocument(session.Ctx, session.Principal, &documentv1.CreateDocumentRequest{
				Title: "  ", Content: "body",
			})
			return err
		}},
		{"empty content", func() error {
			_, err := session.Service.CreateDocument(session.Ctx, session.Principal, &documentv1.CreateDocumentRequest{
				Title: "title", Content: " ",
			})
			return err
		}},
		{"unknown content format", func() error {
			_, err := session.Service.CreateDocument(session.Ctx, session.Principal, &documentv1.CreateDocumentRequest{
				Title: "title", Content: "body", ContentFormat: "html",
			})
			return err
		}},
		{"UpdateDraft asking for an access change", func() error {
			// The flag is checked before the document is looked up, so a bad id
			// cannot mask the unsupported request.
			explicit := true
			_, err := session.Service.UpdateDraft(session.Ctx, session.Principal, &documentv1.UpdateDraftRequest{
				DocumentId: "11111111-1111-4111-8111-111111111111", Title: "title", Content: "body",
				AuthenticatedPublic: &explicit,
			})
			return err
		}},
		{"malformed document id", func() error {
			_, err := session.Service.GetDocument(session.Ctx, session.Principal, &documentv1.GetDocumentRequest{
				DocumentId: "not-a-uuid",
			})
			return err
		}},
		{"malformed subject key", func() error {
			_, err := session.Service.GrantSpaceMember(session.Ctx, session.Principal, &documentv1.GrantSpaceMemberRequest{
				SpaceId: "11111111-1111-4111-8111-111111111111", SubjectKey: "web:robot:1",
			})
			return err
		}},
		{"private conversation with a group id", func() error {
			_, err := session.Service.ResolveAccessScope(session.Ctx, session.Principal, &documentv1.ResolveAccessScopeRequest{
				Conversation: &documentv1.ConversationContext{
					Kind:            documentv1.ConversationKind_CONVERSATION_KIND_PRIVATE,
					ExternalGroupId: "20002",
				},
			})
			return err
		}},
		{"binding without a reason", func() error {
			_, err := session.Service.BindGroupSpace(session.Ctx, session.Principal, &documentv1.BindGroupSpaceRequest{
				BotId: "10001", ExternalGroupId: "20002", SpaceId: "11111111-1111-4111-8111-111111111111",
			})
			return err
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			expectCode(t, testCase.call(), codes.InvalidArgument, testCase.name)
		})
	}

	// A missing document and a missing space are NOT_FOUND, not invalid input.
	expectCode(t, func() error {
		_, err := session.Service.GetDocument(session.Ctx, session.Principal, &documentv1.GetDocumentRequest{
			DocumentId: "11111111-1111-4111-8111-111111111111",
		})
		return err
	}(), codes.NotFound, "unknown document")

	expectCode(t, func() error {
		_, err := session.Service.GetSpace(session.Ctx, session.Principal, &documentv1.GetSpaceRequest{
			SpaceId: "11111111-1111-4111-8111-111111111111",
		})
		return err
	}(), codes.NotFound, "unknown space")
}

// TestIntegrationMembershipRules asserts the membership contract: an owner is not
// revocable, a private space takes no members, duplicates are refused, and a
// revoked membership is restored rather than duplicated.
func TestIntegrationMembershipRules(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	owner := ids.Subject("member-owner")
	session := testsupport.As(service, ids.WebWriter(owner))
	spaceID := mustCreateTeamSpace(t, session, "membership team")
	member := ids.Subject("member")

	mustGrantMember(t, session, spaceID, member, documentv1.MemberRole_MEMBER_ROLE_MEMBER)
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.space_members WHERE space_id = $1::text::uuid AND revoked_at IS NULL`, spaceID); got != 2 {
		t.Fatalf("active members = %d, want owner + member", got)
	}

	// Granting an already active membership is ALREADY_EXISTS.
	_, err := session.Service.GrantSpaceMember(session.Ctx, session.Principal, &documentv1.GrantSpaceMemberRequest{
		SpaceId: spaceID, SubjectKey: member, MemberRole: documentv1.MemberRole_MEMBER_ROLE_MEMBER,
	})
	expectCode(t, err, codes.AlreadyExists, "duplicate membership grant")

	// The owner membership is not revocable.
	_, err = session.Service.RevokeSpaceMember(session.Ctx, session.Principal, &documentv1.RevokeSpaceMemberRequest{
		SpaceId: spaceID, SubjectKey: owner, RequestId: ids.RequestID("revoke-owner"),
	})
	expectCode(t, err, codes.FailedPrecondition, "revoking an owner membership")

	// Revoking and restoring keeps one row.
	if _, err := session.Service.RevokeSpaceMember(session.Ctx, session.Principal, &documentv1.RevokeSpaceMemberRequest{
		SpaceId: spaceID, SubjectKey: member, RequestId: ids.RequestID("revoke-member"),
	}); err != nil {
		t.Fatalf("RevokeSpaceMember: %v", err)
	}
	_, err = session.Service.RevokeSpaceMember(session.Ctx, session.Principal, &documentv1.RevokeSpaceMemberRequest{
		SpaceId: spaceID, SubjectKey: member, RequestId: ids.RequestID("revoke-member-again"),
	})
	expectCode(t, err, codes.NotFound, "revoking a membership twice")

	mustGrantMember(t, session, spaceID, member, documentv1.MemberRole_MEMBER_ROLE_MEMBER)
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.space_members WHERE space_id = $1::text::uuid AND subject_key = $2`,
		spaceID, member); got != 1 {
		t.Fatalf("membership rows = %d, want 1: restoring must not duplicate", got)
	}

	// A private space takes no other member.
	detail := mustCreateDocument(t, session, "own", "body", false, ids.RequestID("private-members"))
	_, err = session.Service.GrantSpaceMember(session.Ctx, session.Principal, &documentv1.GrantSpaceMemberRequest{
		SpaceId: detail.GetSummary().GetOwnerSpaceId(), SubjectKey: member,
	})
	expectCode(t, err, codes.FailedPrecondition, "granting a member to a private space")

	// The space detail reports members and bindings.
	space, err := session.Service.GetSpace(session.Ctx, session.Principal, &documentv1.GetSpaceRequest{SpaceId: spaceID})
	if err != nil {
		t.Fatalf("GetSpace: %v", err)
	}
	if space.GetSpace().GetSpaceType() != documentv1.SpaceType_SPACE_TYPE_TEAM {
		t.Fatalf("space type = %s", space.GetSpace().GetSpaceType())
	}
	if len(space.GetMembers()) != 2 {
		t.Fatalf("members = %d, want 2", len(space.GetMembers()))
	}

	// The registry lists the subjects the space references.
	subjects, err := session.Service.ListSubjects(session.Ctx, session.Principal, &documentv1.ListSubjectsRequest{SubjectKey: member})
	if err != nil {
		t.Fatalf("ListSubjects: %v", err)
	}
	if len(subjects.GetSubjects()) != 1 || subjects.GetSubjects()[0].GetSubjectKey() != member {
		t.Fatalf("registry read = %+v", subjects.GetSubjects())
	}
	if !subjects.GetSubjects()[0].GetActive() {
		t.Fatal("a registered subject is active by default")
	}
}

// TestIntegrationGroupBindingRules asserts the binding contract: a team target is
// required, one active binding per group and per space, and rebinding is audited
// with the previous and the new target.
func TestIntegrationGroupBindingRules(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	session := testsupport.As(service, ids.WebWriter(ids.Subject("bind-owner")))
	botID := ids.NumericID()
	spaceOne := mustCreateTeamSpace(t, session, "binding one")
	spaceTwo := mustCreateTeamSpace(t, session, "binding two")
	groupID := ids.NumericID()

	binding := mustBindGroup(t, session, botID, groupID, spaceOne)
	if binding.GetChannel() != domain.ChannelQQ || binding.GetBotId() != botID || binding.GetSpaceId() != spaceOne {
		t.Fatalf("binding = %+v", binding)
	}

	// The same group cannot be bound twice to the same space.
	_, err := session.Service.BindGroupSpace(session.Ctx, session.Principal, &documentv1.BindGroupSpaceRequest{
		BotId: botID, ExternalGroupId: groupID, SpaceId: spaceOne, Reason: "duplicate",
	})
	expectCode(t, err, codes.AlreadyExists, "duplicate group binding")

	// Rebinding closes the old row and appends a new one.
	rebound := mustBindGroup(t, session, botID, groupID, spaceTwo)
	if rebound.GetSpaceId() != spaceTwo {
		t.Fatalf("rebound space = %q, want %q", rebound.GetSpaceId(), spaceTwo)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.group_space_bindings WHERE bot_id = $1 AND external_group_id = $2 AND revoked_at IS NULL`,
		botID, groupID); got != 1 {
		t.Fatalf("active bindings after rebind = %d, want 1", got)
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.group_space_bindings
		 WHERE bot_id = $1 AND external_group_id = $2 AND revoked_at IS NOT NULL`, botID, groupID); got != 1 {
		t.Fatal("the closed binding row must be kept for audit")
	}

	// The audit trail carries the transition.
	var previous, next string
	if err := pool.Pgx().QueryRow(context.Background(), `
		SELECT COALESCE(previous_target, ''), COALESCE(new_target, '')
		FROM document_service.space_audit_events
		WHERE subject_type = 'group_space_binding' AND action = 'rebind' AND bot_id = $1 AND external_id = $2`,
		botID, groupID).Scan(&previous, &next); err != nil {
		t.Fatalf("read the rebind audit row: %v", err)
	}
	if previous != "space:"+spaceOne || next != "space:"+spaceTwo {
		t.Fatalf("rebind audit targets = (%q,%q)", previous, next)
	}

	// A space can only be bound to one group per Bot.
	otherGroup := ids.NumericID()
	_, err = session.Service.BindGroupSpace(session.Ctx, session.Principal, &documentv1.BindGroupSpaceRequest{
		BotId: botID, ExternalGroupId: otherGroup, SpaceId: spaceTwo, Reason: "space already bound",
	})
	expectCode(t, err, codes.AlreadyExists, "binding a space that already has a group")

	// A private space can never be a binding target.
	detail := mustCreateDocument(t, session, "private target", "body", false, ids.RequestID("private-target"))
	_, err = session.Service.BindGroupSpace(session.Ctx, session.Principal, &documentv1.BindGroupSpaceRequest{
		BotId: botID, ExternalGroupId: ids.NumericID(), SpaceId: detail.GetSummary().GetOwnerSpaceId(), Reason: "private target",
	})
	expectCode(t, err, codes.FailedPrecondition, "binding a private space")

	// Revoking a binding that is not active is NOT_FOUND.
	if _, err := session.Service.RevokeGroupSpace(session.Ctx, session.Principal, &documentv1.RevokeGroupSpaceRequest{
		BotId: botID, ExternalGroupId: groupID, Reason: "first revoke",
	}); err != nil {
		t.Fatalf("RevokeGroupSpace: %v", err)
	}
	_, err = session.Service.RevokeGroupSpace(session.Ctx, session.Principal, &documentv1.RevokeGroupSpaceRequest{
		BotId: botID, ExternalGroupId: groupID, Reason: "second revoke",
	})
	expectCode(t, err, codes.NotFound, "revoking a group binding twice")
}

// TestIntegrationDocumentSourceTrace asserts the promotion trace: the source is
// stored, returned in the detail and carried in the Outbox event.
func TestIntegrationDocumentSourceTrace(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	botID := ids.NumericID()
	subject := ids.QQSubject(botID, ids.NumericID())
	conversationID := ids.NumericID()
	sourceRecordID := ids.RequestID("source-record")
	session := testsupport.As(service, ids.Principal(serviceauth.CallerPyAgent, subject,
		serviceauth.ScopeDocumentWrite, serviceauth.ScopeDocumentRead, serviceauth.ScopeAccessResolve))
	source := &documentv1.DocumentSource{
		Origin:         "qq",
		BotId:          botID,
		ConversationId: conversationID,
		SourceRecordId: sourceRecordID,
	}

	response, err := session.Service.CreateDocument(session.Ctx, session.Principal, &documentv1.CreateDocumentRequest{
		Title:     "promoted qq file",
		Content:   "content promoted from a QQ file",
		Source:    source,
		RequestId: ids.RequestID("promotion"),
	})
	if err != nil {
		t.Fatalf("CreateDocument with a source: %v", err)
	}
	documentID := response.GetDocument().GetSummary().GetDocumentId()
	if response.GetDocument().GetSource().GetSourceRecordId() != sourceRecordID {
		t.Fatalf("detail source = %+v", response.GetDocument().GetSource())
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_sources
		 WHERE document_id = $1::text::uuid AND origin = 'qq' AND bot_id = $2 AND source_record_id = $3`,
		documentID, botID, sourceRecordID); got != 1 {
		t.Fatal("the source trace row is missing")
	}

	var payload []byte
	if err := pool.Pgx().QueryRow(context.Background(),
		`SELECT payload FROM document_service.document_events WHERE document_id = $1::text::uuid`, documentID).Scan(&payload); err != nil {
		t.Fatalf("read the event payload: %v", err)
	}
	envelope := &documentv1.DocumentEventEnvelope{}
	if err := proto.Unmarshal(payload, envelope); err != nil {
		t.Fatalf("decode the event payload: %v", err)
	}
	if envelope.GetSource().GetOrigin() != "qq" || envelope.GetSource().GetBotId() != botID {
		t.Fatalf("envelope source = %+v", envelope.GetSource())
	}

	// A second promotion of the same source record is refused instead of creating a
	// second document for the same raw record.
	_, err = session.Service.CreateDocument(session.Ctx, session.Principal, &documentv1.CreateDocumentRequest{
		Title:     "promoted qq file again",
		Content:   "content promoted from a QQ file",
		Source:    source,
		RequestId: ids.RequestID("promotion-again"),
	})
	expectCode(t, err, codes.AlreadyExists, "promoting the same source record twice")
}

// TestIntegrationOutboxIsAppendOnly asserts the database level guarantee the
// consumer relies on.
func TestIntegrationOutboxIsAppendOnly(t *testing.T) {
	ids, pool, service := newEnvironment(t)
	session := testsupport.As(service, ids.WebWriter(ids.Subject("append-only")))
	detail := mustCreateDocument(t, session, "append only", "body", false, ids.RequestID("append-only"))
	documentID := detail.GetSummary().GetDocumentId()

	ctx := context.Background()
	if _, err := pool.Pgx().Exec(ctx,
		`UPDATE document_service.document_events SET event_kind = 'delete' WHERE document_id = $1::text::uuid`, documentID); err == nil {
		t.Fatal("the Outbox must reject updates")
	}
	if _, err := pool.Pgx().Exec(ctx,
		`DELETE FROM document_service.document_events WHERE document_id = $1::text::uuid`, documentID); err == nil {
		t.Fatal("the Outbox must reject deletes")
	}
	if got := testsupport.Count(t, pool,
		`SELECT count(*) FROM document_service.document_events WHERE document_id = $1::text::uuid`, documentID); got != 1 {
		t.Fatalf("Outbox rows = %d, want 1", got)
	}
}
