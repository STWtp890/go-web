package application

import (
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// The vector flow's embedding function.
//
// The profile is the local hash embedding the previous implementation used
// (apps/mixin-search/internal/rag/embedding.go), taken over unchanged so the two
// produce the same vector for the same text: lower-cased unigrams plus adjacent
// bigrams, each feature hashed with FNV-1a 64 into one of 64 signed buckets, and
// the result L2-normalized. It is deliberately model-free - the takeover was
// about moving the flow, not about changing what it embeds, and a hash embedding
// keeps the vector index reproducible on any machine with no external service.
//
// The profile name is stored in document_search.index_generations and in every
// document_index row that was vectorized, because a vector is only meaningful
// together with the function that produced it: a different profile is a
// different index generation, never a silent reinterpretation of stored vectors.

// VectorProfileLocalHashV1 names the local hash embedding profile.
const VectorProfileLocalHashV1 = "local-hash-v1"

// localHashDimensions is the vector width of VectorProfileLocalHashV1. It is the
// same 64 dimensions the reference implementation used.
const localHashDimensions = 64

// embeddingProfiles is the closed set of profiles this build can produce. A
// profile that is not here cannot be opened: writing or reading vectors under an
// unknown function would compare unrelated numbers.
var embeddingProfiles = map[string]int{
	VectorProfileLocalHashV1: localHashDimensions,
}

// profileDimensions reports the vector width of a profile, or 0 when this build
// does not implement it.
func profileDimensions(profile string) int {
	return embeddingProfiles[strings.TrimSpace(profile)]
}

// embedText returns the vector of one document chunk under the local hash
// profile. It is a pure function of the text: the same text always produces the
// same vector, which is what lets a rebuild reproduce a collection byte for byte
// without asking the fact source for anything.
func embedText(text string) []float64 {
	tokens := tokenize(text)
	features := make([]string, 0, len(tokens)*2)
	features = append(features, tokens...)
	for index := 0; index+1 < len(tokens); index++ {
		features = append(features, tokens[index]+"\x00"+tokens[index+1])
	}

	vector := make([]float64, localHashDimensions)
	for _, feature := range features {
		hash := fnv.New64a()
		_, _ = hash.Write([]byte(feature))
		sum := hash.Sum64()
		sign := 1.0
		if sum&(1<<63) != 0 {
			sign = -1
		}
		vector[int(sum%localHashDimensions)] += sign
	}

	var norm float64
	for _, value := range vector {
		norm += value * value
	}
	if norm == 0 {
		return vector
	}
	norm = math.Sqrt(norm)
	for index := range vector {
		vector[index] /= norm
	}
	return vector
}

// embedText32 narrows an embedding to the width Qdrant stores. The conversion is
// the last step so the arithmetic above stays in float64, exactly as the
// reference implementation does it.
func embedText32(text string) []float32 {
	vector := embedText(text)
	wide := make([]float32, len(vector))
	for index, value := range vector {
		wide[index] = float32(value)
	}
	return wide
}

// tokenize splits text the way the profile does: Han characters are single
// tokens (each character carries meaning, and there is no dictionary here),
// letters and digits form words, and everything else separates tokens.
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

	for _, character := range strings.ToLower(text) {
		switch {
		case unicode.Is(unicode.Han, character):
			flush()
			tokens = append(tokens, string(character))
		case unicode.IsLetter(character) || unicode.IsDigit(character):
			word.WriteRune(character)
		default:
			flush()
		}
	}
	flush()
	return tokens
}
