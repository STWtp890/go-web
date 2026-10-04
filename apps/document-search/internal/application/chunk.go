package application

import (
	"sort"
	"strings"
)

// Chunking is derived data: the index stores how many chunks a piece of content
// would produce for the configured profile, and a later vector projection can
// reuse exactly this split. It is deliberately a pure function of the content
// and the profile bounds, so the same content always reports the same count and
// a rebuild cannot disagree with the live index.

// chunkRuneLimit is the target size of one derived chunk, in characters.
const chunkRuneLimit = 800

// chunkLimitForProfile returns the chunk size bound for an index profile. An
// unknown or empty profile uses the default bound instead of failing a write:
// the profile is a hint about how to split content, and the count stays stable
// for the same content either way. The parameter exists so a future profile can
// change the bound without changing the call sites or the stored count of
// already indexed documents.
func chunkLimitForProfile(profile string) int {
	if strings.TrimSpace(profile) == "" {
		return chunkRuneLimit
	}
	return chunkRuneLimit
}

// chunkCount reports how many chunks the content produces under the profile.
// Content that is empty or only whitespace produces no chunks.
func chunkCount(content, profile string, maxChunks int) int {
	return len(chunkText(content, profile, maxChunks))
}

// chunkText splits content into deterministic chunks: paragraphs (blank-line
// separated) are packed in order up to the profile bound, an oversized paragraph
// is cut on the bound, and the result is truncated at maxChunks so one very
// large document cannot exhaust the index.
func chunkText(content, profile string, maxChunks int) []string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	paragraphs := splitParagraphs(normalized)
	if len(paragraphs) == 0 {
		return nil
	}
	limit := chunkLimitForProfile(profile)
	if limit <= 0 {
		limit = chunkRuneLimit
	}
	chunks := make([]string, 0, len(paragraphs))
	current := make([]string, 0, 4)
	currentLength := 0
	flush := func() {
		if len(current) == 0 {
			return
		}
		chunks = append(chunks, strings.Join(current, "\n\n"))
		current = current[:0]
		currentLength = 0
	}
	for _, paragraph := range paragraphs {
		for _, piece := range splitByRunes(paragraph, limit) {
			pieceLength := len([]rune(piece))
			separator := 0
			if len(current) > 0 {
				separator = 2
			}
			if currentLength+separator+pieceLength > limit {
				flush()
			}
			current = append(current, piece)
			currentLength += separator + pieceLength
			if maxChunks > 0 && len(chunks) >= maxChunks {
				return chunks
			}
		}
	}
	flush()
	if maxChunks > 0 && len(chunks) > maxChunks {
		chunks = chunks[:maxChunks]
	}
	return chunks
}

// splitParagraphs splits on blank lines and drops empty paragraphs.
func splitParagraphs(content string) []string {
	raw := strings.Split(content, "\n\n")
	result := make([]string, 0, len(raw))
	for _, paragraph := range raw {
		trimmed := strings.TrimSpace(paragraph)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	return result
}

// splitByRunes cuts a paragraph that exceeds the bound into bound-sized pieces.
func splitByRunes(value string, limit int) []string {
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return []string{value}
	}
	pieces := make([]string, 0, len(runes)/limit+1)
	for start := 0; start < len(runes); start += limit {
		end := start + limit
		if end > len(runes) {
			end = len(runes)
		}
		pieces = append(pieces, string(runes[start:end]))
	}
	return pieces
}

// canonicalIDs normalizes an identifier set the way both the index and the
// capability have to agree on: trimmed, lower-cased, de-duplicated and sorted.
// PostgreSQL renders uuid values in lower case, so comparing a stored identifier
// with a granted one only works if both sides are canonicalized first.
func canonicalIDs(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed == "" {
			continue
		}
		if _, duplicate := seen[trimmed]; duplicate {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	sort.Strings(result)
	return result
}

// uuidIDs keeps only the canonical identifiers that are valid UUIDs. A granted
// identifier that is not a UUID cannot match any indexed row, so dropping it
// narrows the effective scope, which is the fail-closed direction.
func uuidIDs(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range canonicalIDs(values) {
		normalized, err := normalizeIdentifier(value)
		if err != nil {
			continue
		}
		result = append(result, normalized)
	}
	return result
}
