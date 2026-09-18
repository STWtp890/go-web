// Package architecture_test also turns the corpus boundary into an executable
// contract check. ADR-014 requires the document and chat corpora to stay
// separate contracts: neither may grow a field that expresses the other, and
// neither may gain a corpus selector that would let one request address both.
package architecture_test

import (
	"testing"

	_ "packages/gen/mixin-search/chat/v1"
	_ "packages/gen/mixin-search/v1"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

const (
	documentRPCCount = 7
	chatRPCCount     = 7

	documentProtoPath = "packages/proto/mixin-search/v1/mixin-search.proto"
	chatProtoPath     = "packages/proto/mixin-search/chat/v1/chat.proto"
)

// lookupFile resolves a contract by its source path, so the assertions do not
// depend on generated identifier names.
func lookupFile(t *testing.T, path string) protoreflect.FileDescriptor {
	t.Helper()
	file, err := protoregistry.GlobalFiles.FindFileByPath(path)
	if err != nil {
		t.Fatalf("contract %s is not registered: %v", path, err)
	}
	return file
}

func TestDocumentAndChatServicesHaveDistinctRPCs(t *testing.T) {
	document := lookupFile(t, documentProtoPath).Services().ByName("RAGService")
	if document == nil {
		t.Fatal("document contract lost its RAGService")
	}
	chat := lookupFile(t, chatProtoPath).Services().ByName("ChatIndexService")
	if chat == nil {
		t.Fatal("chat contract lost its ChatIndexService")
	}

	if got := document.Methods().Len(); got != documentRPCCount {
		t.Fatalf("document RPC count = %d, want %d", got, documentRPCCount)
	}
	if got := chat.Methods().Len(); got != chatRPCCount {
		t.Fatalf("chat RPC count = %d, want %d", got, chatRPCCount)
	}

	// A shared method name would mean one of the corpora leaked into the other.
	documentMethods := methodNames(document)
	for index := 0; index < chat.Methods().Len(); index++ {
		name := string(chat.Methods().Get(index).Name())
		if _, shared := documentMethods[name]; shared {
			t.Errorf("method %q exists in both corpora contracts", name)
		}
	}
}

// TestContractsDoNotLeakAcrossCorpora encodes the P3.3 acceptance item
// "the document contract cannot express chat messages, and the chat contract
// cannot return document semantics". It is deliberately about field names: those
// are what a reviewer reads, and a leaked field is how a shared corpus model
// starts.
//
// Only corpus-exclusive vocabulary is listed. Names that describe a mechanism
// both corpora need on their own terms are allowed on both sides:
// lifecycle_revision, tombstone_revision, operation_id, content_sha256 and
// metadata all exist in the two contracts with independent meanings, which is
// what ADR-014 requires ("mechanisms may match, state may not be shared").
func TestContractsDoNotLeakAcrossCorpora(t *testing.T) {
	documentFields := collectFields(lookupFile(t, documentProtoPath))
	chatFields := collectFields(lookupFile(t, chatProtoPath))

	assertFieldsAbsent(t, "document contract", documentFields, []string{
		"message_id", "conversation_id", "sender_id", "sent_at_unix_ms", "retract_revision",
	})
	assertFieldsAbsent(t, "chat contract", chatFields, []string{
		"document_id", "version_id", "owner_space_id", "activation_revision",
		"authenticated_public", "granted_space_ids",
	})
}

// TestNeitherContractGainsACorpusSelector pins the ADR-014 rejection of solving
// multi-corpus retrieval by adding a corpus discriminator to one contract.
func TestNeitherContractGainsACorpusSelector(t *testing.T) {
	forbiddens := []string{"corpus", "corpus_type", "content_type", "knowledge_domain"}
	assertFieldsAbsent(t, "document contract", collectFields(lookupFile(t, documentProtoPath)), forbiddens)
	assertFieldsAbsent(t, "chat contract", collectFields(lookupFile(t, chatProtoPath)), forbiddens)
}

func methodNames(service protoreflect.ServiceDescriptor) map[string]struct{} {
	names := make(map[string]struct{}, service.Methods().Len())
	for index := 0; index < service.Methods().Len(); index++ {
		names[string(service.Methods().Get(index).Name())] = struct{}{}
	}
	return names
}

// collectFields walks every message in a file, including nested messages.
func collectFields(file protoreflect.FileDescriptor) map[string][]string {
	fields := make(map[string][]string)
	var walk func(messages protoreflect.MessageDescriptors)
	walk = func(messages protoreflect.MessageDescriptors) {
		for index := 0; index < messages.Len(); index++ {
			message := messages.Get(index)
			names := make([]string, 0, message.Fields().Len())
			for field := 0; field < message.Fields().Len(); field++ {
				names = append(names, string(message.Fields().Get(field).Name()))
			}
			fields[string(message.Name())] = names
			walk(message.Messages())
		}
	}
	walk(file.Messages())
	return fields
}

func assertFieldsAbsent(t *testing.T, contract string, fields map[string][]string, forbidden []string) {
	t.Helper()
	for message, names := range fields {
		for _, name := range names {
			for _, banned := range forbidden {
				if name == banned {
					t.Errorf("%s message %s has field %q, which belongs to another corpus", contract, message, name)
				}
			}
		}
	}
}
