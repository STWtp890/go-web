// document-search-eval 在一次性环境中创建标注样本，比较正式 BM25 与
// mixin-search 影子结果，并输出可机器读取和人工审阅的 P2.5 报告。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	postgresqlconn "gin-backend/internal/common/base/connection/postgresql"
	"gin-backend/internal/config"
	"gin-backend/internal/modules/document/application"
	"gin-backend/internal/modules/document/domain"
	"gin-backend/internal/modules/document/evaluation"
	"gin-backend/internal/modules/document/infrastructure/mixinsearch"
	documentpostgresql "gin-backend/internal/modules/document/infrastructure/postgresql"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type createdDocument struct {
	key        string
	documentID string
	versionID  string
}

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "document-search-eval 退出: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPathDefault := configPathFromArgs(os.Args[1:])
	conf, err := config.Load(configPathDefault)
	if err != nil {
		return fmt.Errorf("加载配置: %w", err)
	}
	flags := flag.NewFlagSet("document-search-eval", flag.ContinueOnError)
	_ = flags.String("config", configPathDefault, "gin-backend YAML config path")
	datasetPath := flags.String("dataset", filepath.FromSlash("../../deployments/evaluation/document-search-v1.json"), "evaluation dataset JSON")
	reportDirectory := flags.String("report-dir", filepath.FromSlash("../../deployments/test-results"), "report output directory")
	runID := flags.String("run-id", "p25_"+time.Now().Format("20060102_150405"), "report run identifier")
	address := flags.String("mixin-search-address", envOrDefault("MIXIN_SEARCH_GRPC_ADDRESS", conf.ShadowSearchConfig.GRPCAddress), "mixin-search gRPC address")
	topK := flags.Int("top-k", 5, "document-level evaluation cutoff")
	indexWait := flags.Duration("index-wait-timeout", 60*time.Second, "maximum wait for Outbox indexing")
	queryTimeout := flags.Duration("query-timeout", conf.ShadowSearchConfig.Timeout, "per-query shadow timeout")
	embeddingProfile := flags.String("embedding-profile", "local-hash-v1", "retrieval embedding profile under evaluation")
	requireEligible := flags.Bool("require-eligible", false, "exit non-zero unless controlled switch is eligible")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *topK <= 0 || *topK > 100 {
		return errors.New("top-k must be between 1 and 100")
	}
	if *queryTimeout <= 0 || *indexWait <= 0 {
		return errors.New("timeouts must be greater than zero")
	}
	*runID = normalizeRunID(*runID)

	dataset, err := evaluation.LoadDataset(*datasetPath)
	if err != nil {
		return fmt.Errorf("load dataset: %w", err)
	}
	db, err := postgresqlconn.NewDB(conf.PostgresConfig, conf.LogConfig.Level)
	if err != nil {
		return err
	}
	defer postgresqlconn.Close(db)
	store, err := documentpostgresql.New(db)
	if err != nil {
		return err
	}
	client, err := mixinsearch.New(*address, conf.IndexDeliveryConfig.MaxSendBytes)
	if err != nil {
		return err
	}
	defer client.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ownerID, err := createEvaluationUser(ctx, db, *runID)
	if err != nil {
		return err
	}
	created, ownerSpaceID, err := createEvaluationDocuments(ctx, store, ownerID, dataset.Documents)
	if err != nil {
		return err
	}
	if err := waitForActiveIndex(ctx, client, created, *indexWait); err != nil {
		return err
	}

	observer, err := application.NewShadowSearchObserver(store, client, application.ShadowSearchObserverConfig{
		QueueSize: len(dataset.Queries) + 1, Concurrency: 2, Timeout: *queryTimeout,
		RecordTimeout: conf.ShadowSearchConfig.RecordTimeout, TopK: 100,
	})
	if err != nil {
		return err
	}
	queries, err := application.NewQueryService(store, nil)
	if err != nil {
		observer.Close()
		return err
	}
	documentIDsByKey, keysByDocumentID := evaluationDocumentMaps(created)
	report := evaluation.Report{
		SchemaVersion: "document-search-evaluation/v1", RunID: *runID, GeneratedAt: time.Now().UTC(),
		DatasetName: dataset.Name, DatasetSHA256: dataset.SHA256, EmbeddingProfile: *embeddingProfile, TopK: *topK,
		Queries: make([]evaluation.QueryResult, 0, len(dataset.Queries)),
	}
	for _, query := range dataset.Queries {
		result, correctness, err := evaluateQuery(
			ctx, queries, observer, store, client, ownerID, ownerSpaceID,
			query, documentIDsByKey, keysByDocumentID, *topK, *queryTimeout,
		)
		if err != nil {
			observer.Close()
			return err
		}
		report.Queries = append(report.Queries, result)
		report.Correctness.PermissionViolations += correctness.PermissionViolations
		report.Correctness.LifecycleViolations += correctness.LifecycleViolations
		report.Correctness.ActiveVersionViolations += correctness.ActiveVersionViolations
		report.Correctness.FormalScopeMismatches += correctness.FormalScopeMismatches
	}
	observer.Close()
	report.BM25 = evaluation.Summarize(report.Queries, false)
	report.Shadow = evaluation.Summarize(report.Queries, true)
	report.Decide(*queryTimeout)

	reportDir, err := filepath.Abs(*reportDirectory)
	if err != nil {
		return err
	}
	jsonPath, markdownPath, err := evaluation.WriteReport(reportDir, report)
	if err != nil {
		return fmt.Errorf("write evaluation report: %w", err)
	}
	summary := map[string]any{
		"decision": report.Decision, "dataset": report.DatasetName, "queries": len(report.Queries),
		"embeddingProfile": report.EmbeddingProfile,
		"jsonReport":       jsonPath, "markdownReport": markdownPath,
		"bm25": report.BM25, "shadow": report.Shadow, "correctness": report.Correctness,
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(summary); err != nil {
		return err
	}
	if *requireEligible && report.Decision != "ELIGIBLE_FOR_CONTROLLED_SWITCH" {
		return fmt.Errorf("controlled switch gate failed: %s", strings.Join(report.DecisionReasons, "; "))
	}
	return nil
}

