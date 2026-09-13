package documentpipeline

import (
	"context"
	"os"
	"regexp"
	"strings"
	"unicode"
)

// MarkdownPipeline preserves heading paths and code blocks while removing
// markup that does not improve retrieval.
type MarkdownPipeline struct{}

func (MarkdownPipeline) Format() string { return FormatMarkdown }
func (MarkdownPipeline) Extensions() []string {
	return []string{".md", ".markdown"}
}

func (MarkdownPipeline) Load(ctx context.Context, path string) ([]Block, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return (MarkdownPipeline{}).LoadBytes(ctx, path, content)
}

func (MarkdownPipeline) LoadBytes(ctx context.Context, _ string, content []byte) ([]Block, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return parseMarkdownBlocks(string(content)), nil
}

var (
	markdownLinkPattern      = regexp.MustCompile(`!?\[([^]]*)\]\([^)]+\)`)
	markdownReferencePattern = regexp.MustCompile(`\[([^]]+)\]\[[^]]*\]`)
	markdownHTMLPattern      = regexp.MustCompile(`<[^>]+>`)
	markdownListPattern      = regexp.MustCompile(`^\d+[.)]\s+`)
)

func parseMarkdownBlocks(content string) []Block {
	content = strings.TrimPrefix(content, "\ufeff")
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				lines = lines[i+1:]
				break
			}
		}
	}

	var blocks []Block
	var pending []string
	var headings [6]string
	section := ""
	kind := "paragraph"
	inCode := false
	flush := func() {
		if len(pending) == 0 {
			return
		}
		separator := " "
		if kind == "code" {
			separator = "\n"
		}
		text := strings.TrimSpace(strings.Join(pending, separator))
		if text != "" {
			blocks = append(blocks, Block{Kind: kind, Section: section, Text: text})
		}
		pending = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			flush()
			inCode = !inCode
			if inCode {
				kind = "code"
			} else {
				kind = "paragraph"
			}
			continue
		}
		if inCode {
			pending = append(pending, line)
			continue
		}
		if level, text, ok := markdownHeading(line); ok {
			flush()
			for i := level; i < len(headings); i++ {
				headings[i] = ""
			}
			headings[level-1] = cleanMarkdownInline(text)
			section = joinHeadings(headings[:level])
			blocks = append(blocks, Block{Kind: "heading", Section: section, Text: headings[level-1]})
			continue
		}
		if trimmed == "" {
			flush()
			continue
		}
		line = cleanMarkdownInline(trimMarkdownBlockPrefix(line))
		if line != "" {
			pending = append(pending, line)
		}
	}
	flush()
	return blocks
}

func markdownHeading(line string) (int, string, bool) {
	line = strings.TrimLeft(line, " \t")
	level := 0
	for level < len(line) && level < 6 && line[level] == '#' {
		level++
	}
	if level == 0 || len(line) == level || !unicode.IsSpace(rune(line[level])) {
		return 0, "", false
	}
	text := strings.TrimSpace(line[level:])
	text = strings.TrimSpace(strings.TrimRight(text, "#"))
	return level, text, text != ""
}

func trimMarkdownBlockPrefix(line string) string {
	line = strings.TrimSpace(line)
	for strings.HasPrefix(line, ">") {
		line = strings.TrimSpace(strings.TrimPrefix(line, ">"))
	}
	if len(line) >= 2 && strings.ContainsRune("-*+", rune(line[0])) && unicode.IsSpace(rune(line[1])) {
		line = strings.TrimSpace(line[2:])
	}
	return markdownListPattern.ReplaceAllString(line, "")
}

func cleanMarkdownInline(text string) string {
	text = markdownLinkPattern.ReplaceAllString(text, "$1")
	text = markdownReferencePattern.ReplaceAllString(text, "$1")
	text = markdownHTMLPattern.ReplaceAllString(text, " ")
	replacer := strings.NewReplacer("**", "", "__", "", "~~", "", "`", "", "*", "", "_", "")
	return strings.Join(strings.Fields(replacer.Replace(text)), " ")
}
