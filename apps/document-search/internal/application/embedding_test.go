package application

import (
	"math"
	"testing"
)

// The golden vectors below were produced by running the reference implementation
// verbatim: apps/mixin-search/internal/rag/embedding.go, copied into a throwaway
// main package with only a printer added, over exactly these texts. Pinning them
// is what makes "the same profile as the old local hash embedding" a checked
// fact instead of a claim: a change to the tokenizer, the hash, the bucket count
// or the normalization order moves one of these numbers.
//
// Only the non-zero buckets are listed; every other bucket is required to be
// exactly zero.
func TestLocalHashProfileMatchesTheReferenceImplementation(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		tokens  int
		nonZero map[int]float64
	}{
		{
			name:   "english",
			text:   "Reliable Event Delivery\n\nA transactional outbox commits business facts and delivery intent together, then retries failed dispatches.",
			tokens: 17,
			nonZero: map[int]float64{
				0: -0.15617376188860607, 1: 0.15617376188860607, 6: -0.15617376188860607,
				10: 0.15617376188860607, 11: -0.15617376188860607, 12: -0.15617376188860607,
				13: 0.31234752377721214, 14: 0.15617376188860607, 15: -0.15617376188860607,
				17: -0.15617376188860607, 19: -0.15617376188860607, 24: 0.15617376188860607,
				26: -0.15617376188860607, 27: 0.15617376188860607, 29: 0.15617376188860607,
				31: 0.15617376188860607, 35: -0.15617376188860607, 42: -0.15617376188860607,
				52: -0.15617376188860607, 53: 0.62469504755442429, 58: -0.15617376188860607,
				59: -0.15617376188860607, 62: -0.15617376188860607,
			},
		},
		{
			name:   "chinese",
			text:   "使用 goroutine、channel 与 context 组织并发任务，并通过取消信号及时释放资源。",
			tokens: 25,
			nonZero: map[int]float64{
				0: 0.12598815766974239, 2: 0.12598815766974239, 4: 0.25197631533948478,
				5: -0.12598815766974239, 6: 0.12598815766974239, 15: 0.12598815766974239,
				20: 0.12598815766974239, 22: 0.12598815766974239, 24: 0.12598815766974239,
				25: 0.25197631533948478, 28: -0.12598815766974239, 35: -0.12598815766974239,
				36: -0.12598815766974239, 38: -0.50395263067896956, 41: 0.12598815766974239,
				42: -0.25197631533948478, 43: 0.25197631533948478, 44: 0.12598815766974239,
				45: 0.12598815766974239, 46: 0.12598815766974239, 47: 0.12598815766974239,
				50: 0.12598815766974239, 52: 0.12598815766974239, 53: 0.12598815766974239,
				54: 0.12598815766974239, 55: 0.25197631533948478, 57: 0.25197631533948478,
				58: 0.12598815766974239, 60: 0.12598815766974239, 63: 0.12598815766974239,
			},
		},
		{
			name:   "code",
			text:   "ctx, cancel := context.WithTimeout(parent, 750*time.Millisecond)\ndefer cancel()",
			tokens: 10,
			nonZero: map[int]float64{
				11: 0.30151134457776363,
				31: -0.30151134457776363,
				36: -0.30151134457776363,
				38: -0.30151134457776363,
				39: 0.30151134457776363,
				40: 0.30151134457776363,
				42: 0.30151134457776363,
				45: 0.30151134457776363,
				59: 0.30151134457776363,
				60: 0.30151134457776363,
				62: -0.30151134457776363,
			},
		},
		{
			name:   "mixed",
			text:   "并发控制 Guide v2.1",
			tokens: 7,
			nonZero: map[int]float64{
				1:  0.2581988897471611,
				4:  0.2581988897471611,
				19: 0.2581988897471611,
				20: 0.2581988897471611,
				21: 0.2581988897471611,
				38: -0.5163977794943222,
				42: 0.2581988897471611,
				43: 0.2581988897471611,
				53: -0.2581988897471611,
				54: 0.2581988897471611,
				55: 0.2581988897471611,
				60: -0.2581988897471611,
			},
		},
		{
			name:    "empty",
			text:    "",
			tokens:  0,
			nonZero: map[int]float64{},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if tokens := tokenize(testCase.text); len(tokens) != testCase.tokens {
				t.Fatalf("tokenize(%q) produced %d tokens, want %d", testCase.text, len(tokens), testCase.tokens)
			}
			vector := embedText(testCase.text)
			if len(vector) != localHashDimensions {
				t.Fatalf("embedding has %d dimensions, want %d", len(vector), localHashDimensions)
			}
			for index, value := range vector {
				want, listed := testCase.nonZero[index]
				if !listed {
					want = 0
				}
				if math.Abs(value-want) > 1e-15 {
					t.Fatalf("bucket %d is %v, want %v (the profile no longer matches the reference implementation)",
						index, value, want)
				}
			}
		})
	}
}

// TestEmbedTextIsDeterministicAndNormalized pins the two properties the rebuild
// and the collection depend on: the same text always produces the same vector,
// and a non-empty one is a unit vector (Qdrant stores cosine vectors).
func TestEmbedTextIsDeterministicAndNormalized(t *testing.T) {
	text := "A transactional outbox commits business facts and delivery intent together."
	first := embedText(text)
	second := embedText(text)
	for index := range first {
		if first[index] != second[index] {
			t.Fatalf("bucket %d changed between two embeddings of the same text: %v -> %v",
				index, first[index], second[index])
		}
	}
	if different := embedText(text + " retries failed dispatches."); sameVector(first, different) {
		t.Fatal("two different texts produced the same vector")
	}

	var norm float64
	for _, value := range first {
		norm += value * value
	}
	if math.Abs(math.Sqrt(norm)-1) > 1e-12 {
		t.Fatalf("the vector's norm is %v, want 1", math.Sqrt(norm))
	}

	// A text with no features at all is the zero vector, which is why the write
	// path refuses to store a point for it: a zero vector is not a valid cosine
	// point.
	empty := embedText("   ... --- ")
	for index, value := range empty {
		if value != 0 {
			t.Fatalf("a featureless text produced a non-zero bucket %d: %v", index, value)
		}
	}
}

// TestEmbedText32IsTheNarrowedVector pins the conversion the collection receives.
func TestEmbedText32IsTheNarrowedVector(t *testing.T) {
	text := "北斗索引运行手册"
	wide := embedText(text)
	narrow := embedText32(text)
	if len(narrow) != len(wide) {
		t.Fatalf("embedText32 has %d dimensions, want %d", len(narrow), len(wide))
	}
	for index, value := range wide {
		if narrow[index] != float32(value) {
			t.Fatalf("bucket %d is %v, want %v", index, narrow[index], float32(value))
		}
	}
}

// TestProfileDimensionsIsAClosedSet pins that only implemented profiles open a
// collection: an unknown profile would compare vectors produced by different
// functions.
func TestProfileDimensionsIsAClosedSet(t *testing.T) {
	if dimensions := profileDimensions(VectorProfileLocalHashV1); dimensions != localHashDimensions {
		t.Fatalf("profile %q has %d dimensions, want %d", VectorProfileLocalHashV1, dimensions, localHashDimensions)
	}
	for _, unknown := range []string{"", "  ", "openai-text-embedding-3-small", "local-hash-v2"} {
		if dimensions := profileDimensions(unknown); dimensions != 0 {
			t.Fatalf("unknown profile %q reported %d dimensions, want 0", unknown, dimensions)
		}
	}
}

// sameVector reports whether two embeddings are identical.
func sameVector(left, right []float64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
