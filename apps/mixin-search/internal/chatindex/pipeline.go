package chatindex

import (
	"context"
	"errors"
	"strings"

	documentpipeline "mixin-search/document_pipeline"
)

// chatPipeline turns one chat message into indexable blocks.
//
// The chat corpus deliberately does not reuse the Markdown or DOCX pipelines:
// a message has no headings, no document structure and no file source, so
// treating it as Markdown would let a message that starts with "#" become a
// heading and change how it is chunked and cited. The guide requires the two
// corpora to use different split strategies, and this is that difference.
type chatPipeline struct{}

// chatExtension selects this pipeline. It is not a real file format: it is the
// key the shared ingest workflow uses to route a message to the chat splitter.
const chatExtension = ".chat"

func (chatPipeline) Format() string { return "chat" }

func (chatPipeline) Extensions() []string { return []string{chatExtension} }

// Load refuses file input: chat messages arrive from py-agent, never from disk.
func (chatPipeline) Load(context.Context, string) ([]documentpipeline.Block, error) {
	return nil, errors.New("chat messages are indexed from submitted content, not from files")
}

func (chatPipeline) LoadBytes(_ context.Context, _ string, content []byte) ([]documentpipeline.Block, error) {
	text := strings.TrimSpace(string(content))
	if text == "" {
		return nil, nil
	}
	// A single paragraph block: one message is one logical unit, and the shared
	// workflow applies the chunk size to it.
	return []documentpipeline.Block{{Kind: "paragraph", Text: text}}, nil
}
