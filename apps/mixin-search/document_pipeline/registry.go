package documentpipeline

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Registry selects a pipeline by the lower-case file extension.
type Registry struct {
	byExtension map[string]Pipeline
}

func NewRegistry() *Registry {
	registry := &Registry{byExtension: make(map[string]Pipeline)}
	for _, pipeline := range []Pipeline{
		MarkdownPipeline{},
		DOCXPipeline{},
		DOCPipeline{},
	} {
		if err := registry.Register(pipeline); err != nil {
			panic(err)
		}
	}
	return registry
}

func (r *Registry) Register(pipeline Pipeline) error {
	if pipeline == nil {
		return errors.New("document pipeline is required")
	}
	if r.byExtension == nil {
		r.byExtension = make(map[string]Pipeline)
	}
	if strings.TrimSpace(pipeline.Format()) == "" {
		return errors.New("document pipeline format is required")
	}
	for _, extension := range pipeline.Extensions() {
		extension = strings.ToLower(strings.TrimSpace(extension))
		if extension == "" || extension[0] != '.' {
			return fmt.Errorf("invalid document extension %q", extension)
		}
		r.byExtension[extension] = pipeline
	}
	return nil
}

// Prepare loads a local file and returns a format-neutral document.
func (r *Registry) Prepare(ctx context.Context, request FileRequest) (LoadedDocument, error) {
	path := strings.TrimSpace(request.Path)
	if path == "" {
		return LoadedDocument{}, errors.New("document path is required")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return LoadedDocument{}, fmt.Errorf("resolve document path: %w", err)
	}
	pipeline, extension, err := r.pipelineFor(absolutePath)
	if err != nil {
		return LoadedDocument{}, err
	}
	blocks, err := pipeline.Load(ctx, absolutePath)
	if err != nil {
		return LoadedDocument{}, fmt.Errorf("load %s document: %w", pipeline.Format(), err)
	}
	return buildLoadedDocument(
		pipeline,
		extension,
		absolutePath,
		request.DocumentID,
		request.Title,
		blocks,
		request.ChunkSize,
		request.Overlap,
	)
}

// PrepareBytes selects a pipeline using the original filename and parses an
// uploaded document entirely in memory.
func (r *Registry) PrepareBytes(ctx context.Context, request BytesRequest) (LoadedDocument, error) {
	filename := strings.TrimSpace(request.Filename)
	if filename == "" {
		return LoadedDocument{}, errors.New("document filename is required")
	}
	if len(request.Content) == 0 {
		return LoadedDocument{}, errors.New("document content is required")
	}
	pipeline, extension, err := r.pipelineFor(filename)
	if err != nil {
		return LoadedDocument{}, err
	}
	bytesPipeline, ok := pipeline.(BytesPipeline)
	if !ok {
		return LoadedDocument{}, fmt.Errorf("%s pipeline does not support uploaded bytes", pipeline.Format())
	}
	blocks, err := bytesPipeline.LoadBytes(ctx, filename, request.Content)
	if err != nil {
		return LoadedDocument{}, fmt.Errorf("load %s document: %w", pipeline.Format(), err)
	}
	return buildLoadedDocument(
		pipeline,
		extension,
		filename,
		request.DocumentID,
		request.Title,
		blocks,
		request.ChunkSize,
		request.Overlap,
	)
}

func (r *Registry) pipelineFor(filename string) (Pipeline, string, error) {
	extension := strings.ToLower(filepath.Ext(filename))
	pipeline := r.byExtension[extension]
	if pipeline == nil {
		return nil, extension, fmt.Errorf("unsupported document extension %q; supported: .md, .markdown, .doc, .docx", extension)
	}
	return pipeline, extension, nil
}

func buildLoadedDocument(
	pipeline Pipeline,
	extension string,
	source string,
	documentID string,
	title string,
	blocks []Block,
	chunkSize int,
	overlap int,
) (LoadedDocument, error) {
	if len(blocks) == 0 {
		return LoadedDocument{}, errors.New("document contains no indexable text")
	}
	documentID = strings.TrimSpace(documentID)
	if documentID == "" {
		documentID = strings.TrimSuffix(filepath.Base(source), extension)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = firstHeading(blocks)
	}
	if title == "" {
		title = documentID
	}
	return LoadedDocument{
		ID:        documentID,
		Title:     title,
		Content:   joinBlocks(blocks),
		Format:    pipeline.Format(),
		Source:    source,
		Blocks:    blocks,
		ChunkSize: chunkSize,
		Overlap:   overlap,
	}, nil
}

func firstHeading(blocks []Block) string {
	for _, block := range blocks {
		if block.Kind == "heading" && strings.TrimSpace(block.Text) != "" {
			return strings.TrimSpace(block.Text)
		}
	}
	return ""
}

func joinBlocks(blocks []Block) string {
	values := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if text := strings.TrimSpace(block.Text); text != "" {
			values = append(values, text)
		}
	}
	return strings.Join(values, "\n\n")
}
