package documentpipeline

import "strings"

func joinHeadings(headings []string) string {
	values := make([]string, 0, len(headings))
	for _, heading := range headings {
		if heading != "" {
			values = append(values, heading)
		}
	}
	return strings.Join(values, " / ")
}
