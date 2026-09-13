package application

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateContentCommand(t *testing.T) {
	tests := []struct {
		name    string
		ownerID int64
		title   string
		content string
		wantErr bool
	}{
		{name: "valid", ownerID: 1, title: " 标题 ", content: "正文"},
		{name: "missing owner", title: "标题", content: "正文", wantErr: true},
		{name: "blank title", ownerID: 1, title: "  ", content: "正文", wantErr: true},
		{name: "long title", ownerID: 1, title: strings.Repeat("文", 256), content: "正文", wantErr: true},
		{name: "blank content", ownerID: 1, title: "标题", content: "\n\t", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			title, _, err := validateContentCommand(test.ownerID, test.title, test.content)
			if test.wantErr {
				if !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("error = %v, want ErrInvalidInput", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate command: %v", err)
			}
			if title != "标题" {
				t.Fatalf("normalized title = %q, want 标题", title)
			}
		})
	}
}

func TestBuildSummaryUsesRunes(t *testing.T) {
	content := "  " + strings.Repeat("文", summaryMaxRunes+5) + "  "
	summary := buildSummary(content)
	if got := len([]rune(summary)); got != summaryMaxRunes {
		t.Fatalf("summary rune count = %d, want %d", got, summaryMaxRunes)
	}
}

func TestValidateDocumentIDCanonicalizesUUID(t *testing.T) {
	const upper = "018F3F0E-7B20-7000-8000-000000000102"
	got, err := validateDocumentID(" " + upper + " ")
	if err != nil {
		t.Fatalf("validate document id: %v", err)
	}
	if got != strings.ToLower(upper) {
		t.Fatalf("document id = %s, want %s", got, strings.ToLower(upper))
	}
}
