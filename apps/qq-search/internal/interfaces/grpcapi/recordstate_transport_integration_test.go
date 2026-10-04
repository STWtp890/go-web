package grpcapi

// End-to-end transport test for the scoped record-state lookup: a real gRPC
// server, the boundary interceptors, a real client, capabilities minted with
// packages/serviceauth, and the real database.
//
// Its point is that the capability really is what decides the answer. The same
// record id is reported as existent under the capability that grants its
// conversation and as nonexistent under every other capability, including an
// empty one - a lookup that ignored the capability would answer the same way in
// all of them, and a lookup that treated an empty grant as "everything" would
// answer `exists=true` to the last one.

import (
	"context"
	"testing"
	"time"

	"qq-search/internal/ingress/pyagent"

	qqsearchv1 "packages/gen/qqsearch/v1"
	"packages/serviceauth"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// transportScopeFor builds the channel scope py-agent grants for one bot.
func transportScopeFor(botID string, groupIDs ...string) *qqsearchv1.QQChannelScope {
	conversations := make([]string, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		conversations = append(conversations, "qq:"+botID+":group:"+groupID)
	}
	return &qqsearchv1.QQChannelScope{
		BotIds:           []string{botID},
		ConversationIds:  conversations,
		ExternalGroupIds: groupIDs,
	}
}

func TestIntegrationTransportRecordStateUsesTheChannelCapability(t *testing.T) {
	client, codec, prefix := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	token := transportToken()
	inScopeID := prefix + "-state-in"
	otherGroupRecordID := prefix + "-state-out"
	const (
		botID      = "10001"
		groupID    = "999"
		otherGroup = "1000"
	)

	writer := authorized(ctx, assertion(t, codec, serviceauth.ScopeQQIndexWriter))
	indexMessage := func(recordID, targetGroup string) {
		t.Helper()
		inbound := pyagent.InboundEvent{
			BotID:     botID,
			UserID:    "20003",
			MessageID: recordID,
			Text:      "state probe " + token,
			GroupID:   targetGroup,
		}
		request, err := pyagent.RequestFromEnvelope(inbound.MessageEnvelope(1, 1, time.Now().UTC()))
		if err != nil {
			t.Fatalf("convert the py-agent event for %s: %v", recordID, err)
		}
		if _, err := client.IndexQQSourceEvent(writer, request); err != nil {
			t.Fatalf("index %s: %v", recordID, err)
		}
	}
	indexMessage(inScopeID, groupID)
	indexMessage(otherGroupRecordID, otherGroup)

	searcher := authorized(ctx, assertion(t, codec, serviceauth.ScopeQQSearcher))

	// The capability granted for this group only: the in-scope record is
	// reported and the other group's record is not.
	grantedContext := withCapability(searcher, capability(t, codec, serviceauth.AudienceQQSearch,
		[]string{string(serviceauth.ScopeQQSearcher)}, transportScopeFor(botID, groupID)))

	inScope, err := client.GetQQRecordState(grantedContext, &qqsearchv1.GetQQRecordStateRequest{
		Kind:     qqsearchv1.QQRecordKind_QQ_RECORD_KIND_MESSAGE,
		RecordId: inScopeID,
	})
	if err != nil {
		t.Fatalf("GetQQRecordState for the granted conversation: %v", err)
	}
	if !inScope.GetExists() || inScope.GetState().GetStatus() != qqsearchv1.QQRecordStatus_QQ_RECORD_STATUS_INDEXED {
		t.Fatalf("in-scope record = %+v, want exists with INDEXED", inScope)
	}
	if got := inScope.GetState().GetBotId(); got != botID {
		t.Fatalf("in-scope record bot = %q, want %q", got, botID)
	}

	hidden, err := client.GetQQRecordState(grantedContext, &qqsearchv1.GetQQRecordStateRequest{
		Kind:     qqsearchv1.QQRecordKind_QQ_RECORD_KIND_MESSAGE,
		RecordId: otherGroupRecordID,
	})
	if err != nil {
		t.Fatalf("GetQQRecordState for a record outside the grant: %v", err)
	}
	if hidden.GetExists() || hidden.GetState() != nil {
		t.Fatalf("a record outside the granted scope was reported: %+v", hidden)
	}

	// The same record is visible under the capability that grants its own
	// conversation, so the answer above came from the scope and not from a
	// global filter.
	otherGrantedContext := withCapability(searcher, capability(t, codec, serviceauth.AudienceQQSearch,
		[]string{string(serviceauth.ScopeQQSearcher)}, transportScopeFor(botID, otherGroup)))
	visibleElsewhere, err := client.GetQQRecordState(otherGrantedContext, &qqsearchv1.GetQQRecordStateRequest{
		Kind:     qqsearchv1.QQRecordKind_QQ_RECORD_KIND_MESSAGE,
		RecordId: otherGroupRecordID,
	})
	if err != nil {
		t.Fatalf("GetQQRecordState under the other group's capability: %v", err)
	}
	if !visibleElsewhere.GetExists() {
		t.Fatalf("the record is inside its own grant but was reported as missing: %+v", visibleElsewhere)
	}
	// And the in-scope record is hidden from that capability.
	hiddenReverse, err := client.GetQQRecordState(otherGrantedContext, &qqsearchv1.GetQQRecordStateRequest{
		Kind:     qqsearchv1.QQRecordKind_QQ_RECORD_KIND_MESSAGE,
		RecordId: inScopeID,
	})
	if err != nil {
		t.Fatalf("GetQQRecordState for the in-scope record under the other capability: %v", err)
	}
	if hiddenReverse.GetExists() {
		t.Fatalf("a record outside its grant was reported: %+v", hiddenReverse)
	}

	// A request naming a conversation outside the grant is refused as a whole.
	if _, err := client.GetQQRecordState(grantedContext, &qqsearchv1.GetQQRecordStateRequest{
		Kind:     qqsearchv1.QQRecordKind_QQ_RECORD_KIND_MESSAGE,
		RecordId: otherGroupRecordID,
		Scope:    transportScopeFor(botID, otherGroup),
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a request outside the granted scope: got %v, want PERMISSION_DENIED", err)
	}

	// An empty grant is "no conversations": the lookup finds nothing, even for a
	// record that exists and is granted elsewhere.
	emptyContext := withCapability(searcher, capability(t, codec, serviceauth.AudienceQQSearch,
		[]string{string(serviceauth.ScopeQQSearcher)}, &qqsearchv1.QQChannelScope{}))
	empty, err := client.GetQQRecordState(emptyContext, &qqsearchv1.GetQQRecordStateRequest{
		Kind:     qqsearchv1.QQRecordKind_QQ_RECORD_KIND_MESSAGE,
		RecordId: inScopeID,
	})
	if err != nil {
		t.Fatalf("GetQQRecordState with an empty grant: %v", err)
	}
	if empty.GetExists() || empty.GetState() != nil {
		t.Fatalf("an empty grant reported a record: %+v", empty)
	}

	// No capability at all is unauthenticated, not "no scope".
	if _, err := client.GetQQRecordState(searcher, &qqsearchv1.GetQQRecordStateRequest{
		Kind:     qqsearchv1.QQRecordKind_QQ_RECORD_KIND_MESSAGE,
		RecordId: inScopeID,
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("a lookup without a capability: got %v, want UNAUTHENTICATED", err)
	}

	// A recall inside the granted scope stays reportable.
	conversation := pyagent.Conversation{BotID: botID, ExternalGroupID: groupID}
	recall, err := pyagent.RequestFromEnvelope(pyagent.RecallEnvelope(
		conversation, inScopeID, true, "20003", time.Now().UTC(), 2, 2))
	if err != nil {
		t.Fatalf("convert the recall event: %v", err)
	}
	if _, err := client.IndexQQSourceEvent(writer, recall); err != nil {
		t.Fatalf("index the recall: %v", err)
	}
	recalled, err := client.GetQQRecordState(grantedContext, &qqsearchv1.GetQQRecordStateRequest{
		Kind:     qqsearchv1.QQRecordKind_QQ_RECORD_KIND_MESSAGE,
		RecordId: inScopeID,
	})
	if err != nil {
		t.Fatalf("GetQQRecordState after the recall: %v", err)
	}
	if !recalled.GetExists() || recalled.GetState().GetStatus() != qqsearchv1.QQRecordStatus_QQ_RECORD_STATUS_RECALLED {
		t.Fatalf("recalled record inside the grant = %+v, want exists with RECALLED", recalled)
	}
}
