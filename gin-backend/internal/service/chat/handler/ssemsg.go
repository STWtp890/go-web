package handler

import (
	"errors"
	"io"
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/jwt"
	logic "gin-backend/internal/service/chat/logic"
	"gin-backend/internal/service/chat/structure/bridge"
	"gin-backend/internal/service/chat/types/group"

	"github.com/gin-gonic/gin"
)

// MessageHandler 消息发送入口: POST /api/v1/protected/chat/messages
// 请求体为消息 wire 格式 JSON: {"metadata":{...},"content":"..."}
// 支持私聊 (groupType=private, to=接收者) 与群聊 (groupType=group, to=群 ID);
// 服务端强制覆盖 From 字段防伪造身份
func MessageHandler(c *gin.Context) {
	claims, exists := jwt.ExtractClaims(c)
	if !exists {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}
	sub, err := claims.GetSubject()
	if err != nil {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		responses.Fail(c, http.StatusBadRequest, eror.CodeParseError, "读取请求体失败")
		return
	}
	deliveryIDs, err := logic.SendMessageLogic(c.Request.Context(), sub, body)
	if err != nil {
		if errors.Is(err, bridge.ErrNotGroupMember) {
			responses.Fail(c, http.StatusForbidden, eror.CodeForbidden, "非群成员, 无权发送")
			return
		}
		if errors.Is(err, group.ErrGroupNotFound) {
			responses.Fail(c, http.StatusNotFound, eror.CodeNotFound, "聊天室不存在")
			return
		}
		responses.Fail(c, http.StatusBadRequest, eror.CodeParseError, "消息发送失败")
		return
	}
	responses.OK(c, gin.H{"message": "消息已接受", "deliveryIds": deliveryIDs})
}
