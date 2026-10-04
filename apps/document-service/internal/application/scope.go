package application

import (
	"context"
	"fmt"
	"time"

	"document-service/internal/domain"

	documentv1 "packages/gen/document/v1"
	"packages/serviceauth"
)

// resolveScopeForPrincipal is the single resource scope resolution path. Both
// ResolveAccessScope and IssueSearchCapability go through it, so a capability can
// never be minted from a decision that differs from the one the caller was told.
//
// The subject always comes from the verified assertion. The request body's
// subject_key is deliberately ignored: a body field must never be able to name a
// different principal.
func (service *Service) resolveScopeForPrincipal(ctx context.Context, principal *serviceauth.Principal, conversationMessage *documentv1.ConversationContext) (domain.Resolution, error) {
	caller, err := service.verifyCaller(principal, "")
	if err != nil {
		return domain.Resolution{}, err
	}
	conversation, err := resolveConversation(caller.Conversation, conversationMessage)
	if err != nil {
		return domain.Resolution{}, err
	}
	if err := conversation.Validate(); err != nil {
		return domain.Resolution{}, err
	}

	// A QQ group identity only exists inside the Bot that saw it. The assertion is
	// the trusted source; a QQ subject key carries the same namespace, so it is the
	// fallback rather than a second opinion.
	botID := caller.BotID
	if botID == "" {
		botID = caller.Subject.BotID
	}

	facts, err := service.store.ResolveScopeFacts(ctx, caller.Subject.Key, botID, caller.Channel, conversation.GroupID())
	if err != nil {
		return domain.Resolution{}, err
	}
	resolution, err := domain.ResolveResourceScope(caller.Subject.Key, conversation, botID, facts)
	if err != nil {
		return domain.Resolution{}, err
	}
	if err := resolution.Validate(); err != nil {
		return domain.Resolution{}, err
	}
	return resolution, nil
}

// resolveConversation merges the session context of the assertion with the one in
// the request body.
//
// The assertion is trusted material, so it wins. When both are present they must
// agree: preferring one silently would let a caller describe a different session
// than the one it proved, which is exactly how a group session could be turned
// into a private one.
func resolveConversation(asserted *serviceauth.Conversation, message *documentv1.ConversationContext) (domain.Conversation, error) {
	fromAssertion, hasAssertion, err := conversationFromAssertion(asserted)
	if err != nil {
		return domain.Conversation{}, err
	}
	fromRequest, requestErr := conversationFromRequest(message)
	switch {
	case hasAssertion && requestErr == nil:
		if fromAssertion.Kind != fromRequest.Kind || fromAssertion.GroupID() != fromRequest.GroupID() {
			return domain.Conversation{}, fmt.Errorf(
				"%w: the asserted conversation context and the request context disagree", domain.ErrInvalidInput)
		}
		return fromAssertion, nil
	case hasAssertion:
		return fromAssertion, nil
	case requestErr == nil:
		return fromRequest, nil
	default:
		return domain.Conversation{}, requestErr
	}
}

// resolveAccessScope implements the ResolveAccessScope RPC.
func (service *Service) resolveAccessScope(ctx context.Context, principal *serviceauth.Principal, request *documentv1.ResolveAccessScopeRequest) (*documentv1.ResolveAccessScopeResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: a resolve request is required", domain.ErrInvalidInput)
	}
	resolution, err := service.resolveScopeForPrincipal(ctx, principal, request.GetConversation())
	if err != nil {
		return nil, err
	}

	response := &documentv1.ResolveAccessScopeResponse{}
	if !resolution.Granted {
		// A denial carries the reason and nothing else. It is never downgraded into
		// an empty granted envelope: an empty range in the document contract means
		// "authenticated_public only", so that would hand over the public corpus.
		response.Decision = documentv1.Decision_DECISION_DENIED
		response.DeniedReason = deniedReasonProto(resolution.Reason)
		return response, nil
	}
	response.Decision = documentv1.Decision_DECISION_GRANTED
	response.PrivateSpaceIds = resolution.Private
	response.CurrentTeamSpaceId = resolution.CurrentTeam
	response.OtherTeamSpaceIds = resolution.OtherTeams
	response.MemberSpaceIds = resolution.MemberSpaceIDs
	return response, nil
}

