package rag

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"

	"github.com/cloudwego/eino/components/embedding"
)

const localEmbeddingDimensions = 64

// LocalEmbedder is deterministic and dependency-free. It exists only so the
// example can execute the vectorization path without an external model.
type LocalEmbedder struct{}

// EmbedStrings implements Eino's embedding.Embedder component interface.
func (LocalEmbedder) EmbedStrings(
	_ context.Context,
	texts []string,
	_ ...embedding.Option,
) ([][]float64, error) {
	vectors := make([][]float64, len(texts))
	for i, text := range texts {
		vectors[i] = embedOne(text)
	}
	return vectors, nil
}

func embedOne(text string) []float64 {
	tokens := tokenize(text)
	features := make([]string, 0, len(tokens)*2)
	features = append(features, tokens...)
	for i := 0; i+1 < len(tokens); i++ {
		features = append(features, tokens[i]+"\x00"+tokens[i+1])
	}

	vector := make([]float64, localEmbeddingDimensions)
	for _, feature := range features {
		h := fnv.New64a()
		_, _ = h.Write([]byte(feature))
		sum := h.Sum64()
		index := int(sum % localEmbeddingDimensions)
		sign := 1.0
		if sum&(1<<63) != 0 {
			sign = -1
		}
		vector[index] += sign
	}

	var norm float64
	for _, value := range vector {
		norm += value * value
	}
	if norm == 0 {
		return vector
	}
	norm = math.Sqrt(norm)
	for i := range vector {
		vector[i] /= norm
	}
	return vector
}

func tokenize(text string) []string {
	var tokens []string
	var word strings.Builder
	flush := func() {
		if word.Len() == 0 {
			return
		}
		tokens = append(tokens, word.String())
		word.Reset()
	}

	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.Is(unicode.Han, r):
			flush()
			tokens = append(tokens, string(r))
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return tokens
}