func createEvaluationUser(ctx context.Context, db *gorm.DB, runID string) (int64, error) {
	email := fmt.Sprintf("p25.%s.%s@example.test", strings.ToLower(runID), uuid.NewString()[:8])
	now := time.Now().Unix()
	var ownerID int64
	row := db.WithContext(ctx).Raw(`
INSERT INTO users (email, password, nickname, banned, created_at, updated_at)
VALUES (?, ?, ?, false, ?, ?)
RETURNING id`, email, "evaluation-only-not-a-login-secret", "P2.5 Evaluation", now, now).Row()
	if err := row.Scan(&ownerID); err != nil {
		return 0, fmt.Errorf("create evaluation user: %w", err)
	}
	return ownerID, nil
}

func createEvaluationDocuments(
	ctx context.Context,
	store *documentpostgresql.Repository,
	ownerID int64,
	documents []evaluation.Document,
) ([]createdDocument, string, error) {
	commands, err := application.NewCommandService(store)
	if err != nil {
		return nil, "", err
	}
	created := make([]createdDocument, 0, len(documents))
	ownerSpaceID := ""
	for _, document := range documents {
		result, createErr := commands.Create(ctx, application.CreateCommand{
			OwnerID: ownerID, Title: document.Title, Content: document.Content, AuthenticatedPublic: false,
		})
		if createErr != nil {
			return nil, "", fmt.Errorf("create evaluation document %s: %w", document.Key, createErr)
		}
		if ownerSpaceID == "" {
			ownerSpaceID = result.Document.OwnerSpaceID
		}
		created = append(created, createdDocument{
			key: document.Key, documentID: result.Document.DocumentID, versionID: result.Version.VersionID,
		})
	}
	return created, ownerSpaceID, nil
}