// issueSearchCapability implements the IssueSearchCapability RPC.
func (service *Service) issueSearchCapability(ctx context.Context, principal *serviceauth.Principal, request *documentv1.IssueSearchCapabilityRequest) (*documentv1.IssueSearchCapabilityResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: a capability request is required", domain.ErrInvalidInput)
	}
	resolution, err := service.resolveScopeForPrincipal(ctx, principal, request.GetConversation())
	if err != nil {
		return nil, err
	}

	response := &documentv1.IssueSearchCapabilityResponse{}
	if !resolution.Granted {
		response.Decision = documentv1.Decision_DECISION_DENIED
		response.DeniedReason = deniedReasonProto(resolution.Reason)
		// The capability stays empty. A denied resolution must never be signed, and
		// the caller must not be able to mistake it for a valid empty grant.
		return response, nil
	}

	capability, err := service.MintSearchCapability(resolution, SearchCapabilityRequest{
		RequestedSpaceIDs:    request.GetRequestedSpaceIds(),
		RequestedDocumentIDs: request.GetRequestedDocumentIds(),
	})
	if err != nil {
		return nil, err
	}

	// The response repeats the range the capability actually carries, which is the
	// resolved envelope unless the caller narrowed it.
	response.Decision = documentv1.Decision_DECISION_GRANTED
	response.Capability = capability.Token
	response.ExpiresAt = formatTime(time.Unix(capability.Claims.ExpiresAt, 0).UTC())
	response.PrivateSpaceIds = capability.Claims.PrivateSpaceIDs
	response.CurrentTeamSpaceId = capability.Claims.CurrentTeamSpaceID
	response.OtherTeamSpaceIds = capability.Claims.OtherTeamSpaceIDs
	response.MemberSpaceIds = capability.Claims.AllowedSpaceIDs
	response.AllowedDocumentIds = capability.Claims.AllowedDocumentIDs
	response.AuthenticatedPublic = capability.Claims.AuthenticatedPublic
	return response, nil
}

// SearchCapability is one minted resource range credential together with the
// resolution it was minted from.
type SearchCapability struct {
	Token      string
	Claims     serviceauth.CapabilityClaims
	Resolution domain.Resolution
}

// SearchCapabilityRequest is the optional narrowing a caller proposes. Both sets
// are empty in the common case, which asks for the whole granted envelope.
type SearchCapabilityRequest struct {
	RequestedSpaceIDs    []string
	RequestedDocumentIDs []string
}

// MintSearchCapability signs a document-search capability from a granted
// resolution.
//
// A denied resolution is refused here as well as at the RPC boundary: minting is
// the last place a denial could be turned into a usable credential, so the check
// lives in the function that signs, not only in its caller.
//
// The requested sets may only narrow. Any identifier outside the granted envelope
// rejects the whole request - nothing is trimmed silently, because trimming would
// let a caller probe for identifiers it was not granted.
func (service *Service) MintSearchCapability(resolution domain.Resolution, request SearchCapabilityRequest) (*SearchCapability, error) {
	if !resolution.Granted {
		return nil, fmt.Errorf("%w: a denied resolution cannot be signed into a capability", domain.ErrForbidden)
	}
	if err := resolution.Validate(); err != nil {
		return nil, err
	}

	allowedSpaceIDs := resolution.Envelope()
	if requested := serviceauth.CanonicalIDs(request.RequestedSpaceIDs); len(requested) > 0 {
		if !serviceauth.ContainsAll(allowedSpaceIDs, requested) {
			return nil, fmt.Errorf("%w: the requested space range is not a subset of the granted envelope", domain.ErrForbidden)
		}
		allowedSpaceIDs = requested
	}

	allowedDocumentIDs := serviceauth.CanonicalIDs(resolution.DocumentIDs)
	if requested := serviceauth.CanonicalIDs(request.RequestedDocumentIDs); len(requested) > 0 {
		if !serviceauth.ContainsAll(allowedDocumentIDs, requested) {
			return nil, fmt.Errorf("%w: the requested document range is not a subset of the granted range", domain.ErrForbidden)
		}
		allowedDocumentIDs = requested
	}

	currentTeam := ""
	if resolution.CurrentTeam != "" && containsID(allowedSpaceIDs, resolution.CurrentTeam) {
		currentTeam = resolution.CurrentTeam
	}

	now := service.now().UTC()
	claims := serviceauth.CapabilityClaims{
		Issuer:              serviceauth.CallerDocumentService,
		Audience:            serviceauth.AudienceDocumentSearch,
		Scopes:              []string{string(serviceauth.ScopeDocumentSearcher)},
		SubjectKey:          resolution.SubjectKey,
		PrivateSpaceIDs:     keepIDs(resolution.Private, allowedSpaceIDs),
		CurrentTeamSpaceID:  currentTeam,
		OtherTeamSpaceIDs:   keepIDs(resolution.OtherTeams, allowedSpaceIDs),
		AllowedSpaceIDs:     allowedSpaceIDs,
		AllowedDocumentIDs:  allowedDocumentIDs,
		AuthenticatedPublic: true,
		IssuedAt:            now.Unix(),
		ExpiresAt:           now.Add(service.capTTL).Unix(),
	}
	token, err := service.codec.SealCapability(claims)
	if err != nil {
		return nil, fmt.Errorf("document-service: sign search capability: %w", err)
	}
	// Re-read the canonical claims the codec sealed, so the caller sees exactly
	// what a verifier will open.
	sealed, err := service.codec.OpenCapability(token, serviceauth.AudienceDocumentSearch)
	if err != nil {
		return nil, fmt.Errorf("document-service: verify minted capability: %w", err)
	}
	return &SearchCapability{Token: token, Claims: sealed.Claims, Resolution: resolution}, nil
}

// keepIDs narrows a labelled family to the identifiers the capability carries.
// Filtering a family by the allowed set keeps the three labels disjoint: an
// identifier that was dropped from the envelope cannot reappear under a label.
func keepIDs(values, allowed []string) []string {
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if containsID(allowed, value) {
			kept = append(kept, value)
		}
	}
	return serviceauth.CanonicalIDs(kept)
}

func containsID(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
