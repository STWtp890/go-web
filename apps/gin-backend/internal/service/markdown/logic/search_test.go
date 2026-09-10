package logic

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeSearchKeyword(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "trim spaces", input: "  Go 全文搜索  ", want: "Go 全文搜索"},
		{name: "maximum length", input: strings.Repeat("文", MaxSearchKeywordLength), want: strings.Repeat("文", MaxSearchKeywordLength)},
		{name: "blank", input: " \n\t ", wantErr: true},
		{name: "too long", input: strings.Repeat("文", MaxSearchKeywordLength+1), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeSearchKeyword(tt.input)
			if tt.wantErr {
				if !errors.Is(err, ErrMarkdownInvalidInput) {
					t.Fatalf("NormalizeSearchKeyword() error = %v, want ErrMarkdownInvalidInput", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeSearchKeyword() unexpected error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeSearchKeyword() = %q, want %q", got, tt.want)
			}
		})
	}
}
