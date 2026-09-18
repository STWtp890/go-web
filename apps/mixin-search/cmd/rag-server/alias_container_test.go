package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"mixin-search/internal/rag"
	"mixin-search/internal/security"
	mixinsearchchatv1 "packages/gen/mixin-search/chat/v1"

	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestChatAliasSwitchAgainstADeployedStack is ADR-014 decision 4 against the
// running deployment: it records both corpora's alias mappings, moves the chat
// alias to an empty generation, and checks the consequences directly.
//
// It is the container-level counterpart of the store-level integration test:
// that one proves the mechanism, this one proves the deployed stack is actually
// wired that way - both configured collection names are aliases over <name>_g1 -
// and that a switch performed while the service is running is picked up without
// a restart.
//
// Whatever happens, the original mapping is restored and the generation this
// test created is deleted, so a failing run cannot leave the stack switched.
//
//	GIN_BACKEND_PORT=... deployments/verify.ps1 sets these for the gate:
//	ALIAS_CONTAINER_INTEGRATION=1
//	ALIAS_CONTAINER_GRPC_ADDRESS=127.0.0.1:19090
//	ALIAS_CONTAINER_QDRANT_ADDRESS=127.0.0.1:16334
//	ALIAS_CONTAINER_CAPABILITY_KEY_FILE=deployments/secrets/mixin_search_capability.key
func TestChatAliasSwitchAgainstADeployedStack(t *testing.T) {
	if os.Getenv("ALIAS_CONTAINER_INTEGRATION") != "1" {
		t.Skip("set ALIAS_CONTAINER_INTEGRATION=1 to run against a deployed stack")
	}
	grpcAddress := requireEnv(t, "ALIAS_CONTAINER_GRPC_ADDRESS")
	qdrantAddress := requireEnv(t, "ALIAS_CONTAINER_QDRANT_ADDRESS")
	keyPath := requireEnv(t, "ALIAS_CONTAINER_CAPABILITY_KEY_FILE")
	documentAlias := envOrDefaultString("ALIAS_CONTAINER_DOCUMENT_ALIAS", "go_web_shadow_v1")
	chatAlias := envOrDefaultString("ALIAS_CONTAINER_CHAT_ALIAS", "go_web_chat_v1")

	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read boundary key: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client := newQdrantAliasClient(t, qdrantAddress)
	// Registered here so that cleanup order (last in, first out) runs the restore
	// below while the client is still open; a deferred Close would run before any
	// t.Cleanup handler and leave the stack switched.
	t.Cleanup(func() { _ = client.Close() })
	originalChatTarget := assertDeployedAlias(t, ctx, client, chatAlias)
	originalDocumentTarget := assertDeployedAlias(t, ctx, client, documentAlias)

	// A store handle on the deployed chat alias: resolving it is what a rebuild
	// would use to prepare and switch a generation.
	chatStore, err := rag.NewQdrantStore(ctx, rag.QdrantConfig{
		Host:       qdrantAddressHost(t, qdrantAddress),
		Port:       qdrantAddressPort(t, qdrantAddress),
		Collection: chatAlias,
		Dimensions: localEmbeddingDimensions,
	})
	if err != nil {
		t.Fatalf("open deployed chat alias %q: %v", chatAlias, err)
	}
	t.Cleanup(func() { _ = chatStore.Close() })

	restored := true
	createdGeneration := ""
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cleanupCancel()
		if !restored {
			if err := chatStore.SwitchAlias(cleanupContext, originalChatTarget); err != nil {
				t.Errorf("restore chat alias %q to %q: %v", chatAlias, originalChatTarget, err)
			}
		}
		if createdGeneration != "" {
			if err := client.DeleteCollection(cleanupContext, createdGeneration); err != nil {
				t.Errorf("delete generation %q: %v", createdGeneration, err)
			}
		}
		current, err := deployedAliasTarget(cleanupContext, client, chatAlias)
		if err != nil {
			t.Errorf("resolve chat alias %q after the test: %v", chatAlias, err)
		} else if current != originalChatTarget {
			t.Errorf("chat alias %q ended at %q, want %q", chatAlias, current, originalChatTarget)
		}
	})

	// Chat data through the deployed service, so an empty result after the switch
	// means "the alias moved" and not "the corpus was never written".
	issuer, err := security.NewIssuer(trimBoundaryKey(key), "go-web", security.AudienceChat, 5*time.Minute)
	if err != nil {
		t.Fatalf("build chat issuer: %v", err)
	}
	connection, err := grpc.NewClient(grpcAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial %s: %v", grpcAddress, err)
	}
	defer func() { _ = connection.Close() }()
	chat := mixinsearchchatv1.NewChatIndexServiceClient(connection)

	run := strconv.FormatInt(time.Now().UnixNano(), 10)
	conversationID := "alias-" + run
	scopeID := "alias-scope-" + run
	needle := "aliasneedle" + run
	writerToken := mustIssue(t, issuer, "alias-gate-writer", security.RoleChatIndexWriter, "", nil, nil)
	searcherToken := mustIssue(t, issuer, "alias-gate-searcher", security.RoleChatSearcher, "7", []string{scopeID}, []string{conversationID})
	if _, err := chat.IndexConversationMessages(withToken(ctx, writerToken), &mixinsearchchatv1.IndexConversationMessagesRequest{
		OperationId: "alias-index-" + run, ConversationId: conversationID, OwnerScopeId: scopeID,
		LifecycleRevision: 1,
		Messages: []*mixinsearchchatv1.ChatMessageInput{{
			MessageId: "m-1", SenderId: "qq-10001", SentAtUnixMs: 1_700_000_000_000, Content: needle,
		}},
	}); err != nil {
		t.Fatalf("index chat message: %v", err)
	}
	if _, err := chat.ArchiveConversation(withToken(ctx, writerToken), &mixinsearchchatv1.ArchiveConversationRequest{
		OperationId: "alias-archive-" + run, ConversationId: conversationID, ArchiveRevision: 1, LifecycleRevision: 1,
	}); err != nil {
		t.Fatalf("archive conversation: %v", err)
	}
	if hits := searchContainerChat(t, chat, ctx, searcherToken, needle, scopeID); len(hits) != 1 {
		t.Fatalf("chat hits before the switch = %d, want 1", len(hits))
	}

	// Move the chat corpus to an empty generation while the service keeps running.
	generation, err := chatStore.PrepareGeneration(ctx, "g2", localEmbeddingDimensions)
	if err != nil {
		t.Fatalf("prepare chat generation g2: %v", err)
	}
	createdGeneration = generation
	if err := chatStore.SwitchAlias(ctx, generation); err != nil {
		t.Fatalf("switch chat alias to %q: %v", generation, err)
	}
	restored = false

	// The three assertions the decision asks for, made against the deployed state.
	if target, err := deployedAliasTarget(ctx, client, chatAlias); err != nil || target != generation {
		t.Fatalf("chat alias %q = %q (err=%v), want %q", chatAlias, target, err, generation)
	}
	if target, err := deployedAliasTarget(ctx, client, documentAlias); err != nil || target != originalDocumentTarget {
		t.Fatalf("document alias %q moved to %q (err=%v), want %q", documentAlias, target, err, originalDocumentTarget)
	}
	if hits := searchContainerChat(t, chat, ctx, searcherToken, needle, scopeID); len(hits) != 0 {
		t.Fatalf("chat hits on the empty generation = %d, want 0", len(hits))
	}

	// Switching back restores the corpus without a restart.
	if err := chatStore.SwitchAlias(ctx, originalChatTarget); err != nil {
		t.Fatalf("switch chat alias back to %q: %v", originalChatTarget, err)
	}
	restored = true
	if hits := searchContainerChat(t, chat, ctx, searcherToken, needle, scopeID); len(hits) != 1 {
		t.Fatalf("chat hits after switching back = %d, want 1", len(hits))
	}

	// Fail closed: an unknown target is refused and both mappings stay put.
	if err := chatStore.SwitchAlias(ctx, chatAlias+"_does_not_exist"); err == nil {
		t.Fatal("switching the deployed chat alias to a missing collection was accepted")
	}
	if target, err := deployedAliasTarget(ctx, client, chatAlias); err != nil || target != originalChatTarget {
		t.Fatalf("chat alias %q = %q (err=%v) after a refused switch, want %q", chatAlias, target, err, originalChatTarget)
	}
}

