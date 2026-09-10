package logic

import (
	"errors"
	"testing"

	"gin-backend/internal/service/markdown/types/requests"
)

func TestValidateMutationInput(t *testing.T) {
	tests := []struct {
		name       string
		authorID   string
		markdownID string
		request    *requests.UpdateMarkdownRequest
		wantErr    bool
	}{
		{name: "valid public", authorID: "1", markdownID: "markdown-1", request: &requests.UpdateMarkdownRequest{Title: "标题", Content: "正文", Visibility: "public"}},
		{name: "valid private", authorID: "1", markdownID: "markdown-1", request: &requests.UpdateMarkdownRequest{Title: "标题", Content: "正文", Visibility: "private"}},
		{name: "missing author", markdownID: "markdown-1", request: &requests.UpdateMarkdownRequest{Title: "标题", Content: "正文", Visibility: "private"}, wantErr: true},
		{name: "missing markdown", authorID: "1", request: &requests.UpdateMarkdownRequest{Title: "标题", Content: "正文", Visibility: "private"}, wantErr: true},
		{name: "missing request", authorID: "1", markdownID: "markdown-1", wantErr: true},
		{name: "blank title", authorID: "1", markdownID: "markdown-1", request: &requests.UpdateMarkdownRequest{Title: "  ", Content: "正文", Visibility: "private"}, wantErr: true},
		{name: "blank content", authorID: "1", markdownID: "markdown-1", request: &requests.UpdateMarkdownRequest{Title: "标题", Content: "\n\t", Visibility: "private"}, wantErr: true},
		{name: "invalid visibility", authorID: "1", markdownID: "markdown-1", request: &requests.UpdateMarkdownRequest{Title: "标题", Content: "正文", Visibility: "friends"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMutationInput(tt.authorID, tt.markdownID, tt.request)
			if tt.wantErr && !errors.Is(err, ErrMarkdownInvalidInput) {
				t.Fatalf("validateMutationInput() error = %v, want ErrMarkdownInvalidInput", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateMutationInput() unexpected error = %v", err)
			}
		})
	}
}
