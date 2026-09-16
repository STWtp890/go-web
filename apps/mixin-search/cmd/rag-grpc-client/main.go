package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	mixinsearchv1 "packages/gen/mixin-search/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
)

func main() {
	address := flag.String("address", "127.0.0.1:9090", "RAG gRPC server address")
	filePath := flag.String("file", "", "path to a .md, .markdown, .doc, or .docx document")
	documentID := flag.String("document-id", "knowledge", "document ID")
	versionID := flag.String("version-id", "v1", "document version ID")
	ownerSpaceID := flag.String("owner-space-id", "demo", "document owner knowledge-space ID")
	title := flag.String("title", "", "optional document title")
	query := flag.String("query", "文档向量化", "hybrid-search query")
	topK := flag.Int("top-k", 3, "number of search results")
	chunkSize := flag.Int("chunk-size", 180, "chunk size in runes")
	overlap := flag.Int("overlap", 20, "overlap in runes")
	timeout := flag.Duration("timeout", 15*time.Second, "deadline for each RPC")
	maxSendBytes := flag.Int("max-send-bytes", 16<<20, "maximum gRPC request size")
	indexToken := flag.String(
		"index-token", os.Getenv("MIXIN_SEARCH_INDEX_TOKEN"),
		"capability with the index-writer role; mint one with cmd/rag-token",
	)
	searchToken := flag.String(
		"search-token", os.Getenv("MIXIN_SEARCH_SEARCH_TOKEN"),
		"capability with the searcher role; mint one with cmd/rag-token",
	)
	flag.Parse()

	if *filePath == "" {
		log.Fatal("-file is required")
	}
	if *documentID == "" || *versionID == "" || *ownerSpaceID == "" {
		log.Fatal("-document-id, -version-id and -owner-space-id are required")
	}
	if *timeout <= 0 {
		log.Fatal("timeout must be greater than zero")
	}
	if *maxSendBytes <= 0 {
		log.Fatal("max-send-bytes must be greater than zero")
	}
	// Index mutation and search are separate roles on purpose, so this demo needs
	// two capabilities instead of one broad credential.
	if strings.TrimSpace(*indexToken) == "" || strings.TrimSpace(*searchToken) == "" {
		log.Fatal("-index-token and -search-token are required; mint them with: go run ./cmd/rag-token")
	}
	content, err := os.ReadFile(*filePath)
	if err != nil {
		log.Fatalf("read document: %v", err)
	}

	connection, err := grpc.NewClient(
		*address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(*maxSendBytes)),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer connection.Close()
	client := mixinsearchv1.NewRAGServiceClient(connection)

	operationSuffix := fmt.Sprintf("%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(withCapability(context.Background(), *indexToken), *timeout)
	indexed, err := client.IndexDocumentVersion(ctx, &mixinsearchv1.IndexDocumentVersionRequest{
		OperationId:  "example-index-" + operationSuffix,
		DocumentId:   *documentID,
		VersionId:    *versionID,
		OwnerSpaceId: *ownerSpaceID,
		Filename:     filepath.Base(*filePath),
		Title:        *title,
		Content:      content,
		ChunkSize:    int32(*chunkSize),
		Overlap:      int32(*overlap),
		SourceUri:    "file://" + filepath.ToSlash(filepath.Base(*filePath)),
	})
	cancel()
	if err != nil {
		log.Fatalf("index document version: %v", err)
	}
	fmt.Fprintf(os.Stderr, "indexed document=%s version=%s chunks=%d\n",
		indexed.GetState().GetRef().GetDocumentId(),
		indexed.GetState().GetRef().GetVersionId(),
		indexed.GetState().GetChunkCount(),
	)

	ctx, cancel = context.WithTimeout(withCapability(context.Background(), *indexToken), *timeout)
	_, err = client.UpdateDocumentAccess(ctx, &mixinsearchv1.UpdateDocumentAccessRequest{
		OperationId:     "example-access-" + operationSuffix,
		DocumentId:      *documentID,
		AccessRevision:  1,
		GrantedSpaceIds: []string{*ownerSpaceID},
	})
	cancel()
	if err != nil {
		log.Fatalf("update document access: %v", err)
	}

	ctx, cancel = context.WithTimeout(withCapability(context.Background(), *indexToken), *timeout)
	_, err = client.ActivateDocumentVersion(ctx, &mixinsearchv1.ActivateDocumentVersionRequest{
		OperationId:        "example-activate-" + operationSuffix,
		DocumentId:         *documentID,
		VersionId:          *versionID,
		ActivationRevision: 1,
	})
	cancel()
	if err != nil {
		log.Fatalf("activate document version: %v", err)
	}

	ctx, cancel = context.WithTimeout(withCapability(context.Background(), *searchToken), *timeout)
	searched, err := client.SearchDocuments(ctx, &mixinsearchv1.SearchDocumentsRequest{
		Query:           *query,
		AllowedSpaceIds: []string{*ownerSpaceID},
		TopK:            int32(*topK),
	})
	cancel()
	if err != nil {
		log.Fatalf("search documents: %v", err)
	}

	encoded, err := protojson.MarshalOptions{
		Multiline:       true,
		Indent:          "  ",
		UseProtoNames:   true,
		EmitUnpopulated: true,
	}.Marshal(searched)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(encoded))
}

// withCapability attaches the caller capability the boundary expects.
func withCapability(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+strings.TrimSpace(token))
}