// assertDeployedAlias requires the configured corpus name to be an alias and
// returns the collection it points at. A physical collection carrying the name
// is the pre-alias layout, which fails here instead of being silently adopted.
func assertDeployedAlias(t *testing.T, ctx context.Context, client *qdrant.Client, alias string) string {
	t.Helper()
	target, err := deployedAliasTarget(ctx, client, alias)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if target == "" {
		exists, existsErr := client.CollectionExists(ctx, alias)
		if existsErr != nil {
			t.Fatalf("check collection %q: %v", alias, existsErr)
		}
		if exists {
			t.Fatalf("collection %q is a physical collection, want an alias over %s_%s", alias, alias, rag.DefaultQdrantGeneration)
		}
		t.Fatalf("alias %q does not exist", alias)
	}
	suffix := "_" + rag.DefaultQdrantGeneration
	if !strings.HasSuffix(target, suffix) {
		t.Fatalf("alias %q points at %q, want a %s generation", alias, target, suffix)
	}
	return target
}

// deployedAliasTarget resolves an alias, returning "" when the name is not an
// alias at all.
func deployedAliasTarget(ctx context.Context, client *qdrant.Client, alias string) (string, error) {
	aliases, err := client.ListAliases(ctx)
	if err != nil {
		return "", fmt.Errorf("list qdrant aliases: %w", err)
	}
	for _, description := range aliases {
		if description.GetAliasName() == alias {
			return description.GetCollectionName(), nil
		}
	}
	return "", nil
}

func newQdrantAliasClient(t *testing.T, address string) *qdrant.Client {
	t.Helper()
	client, err := qdrant.NewClient(&qdrant.Config{
		Host: qdrantAddressHost(t, address),
		Port: qdrantAddressPort(t, address),
	})
	if err != nil {
		t.Fatalf("connect qdrant at %s: %v", address, err)
	}
	return client
}

func qdrantAddressHost(t *testing.T, address string) string {
	t.Helper()
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("invalid qdrant address %q: %v", address, err)
	}
	return host
}

func qdrantAddressPort(t *testing.T, address string) int {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("invalid qdrant address %q: %v", address, err)
	}
	value, err := strconv.Atoi(port)
	if err != nil || value <= 0 || value > 65535 {
		t.Fatalf("invalid qdrant port in %q", address)
	}
	return value
}

func requireEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required", name)
	}
	return value
}

func envOrDefaultString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
