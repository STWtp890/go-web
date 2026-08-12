package handler

import (
	"io"
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/jwtmethod"
	logic "gin-backend/internal/service/chat/logic"

	"github.com/gin-gonic/gin"
)

// MessageHandler 消息发送入口: POST /api/v1/protected/chat/messages
// 请求体为消息 wire 格式 JSON: {"metadata":{...},"content":"..."}
// 支持私聊 (groupType=private, to=接收者) 与群聊 (groupType=group, to=群 ID);
// 服务端强制覆盖 From 字段防伪造身份
func MessageHandler(c *gin.Context) {
	claims, exists := jwtmethod.ExtractClaims(c)
	if !exists {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}
	sub, err := claims.GetSubject()
	if err != nil {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		responses.Fail(c, http.StatusBadRequest, eror.CodeParseError, "读取请求体失败")
		return
	}
	if err := logic.SendMessageLogic(c.Request.Context(), sub, body); err != nil {
		responses.Fail(c, http.StatusBadRequest, eror.CodeParseError, "消息解析失败: "+err.Error())
		return
	}
	responses.OK(c, gin.H{"message": "消息已发送"})
}
