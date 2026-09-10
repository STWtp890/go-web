package utils

import (
	"strings"
)

const (
	// summaryMaxLen summary 取正文前 N 个字符
	summaryMaxLen = 100
)

// BuildSummary 从正文生成摘要: 去除首尾空白后取前 summaryMaxLen 个字符
// :Param
// - `content` 完整 Markdown 正文
// :Return
// - `string` 摘要文本 (空正文返回空串)
func BuildSummary(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}

	runes := []rune(trimmed)
	if len(runes) > summaryMaxLen {
		runes = runes[:summaryMaxLen]
	}
	return string(runes)
}
