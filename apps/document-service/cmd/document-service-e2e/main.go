// Command document-service-e2e is the cross-service acceptance client.
//
// It exercises the ADR-017 chains end to end against running processes:
//
//	document-service  <- go-web business command + detail read
//	document-service  --Outbox--> document-search (consumed by its own consumer)
//	document-service  --resource capability--> document-search query
//
// It deliberately depends only on the versioned contracts and the shared
// credential package, so it can cross both service boundaries without importing
// another application's internal packages.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	documentv1 "packages/gen/document/v1"
	documentsearchv1 "packages/gen/documentsearch/v1"
	qqsearchv1 "packages/gen/qqsearch/v1"
	"packages/serviceauth"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "document-service-e2e:", err)
		os.Exit(1)
	}
}

func run() error {
	documentServiceAddress := flag.String("document-service", "127.0.0.1:18081", "document service gRPC address")
	documentSearchAddress := flag.String("document-search", "127.0.0.1:18082", "document search gRPC address")
	qqSearchAddress := flag.String("qq-search", "127.0.0.1:18083", "qq search gRPC address; empty skips the QQ chain")
	boundaryKeyPath := flag.String("capability-key-file", os.Getenv("BOUNDARY_KEY_FILE"), "shared boundary key file")
	keyInline := flag.String("capability-key", "", "shared boundary key inline; prefer -capability-key-file")
	webUserID := flag.Int64("web-user-id", 0, "Web user id to act for; 0 uses a random id per run")
	waitTimeout := flag.Duration("wait", 30*time.Second, "how long to wait for the index to catch up")
	flag.Parse()

	if *webUserID == 0 {
		*webUserID = time.Now().UnixNano() % 1_000_000_000
		if *webUserID < 0 {
			*webUserID = -*webUserID
		}
	}

	key, err := loadKey(*boundaryKeyPath, *keyInline)
	if err != nil {
		return err
	}
	codec, err := serviceauth.NewCodec(key)
	if err != nil {
		return err
	}
	subjectKey, err := documentserviceWebSubject(*webUserID)
	if err != nil {
		return err
	}

	documentConn, err := grpc.NewClient(*documentServiceAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial document service: %w", err)
	}
	defer func() { _ = documentConn.Close() }()
	searchConn, err := grpc.NewClient(*documentSearchAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial document search: %w", err)
	}
	defer func() { _ = searchConn.Close() }()

	documents := documentv1.NewDocumentServiceClient(documentConn)
	searches := documentsearchv1.NewDocumentSearchServiceClient(searchConn)

	ctx, cancel := context.WithTimeout(context.Background(), *waitTimeout+time.Minute)
	defer cancel()

	// 1. go-web creates a formal document through the document service.
	marker := fmt.Sprintf("e2e-marker-%d", time.Now().UnixNano())
	createCtx, err := assertionContext(ctx, codec, serviceauth.CallerGoWeb, serviceauth.AudienceDocumentService, subjectKey,
		[]string{string(serviceauth.ScopeDocumentWrite)})
	if err != nil {
		return err
	}
	created, err := documents.CreateDocument(createCtx, &documentv1.CreateDocumentRequest{
		Title:         "E2E " + marker,
		Content:       "cross service acceptance body " + marker,
		ContentFormat: "markdown",
		Source:        &documentv1.DocumentSource{Origin: "web"},
	})
	if err != nil {
		return fmt.Errorf("create document: %w", err)
	}
	documentID := created.GetDocument().GetSummary().GetDocumentId()
	if documentID == "" {
		return errors.New("create document returned no document id")
	}
	step("created document %s as %s", documentID, subjectKey)

	// 2. The Web surface reads the detail back through the document service.
	readCtx, err := assertionContext(ctx, codec, serviceauth.CallerGoWeb, serviceauth.AudienceDocumentService, subjectKey,
		[]string{string(serviceauth.ScopeDocumentRead)})
	if err != nil {
		return err
	}
	detail, err := documents.GetDocument(readCtx, &documentv1.GetDocumentRequest{DocumentId: documentID})
	if err != nil {
		return fmt.Errorf("get document detail: %w", err)
	}
	if !strings.Contains(detail.GetDocument().GetVersion().GetContent(), marker) {
		return fmt.Errorf("detail read returned the wrong content: %q", detail.GetDocument().GetVersion().GetContent())
	}
	step("detail read matches the written content")

	// 3. The fact source mints the resource capability for the search boundary.
	capabilityCtx, err := assertionContext(ctx, codec, serviceauth.CallerGoWeb, serviceauth.AudienceDocumentService, subjectKey,
		[]string{string(serviceauth.ScopeAccessResolve)})
	if err != nil {
		return err
	}
	issued, err := documents.IssueSearchCapability(capabilityCtx, &documentv1.IssueSearchCapabilityRequest{
		SubjectKey:   subjectKey,
		Conversation: &documentv1.ConversationContext{Kind: documentv1.ConversationKind_CONVERSATION_KIND_PRIVATE},
	})
	if err != nil {
		return fmt.Errorf("issue search capability: %w", err)
	}
	if issued.GetDecision() != documentv1.Decision_DECISION_GRANTED {
		return fmt.Errorf("resource scope was denied: %s", issued.GetDeniedReason())
	}
	if issued.GetCapability() == "" {
		return errors.New("a granted resolution must mint a capability")
	}
	step("issued a %s capability with %d member spaces", serviceauth.AudienceDocumentSearch, len(issued.GetMemberSpaceIds()))

	// 4. document-search consumes the Outbox event; wait for the index to catch up.
	searchCtx, err := assertionContext(ctx, codec, serviceauth.CallerGoWeb, serviceauth.AudienceDocumentSearch, subjectKey,
		[]string{string(serviceauth.ScopeDocumentSearcher)})
	if err != nil {
		return err
	}
	searchCtx = metadata.AppendToOutgoingContext(searchCtx,
		serviceauth.HeaderCapability, serviceauth.AuthorizationHeader(issued.GetCapability()),
	)
	var response *documentsearchv1.SearchDocumentsResponse
	deadline := time.Now().Add(*waitTimeout)
	for {
		response, err = searches.SearchDocuments(searchCtx, &documentsearchv1.SearchDocumentsRequest{
			Query: marker, PageSize: 10,
		})
		if err != nil {
			return fmt.Errorf("search documents: %w", err)
		}
		if containsDocument(response, documentID) {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("document %s never appeared in the index within %s", documentID, *waitTimeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
	step("document-search returned %d hit(s) for the marker", len(response.GetHits()))

	// 5. A request outside the granted range is rejected as a whole.
	//
	// searchCtx already carries the capability. Re-appending the header would
	// produce two values, which the boundary rejects as a malformed credential
	// before the range check ever runs - a failure that would look like a range
	// bug while actually being a client bug.
	_, err = searches.SearchDocuments(searchCtx, &documentsearchv1.SearchDocumentsRequest{
		Query:           marker,
		AllowedSpaceIds: []string{"11111111-1111-1111-1111-111111111111"},
		PageSize:        10,
	})
	if status.Code(err) != codes.PermissionDenied {
		return fmt.Errorf("an out-of-grant space must be rejected with PermissionDenied, got %v", err)
	}
	step("an out-of-grant request was rejected as a whole")

	// 6. A capability minted for another audience cannot be replayed here.
	wrongAudience, err := codec.SealCapability(serviceauth.CapabilityClaims{
		Issuer:   serviceauth.CallerPyAgent,
		Audience: serviceauth.AudienceQQSearch,
		Scopes:   []string{string(serviceauth.ScopeQQSearcher)},
		IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		return err
	}
	replayCtx, err := assertionContext(ctx, codec, serviceauth.CallerGoWeb, serviceauth.AudienceDocumentSearch, subjectKey,
		[]string{string(serviceauth.ScopeDocumentSearcher)})
	if err != nil {
		return err
	}
	replayCtx = metadata.AppendToOutgoingContext(replayCtx,
		serviceauth.HeaderCapability, serviceauth.AuthorizationHeader(wrongAudience),
	)
	_, err = searches.SearchDocuments(replayCtx, &documentsearchv1.SearchDocumentsRequest{Query: marker, PageSize: 10})
	if status.Code(err) != codes.Unauthenticated {
		return fmt.Errorf("a capability for another audience must be Unauthenticated, got %v", err)
	}
	step("a capability minted for another audience was rejected")

	// 7. A service assertion without the required scope cannot write the index.
	noScopeCtx, err := assertionContext(ctx, codec, serviceauth.CallerDocumentService, serviceauth.AudienceDocumentSearch, "",
		[]string{string(serviceauth.ScopeDocumentSearcher)})
	if err != nil {
		return err
	}
	_, err = searches.IndexDocumentEvent(noScopeCtx, &documentsearchv1.IndexDocumentEventRequest{
		EventId: "00000000-0000-0000-0000-0000000000ff", Kind: "delete", DocumentId: documentID,
	})
	if status.Code(err) != codes.PermissionDenied {
		return fmt.Errorf("indexing without document-index-writer must be PermissionDenied, got %v", err)
	}
	step("indexing without the writer scope was rejected")

	// 8. The index status is an operational read and needs the writer identity,
	// not the searcher one: the two roles are separated so a compromised searcher
	// cannot inspect or rewrite the index it reads from.
	statusCtx, err := assertionContext(ctx, codec, serviceauth.CallerDocumentService, serviceauth.AudienceDocumentSearch, "",
		[]string{string(serviceauth.ScopeDocumentIndexWriter)})
	if err != nil {
		return err
	}
	status, err := searches.GetIndexStatus(statusCtx, &documentsearchv1.GetIndexStatusRequest{})
	if err != nil {
		return fmt.Errorf("get index status: %w", err)
	}
	if status.GetIndexedDocuments() <= 0 {
		return fmt.Errorf("the index reports no documents after a successful chain: %+v", status)
	}
	step("index status: alias=%s generation=%s indexed=%d", status.GetCollectionAlias(), status.GetGeneration(), status.GetIndexedDocuments())

	slog.Info("DOCUMENT_CHAIN_OK", "document_id", documentID, "subject", subjectKey)
	fmt.Println("DOCUMENT_CHAIN_OK")

	if strings.TrimSpace(*qqSearchAddress) != "" {
		if err := runQQChain(ctx, codec, *qqSearchAddress); err != nil {
			return err
		}
	}
	return nil
}

// runQQChain exercises the QQ raw content chain: py-agent pushes qqsource events
// to qq-search, then queries with a channel capability it issued itself. The QQ
// chain deliberately never touches document-service for resource permission:
// channel facts belong to py-agent, resource facts to document-service.
func runQQChain(ctx context.Context, codec *serviceauth.Codec, address string) error {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial qq search: %w", err)
	}
	defer func() { _ = conn.Close() }()
	qq := qqsearchv1.NewQQSearchServiceClient(conn)

	marker := fmt.Sprintf("qq-marker-%d", time.Now().UnixNano())
	botID := "10001"
	groupID := "999"
	conversationID := fmt.Sprintf("qq:%s:group:%s", botID, groupID)
	messageRecordID := fmt.Sprintf("msg-%d", time.Now().UnixNano())
	fileRecordID := fmt.Sprintf("file-%d", time.Now().UnixNano())
	eventSequence := time.Now().UnixNano()

	indexCtx, err := assertionContext(ctx, codec, serviceauth.CallerPyAgent, serviceauth.AudienceQQSearch, "",
		[]string{string(serviceauth.ScopeQQIndexWriter)})
	if err != nil {
		return err
	}
	messageEvent := &qqsearchv1.IndexQQSourceEventRequest{
		EventId:              fmt.Sprintf("e2e-message-%d", time.Now().UnixNano()),
		Sequence:             eventSequence,
		RecordRevision:       1,
		Kind:                 "message_upsert",
		Channel:              "qq",
		BotId:                botID,
		ConversationKind:     "group",
		ExternalGroupId:      groupID,
		ConversationId:       conversationID,
		RecordId:             messageRecordID,
		MessageText:          "原始聊天内容 " + marker,
		SenderExternalUserId: "20002",
		SentAt:               time.Now().UTC().Format(time.RFC3339Nano),
	}
	if _, err := qq.IndexQQSourceEvent(indexCtx, messageEvent); err != nil {
		return fmt.Errorf("index qq message: %w", err)
	}
	fileEvent := &qqsearchv1.IndexQQSourceEventRequest{
		EventId:                fmt.Sprintf("e2e-file-%d", time.Now().UnixNano()),
		Sequence:               eventSequence + 1,
		RecordRevision:         1,
		Kind:                   "file_upsert",
		Channel:                "qq",
		BotId:                  botID,
		ConversationKind:       "group",
		ExternalGroupId:        groupID,
		ConversationId:         conversationID,
		RecordId:               fileRecordID,
		FileName:               "原始文件 " + marker + ".txt",
		MimeType:               "text/plain",
		SizeBytes:              1024,
		UploaderExternalUserId: "20002",
		UploadedAt:             time.Now().UTC().Format(time.RFC3339Nano),
	}
	if _, err := qq.IndexQQSourceEvent(indexCtx, fileEvent); err != nil {
		return fmt.Errorf("index qq file: %w", err)
	}
	step("indexed one raw QQ message and one raw QQ file")

	// py-agent issues the channel scope capability for the querying caller.
	channelCapability, err := codec.SealCapability(serviceauth.CapabilityClaims{
		Issuer:           serviceauth.CallerPyAgent,
		Audience:         serviceauth.AudienceQQSearch,
		Scopes:           []string{string(serviceauth.ScopeQQSearcher)},
		SubjectKey:       "qq:user:" + botID + "/20002",
		BotIDs:           []string{botID},
		ConversationIDs:  []string{conversationID},
		ExternalGroupIDs: []string{groupID},
		Channel:          "qq",
		IssuedAt:         time.Now().Unix(),
		ExpiresAt:        time.Now().Add(2 * time.Minute).Unix(),
	})
	if err != nil {
		return err
	}
	queryCtx, err := assertionContext(ctx, codec, serviceauth.CallerPyAgent, serviceauth.AudienceQQSearch, "",
		[]string{string(serviceauth.ScopeQQSearcher)})
	if err != nil {
		return err
	}
	queryCtx = metadata.AppendToOutgoingContext(queryCtx,
		serviceauth.HeaderCapability, serviceauth.AuthorizationHeader(channelCapability),
	)
	scope := &qqsearchv1.QQChannelScope{
		BotIds: []string{botID}, ConversationIds: []string{conversationID}, ExternalGroupIds: []string{groupID},
	}

	// The raw file must never be returned by a message query, and the other way
	// round: separate models, separate tables, separate collections.
	messages, err := qq.SearchQQMessages(queryCtx, &qqsearchv1.SearchQQMessagesRequest{
		Query: marker, Scope: scope, TopK: 10,
	})
	if err != nil {
		return fmt.Errorf("search qq messages: %w", err)
	}
	if len(messages.GetHits()) == 0 || messages.GetHits()[0].GetRecordId() != messageRecordID {
		return fmt.Errorf("qq message search did not return the indexed message: %+v", messages.GetHits())
	}
	files, err := qq.SearchQQFiles(queryCtx, &qqsearchv1.SearchQQFilesRequest{
		Query: marker, Scope: scope, TopK: 10,
	})
	if err != nil {
		return fmt.Errorf("search qq files: %w", err)
	}
	if len(files.GetHits()) == 0 || files.GetHits()[0].GetRecordId() != fileRecordID {
		return fmt.Errorf("qq file search did not return the indexed file: %+v", files.GetHits())
	}
	for _, hit := range messages.GetHits() {
		if hit.GetRecordId() == fileRecordID {
			return fmt.Errorf("a message query returned a file record %s", fileRecordID)
		}
	}
	step("qq message and file indexes are separate and both searchable")

	// Recall is an append-only fact: the record stays, the search stops returning
	// it, and the state reports RECALLED.
	recallEvent := &qqsearchv1.IndexQQSourceEventRequest{
		EventId:          fmt.Sprintf("e2e-recall-%d", time.Now().UnixNano()),
		Sequence:         eventSequence + 2,
		RecordRevision:   2,
		Kind:             "message_recalled",
		Channel:          "qq",
		BotId:            botID,
		ConversationKind: "group",
		ExternalGroupId:  groupID,
		ConversationId:   conversationID,
		RecordId:         messageRecordID,
		RecalledAt:       time.Now().UTC().Format(time.RFC3339Nano),
	}
	if _, err := qq.IndexQQSourceEvent(indexCtx, recallEvent); err != nil {
		return fmt.Errorf("recall qq message: %w", err)
	}
	recalled, err := qq.SearchQQMessages(queryCtx, &qqsearchv1.SearchQQMessagesRequest{
		Query: marker, Scope: scope, TopK: 10,
	})
	if err != nil {
		return err
	}
	for _, hit := range recalled.GetHits() {
		if hit.GetRecordId() == messageRecordID {
			return errors.New("a recalled message is still returned by search")
		}
	}
	state, err := qq.GetQQRecordState(queryCtx, &qqsearchv1.GetQQRecordStateRequest{
		Kind: qqsearchv1.QQRecordKind_QQ_RECORD_KIND_MESSAGE, RecordId: messageRecordID,
	})
	if err != nil {
		return fmt.Errorf("get qq record state: %w", err)
	}
	if !state.GetExists() || state.GetState().GetStatus() != qqsearchv1.QQRecordStatus_QQ_RECORD_STATUS_RECALLED {
		return fmt.Errorf("a recalled record must still exist and report RECALLED, got %+v", state)
	}
	step("recall hid the message from search while keeping the record")

	// An out-of-scope conversation is rejected as a whole.
	_, err = qq.SearchQQMessages(queryCtx, &qqsearchv1.SearchQQMessagesRequest{
		Query: marker,
		Scope: &qqsearchv1.QQChannelScope{BotIds: []string{botID}, ConversationIds: []string{"qq:10001:group:12345"}},
		TopK:  10,
	})
	if status.Code(err) != codes.PermissionDenied {
		return fmt.Errorf("an out-of-scope conversation must be PermissionDenied, got %v", err)
	}
	step("an out-of-scope QQ conversation was rejected as a whole")

	slog.Info("QQ_CHAIN_OK", "message_record", messageRecordID, "file_record", fileRecordID)
	fmt.Println("QQ_CHAIN_OK")
	return nil
}

func containsDocument(response *documentsearchv1.SearchDocumentsResponse, documentID string) bool {
	for _, hit := range response.GetHits() {
		if hit.GetDocumentId() == documentID {
			return true
		}
	}
	return false
}

// assertionContext attaches a signed service assertion for one call.
func assertionContext(ctx context.Context, codec *serviceauth.Codec, caller serviceauth.Caller, audience serviceauth.Audience, subjectKey string, scopes []string) (context.Context, error) {
	now := time.Now().UTC()
	var conversation *serviceauth.Conversation
	if subjectKey != "" {
		conversation = &serviceauth.Conversation{Kind: serviceauth.ConversationPrivate}
	}
	token, err := codec.SealAssertion(serviceauth.AssertionClaims{
		Caller:       caller,
		Audience:     audience,
		Scopes:       scopes,
		SubjectKey:   subjectKey,
		Conversation: conversation,
		Actor:        "document-service-e2e",
		IssuedAt:     now.Unix(),
		ExpiresAt:    now.Add(2 * time.Minute).Unix(),
	})
	if err != nil {
		return nil, err
	}
	return metadata.AppendToOutgoingContext(ctx, serviceauth.HeaderAuthorization, serviceauth.AuthorizationHeader(token)), nil
}

// documentserviceWebSubject builds the canonical web subject key without
// importing the go-web module: it is the same canonical form.
func documentserviceWebSubject(userID int64) (string, error) {
	if userID <= 0 {
		return "", fmt.Errorf("web user id must be positive")
	}
	return serviceauth.WebSubjectKey(strconv.FormatInt(userID, 10))
}

func loadKey(path, inline string) ([]byte, error) {
	if strings.TrimSpace(inline) != "" {
		key := []byte(strings.TrimSpace(inline))
		if len(key) < 32 {
			return nil, errors.New("inline boundary key must be at least 32 bytes")
		}
		return key, nil
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("a boundary key is required: pass -capability-key-file or -capability-key")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read boundary key %s: %w", path, err)
	}
	key := []byte(strings.TrimSpace(string(raw)))
	if len(key) < 32 {
		return nil, fmt.Errorf("boundary key %s must be at least 32 bytes", path)
	}
	return key, nil
}

func step(format string, args ...any) {
	fmt.Printf("  - "+format+"\n", args...)
}
