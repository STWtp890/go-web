package utils

import (
	"regexp"
	"strings"
)

func TruncateSummary(content string) string {
	s := StripMarkdown(content)
	if len(s) <= 100 {
		return s
	}
	return s[:100]
}

func StripMarkdown(s string) string {
	for _, re := range MarkdownStripPatterns {
		s = re.ReplaceAllString(s, "")
	}
	// 合并多余空白
	s = regexp.MustCompile(`\s+`).ReplaceAllString(strings.TrimSpace(s), " ")
	return s
}

// 用于编译期预编译正则，避免每次调用 TruncateSummary 都重新编译。
var MarkdownStripPatterns = []*regexp.Regexp{
	regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`),   // 图片 ![alt](url)
	regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`),  // 链接 [text](url)
	regexp.MustCompile("(?m)^#{1,6}\\s+"),        // 标题 # ## ...
	regexp.MustCompile(`\*{1,3}([^*]+?)\*{1,3}`), // *italic* **bold** ***bold italic***
	regexp.MustCompile("_{1,3}([^_]+?)_{1,3}"),   // _italic_ __bold__
	regexp.MustCompile("`{1,3}[^`]+`{1,3}"),      // 行内代码 `code` ```code```
	regexp.MustCompile(`~~([^~]+)~~`),            // ~~strikethrough~~
	regexp.MustCompile("(?m)^[>]+\\s+"),          // 引用 > >> ...
	regexp.MustCompile("(?m)^[\\s]*[-*+]\\s+"),   // 无序列表项
	regexp.MustCompile("(?m)^[\\s]*\\d+\\.\\s+"), // 有序列表项
	regexp.MustCompile("(?m)^[-*_]{3,}\\s*$"),    // 水平线 --- *** ___
}
