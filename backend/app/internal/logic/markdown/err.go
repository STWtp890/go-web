package markdown

// 响应消息常量
const (
	MsgSuccess            = "success"
	MsgNotFound           = "markdown not found"
	MsgNotAuthor          = "you can only modify your own markdown"
	MsgUploadFailed       = "upload markdown failed"
	MsgUpdateFailed       = "update markdown failed"
	MsgDeleteFailed       = "delete markdown failed"
	MsgQueryFailed        = "query failed"
	MsgReviewInfoNotFound = "review info not found"
	MsgReviewFailed       = "review markdown failed"
	MsgReviewRecordFailed = "failed to create review record"
	MsgInvalidStatus      = "status must be 'approved' or 'rejected'"
	MsgMissingReviewerId  = "invalid token: missing userId"
	MsgMissingAuthorId    = "invalid token: missing userId"
	MsgFindMarkdownFailed  = "failed to find markdown"
)