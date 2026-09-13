package constant

import "errors"

// 消息解析错误
var (
	// ErrUnknownGroupType 未知群组类型 (FromOrigin 解析失败时返回)
	ErrUnknownGroupType = errors.New("未知群组类型")
)
