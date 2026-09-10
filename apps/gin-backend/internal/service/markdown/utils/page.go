package utils

// NormalizePage 归一化分页参数: page 至少 1, pageSize 落在 [1, 100]
func NormalizePage(page, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 10
	}
	return page, pageSize
}
