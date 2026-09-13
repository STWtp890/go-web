package documentpipeline

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
)

// DOCXPipeline extracts headings, paragraphs, and table-cell paragraphs from
// the main WordprocessingML document part.
type DOCXPipeline struct{}

func (DOCXPipeline) Format() string       { return FormatDOCX }
func (DOCXPipeline) Extensions() []string { return []string{".docx"} }

func (DOCXPipeline) Load(ctx context.Context, path string) ([]Block, error) {
	document, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer document.Close()
	return loadDOCXArchive(ctx, document.File)
}

func (DOCXPipeline) LoadBytes(ctx context.Context, _ string, content []byte) ([]Block, error) {
	document, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, err
	}
	return loadDOCXArchive(ctx, document.File)
}

func loadDOCXArchive(ctx context.Context, files []*zip.File) ([]Block, error) {
	for _, file := range files {
		if filepath.ToSlash(file.Name) != "word/document.xml" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return nil, err
		}
		blocks, parseErr := parseDOCXBlocks(ctx, reader)
		closeErr := reader.Close()
		if parseErr != nil {
			return nil, parseErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		return blocks, nil
	}
	return nil, errors.New("word/document.xml is missing")
}

func parseDOCXBlocks(ctx context.Context, reader io.Reader) ([]Block, error) {
	decoder := xml.NewDecoder(reader)
	var blocks []Block
	var paragraph strings.Builder
	var headings [6]string
	section := ""
	style := ""
	outlineLevel := ""
	inParagraph := false
	textDepth := 0
	tableDepth := 0

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse word/document.xml: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "tbl":
				tableDepth++
			case "p":
				inParagraph = true
				paragraph.Reset()
				style = ""
				outlineLevel = ""
			case "pStyle":
				if inParagraph {
					style = xmlAttribute(value.Attr, "val")
				}
			case "outlineLvl":
				if inParagraph {
					outlineLevel = xmlAttribute(value.Attr, "val")
				}
			case "t":
				if inParagraph {
					textDepth++
				}
			case "tab":
				if inParagraph {
					paragraph.WriteByte('\t')
				}
			case "br", "cr":
				if inParagraph {
					paragraph.WriteByte('\n')
				}
			}
		case xml.CharData:
			if inParagraph && textDepth > 0 {
				paragraph.Write([]byte(value))
			}
		case xml.EndElement:
			switch value.Name.Local {
			case "t":
				if textDepth > 0 {
					textDepth--
				}
			case "p":
				text := normalizeWordText(paragraph.String())
				if text != "" {
					if level, ok := wordHeadingLevel(style, outlineLevel); ok {
						for i := level; i < len(headings); i++ {
							headings[i] = ""
						}
						headings[level-1] = text
						section = joinHeadings(headings[:level])
						blocks = append(blocks, Block{Kind: "heading", Section: section, Text: text})
					} else {
						kind := "paragraph"
						if tableDepth > 0 {
							kind = "table"
						}
						blocks = append(blocks, Block{Kind: kind, Section: section, Text: text})
					}
				}
				inParagraph = false
				textDepth = 0
			case "tbl":
				if tableDepth > 0 {
					tableDepth--
				}
			}
		}
	}
	return blocks, nil
}

func xmlAttribute(attributes []xml.Attr, localName string) string {
	for _, attribute := range attributes {
		if attribute.Name.Local == localName {
			return attribute.Value
		}
	}
	return ""
}

func wordHeadingLevel(style string, outlineLevel string) (int, bool) {
	if outlineLevel != "" {
		if level, err := strconv.Atoi(outlineLevel); err == nil && level >= 0 && level < 6 {
			return level + 1, true
		}
	}
	lower := strings.ToLower(style)
	for _, prefix := range []string{"heading", "title", "标题"} {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		suffix := strings.TrimSpace(strings.TrimPrefix(lower, prefix))
		if level, err := strconv.Atoi(suffix); err == nil && level >= 1 && level <= 6 {
			return level, true
		}
		return 1, true
	}
	return 0, false
}

func normalizeWordText(text string) string {
	text = strings.ReplaceAll(text, "\u00a0", " ")
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.Join(strings.Fields(lines[i]), " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
