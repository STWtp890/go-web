package documentpipeline

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/EndFirstCorp/doc2txt"
)

type legacyDOCParser func(io.Reader) (io.Reader, error)

// DOCPipeline extracts plain text from the legacy OLE Word binary format.
type DOCPipeline struct {
	parse legacyDOCParser
}

func (DOCPipeline) Format() string       { return FormatDOC }
func (DOCPipeline) Extensions() []string { return []string{".doc"} }

func (pipeline DOCPipeline) Load(ctx context.Context, path string) ([]Block, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return pipeline.loadReader(ctx, file)
}

func (pipeline DOCPipeline) LoadBytes(ctx context.Context, _ string, content []byte) ([]Block, error) {
	return pipeline.loadReader(ctx, bytes.NewReader(content))
}

func (pipeline DOCPipeline) loadReader(ctx context.Context, source io.Reader) ([]Block, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parser := pipeline.parse
	if parser == nil {
		parser = doc2txt.ParseDoc
	}
	reader, err := parser(source)
	if err != nil {
		return nil, err
	}
	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return splitPlainTextBlocks(string(content)), nil
}

func splitPlainTextBlocks(content string) []Block {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.NewReplacer("\r", "\n", "\f", "\n\n", "\v", "\n").Replace(content)
	content = strings.Map(func(value rune) rune {
		if value == '\n' || value == '\t' || unicode.IsPrint(value) {
			return value
		}
		return -1
	}, content)

	var blocks []Block
	var paragraph []string
	flush := func() {
		text := strings.Join(paragraph, " ")
		text = strings.Join(strings.Fields(text), " ")
		if text != "" {
			blocks = append(blocks, Block{Kind: "paragraph", Text: text})
		}
		paragraph = nil
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			flush()
			continue
		}
		paragraph = append(paragraph, line)
	}
	flush()
	return blocks
}
