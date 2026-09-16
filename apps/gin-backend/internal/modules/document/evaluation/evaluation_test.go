package evaluation

import (
	"math"
	"testing"
	"time"
)

func TestScoreUsesGradedRankPosition(t *testing.T) {
	metrics := Score([]string{"noise", "relevant", "other"}, []string{"relevant"}, 3)
	if metrics.RecallAtK != 1 || metrics.MRR != 0.5 {
		t.Fatalf("metrics = %#v", metrics)
	}
	wantNDCG := 1 / math.Log2(3)
	if math.Abs(metrics.NDCGAtK-wantNDCG) > 1e-9 {
		t.Fatalf("nDCG = %f, want %f", metrics.NDCGAtK, wantNDCG)
	}
}

func TestReportKeepsBM25WhenSemanticGateFails(t *testing.T) {
	report := Report{
		EmbeddingProfile: "production-semantic-v1",
		Shadow:           EngineSummary{MeanRecallAtK: 1, MeanMRR: 1, MeanNDCGAtK: 1},
		Queries:          []QueryResult{{Category: "semantic_expression", ShadowMetrics: Metrics{RecallAtK: 0}}},
	}
	report.Decide(time.Second)
	if report.Decision != "KEEP_BM25" {
		t.Fatalf("decision = %s", report.Decision)
	}
}

func TestReportKeepsBM25ForLocalHashEmbedding(t *testing.T) {
	report := Report{
		EmbeddingProfile: "local-hash-v1",
		Shadow:           EngineSummary{MeanRecallAtK: 1, MeanMRR: 1, MeanNDCGAtK: 1},
		Queries:          []QueryResult{{Category: "semantic_expression", ShadowMetrics: Metrics{RecallAtK: 1}}},
	}
	report.Decide(time.Second)
	if report.Decision != "KEEP_BM25" {
		t.Fatalf("decision = %s", report.Decision)
	}
}
