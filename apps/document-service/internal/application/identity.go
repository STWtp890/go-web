package application

import (
	"context"
	"strings"

	"document-service/internal/domain"

	"packages/serviceauth"

	"github.com/jackc/pgx/v5"
)

// callerIdentity is the verified identity material of one request, resolved once
// per command.
//
// Nothing here comes from the request body except the optional request id, which
// is a deduplication hint and never an authorization input. The subject only
// comes from the assertion the boundary already validated, so a caller cannot
// act for a subject outside its own namespace.
type callerIdentity struct {
	Subject   domain.SubjectIdentity
	Caller    serviceauth.Caller
	Actor     string
	Source    string
	RequestID string
	// Conversation is the session context carried by the assertion, when it
	// carried one. It is trusted material, so it wins over the request body.
	Conversation *serviceauth.Conversation
	BotID        string
	Channel      string
}

// registerAuditReason is the audit reason recorded when a subject is first seen.
const registerAuditReason = "subject first seen by a trusted entry point"

// verifyCaller turns the authenticated principal into the identity and audit
// fields one command needs.
func (service *Service) verifyCaller(principal *serviceauth.Principal, bodyRequestID string) (callerIdentity, error) {
	if principal == nil {
		return callerIdentity{}, errUnauthenticated
	}
	subjectKey, err := principal.RequireSubject()
	if err != nil {
		return callerIdentity{}, err
	}
	subject, err := domain.ParseSubjectKey(subjectKey)
	if err != nil {
		return callerIdentity{}, err
	}

	// The audit actor is the operator when the entry point named one, and the
	// calling service otherwise: an audit row always identifies somebody.
	actor := strings.TrimSpace(principal.Actor)
	if actor == "" {
		actor = string(principal.Caller)
	}

	// request_id: the assertion is trusted material, so it wins. The request body
	// field is only consulted when the assertion carried none - and it is only a
	// deduplication hint in either case.
	requestID, err := domain.ValidateRequestID(principal.RequestID)
	if err != nil {
		return callerIdentity{}, err
	}
	if requestID == "" {
		if requestID, err = domain.ValidateRequestID(bodyRequestID); err != nil {
			return callerIdentity{}, err
		}
	}

	channel := strings.TrimSpace(principal.Channel)
	if channel == "" {
		channel = domain.ChannelQQ
	}

	return callerIdentity{
		Subject:      subject,
		Caller:       principal.Caller,
		Actor:        actor,
		Source:       domain.NormalizeSource(string(principal.Caller)),
		RequestID:    requestID,
		Conversation: principal.Conversation,
		BotID:        strings.TrimSpace(principal.BotID),
		Channel:      channel,
	}, nil
}

// ensureSubject registers a subject the first time a trusted entry point declares
// it and appends the matching audit row in the caller's transaction.
//
// Registration grants nothing: it records that the subject exists so a later
// membership or grant can reference it. Registering and auditing in the same
// transaction is what keeps a failed command from leaving a subject behind.
func (service *Service) ensureSubject(ctx context.Context, tx pgx.Tx, identity domain.SubjectIdentity, caller callerIdentity) error {
	created, err := service.store.RegisterSubject(ctx, tx, domain.Subject{
		SubjectKey:  identity.Key,
		SubjectType: domain.SubjectTypeOf(identity),
		Origin:      identity.Origin,
		DisplayName: strings.TrimSpace(caller.Actor),
	})
	if err != nil {
		return err
	}
	if !created {
		return nil
	}
	return service.appendAudit(ctx, tx, domain.AuditEvent{
		SubjectType: domain.AuditSubjectTypeAccessSubject,
		Action:      domain.AuditActionRegister,
		SubjectKey:  identity.Key,
		Actor:       caller.Actor,
		Source:      caller.Source,
		Reason:      registerAuditReason,
		RequestID:   caller.RequestID,
	})
}

// appendAudit writes one audit row in the caller's transaction, filling the
// server generated fields.
func (service *Service) appendAudit(ctx context.Context, tx pgx.Tx, event domain.AuditEvent) error {
	if event.EventID == "" {
		event.EventID = service.newID()
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = service.now().UTC()
	}
	return service.store.AppendAudit(ctx, tx, event)
}