func waitForActiveIndex(
	ctx context.Context,
	client *mixinsearch.Client,
	documents []createdDocument,
	timeout time.Duration,
) error {
	deadline := time.Now().Add(timeout)
	remaining := append([]createdDocument{}, documents...)
	lastError := ""
	for len(remaining) > 0 && time.Now().Before(deadline) {
		next := make([]createdDocument, 0, len(remaining))
		for _, document := range remaining {
			callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			state, err := client.GetDocumentVersionState(callCtx, document.documentID, document.versionID)
			cancel()
			if err != nil {
				lastError = err.Error()
				next = append(next, document)
				continue
			}
			if !state.Exists || state.VersionID != document.versionID || !strings.HasSuffix(state.Status, "_ACTIVE") {
				next = append(next, document)
			}
		}
		remaining = next
		if len(remaining) > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	if len(remaining) != 0 {
		return fmt.Errorf("wait for evaluation documents to become active: remaining=%d last_error=%s", len(remaining), lastError)
	}
	return nil
}

func evaluateQuery(
	ctx context.Context,
	queries *application.QueryService,
	observer *application.ShadowSearchObserver,
	store *documentpostgresql.Repository,
	client *mixinsearch.Client,
	ownerID int64,
	ownerSpaceID string,
	query evaluation.Query,
	documentIDsByKey map[string]string,
	keysByDocumentID map[string]string,
	topK int,
	queryTimeout time.Duration,
) (evaluation.QueryResult, evaluation.Correctness, error) {
	bm25Started := time.Now()
	bm25Items, page, _, err := queries.SearchMine(ctx, ownerID, query.Text, 1, topK)
	bm25Latency := time.Since(bm25Started)
	if err != nil {
		return evaluation.QueryResult{}, evaluation.Correctness{}, fmt.Errorf("BM25 query %s: %w", query.ID, err)
	}
	bm25IDs := make([]string, 0, len(bm25Items))
	for _, item := range bm25Items {
		bm25IDs = append(bm25IDs, item.DocumentID)
	}
	if !observer.Observe(application.ShadowSearchRequest{
		Source: "evaluation", OwnerID: ownerID, Query: query.Text, Page: 1, PageSize: topK,
		BM25Total: page.Total, BM25Latency: bm25Latency, BM25DocumentIDs: bm25IDs,
	}) {
		return evaluation.QueryResult{}, evaluation.Correctness{}, fmt.Errorf("shadow observation queue rejected query %s", query.ID)
	}

	shadowStarted := time.Now()
	callCtx, cancel := context.WithTimeout(ctx, queryTimeout)
	shadowResult, shadowErr := client.SearchDocuments(callCtx, domain.DocumentSearchInput{
		Query: query.Text, AllowedSpaceIDs: []string{ownerSpaceID}, TopK: 100,
	})
	cancel()
	shadowLatency := time.Since(shadowStarted)
	shadowIDs := deduplicateDocumentIDs(shadowResult.Hits)
	correctness := inspectCorrectness(ctx, store, shadowResult.Hits, ownerID, ownerSpaceID)
	if len(shadowIDs) > topK {
		shadowIDs = shadowIDs[:topK]
	}
	relevantIDs := make([]string, 0, len(query.Relevant))
	for _, key := range query.Relevant {
		relevantIDs = append(relevantIDs, documentIDsByKey[key])
	}
	result := evaluation.QueryResult{
		ID: query.ID, Category: query.Category, Query: query.Text,
		RelevantKeys: append([]string{}, query.Relevant...),
		BM25Keys:     documentKeys(bm25IDs, keysByDocumentID), ShadowKeys: documentKeys(shadowIDs, keysByDocumentID),
		BM25Metrics: evaluation.Score(bm25IDs, relevantIDs, topK), ShadowMetrics: evaluation.Score(shadowIDs, relevantIDs, topK),
		BM25LatencyUS: bm25Latency.Microseconds(), ShadowLatencyUS: shadowLatency.Microseconds(),
	}
	if shadowErr != nil {
		result.ShadowError = shadowErr.Error()
	}
	return result, correctness, nil
}

func inspectCorrectness(
	ctx context.Context,
	store *documentpostgresql.Repository,
	hits []domain.DocumentSearchHit,
	ownerID int64,
	allowedSpaceID string,
) evaluation.Correctness {
	unique := deduplicateHits(hits)
	ids := make([]string, 0, len(unique))
	for _, hit := range unique {
		ids = append(ids, hit.DocumentID)
	}
	facts, err := store.GetShadowSearchFacts(ctx, ids)
	if err != nil {
		return evaluation.Correctness{LifecycleViolations: len(unique)}
	}
	var result evaluation.Correctness
	for _, hit := range unique {
		fact, exists := facts[hit.DocumentID]
		if !exists || fact.LifecycleStatus != domain.LifecycleActive {
			result.LifecycleViolations++
			continue
		}
		if fact.ActiveVersionID != hit.VersionID {
			result.ActiveVersionViolations++
			continue
		}
		if !fact.AuthenticatedPublic && fact.OwnerSpaceID != allowedSpaceID {
			result.PermissionViolations++
			continue
		}
		if fact.OwnerID != ownerID {
			result.FormalScopeMismatches++
		}
	}
	return result
}

func deduplicateDocumentIDs(hits []domain.DocumentSearchHit) []string {
	unique := deduplicateHits(hits)
	ids := make([]string, 0, len(unique))
	for _, hit := range unique {
		ids = append(ids, hit.DocumentID)
	}
	return ids
}

func deduplicateHits(hits []domain.DocumentSearchHit) []domain.DocumentSearchHit {
	seen := make(map[string]struct{}, len(hits))
	result := make([]domain.DocumentSearchHit, 0, len(hits))
	for _, hit := range hits {
		if hit.DocumentID == "" {
			continue
		}
		if _, exists := seen[hit.DocumentID]; exists {
			continue
		}
		seen[hit.DocumentID] = struct{}{}
		result = append(result, hit)
	}
	return result
}

func evaluationDocumentMaps(documents []createdDocument) (map[string]string, map[string]string) {
	byKey := make(map[string]string, len(documents))
	byID := make(map[string]string, len(documents))
	for _, document := range documents {
		byKey[document.key] = document.documentID
		byID[document.documentID] = document.key
	}
	return byKey, byID
}

func documentKeys(documentIDs []string, keysByDocumentID map[string]string) []string {
	keys := make([]string, 0, len(documentIDs))
	for _, documentID := range documentIDs {
		if key, exists := keysByDocumentID[documentID]; exists {
			keys = append(keys, key)
		} else {
			keys = append(keys, "external:"+documentID)
		}
	}
	return keys
}

var invalidRunIDCharacters = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

func normalizeRunID(value string) string {
	value = strings.Trim(invalidRunIDCharacters.ReplaceAllString(value, "-"), "-.")
	if value == "" {
		value = "p25-" + uuid.NewString()
	}
	if len(value) > 80 {
		value = value[:80]
	}
	return value
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func configPathFromArgs(args []string) string {
	path := envOrDefault("GIN_CONFIG_PATH", "configs/config.yaml")
	for index, argument := range args {
		if (argument == "-config" || argument == "--config") && index+1 < len(args) {
			return args[index+1]
		}
		if strings.HasPrefix(argument, "-config=") {
			return strings.TrimPrefix(argument, "-config=")
		}
		if strings.HasPrefix(argument, "--config=") {
			return strings.TrimPrefix(argument, "--config=")
		}
	}
	return path
}
