package evaluation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var requiredCategories = []string{
	"chinese", "english", "code", "title", "body", "exact_keyword", "semantic_expression",
}

type Dataset struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Documents   []Document `json:"documents"`
	Queries     []Query    `json:"queries"`
	SHA256      string     `json:"-"`
}

type Document struct {
	Key     string `json:"key"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

type Query struct {
	ID       string   `json:"id"`
	Category string   `json:"category"`
	Text     string   `json:"text"`
	Relevant []string `json:"relevant"`
}

func LoadDataset(path string) (Dataset, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Dataset{}, err
	}
	var dataset Dataset
	if err := json.Unmarshal(raw, &dataset); err != nil {
		return Dataset{}, fmt.Errorf("decode evaluation dataset: %w", err)
	}
	digest := sha256.Sum256(raw)
	dataset.SHA256 = hex.EncodeToString(digest[:])
	if err := dataset.Validate(); err != nil {
		return Dataset{}, err
	}
	return dataset, nil
}

func (dataset Dataset) Validate() error {
	if strings.TrimSpace(dataset.Name) == "" || len(dataset.Documents) == 0 || len(dataset.Queries) == 0 {
		return errors.New("evaluation dataset requires name, documents and queries")
	}
	documents := make(map[string]struct{}, len(dataset.Documents))
	for _, document := range dataset.Documents {
		if strings.TrimSpace(document.Key) == "" || strings.TrimSpace(document.Title) == "" || strings.TrimSpace(document.Content) == "" {
			return errors.New("evaluation document requires key, title and content")
		}
		if _, exists := documents[document.Key]; exists {
			return fmt.Errorf("duplicate evaluation document key %q", document.Key)
		}
		documents[document.Key] = struct{}{}
	}
	categories := make(map[string]bool, len(requiredCategories))
	queries := make(map[string]struct{}, len(dataset.Queries))
	for _, query := range dataset.Queries {
		if strings.TrimSpace(query.ID) == "" || strings.TrimSpace(query.Text) == "" || len(query.Relevant) == 0 {
			return errors.New("evaluation query requires id, text and relevant judgments")
		}
		if _, exists := queries[query.ID]; exists {
			return fmt.Errorf("duplicate evaluation query id %q", query.ID)
		}
		queries[query.ID] = struct{}{}
		categories[query.Category] = true
		for _, key := range query.Relevant {
			if _, exists := documents[key]; !exists {
				return fmt.Errorf("query %q references unknown document %q", query.ID, key)
			}
		}
	}
	for _, category := range requiredCategories {
		if !categories[category] {
			return fmt.Errorf("evaluation dataset is missing category %q", category)
		}
	}
	return nil
}

type Metrics struct {
	RecallAtK float64 `json:"recallAtK"`
	MRR       float64 `json:"mrr"`
	NDCGAtK   float64 `json:"ndcgAtK"`
}

func Score(retrieved, relevant []string, topK int) Metrics {
	if topK <= 0 || len(relevant) == 0 {
		return Metrics{}
	}
	if len(retrieved) > topK {
		retrieved = retrieved[:topK]
	}
	relevantSet := make(map[string]struct{}, len(relevant))
	for _, id := range relevant {
		relevantSet[id] = struct{}{}
	}
	found := 0
	firstRank := 0
	dcg := 0.0
	for index, id := range retrieved {
		if _, exists := relevantSet[id]; !exists {
			continue
		}
		found++
		if firstRank == 0 {
			firstRank = index + 1
		}
		dcg += 1 / math.Log2(float64(index+2))
	}
	idealCount := len(relevant)
	if idealCount > topK {
		idealCount = topK
	}
	idcg := 0.0
	for index := range idealCount {
		idcg += 1 / math.Log2(float64(index+2))
	}
	mrr := 0.0
	if firstRank > 0 {
		mrr = 1 / float64(firstRank)
	}
	ndcg := 0.0
	if idcg > 0 {
		ndcg = dcg / idcg
	}
	return Metrics{RecallAtK: float64(found) / float64(len(relevant)), MRR: mrr, NDCGAtK: ndcg}
}

type QueryResult struct {
	ID              string   `json:"id"`
	Category        string   `json:"category"`
	Query           string   `json:"query"`
	RelevantKeys    []string `json:"relevantKeys"`
	BM25Keys        []string `json:"bm25Keys"`
	ShadowKeys      []string `json:"shadowKeys"`
	BM25Metrics     Metrics  `json:"bm25Metrics"`
	ShadowMetrics   Metrics  `json:"shadowMetrics"`
	BM25LatencyUS   int64    `json:"bm25LatencyMicros"`
	ShadowLatencyUS int64    `json:"shadowLatencyMicros"`
	ShadowError     string   `json:"shadowError,omitempty"`
}

type EngineSummary struct {
	MeanRecallAtK float64 `json:"meanRecallAtK"`
	MeanMRR       float64 `json:"meanMrr"`
	MeanNDCGAtK   float64 `json:"meanNdcgAtK"`
	LatencyP50US  int64   `json:"latencyP50Micros"`
	LatencyP95US  int64   `json:"latencyP95Micros"`
	LatencyMaxUS  int64   `json:"latencyMaxMicros"`
	Errors        int     `json:"errors"`
}

func Summarize(results []QueryResult, shadow bool) EngineSummary {
	if len(results) == 0 {
		return EngineSummary{}
	}
	latencies := make([]int64, 0, len(results))
	var summary EngineSummary
	for _, result := range results {
		metrics, latency := result.BM25Metrics, result.BM25LatencyUS
		if shadow {
			metrics, latency = result.ShadowMetrics, result.ShadowLatencyUS
			if result.ShadowError != "" {
				summary.Errors++
			}
		}
		summary.MeanRecallAtK += metrics.RecallAtK
		summary.MeanMRR += metrics.MRR
		summary.MeanNDCGAtK += metrics.NDCGAtK
		latencies = append(latencies, latency)
	}
	count := float64(len(results))
	summary.MeanRecallAtK /= count
	summary.MeanMRR /= count
	summary.MeanNDCGAtK /= count
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	summary.LatencyP50US = percentile(latencies, 0.50)
	summary.LatencyP95US = percentile(latencies, 0.95)
	summary.LatencyMaxUS = latencies[len(latencies)-1]
	return summary
}

func percentile(values []int64, fraction float64) int64 {
	if len(values) == 0 {
		return 0
	}
	index := int(math.Ceil(float64(len(values))*fraction)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}

type Correctness struct {
	PermissionViolations    int `json:"permissionViolations"`
	LifecycleViolations     int `json:"lifecycleViolations"`
	ActiveVersionViolations int `json:"activeVersionViolations"`
	FormalScopeMismatches   int `json:"formalScopeMismatches"`
}

type Report struct {
	SchemaVersion    string        `json:"schemaVersion"`
	RunID            string        `json:"runId"`
	GeneratedAt      time.Time     `json:"generatedAt"`
	DatasetName      string        `json:"datasetName"`
	DatasetSHA256    string        `json:"datasetSha256"`
	EmbeddingProfile string        `json:"embeddingProfile"`
	TopK             int           `json:"topK"`
	BM25             EngineSummary `json:"bm25"`
	Shadow           EngineSummary `json:"shadow"`
	Correctness      Correctness   `json:"correctness"`
	Decision         string        `json:"decision"`
	DecisionReasons  []string      `json:"decisionReasons"`
	Queries          []QueryResult `json:"queries"`
}

func (report *Report) Decide(maxShadowP95 time.Duration) {
	reasons := make([]string, 0)
	if report.Correctness.PermissionViolations != 0 || report.Correctness.LifecycleViolations != 0 ||
		report.Correctness.ActiveVersionViolations != 0 {
		reasons = append(reasons, "authorization or lifecycle correctness gate failed")
	}
	if report.Correctness.FormalScopeMismatches != 0 {
		reasons = append(reasons, "formal SearchMine scope differs from shadow candidates")
	}
	if report.Shadow.Errors != 0 {
		reasons = append(reasons, "shadow query errors were observed")
	}
	if report.EmbeddingProfile == "" || strings.HasPrefix(report.EmbeddingProfile, "local-hash") {
		reasons = append(reasons, "evaluation-only local hash embedding is not eligible for a semantic read switch")
	}
	if report.Shadow.MeanRecallAtK < 0.85 || report.Shadow.MeanMRR < 0.75 || report.Shadow.MeanNDCGAtK < 0.80 {
		reasons = append(reasons, "aggregate relevance threshold was not met")
	}
	for _, query := range report.Queries {
		if query.Category == "semantic_expression" && query.ShadowMetrics.RecallAtK < 1 {
			reasons = append(reasons, "semantic-expression recall gate was not met")
			break
		}
	}
	if report.Shadow.LatencyP95US > maxShadowP95.Microseconds() {
		reasons = append(reasons, "shadow p95 exceeded the configured evaluation budget")
	}
	if len(reasons) == 0 {
		report.Decision = "ELIGIBLE_FOR_CONTROLLED_SWITCH"
		report.DecisionReasons = []string{"all P2.5 correctness, quality and latency gates passed"}
		return
	}
	report.Decision = "KEEP_BM25"
	report.DecisionReasons = reasons
}

func WriteReport(directory string, report Report) (string, string, error) {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", "", err
	}
	base := "document-search-evaluation-" + report.RunID
	jsonPath := filepath.Join(directory, base+".json")
	markdownPath := filepath.Join(directory, base+".md")
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", "", err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(jsonPath, raw, 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(markdownPath, []byte(markdown(report)), 0o644); err != nil {
		return "", "", err
	}
	return jsonPath, markdownPath, nil
}

func markdown(report Report) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# Document search evaluation %s\n\n", report.RunID)
	fmt.Fprintf(&builder, "- Dataset: `%s` (`%s`)\n", report.DatasetName, report.DatasetSHA256)
	fmt.Fprintf(&builder, "- Embedding profile: `%s`\n", report.EmbeddingProfile)
	fmt.Fprintf(&builder, "- Top K: `%d`\n", report.TopK)
	fmt.Fprintf(&builder, "- Decision: **%s**\n\n", report.Decision)
	builder.WriteString("## Summary\n\n")
	builder.WriteString("| Engine | Recall@K | MRR | nDCG@K | p50 us | p95 us | max us | errors |\n")
	builder.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	fmt.Fprintf(&builder, "| PostgreSQL BM25 | %.4f | %.4f | %.4f | %d | %d | %d | %d |\n",
		report.BM25.MeanRecallAtK, report.BM25.MeanMRR, report.BM25.MeanNDCGAtK,
		report.BM25.LatencyP50US, report.BM25.LatencyP95US, report.BM25.LatencyMaxUS, report.BM25.Errors)
	fmt.Fprintf(&builder, "| mixin-search shadow | %.4f | %.4f | %.4f | %d | %d | %d | %d |\n\n",
		report.Shadow.MeanRecallAtK, report.Shadow.MeanMRR, report.Shadow.MeanNDCGAtK,
		report.Shadow.LatencyP50US, report.Shadow.LatencyP95US, report.Shadow.LatencyMaxUS, report.Shadow.Errors)
	builder.WriteString("## Correctness\n\n")
	fmt.Fprintf(&builder, "Permission=%d, lifecycle=%d, active-version=%d, formal-scope=%d.\n\n",
		report.Correctness.PermissionViolations, report.Correctness.LifecycleViolations,
		report.Correctness.ActiveVersionViolations, report.Correctness.FormalScopeMismatches)
	builder.WriteString("## Per query\n\n")
	builder.WriteString("| ID | Category | BM25 R/M/N | Shadow R/M/N | BM25 us | Shadow us |\n")
	builder.WriteString("| --- | --- | --- | --- | ---: | ---: |\n")
	for _, query := range report.Queries {
		fmt.Fprintf(&builder, "| %s | %s | %.2f / %.2f / %.2f | %.2f / %.2f / %.2f | %d | %d |\n",
			query.ID, query.Category, query.BM25Metrics.RecallAtK, query.BM25Metrics.MRR, query.BM25Metrics.NDCGAtK,
			query.ShadowMetrics.RecallAtK, query.ShadowMetrics.MRR, query.ShadowMetrics.NDCGAtK,
			query.BM25LatencyUS, query.ShadowLatencyUS)
	}
	builder.WriteString("\n## Decision reasons\n\n")
	for _, reason := range report.DecisionReasons {
		fmt.Fprintf(&builder, "- %s\n", reason)
	}
	return builder.String()
}
